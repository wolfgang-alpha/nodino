package main

import (
	"bytes"
	"crypto/tls"
	"encoding/json"
	"encoding/xml"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"
)

var mempalaceURL, anthropicKey, anthropicModel, piperURL, whisperURL string
var calDavURL, nextcloudUser, nextcloudPass string

var (
	convHistory   = make(map[string][]anthropicMsg)
	convHistoryMu sync.RWMutex
	convCounter   int
	convCounterMu sync.Mutex
)

type anthropicMsg struct {
	Role    string        `json:"role"`
	Content []contentPart `json:"content"`
}

type contentPart struct {
	Type      string          `json:"type"`
	Text      string          `json:"text,omitempty"`
	ID        string          `json:"id,omitempty"`
	Name      string          `json:"name,omitempty"`
	Input     json.RawMessage `json:"input,omitempty"`
	ToolUseID string          `json:"tool_use_id,omitempty"`
	Content   string          `json:"content,omitempty"`
}

const systemPrompt = `You are nodino, a second-brain assistant. You listen to the user and extract structured knowledge from natural conversation. You store information as "knots" in a memory palace using the tools provided.

YOUR BEHAVIOR:
- Extract meaningful pieces of information from what the user says
- Categorize each piece with the correct type and importance
- Recognize people, animals, places, organizations, and things as entities
- Link related knots together via relationships
- When you cannot determine a field (especially time, type, or entity identity), ASK the user
- Keep your spoken replies concise and natural
- Never mention databases, APIs, or technical internals unless asked
- IMPORTANT: Whenever you reference or discuss stored information, you MUST use the search_knowledge tool so the data is actually shown to the user on screen. The user can ONLY see data that you retrieve — just talking about it is not enough.

KNOT TYPES (use as the "room" when storing):
event, appointment, reminder, observation, mood, log, anecdote, idea, project, decision, contact, task

IMPORTANCE SCALE (1-5):
1 = trivial/routine, 2 = minor, 3 = normal, 4 = important, 5 = urgent/critical

For tasks, ALWAYS set a status: backlog, todo, in_progress, or done.

ENTITY KINDS: person, animal, place, organization, thing

RELATIONSHIP TYPES: same_thread, follow_up, caused_by, related`

