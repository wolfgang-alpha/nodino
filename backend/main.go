package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"time"
)

var mempalaceURL, piperURL, whisperURL string
var claudePath, mcpConfigPath, claudeModel string

var (
	sessionMap   = make(map[string]string) // conversation_id -> claude session_id
	sessionMapMu sync.RWMutex
	convCounter  int
	convCounterMu sync.Mutex
)

func buildSystemPrompt() string {
	today := time.Now().Format("2006-01-02")
	return fmt.Sprintf(`You are nodino, a voice-first second-brain assistant. You listen to the user and extract structured knowledge from natural conversation using the mempalace tools.

Today's date is %s.

YOUR BEHAVIOR:
- Extract meaningful pieces of information from what the user says
- Store knowledge using the mempalace store_drawer tool with wing="nodino"
- Recognize people, animals, places, organizations, and things as entities
- Link related knowledge via the knowledge graph (add_fact tool)
- When you cannot determine a field (especially time, type, or entity identity), ASK the user
- Keep your spoken replies concise and natural
- Never mention databases, APIs, or technical internals unless asked
- When the user asks about stored information, use the mempalace search tool to retrieve it

STORING KNOWLEDGE:
Use store_drawer with wing="nodino" and room set to the knot type.
Encode metadata as prefix tags in the content:
  [importance:N] where N is 1-5 (1=trivial, 2=minor, 3=normal, 4=important, 5=urgent)
  [status:VALUE] for tasks (backlog, todo, in_progress, done)
  [occurs_at:DATETIME] for time-sensitive items
Example: store_drawer(wing="nodino", room="task", content="[importance:4][status:todo] Review the design mockups")

KNOT TYPES (use as room):
event, appointment, reminder, observation, mood, log, anecdote, idea, project, decision, contact, task

For tasks, ALWAYS include a [status:] tag. Default to [status:todo] if not specified.

ENTITIES:
Record entities as knowledge graph facts:
  add_fact(subject="Alice", predicate="is_a", object="person")
  add_fact(subject="Alice", predicate="described_as", object="Project lead")
Entity kinds: person, animal, place, organization, thing

RELATIONSHIPS:
Link related knowledge: add_fact(subject="knot_id", predicate="related", object="other_id")
Predicates: same_thread, follow_up, caused_by, related

TASK STATUS UPDATES:
To change a task's status, invalidate the old fact and add a new one:
  invalidate_fact(subject="drawer_id", predicate="has_status", object="todo")
  add_fact(subject="drawer_id", predicate="has_status", object="in_progress")

CALENDAR:
Use the caldav tools (list_events, create_event) for calendar queries and event creation.`, today)
}

func main() {
	mempalaceURL = envOr("MEMPALACE_URL", "http://localhost:8002")
	piperURL = envOr("PIPER_URL", "http://localhost:9000")
	whisperURL = envOr("WHISPER_URL", "http://localhost:9001")
	claudePath = envOr("CLAUDE_PATH", "claude")
	mcpConfigPath = envOr("MCP_CONFIG_PATH", "./mcp.json")
	claudeModel = envOr("CLAUDE_MODEL", "sonnet")

	waitForMempalace()

	http.HandleFunc("/api/conversation/start", handleConversationStart)
	http.HandleFunc("/api/conversation/end", handleConversationEnd)
	http.HandleFunc("/api/chat", handleChat)
	http.HandleFunc("/api/speak", handleSpeak)
	http.HandleFunc("/api/transcribe", handleTranscribe)
	http.HandleFunc("/api/knots", handleKnots)

	port := envOr("PORT", "8085")
	log.Printf("backend listening on :%s", port)
	log.Fatal(http.ListenAndServe(":"+port, nil))
}

func waitForMempalace() {
	client := &http.Client{Timeout: 2 * time.Second}
	for i := 0; i < 30; i++ {
		resp, err := client.Get(mempalaceURL + "/health")
		if err == nil {
			resp.Body.Close()
			if resp.StatusCode == 200 {
				log.Println("mempalace is ready")
				return
			}
		}
		log.Printf("waiting for mempalace... (%v)", err)
		time.Sleep(2 * time.Second)
	}
	log.Fatal("mempalace did not become ready")
}

// --- Conversation management ---

func handleConversationStart(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	convCounterMu.Lock()
	convCounter++
	id := fmt.Sprintf("conv_%d_%d", time.Now().Unix(), convCounter)
	convCounterMu.Unlock()

	writeJSON(w, 200, map[string]string{"conversation_id": id})
}