func main() {
	mempalaceURL = envOr("MEMPALACE_URL", "http://mempalace-api:8000")
	anthropicKey = envOr("ANTHROPIC_API_KEY", "")
	anthropicModel = envOr("ANTHROPIC_MODEL", "claude-sonnet-4-6")
	piperURL = envOr("PIPER_URL", "http://piper:5000")
	whisperURL = envOr("WHISPER_URL", "http://whisper:5001")
	calDavURL = envOr("CAL_DAV", "")
	nextcloudUser = envOr("NEXTCLOUD_USER", "")
	nextcloudPass = envOr("NEXTCLOUD_PASSWORD", "")

	if anthropicKey == "" {
		log.Fatal("ANTHROPIC_API_KEY is required")
	}

	waitForMempalace()

	http.HandleFunc("/api/conversation/start", handleConversationStart)
	http.HandleFunc("/api/conversation/end", handleConversationEnd)
	http.HandleFunc("/api/chat", handleChat)
	http.HandleFunc("/api/speak", handleSpeak)
	http.HandleFunc("/api/transcribe", handleTranscribe)
	http.HandleFunc("/api/knots", handleKnots)

	log.Println("backend listening on :8080")
	log.Fatal(http.ListenAndServe(":8080", nil))
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

	convHistoryMu.Lock()
	convHistory[id] = []anthropicMsg{}
	convHistoryMu.Unlock()

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

	convHistoryMu.Lock()
	delete(convHistory, req.ConversationID)
	convHistoryMu.Unlock()

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

	convHistoryMu.RLock()
	history := convHistory[req.ConversationID]
	convHistoryMu.RUnlock()

	msgs := make([]anthropicMsg, len(history))
	copy(msgs, history)
	msgs = append(msgs, anthropicMsg{
		Role:    "user",
		Content: []contentPart{{Type: "text", Text: req.Message}},
	})
	if len(msgs) > 21 {
		msgs = msgs[len(msgs)-21:]
	}

	var allKnots []knotJSON
	var allEntities []entityJSON
	var replyText string

	for loop := 0; loop < 8; loop++ {
		resp, err := callAnthropic(msgs)
		if err != nil {
			log.Printf("anthropic error: %v", err)
			writeJSON(w, 502, map[string]string{"error": "AI service unavailable: " + err.Error()})
			return
		}

		var assistantParts []contentPart
		var toolCalls []contentPart

		for _, block := range resp.Content {
			switch block.Type {
			case "text":
				replyText = block.Text
				assistantParts = append(assistantParts, block)
			case "tool_use":
				toolCalls = append(toolCalls, block)
				assistantParts = append(assistantParts, block)
			}
		}

		msgs = append(msgs, anthropicMsg{Role: "assistant", Content: assistantParts})

		if len(toolCalls) == 0 || resp.StopReason == "end_turn" {
			break
		}

		var toolResults []contentPart
		for _, tc := range toolCalls {
			result, knots, entities := executeToolCall(tc.Name, tc.Input)
			allKnots = append(allKnots, knots...)
			allEntities = append(allEntities, entities...)
			toolResults = append(toolResults, contentPart{
				Type:      "tool_result",
				ToolUseID: tc.ID,
				Content:   result,
			})
		}
		msgs = append(msgs, anthropicMsg{Role: "user", Content: toolResults})
	}

	convHistoryMu.Lock()
	convHistory[req.ConversationID] = append(convHistory[req.ConversationID],
		anthropicMsg{Role: "user", Content: []contentPart{{Type: "text", Text: req.Message}}},
		anthropicMsg{Role: "assistant", Content: []contentPart{{Type: "text", Text: replyText}}},
	)
	convHistoryMu.Unlock()

	writeJSON(w, 200, chatResponse{
		Reply:    replyText,
		Knots:    allKnots,
		Entities: allEntities,
	})
}

// --- Anthropic API ---

type anthropicRequest struct {
	Model     string         `json:"model"`
	MaxTokens int            `json:"max_tokens"`
	System    string         `json:"system"`
	Tools     []toolDef      `json:"tools"`
	Messages  []anthropicMsg `json:"messages"`
}

type anthropicResponse struct {
	Content    []contentPart `json:"content"`
	StopReason string        `json:"stop_reason"`
}

type toolDef struct {
	Name        string      `json:"name"`
	Description string      `json:"description"`
	InputSchema interface{} `json:"input_schema"`
}