func handleConversationEnd(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var req struct {
		ConversationID string `json:"conversation_id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, 400, map[string]string{"error": "invalid request"})
		return
	}

	sessionMapMu.Lock()
	delete(sessionMap, req.ConversationID)
	sessionMapMu.Unlock()

	writeJSON(w, 200, map[string]string{"status": "ended"})
}

// --- Core chat endpoint ---

func handleChat(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var req struct {
		ConversationID string `json:"conversation_id"`
		Message        string `json:"message"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, 400, map[string]string{"error": "invalid request"})
		return
	}

	sessionMapMu.RLock()
	sessionID := sessionMap[req.ConversationID]
	sessionMapMu.RUnlock()

	args := []string{
		"-p", req.Message,
		"--output-format", "json",
		"--model", claudeModel,
		"--system-prompt", buildSystemPrompt(),
		"--mcp-config", mcpConfigPath,
		"--allowedTools", "mcp__mempalace__*,mcp__caldav__*",
		"--max-turns", "8",
	}
	if sessionID != "" {
		args = append(args, "--resume", sessionID)
	}

	cmd := exec.Command(claudePath, args...)
	cmd.Env = os.Environ()

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	log.Printf("calling claude for conversation %s", req.ConversationID)
	err := cmd.Run()
	if err != nil {
		log.Printf("claude error: %v, stderr: %s", err, stderr.String())
		writeJSON(w, 502, map[string]string{"error": "AI service unavailable: " + err.Error()})
		return
	}

	var claudeResult struct {
		SessionID string `json:"session_id"`
		Result    string `json:"result"`
		Subtype   string `json:"subtype"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &claudeResult); err != nil {
		log.Printf("failed to parse claude output: %v, raw: %s", err, stdout.String())
		writeJSON(w, 502, map[string]string{"error": "failed to parse AI response"})
		return
	}

	if claudeResult.Subtype != "success" {
		log.Printf("claude returned non-success: %s", stdout.String())
		writeJSON(w, 502, map[string]string{"error": "AI returned an error"})
		return
	}

	sessionMapMu.Lock()
	sessionMap[req.ConversationID] = claudeResult.SessionID
	sessionMapMu.Unlock()

	var knots []knotJSON
	results, err := mpSearchRecent()
	if err == nil {
		knots = results
	}

	writeJSON(w, 200, chatResponse{
		Reply: claudeResult.Result,
		Knots: knots,
	})
}

func mpSearchRecent() ([]knotJSON, error) {
	results, err := mpSearch("*", "", 5)
	if err != nil {
		return nil, err
	}
	var knots []knotJSON
	for _, r := range results {
		knots = append(knots, parseDrawerToKnot(r))
	}
	return knots, nil
}

// --- Mempalace REST client ---

type searchResult struct {
	ID         string  `json:"id"`
	Content    string  `json:"content"`
	Text       string  `json:"text"`
	Wing       string  `json:"wing"`
	Room       string  `json:"room"`
	Score      float64 `json:"score"`
	Similarity float64 `json:"similarity"`
	FiledAt    string  `json:"filed_at"`
	SourceFile string  `json:"source_file"`
}

func mpSearch(query, room string, limit int) ([]searchResult, error) {
	payload := map[string]interface{}{
		"query": query,
		"limit": limit,
		"wing":  "nodino",
	}
	if room != "" {
		payload["room"] = room
	}
	body, _ := json.Marshal(payload)
	resp, err := http.Post(mempalaceURL+"/search", "application/json", bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	respBody, _ := io.ReadAll(resp.Body)

	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("search error %d: %s", resp.StatusCode, string(respBody))
	}

	var parsed struct {
		Results []searchResult `json:"results"`
	}
	if err := json.Unmarshal(respBody, &parsed); err != nil {
		var directResults []searchResult
		if err2 := json.Unmarshal(respBody, &directResults); err2 != nil {
			return nil, fmt.Errorf("parse error: %w", err)
		}
		return directResults, nil
	}
	return parsed.Results, nil
}

func mpAddFact(subject, predicate, object string) error {
	payload := map[string]string{
		"subject":   subject,
		"predicate": predicate,
		"object":    object,
	}
	body, _ := json.Marshal(payload)
	resp, err := http.Post(mempalaceURL+"/kg/facts", "application/json", bytes.NewReader(body))
	if err != nil {
		return err
	}
	resp.Body.Close()
	return nil
}

func mpInvalidateFact(subject, predicate, object string) error {
	payload := map[string]string{
		"subject":   subject,
		"predicate": predicate,
		"object":    object,
	}
	body, _ := json.Marshal(payload)
	resp, err := http.Post(mempalaceURL+"/kg/facts/invalidate", "application/json", bytes.NewReader(body))
	if err != nil {
		return err
	}
	resp.Body.Close()
	return nil
}

// --- Drawer to knot parsing ---

func parseDrawerToKnot(r searchResult) knotJSON {
	k := knotJSON{
		ID:         r.ID,
		Type:       r.Room,
		Importance: 3,
		CreatedAt:  r.FiledAt,
	}

	content := r.Content
	if content == "" {
		content = r.Text
	}

	if strings.HasPrefix(content, "[") {
		for strings.HasPrefix(content, "[") {
			end := strings.Index(content, "]")
			if end == -1 {
				break
			}
			tag := content[1:end]
			content = content[end+1:]

			parts := strings.SplitN(tag, ":", 2)
			if len(parts) != 2 {
				break
			}
			switch parts[0] {
			case "importance":
				if v, err := strconv.Atoi(parts[1]); err == nil {
					k.Importance = v
				}
			case "occurs_at":
				s := parts[1]
				k.OccursAt = &s
			case "status":
				s := parts[1]
				k.Status = &s
			}
		}
		content = strings.TrimSpace(content)
	}

	k.Content = content
	return k
}

// --- STT proxy ---

func handleTranscribe(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	r.ParseMultipartForm(25 << 20)

	file, header, err := r.FormFile("file")
	if err != nil {
		writeJSON(w, 400, map[string]string{"error": "no audio file"})
		return
	}
	defer file.Close()

	var buf bytes.Buffer
	writer := io.MultiWriter(&buf)
	io.Copy(writer, file)

	body := &bytes.Buffer{}
	mpWriter := newMultipartWriter(body, header.Filename, buf.Bytes())

	client := &http.Client{Timeout: 120 * time.Second}
	resp, err := client.Post(whisperURL+"/transcribe", mpWriter.FormDataContentType(), body)
	if err != nil {
		writeJSON(w, 502, map[string]string{"error": "whisper unavailable: " + err.Error()})
		return
	}
	defer resp.Body.Close()

	respBody, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != 200 {
		writeJSON(w, 502, map[string]string{"error": "whisper error: " + string(respBody)})
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.Write(respBody)
}

// --- TTS proxy ---

func handleSpeak(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var req struct {
		Text string `json:"text"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.Text == "" {
		writeJSON(w, 400, map[string]string{"error": "missing text"})
		return
	}

	payload, _ := json.Marshal(map[string]string{"text": req.Text})
	client := &http.Client{Timeout: 60 * time.Second}
	resp, err := client.Post(piperURL+"/synthesize", "application/json", bytes.NewReader(payload))
	if err != nil {
		writeJSON(w, 502, map[string]string{"error": "TTS unavailable: " + err.Error()})
		return
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		body, _ := io.ReadAll(resp.Body)
		writeJSON(w, 502, map[string]string{"error": "TTS error: " + string(body)})
		return
	}

	w.Header().Set("Content-Type", "audio/wav")
	io.Copy(w, resp.Body)
}