func getTools() []toolDef {
	return []toolDef{
		{
			Name:        "store_knot",
			Description: "Store a piece of knowledge (a knot) in the memory palace. Use the appropriate room/type for categorization.",
			InputSchema: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"content":    map[string]string{"type": "string", "description": "The knowledge content to store"},
					"room":       map[string]string{"type": "string", "description": "Knot type: event, appointment, reminder, observation, mood, log, anecdote, idea, project, decision, contact, task"},
					"importance": map[string]interface{}{"type": "integer", "description": "1-5 importance scale", "minimum": 1, "maximum": 5},
					"occurs_at":  map[string]string{"type": "string", "description": "When this occurs (ISO datetime), if applicable"},
					"status":     map[string]string{"type": "string", "description": "For tasks only: backlog, todo, in_progress, or done"},
				},
				"required": []string{"content", "room", "importance"},
			},
		},
		{
			Name:        "search_knowledge",
			Description: "Search stored knowledge semantically. ALWAYS use this when referencing or discussing existing data — the user can only see data you retrieve.",
			InputSchema: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"query": map[string]string{"type": "string", "description": "What to search for"},
					"room":  map[string]string{"type": "string", "description": "Optional: filter by knot type (event, task, etc.)"},
					"limit": map[string]interface{}{"type": "integer", "description": "Max results (default 10)", "minimum": 1, "maximum": 50},
				},
				"required": []string{"query"},
			},
		},
		{
			Name:        "add_entity",
			Description: "Record a named entity (person, place, organization, animal, thing) in the knowledge graph.",
			InputSchema: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"name":        map[string]string{"type": "string", "description": "Entity name"},
					"kind":        map[string]string{"type": "string", "description": "person, animal, place, organization, or thing"},
					"description": map[string]string{"type": "string", "description": "Brief description"},
				},
				"required": []string{"name", "kind"},
			},
		},
		{
			Name:        "link_knowledge",
			Description: "Create a relationship between two pieces of knowledge or between an entity and a knot in the knowledge graph.",
			InputSchema: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"subject":      map[string]string{"type": "string", "description": "Source entity/knot name or ID"},
					"predicate":    map[string]string{"type": "string", "description": "Relationship: same_thread, follow_up, caused_by, related, or a custom role"},
					"object":       map[string]string{"type": "string", "description": "Target entity/knot name or ID"},
					"valid_from":   map[string]string{"type": "string", "description": "When this relationship started (YYYY-MM-DD), if applicable"},
				},
				"required": []string{"subject", "predicate", "object"},
			},
		},
		{
			Name:        "set_task_status",
			Description: "Update the status of a task knot (backlog, todo, in_progress, done).",
			InputSchema: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"knot_id":    map[string]string{"type": "string", "description": "The drawer ID of the task"},
					"new_status": map[string]string{"type": "string", "description": "New status: backlog, todo, in_progress, or done"},
				},
				"required": []string{"knot_id", "new_status"},
			},
		},
		{
			Name:        "query_entity",
			Description: "Look up everything known about a specific entity from the knowledge graph.",
			InputSchema: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"entity_name": map[string]string{"type": "string", "description": "Name of the entity to look up"},
				},
				"required": []string{"entity_name"},
			},
		},
		{
			Name:        "query_calendar",
			Description: "Query the user's Nextcloud calendar for upcoming or past events within a date range.",
			InputSchema: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"from": map[string]string{"type": "string", "description": "Start date (YYYY-MM-DD). Defaults to today."},
					"to":   map[string]string{"type": "string", "description": "End date (YYYY-MM-DD). Defaults to 7 days from start."},
				},
				"required": []string{},
			},
		},
		{
			Name:        "create_calendar_event",
			Description: "Create a new event in the user's Nextcloud calendar.",
			InputSchema: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"summary":     map[string]string{"type": "string", "description": "Event title"},
					"description": map[string]string{"type": "string", "description": "Event description"},
					"start":       map[string]string{"type": "string", "description": "Start datetime (YYYY-MM-DDTHH:MM:SS)"},
					"end":         map[string]string{"type": "string", "description": "End datetime (YYYY-MM-DDTHH:MM:SS)"},
					"all_day":     map[string]interface{}{"type": "boolean", "description": "If true, create an all-day event (only date part of start/end is used)"},
				},
				"required": []string{"summary", "start"},
			},
		},
	}
}

func callAnthropic(msgs []anthropicMsg) (*anthropicResponse, error) {
	reqBody := anthropicRequest{
		Model:     anthropicModel,
		MaxTokens: 4096,
		System:    systemPrompt,
		Tools:     getTools(),
		Messages:  msgs,
	}

	body, err := json.Marshal(reqBody)
	if err != nil {
		return nil, err
	}

	req, _ := http.NewRequest("POST", "https://api.anthropic.com/v1/messages", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("x-api-key", anthropicKey)
	req.Header.Set("anthropic-version", "2023-06-01")

	client := &http.Client{Timeout: 120 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to reach Anthropic: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read response: %w", err)
	}

	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("anthropic returned %d: %s", resp.StatusCode, string(respBody))
	}

	var result anthropicResponse
	if err := json.Unmarshal(respBody, &result); err != nil {
		return nil, fmt.Errorf("failed to parse response: %w", err)
	}

	return &result, nil
}

// --- Tool execution ---

func executeToolCall(name string, rawInput json.RawMessage) (string, []knotJSON, []entityJSON) {
	var input map[string]interface{}
	json.Unmarshal(rawInput, &input)

	switch name {
	case "store_knot":
		return toolStoreKnot(input)
	case "search_knowledge":
		return toolSearchKnowledge(input)
	case "add_entity":
		return toolAddEntity(input)
	case "link_knowledge":
		return toolLinkKnowledge(input)
	case "set_task_status":
		return toolSetTaskStatus(input)
	case "query_entity":
		return toolQueryEntity(input)
	case "query_calendar":
		return toolQueryCalendar(input)
	case "create_calendar_event":
		return toolCreateCalendarEvent(input)
	default:
		return fmt.Sprintf("unknown tool: %s", name), nil, nil
	}
}

func toolStoreKnot(input map[string]interface{}) (string, []knotJSON, []entityJSON) {
	content, _ := input["content"].(string)
	room, _ := input["room"].(string)
	importance := 3
	if imp, ok := input["importance"].(float64); ok {
		importance = int(imp)
	}
	occursAt, _ := input["occurs_at"].(string)
	status, _ := input["status"].(string)

	if room == "task" && status == "" {
		status = "todo"
	}

	// Build content with metadata prefix
	storedContent := fmt.Sprintf("[importance:%d]", importance)
	if occursAt != "" {
		storedContent += fmt.Sprintf("[occurs_at:%s]", occursAt)
	}
	if status != "" {
		storedContent += fmt.Sprintf("[status:%s]", status)
	}
	storedContent += " " + content

	drawerID, err := mpStoreDrawer(room, storedContent)
	if err != nil {
		return "error storing knot: " + err.Error(), nil, nil
	}

	if status != "" {
		mpAddFact(drawerID, "has_status", status)
	}

	k := knotJSON{
		ID:         drawerID,
		Content:    content,
		Type:       room,
		Importance: importance,
		CreatedAt:  time.Now().Format("2006-01-02 15:04:05"),
	}
	if occursAt != "" {
		k.OccursAt = &occursAt
	}
	if status != "" {
		k.Status = &status
	}

	return fmt.Sprintf("Stored knot %s in room '%s'", drawerID, room), []knotJSON{k}, nil
}

func toolSearchKnowledge(input map[string]interface{}) (string, []knotJSON, []entityJSON) {
	query, _ := input["query"].(string)
	room, _ := input["room"].(string)
	limit := 10
	if l, ok := input["limit"].(float64); ok {
		limit = int(l)
	}

	results, err := mpSearch(query, room, limit)
	if err != nil {
		return "search error: " + err.Error(), nil, nil
	}

	var knots []knotJSON
	var summaryParts []string
	for _, r := range results {
		k := parseDrawerToKnot(r)
		knots = append(knots, k)
		summaryParts = append(summaryParts, fmt.Sprintf("[%s] %s", k.Type, k.Content))
	}

	if len(knots) == 0 {
		return "No results found.", nil, nil
	}

	return fmt.Sprintf("Found %d results:\n%s", len(knots), strings.Join(summaryParts, "\n")), knots, nil
}

func toolAddEntity(input map[string]interface{}) (string, []knotJSON, []entityJSON) {
	name, _ := input["name"].(string)
	kind, _ := input["kind"].(string)
	desc, _ := input["description"].(string)

	mpAddFact(name, "is_a", kind)
	if desc != "" {
		mpAddFact(name, "described_as", desc)
	}

	e := entityJSON{
		Name:        name,
		Kind:        kind,
		Description: desc,
	}

	return fmt.Sprintf("Recorded entity '%s' (%s)", name, kind), nil, []entityJSON{e}
}