// --- Knots API (for kanban/todo) ---

func handleKnots(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		handleGetKnots(w, r)
	case http.MethodPut:
		handleUpdateKnot(w, r)
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

func handleGetKnots(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	ktype := q.Get("type")
	limitStr := q.Get("limit")
	limit := 20
	if limitStr != "" {
		if l, err := strconv.Atoi(limitStr); err == nil && l > 0 {
			limit = l
		}
	}

	results, err := mpSearch("*", ktype, limit)
	if err != nil {
		writeJSON(w, 500, map[string]string{"error": err.Error()})
		return
	}

	var knots []knotJSON
	for _, r := range results {
		knots = append(knots, parseDrawerToKnot(r))
	}

	writeJSON(w, 200, knots)
}

func handleUpdateKnot(w http.ResponseWriter, r *http.Request) {
	var req struct {
		ID     string `json:"id"`
		Status string `json:"status,omitempty"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.ID == "" {
		writeJSON(w, 400, map[string]string{"error": "invalid request"})
		return
	}

	if req.Status != "" {
		currentStatus := "backlog"
		results, err := mpSearch("*", "task", 100)
		if err == nil {
			for _, sr := range results {
				if sr.ID == req.ID {
					k := parseDrawerToKnot(sr)
					if k.Status != nil {
						currentStatus = *k.Status
					}
					break
				}
			}
		}
		mpInvalidateFact(req.ID, "has_status", currentStatus)
		mpAddFact(req.ID, "has_status", req.Status)
	}

	writeJSON(w, 200, map[string]string{"status": "updated"})
}

// --- Response types ---

type knotJSON struct {
	ID         string  `json:"id"`
	Content    string  `json:"content"`
	Type       string  `json:"type"`
	Importance int     `json:"importance"`
	OccursAt   *string `json:"occurs_at,omitempty"`
	Status     *string `json:"status,omitempty"`
	CreatedAt  string  `json:"created_at"`
}

type chatResponse struct {
	Reply    string     `json:"reply"`
	Knots    []knotJSON `json:"knots,omitempty"`
}

// --- Multipart helper ---

type multipartBody struct {
	contentType string
}

func (m *multipartBody) FormDataContentType() string {
	return m.contentType
}

func newMultipartWriter(body *bytes.Buffer, filename string, data []byte) *multipartBody {
	boundary := fmt.Sprintf("----nodino%d", time.Now().UnixNano())
	ct := "multipart/form-data; boundary=" + boundary

	body.WriteString("--" + boundary + "\r\n")
	body.WriteString(fmt.Sprintf("Content-Disposition: form-data; name=\"file\"; filename=\"%s\"\r\n", filename))
	body.WriteString("Content-Type: application/octet-stream\r\n\r\n")
	body.Write(data)
	body.WriteString("\r\n--" + boundary + "--\r\n")

	return &multipartBody{contentType: ct}
}

// --- Helpers ---

func writeJSON(w http.ResponseWriter, status int, v interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v)
}

func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