func toolLinkKnowledge(input map[string]interface{}) (string, []knotJSON, []entityJSON) {
	subject, _ := input["subject"].(string)
	predicate, _ := input["predicate"].(string)
	object, _ := input["object"].(string)
	validFrom, _ := input["valid_from"].(string)

	err := mpAddFactWithDate(subject, predicate, object, validFrom)
	if err != nil {
		return "error linking: " + err.Error(), nil, nil
	}

	return fmt.Sprintf("Linked '%s' -[%s]-> '%s'", subject, predicate, object), nil, nil
}

func toolSetTaskStatus(input map[string]interface{}) (string, []knotJSON, []entityJSON) {
	knotID, _ := input["knot_id"].(string)
	newStatus, _ := input["new_status"].(string)

	mpInvalidateFact(knotID, "has_status")
	mpAddFact(knotID, "has_status", newStatus)

	return fmt.Sprintf("Task %s status set to '%s'", knotID, newStatus), nil, nil
}

func toolQueryEntity(input map[string]interface{}) (string, []knotJSON, []entityJSON) {
	entityName, _ := input["entity_name"].(string)

	facts, err := mpQueryEntity(entityName)
	if err != nil {
		return "error querying entity: " + err.Error(), nil, nil
	}

	if facts == "" {
		return fmt.Sprintf("No information found about '%s'", entityName), nil, nil
	}

	return facts, nil, nil
}

// --- Mempalace REST client ---

func mpStoreDrawer(room, content string) (string, error) {
	payload := map[string]string{
		"wing":    "nodino",
		"room":    room,
		"content": content,
	}
	body, _ := json.Marshal(payload)
	resp, err := http.Post(mempalaceURL+"/drawers", "application/json", bytes.NewReader(body))
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	respBody, _ := io.ReadAll(resp.Body)

	if resp.StatusCode != 200 && resp.StatusCode != 201 {
		return "", fmt.Errorf("mempalace store error %d: %s", resp.StatusCode, string(respBody))
	}

	var result map[string]interface{}
	json.Unmarshal(respBody, &result)

	if id, ok := result["drawer_id"].(string); ok {
		return id, nil
	}
	if id, ok := result["id"].(string); ok {
		return id, nil
	}
	return fmt.Sprintf("drawer_%d", time.Now().UnixNano()), nil
}

type searchResult struct {
	ID       string  `json:"id"`
	Content  string  `json:"content"`
	Wing     string  `json:"wing"`
	Room     string  `json:"room"`
	Score    float64 `json:"score"`
	FiledAt  string  `json:"filed_at"`
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
	return mpAddFactWithDate(subject, predicate, object, "")
}

func mpAddFactWithDate(subject, predicate, object, validFrom string) error {
	payload := map[string]string{
		"subject":   subject,
		"predicate": predicate,
		"object":    object,
	}
	if validFrom != "" {
		payload["valid_from"] = validFrom
	}
	body, _ := json.Marshal(payload)
	resp, err := http.Post(mempalaceURL+"/kg/facts", "application/json", bytes.NewReader(body))
	if err != nil {
		return err
	}
	resp.Body.Close()
	return nil
}

func mpInvalidateFact(subject, predicate string) error {
	payload := map[string]string{
		"subject":   subject,
		"predicate": predicate,
	}
	body, _ := json.Marshal(payload)
	resp, err := http.Post(mempalaceURL+"/kg/facts/invalidate", "application/json", bytes.NewReader(body))
	if err != nil {
		return err
	}
	resp.Body.Close()
	return nil
}

func mpQueryEntity(entity string) (string, error) {
	resp, err := http.Get(fmt.Sprintf("%s/kg/query?entity=%s", mempalaceURL, entity))
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)

	if resp.StatusCode != 200 {
		return "", fmt.Errorf("kg query error %d: %s", resp.StatusCode, string(body))
	}

	var result struct {
		Facts []struct {
			Subject   string `json:"subject"`
			Predicate string `json:"predicate"`
			Object    string `json:"object"`
			ValidFrom string `json:"valid_from"`
			ValidTo   string `json:"valid_to"`
		} `json:"facts"`
	}
	json.Unmarshal(body, &result)

	var lines []string
	for _, f := range result.Facts {
		line := fmt.Sprintf("%s %s %s", f.Subject, f.Predicate, f.Object)
		if f.ValidFrom != "" {
			line += fmt.Sprintf(" (from: %s)", f.ValidFrom)
		}
		lines = append(lines, line)
	}
	return strings.Join(lines, "\n"), nil
}

func mpGetTasksByStatus(statuses []string) ([]knotJSON, error) {
	var allKnots []knotJSON

	results, err := mpSearch("task", "task", 100)
	if err != nil {
		return nil, err
	}

	statusSet := make(map[string]bool)
	for _, s := range statuses {
		statusSet[s] = true
	}

	for _, r := range results {
		k := parseDrawerToKnot(r)
		if k.Status != nil && statusSet[*k.Status] {
			allKnots = append(allKnots, k)
		} else if k.Status == nil && statusSet["backlog"] {
			s := "backlog"
			k.Status = &s
			allKnots = append(allKnots, k)
		}
	}

	return allKnots, nil
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

	room := ktype
	if room == "" {
		room = ""
	}

	results, err := mpSearch("*", room, limit)
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
		mpInvalidateFact(req.ID, "has_status")
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

type entityJSON struct {
	Name        string `json:"name"`
	Kind        string `json:"kind"`
	Description string `json:"description"`
}

type chatResponse struct {
	Reply    string       `json:"reply"`
	Knots    []knotJSON   `json:"knots,omitempty"`
	Entities []entityJSON `json:"entities,omitempty"`
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

// --- CalDAV integration ---

func caldavClient() *http.Client {
	return &http.Client{
		Timeout: 30 * time.Second,
		Transport: &http.Transport{
			TLSClientConfig: &tls.Config{InsecureSkipVerify: false},
		},
	}
}

func caldavRequest(method, url string, body string) ([]byte, error) {
	req, err := http.NewRequest(method, url, strings.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.SetBasicAuth(nextcloudUser, nextcloudPass)
	req.Header.Set("Content-Type", "application/xml; charset=utf-8")
	req.Header.Set("Depth", "1")

	resp, err := caldavClient().Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	return io.ReadAll(resp.Body)
}

type calMultistatus struct {
	XMLName   xml.Name      `xml:"multistatus"`
	Responses []calResponse `xml:"response"`
}

type calResponse struct {
	Href    string     `xml:"href"`
	Propstat calPropstat `xml:"propstat"`
}

type calPropstat struct {
	Prop calProp `xml:"prop"`
}

type calProp struct {
	CalendarData string `xml:"calendar-data"`
	DisplayName  string `xml:"displayname"`
}

func toolQueryCalendar(input map[string]interface{}) (string, []knotJSON, []entityJSON) {
	if calDavURL == "" {
		return "Calendar not configured.", nil, nil
	}

	fromStr, _ := input["from"].(string)
	toStr, _ := input["to"].(string)

	now := time.Now()
	var fromTime, toTime time.Time

	if fromStr != "" {
		t, err := time.Parse("2006-01-02", fromStr)
		if err == nil {
			fromTime = t
		}
	}
	if fromTime.IsZero() {
		fromTime = now
	}

	if toStr != "" {
		t, err := time.Parse("2006-01-02", toStr)
		if err == nil {
			toTime = t
		}
	}
	if toTime.IsZero() {
		toTime = fromTime.AddDate(0, 0, 7)
	}

	calURL := calDavURL + "/calendars/" + nextcloudUser + "/"

	listBody, err := caldavRequest("PROPFIND", calURL, `<?xml version="1.0" encoding="utf-8" ?>
<d:propfind xmlns:d="DAV:" xmlns:cs="urn:ietf:params:xml:ns:caldav">
  <d:prop><d:displayname/></d:prop>
</d:propfind>`)
	if err != nil {
		return "Calendar connection error: " + err.Error(), nil, nil
	}

	var listResult calMultistatus
	xml.Unmarshal(listBody, &listResult)

	var calendars []string
	for _, r := range listResult.Responses {
		parts := strings.Split(strings.TrimRight(r.Href, "/"), "/")
		name := parts[len(parts)-1]
		if name != "" && name != nextcloudUser {
			calendars = append(calendars, name)
		}
	}

	if len(calendars) == 0 {
		calendars = []string{"personal"}
	}

	fromFmt := fromTime.UTC().Format("20060102T150405Z")
	toFmt := toTime.UTC().Format("20060102T150405Z")

	reportBody := fmt.Sprintf(`<?xml version="1.0" encoding="utf-8" ?>
<c:calendar-query xmlns:d="DAV:" xmlns:c="urn:ietf:params:xml:ns:caldav">
  <d:prop>
    <d:getetag/>
    <c:calendar-data/>
  </d:prop>
  <c:filter>
    <c:comp-filter name="VCALENDAR">
      <c:comp-filter name="VEVENT">
        <c:time-range start="%s" end="%s"/>
      </c:comp-filter>
    </c:comp-filter>
  </c:filter>
</c:calendar-query>`, fromFmt, toFmt)

	var allEvents []string

	for _, cal := range calendars {
		eventURL := calDavURL + "/calendars/" + nextcloudUser + "/" + cal + "/"
		respBody, err := caldavRequest("REPORT", eventURL, reportBody)
		if err != nil {
			continue
		}

		var result calMultistatus
		xml.Unmarshal(respBody, &result)

		for _, r := range result.Responses {
			events := parseICalEvents(r.Propstat.Prop.CalendarData)
			allEvents = append(allEvents, events...)
		}
	}

	if len(allEvents) == 0 {
		return fmt.Sprintf("No events found between %s and %s.", fromTime.Format("2006-01-02"), toTime.Format("2006-01-02")), nil, nil
	}

	var knots []knotJSON
	for i, ev := range allEvents {
		knots = append(knots, knotJSON{
			ID:         fmt.Sprintf("cal_%d", i),
			Content:    ev,
			Type:       "appointment",
			Importance: 3,
			CreatedAt:  time.Now().Format("2006-01-02 15:04:05"),
		})
	}

	return fmt.Sprintf("Found %d events between %s and %s:\n%s",
		len(allEvents), fromTime.Format("2006-01-02"), toTime.Format("2006-01-02"),
		strings.Join(allEvents, "\n")), knots, nil
}

func parseICalEvents(ical string) []string {
	var events []string
	lines := strings.Split(ical, "\n")

	var summary, dtstart, dtend, location, description string
	inEvent := false

	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "BEGIN:VEVENT" {
			inEvent = true
			summary, dtstart, dtend, location, description = "", "", "", "", ""
		} else if line == "END:VEVENT" && inEvent {
			ev := summary
			if dtstart != "" {
				start := formatICalDate(dtstart)
				ev += " | " + start
				if dtend != "" {
					end := formatICalDate(dtend)
					ev += " - " + end
				}
			}
			if location != "" {
				ev += " @ " + location
			}
			if description != "" {
				ev += " (" + description + ")"
			}
			events = append(events, ev)
			inEvent = false
		} else if inEvent {
			if strings.HasPrefix(line, "SUMMARY:") {
				summary = strings.TrimPrefix(line, "SUMMARY:")
			} else if strings.HasPrefix(line, "DTSTART") {
				parts := strings.SplitN(line, ":", 2)
				if len(parts) == 2 {
					dtstart = parts[1]
				}
			} else if strings.HasPrefix(line, "DTEND") {
				parts := strings.SplitN(line, ":", 2)
				if len(parts) == 2 {
					dtend = parts[1]
				}
			} else if strings.HasPrefix(line, "LOCATION:") {
				location = strings.TrimPrefix(line, "LOCATION:")
			} else if strings.HasPrefix(line, "DESCRIPTION:") {
				desc := strings.TrimPrefix(line, "DESCRIPTION:")
				desc = strings.ReplaceAll(desc, "\\n", " ")
				if len(desc) > 100 {
					desc = desc[:100] + "..."
				}
				description = desc
			}
		}
	}
	return events
}

func formatICalDate(s string) string {
	for _, layout := range []string{"20060102T150405Z", "20060102T150405", "20060102"} {
		t, err := time.Parse(layout, s)
		if err == nil {
			if layout == "20060102" {
				return t.Format("2006-01-02")
			}
			return t.Local().Format("2006-01-02 15:04")
		}
	}
	return s
}

func toolCreateCalendarEvent(input map[string]interface{}) (string, []knotJSON, []entityJSON) {
	if calDavURL == "" {
		return "Calendar not configured.", nil, nil
	}

	summary, _ := input["summary"].(string)
	description, _ := input["description"].(string)
	startStr, _ := input["start"].(string)
	endStr, _ := input["end"].(string)
	allDay, _ := input["all_day"].(bool)

	if summary == "" || startStr == "" {
		return "Missing required fields: summary and start", nil, nil
	}

	uid := fmt.Sprintf("nodino-%d@nodino", time.Now().UnixNano())

	var dtstart, dtend string
	if allDay {
		t, err := time.Parse("2006-01-02", startStr[:10])
		if err != nil {
			return "Invalid start date: " + err.Error(), nil, nil
		}
		dtstart = "DTSTART;VALUE=DATE:" + t.Format("20060102")
		if endStr != "" {
			te, _ := time.Parse("2006-01-02", endStr[:10])
			dtend = "DTEND;VALUE=DATE:" + te.AddDate(0, 0, 1).Format("20060102")
		} else {
			dtend = "DTEND;VALUE=DATE:" + t.AddDate(0, 0, 1).Format("20060102")
		}
	} else {
		t, err := time.Parse("2006-01-02T15:04:05", startStr)
		if err != nil {
			t, err = time.Parse("2006-01-02T15:04", startStr)
		}
		if err != nil {
			return "Invalid start datetime: " + err.Error(), nil, nil
		}
		dtstart = "DTSTART:" + t.UTC().Format("20060102T150405Z")
		if endStr != "" {
			te, err := time.Parse("2006-01-02T15:04:05", endStr)
			if err != nil {
				te, _ = time.Parse("2006-01-02T15:04", endStr)
			}
			dtend = "DTEND:" + te.UTC().Format("20060102T150405Z")
		} else {
			dtend = "DTEND:" + t.Add(time.Hour).UTC().Format("20060102T150405Z")
		}
	}

	descLine := ""
	if description != "" {
		descLine = "DESCRIPTION:" + strings.ReplaceAll(description, "\n", "\\n") + "\n"
	}

	ical := fmt.Sprintf(`BEGIN:VCALENDAR
VERSION:2.0
PRODID:-//nodino//EN
BEGIN:VEVENT
UID:%s
DTSTAMP:%s
%s
%s
SUMMARY:%s
%sEND:VEVENT
END:VCALENDAR`, uid, time.Now().UTC().Format("20060102T150405Z"), dtstart, dtend, summary, descLine)

	eventURL := calDavURL + "/calendars/" + nextcloudUser + "/personal/" + uid + ".ics"

	req, err := http.NewRequest("PUT", eventURL, strings.NewReader(ical))
	if err != nil {
		return "Error creating request: " + err.Error(), nil, nil
	}
	req.SetBasicAuth(nextcloudUser, nextcloudPass)
	req.Header.Set("Content-Type", "text/calendar; charset=utf-8")

	resp, err := caldavClient().Do(req)
	if err != nil {
		return "Calendar error: " + err.Error(), nil, nil
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		return fmt.Sprintf("Created calendar event '%s' on %s", summary, startStr), nil, nil
	}

	body, _ := io.ReadAll(resp.Body)
	return fmt.Sprintf("Calendar error %d: %s", resp.StatusCode, string(body)), nil, nil
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
