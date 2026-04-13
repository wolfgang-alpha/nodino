package main

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	_ "github.com/go-sql-driver/mysql"
)

var db *sql.DB
var ollamaURL, ollamaModel, piperURL string

// conversation history for context window
var (
	convHistory   = make(map[int][]message)
	convHistoryMu sync.RWMutex
)

type message struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

const systemPromptTemplate = `You are nodino, a second-brain assistant. You listen to the user and extract structured knowledge from their speech. You store important information as "knots" in a database.

YOUR BEHAVIOR:
- Extract meaningful pieces of information from what the user says
- Categorize each piece with the correct type and importance
- Recognize people, animals, places, organizations, and things as entities
- Link related knots together via "nodinos" (relationship links)
- When you cannot determine a field (especially time, type, or entity identity), ASK the user
- Keep your spoken replies concise and natural — you are a conversation partner, not a robot
- Never mention SQL, databases, or technical internals unless the user asks
- IMPORTANT: Whenever you reference, mention, or discuss existing stored information, you MUST include a "retrieve" action so the data is actually shown to the user on screen. The user can ONLY see data that you retrieve — just talking about it is not enough. Always retrieve relevant knots when they are related to the conversation.

KNOT TYPES (pick the best fit):
- event: something that happened
- appointment: future scheduled thing with a time
- reminder: something to not forget, no fixed time
- observation: thoughts, concerns, opinions, realizations
- mood: emotional state
- log: routine tracking (meals, sleep, exercise, etc.)
- anecdote: stories, memorable moments
- idea: creative sparks, inspiration
- project: ongoing initiative or body of work
- decision: a choice that was made
- contact: first encounter or key update about a person
- task: an actionable to-do item (always include a "status" field: backlog, todo, in_progress, or done)

IMPORTANCE SCALE (1-5):
1 = trivial/routine, 2 = minor, 3 = normal, 4 = important, 5 = urgent/critical

ENTITY KINDS: person, animal, place, organization, thing

RELATIONSHIP TYPES: same_thread, follow_up, caused_by, related

You MUST respond with valid JSON in this exact format:
{
  "reply": "your natural language response to speak aloud",
  "actions": [
    {
      "action": "create_knot",
      "content": "the information",
      "type": "event",
      "importance": 3,
      "occurs_at": "2026-03-20 14:00:00 or null",
      "status": "todo (only for tasks, omit for other types)"
    },
    {
      "action": "create_entity",
      "name": "Person Name",
      "kind": "person",
      "description": "brief description"
    },
    {
      "action": "link_entity",
      "knot_index": 0,
      "entity_name": "Person Name",
      "role": "attendee"
    },
    {
      "action": "create_nodino",
      "knot_a_index": 0,
      "knot_b_id": 42,
      "relationship": "follow_up"
    },
    {
      "action": "retrieve",
      "type": "appointment",
      "limit": 5
    }
  ]
}

RULES FOR ACTIONS:
- knot_index refers to the 0-based index of a knot created in the same actions array
- knot_b_id refers to an existing knot ID from the database
- entity_name in link_entity should match the name of an entity being created OR an existing entity
- retrieve returns existing knots matching the filter — use this to show relevant data to the user ON SCREEN
- You MUST use retrieve whenever you talk about existing data — without it the user sees nothing
- retrieve with no type filter returns all recent knots; with a type filter returns only that type
- You can have zero actions if the user is just chatting and there is nothing to store or show
- If you are unsure about a field, set "reply" to ask a clarifying question and skip that action

EXISTING ENTITIES:
%s

RECENT KNOTS:
%s

DATABASE SCHEMA:
%s`

func main() {
	dsn := envOr("DB_DSN", "root:nodino@tcp(mariadb:3306)/nodino")
	ollamaURL = envOr("OLLAMA_URL", "http://192.168.0.132:11434")
	ollamaModel = envOr("OLLAMA_MODEL", "devstral-2:123b-cloud")
	piperURL = envOr("PIPER_URL", "http://piper:5000")

	var err error
	for i := 0; i < 30; i++ {
		db, err = sql.Open("mysql", dsn+"?parseTime=true&multiStatements=false")
		if err == nil {
			err = db.Ping()
		}
		if err == nil {
			break
		}
		log.Printf("waiting for database... (%v)", err)
		time.Sleep(2 * time.Second)
	}
	if err != nil {
		log.Fatalf("could not connect to database: %v", err)
	}
	log.Println("connected to database")

	http.HandleFunc("/api/conversation/start", handleConversationStart)
	http.HandleFunc("/api/conversation/end", handleConversationEnd)
	http.HandleFunc("/api/chat", handleChat)
	http.HandleFunc("/api/speak", handleSpeak)
	http.HandleFunc("/api/knots", handleKnots)

	log.Println("backend listening on :8080")
	log.Fatal(http.ListenAndServe(":8080", nil))
}

// --- Conversation management ---

func handleConversationStart(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	result, err := db.Exec("INSERT INTO conversations () VALUES ()")
	if err != nil {
		writeJSON(w, 500, map[string]string{"error": err.Error()})
		return
	}
	id, _ := result.LastInsertId()
	convHistoryMu.Lock()
	convHistory[int(id)] = []message{}
	convHistoryMu.Unlock()
	writeJSON(w, 200, map[string]int64{"conversation_id": id})
}

func handleConversationEnd(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var req struct {
		ConversationID int `json:"conversation_id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, 400, map[string]string{"error": "invalid request"})
		return
	}

	// Generate summary from conversation knots
	summary := generateSummary(req.ConversationID)
	db.Exec("UPDATE conversations SET ended_at = NOW(), summary = ? WHERE id = ?", summary, req.ConversationID)

	convHistoryMu.Lock()
	delete(convHistory, req.ConversationID)
	convHistoryMu.Unlock()

	writeJSON(w, 200, map[string]string{"status": "ended", "summary": summary})
}

func generateSummary(convID int) string {
	rows, err := db.Query("SELECT content, type FROM knots WHERE conversation_id = ? ORDER BY created_at", convID)
	if err != nil {
		return ""
	}
	defer rows.Close()

	var parts []string
	for rows.Next() {
		var content, ktype string
		rows.Scan(&content, &ktype)
		parts = append(parts, fmt.Sprintf("[%s] %s", ktype, content))
	}
	if len(parts) == 0 {
		return "No knots recorded"
	}
	if len(parts) > 5 {
		parts = parts[:5]
	}
	return strings.Join(parts, "; ")
}

// --- Core chat endpoint ---

func handleChat(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var req struct {
		ConversationID int    `json:"conversation_id"`
		Message        string `json:"message"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, 400, map[string]string{"error": "invalid request"})
		return
	}

	// Build context
	entities := getEntitiesText()
	recentKnots := getRecentKnotsText(req.ConversationID)
	schema, _ := getSchemaText()
	systemPrompt := fmt.Sprintf(systemPromptTemplate, entities, recentKnots, schema)

	// Get conversation history
	convHistoryMu.RLock()
	history := convHistory[req.ConversationID]
	convHistoryMu.RUnlock()

	// Build messages for Ollama
	msgs := []message{{Role: "system", Content: systemPrompt}}
	// Keep last 20 messages for context
	if len(history) > 20 {
		msgs = append(msgs, history[len(history)-20:]...)
	} else {
		msgs = append(msgs, history...)
	}
	msgs = append(msgs, message{Role: "user", Content: req.Message})

	// Call Ollama
	aiReply, err := queryOllama(msgs)
	if err != nil {
		log.Printf("ollama error: %v", err)
		writeJSON(w, 502, map[string]string{"error": "AI service unavailable: " + err.Error()})
		return
	}

	// Parse structured response — strip markdown code fences if present
	cleanJSON := extractJSONFromResponse(aiReply)
	var agentResp agentResponse
	if err := json.Unmarshal([]byte(cleanJSON), &agentResp); err != nil {
		log.Printf("failed to parse agent JSON: %v, raw: %s", err, cleanJSON)
		// Fallback: treat entire response as reply text
		agentResp = agentResponse{Reply: aiReply}
	}

	// Execute actions
	createdKnots, retrievedKnots, createdEntities := executeActions(agentResp.Actions, req.ConversationID)

	// Update conversation history
	convHistoryMu.Lock()
	convHistory[req.ConversationID] = append(convHistory[req.ConversationID],
		message{Role: "user", Content: req.Message},
		message{Role: "assistant", Content: agentResp.Reply},
	)
	convHistoryMu.Unlock()

	// Build response
	allKnots := append(createdKnots, retrievedKnots...)
	writeJSON(w, 200, chatResponse{
		Reply:    agentResp.Reply,
		Knots:    allKnots,
		Entities: createdEntities,
	})
}

type agentResponse struct {
	Reply   string         `json:"reply"`
	Actions []agentAction  `json:"actions"`
}

type agentAction struct {
	Action       string `json:"action"`
	// create_knot fields
	Content      string `json:"content,omitempty"`
	Type         string `json:"type,omitempty"`
	Importance   int    `json:"importance,omitempty"`
	OccursAt     string `json:"occurs_at,omitempty"`
	Status       string `json:"status,omitempty"`
	// create_entity fields
	Name         string `json:"name,omitempty"`
	Kind         string `json:"kind,omitempty"`
	Description  string `json:"description,omitempty"`
	// link_entity fields
	KnotIndex    int    `json:"knot_index,omitempty"`
	EntityName   string `json:"entity_name,omitempty"`
	Role         string `json:"role,omitempty"`
	// create_nodino fields
	KnotAIndex   int    `json:"knot_a_index,omitempty"`
	KnotBID      int    `json:"knot_b_id,omitempty"`
	Relationship string `json:"relationship,omitempty"`
	// retrieve fields
	Limit        int    `json:"limit,omitempty"`
}

type knotJSON struct {
	ID         int     `json:"id"`
	Content    string  `json:"content"`
	Type       string  `json:"type"`
	Importance int     `json:"importance"`
	OccursAt   *string `json:"occurs_at,omitempty"`
	Status     *string `json:"status,omitempty"`
	CreatedAt  string  `json:"created_at"`
}

type entityJSON struct {
	ID          int    `json:"id"`
	Name        string `json:"name"`
	Kind        string `json:"kind"`
	Description string `json:"description"`
}

type chatResponse struct {
	Reply    string       `json:"reply"`
	Knots    []knotJSON   `json:"knots,omitempty"`
	Entities []entityJSON `json:"entities,omitempty"`
}

func executeActions(actions []agentAction, convID int) (created []knotJSON, retrieved []knotJSON, entities []entityJSON) {
	// Track created knot IDs by index for linking
	knotIDs := make(map[int]int64)
	// Track entity names to IDs
	entityMap := make(map[string]int64)

	for i, a := range actions {
		switch a.Action {
		case "create_knot":
			imp := a.Importance
			if imp < 1 {
				imp = 3
			}
			if imp > 5 {
				imp = 5
			}
			var occursAt interface{}
			if a.OccursAt != "" && a.OccursAt != "null" {
				occursAt = a.OccursAt
			}
			var status interface{}
			if a.Status != "" && a.Status != "null" {
				status = a.Status
			} else if a.Type == "task" {
				status = "todo"
			}
			result, err := db.Exec(
				"INSERT INTO knots (conversation_id, content, type, importance, occurs_at, status) VALUES (?, ?, ?, ?, ?, ?)",
				convID, a.Content, a.Type, imp, occursAt, status,
			)
			if err != nil {
				log.Printf("create_knot error: %v", err)
				continue
			}
			id, _ := result.LastInsertId()
			knotIDs[i] = id
			k := knotJSON{
				ID:         int(id),
				Content:    a.Content,
				Type:       a.Type,
				Importance: imp,
				CreatedAt:  time.Now().Format("2006-01-02 15:04:05"),
			}
			if occursAt != nil {
				s := a.OccursAt
				k.OccursAt = &s
			}
			if status != nil {
				s := status.(string)
				k.Status = &s
			}
			created = append(created, k)

		case "create_entity":
			// Check if entity already exists
			var existingID int64
			err := db.QueryRow("SELECT id FROM entities WHERE name = ?", a.Name).Scan(&existingID)
			if err == nil {
				// Update existing entity
				if a.Description != "" {
					db.Exec("UPDATE entities SET description = ?, updated_at = NOW() WHERE id = ?", a.Description, existingID)
				}
				entityMap[a.Name] = existingID
				entities = append(entities, entityJSON{
					ID: int(existingID), Name: a.Name, Kind: a.Kind, Description: a.Description,
				})
				continue
			}
			result, err := db.Exec(
				"INSERT INTO entities (name, kind, description) VALUES (?, ?, ?)",
				a.Name, a.Kind, a.Description,
			)
			if err != nil {
				log.Printf("create_entity error: %v", err)
				continue
			}
			id, _ := result.LastInsertId()
			entityMap[a.Name] = id
			entities = append(entities, entityJSON{
				ID: int(id), Name: a.Name, Kind: a.Kind, Description: a.Description,
			})

		case "link_entity":
			knotID, ok := knotIDs[a.KnotIndex]
			if !ok {
				continue
			}
			entityID, ok := entityMap[a.EntityName]
			if !ok {
				// Try to find existing entity
				err := db.QueryRow("SELECT id FROM entities WHERE name = ?", a.EntityName).Scan(&entityID)
				if err != nil {
					continue
				}
			}
			db.Exec(
				"INSERT IGNORE INTO knot_entities (knot_id, entity_id, role) VALUES (?, ?, ?)",
				knotID, entityID, a.Role,
			)

		case "create_nodino":
			knotAID, ok := knotIDs[a.KnotAIndex]
			if !ok {
				continue
			}
			if a.KnotBID <= 0 {
				continue
			}
			rel := a.Relationship
			if rel == "" {
				rel = "related"
			}
			db.Exec(
				"INSERT INTO nodinos (knot_a_id, knot_b_id, relationship) VALUES (?, ?, ?)",
				knotAID, a.KnotBID, rel,
			)

		case "retrieve":
			retrieved = append(retrieved, retrieveKnots(a.Type, a.Limit)...)
		}
	}
	return
}

func retrieveKnots(ktype string, limit int) []knotJSON {
	if limit <= 0 {
		limit = 10
	}
	var rows *sql.Rows
	var err error
	if ktype != "" {
		rows, err = db.Query(
			"SELECT id, content, type, importance, occurs_at, status, created_at FROM knots WHERE type = ? ORDER BY created_at DESC LIMIT ?",
			ktype, limit,
		)
	} else {
		rows, err = db.Query(
			"SELECT id, content, type, importance, occurs_at, status, created_at FROM knots ORDER BY created_at DESC LIMIT ?",
			limit,
		)
	}
	if err != nil {
		return nil
	}
	defer rows.Close()

	var knots []knotJSON
	for rows.Next() {
		var k knotJSON
		var occursAt sql.NullTime
		var status sql.NullString
		var createdAt time.Time
		rows.Scan(&k.ID, &k.Content, &k.Type, &k.Importance, &occursAt, &status, &createdAt)
		k.CreatedAt = createdAt.Format("2006-01-02 15:04:05")
		if occursAt.Valid {
			s := occursAt.Time.Format("2006-01-02 15:04:05")
			k.OccursAt = &s
		}
		if status.Valid {
			s := status.String
			k.Status = &s
		}
		knots = append(knots, k)
	}
	return knots
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

// --- Knots CRUD ---

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
	writeJSON(w, 200, retrieveKnots(ktype, limit))
}

func handleUpdateKnot(w http.ResponseWriter, r *http.Request) {
	var req struct {
		ID         int    `json:"id"`
		Content    string `json:"content,omitempty"`
		Importance int    `json:"importance,omitempty"`
		Status     string `json:"status,omitempty"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.ID == 0 {
		writeJSON(w, 400, map[string]string{"error": "invalid request"})
		return
	}

	if req.Content != "" {
		db.Exec("UPDATE knots SET content = ? WHERE id = ?", req.Content, req.ID)
	}
	if req.Importance >= 1 && req.Importance <= 5 {
		db.Exec("UPDATE knots SET importance = ? WHERE id = ?", req.Importance, req.ID)
	}
	if req.Status != "" {
		db.Exec("UPDATE knots SET status = ? WHERE id = ?", req.Status, req.ID)
	}

	writeJSON(w, 200, map[string]string{"status": "updated"})
}

// --- Ollama integration ---

func queryOllama(msgs []message) (string, error) {
	payload := map[string]interface{}{
		"model":    ollamaModel,
		"messages": msgs,
		"stream":   false,
		"format":   "json",
	}

	body, err := json.Marshal(payload)
	if err != nil {
		return "", err
	}

	resp, err := http.Post(ollamaURL+"/api/chat", "application/json", bytes.NewReader(body))
	if err != nil {
		return "", fmt.Errorf("failed to reach Ollama: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("failed to read Ollama response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("ollama returned %d: %s", resp.StatusCode, string(respBody))
	}

	var result struct {
		Message struct {
			Content string `json:"content"`
		} `json:"message"`
	}
	if err := json.Unmarshal(respBody, &result); err != nil {
		return "", fmt.Errorf("failed to parse Ollama response: %w", err)
	}

	return result.Message.Content, nil
}

// --- Context helpers ---

func getEntitiesText() string {
	rows, err := db.Query("SELECT name, kind, description FROM entities ORDER BY updated_at DESC LIMIT 50")
	if err != nil {
		return "(none)"
	}
	defer rows.Close()

	var buf strings.Builder
	count := 0
	for rows.Next() {
		var name, kind string
		var desc sql.NullString
		rows.Scan(&name, &kind, &desc)
		buf.WriteString(fmt.Sprintf("- %s (%s)", name, kind))
		if desc.Valid && desc.String != "" {
			buf.WriteString(": " + desc.String)
		}
		buf.WriteString("\n")
		count++
	}
	if count == 0 {
		return "(none yet)"
	}
	return buf.String()
}

func getRecentKnotsText(convID int) string {
	rows, err := db.Query(
		"SELECT id, content, type, importance, occurs_at FROM knots ORDER BY created_at DESC LIMIT 30",
	)
	if err != nil {
		return "(none)"
	}
	defer rows.Close()

	var buf strings.Builder
	count := 0
	for rows.Next() {
		var id, importance int
		var content, ktype string
		var occursAt sql.NullTime
		rows.Scan(&id, &content, &ktype, &importance, &occursAt)
		buf.WriteString(fmt.Sprintf("- [id:%d] [%s] (imp:%d) %s", id, ktype, importance, content))
		if occursAt.Valid {
			buf.WriteString(fmt.Sprintf(" (occurs: %s)", occursAt.Time.Format("2006-01-02 15:04")))
		}
		buf.WriteString("\n")
		count++
	}
	if count == 0 {
		return "(none yet)"
	}
	return buf.String()
}

func getSchemaText() (string, error) {
	rows, err := db.Query("SHOW TABLES")
	if err != nil {
		return "", err
	}
	defer rows.Close()

	var buf strings.Builder
	var tables []string
	for rows.Next() {
		var table string
		if err := rows.Scan(&table); err != nil {
			return "", err
		}
		tables = append(tables, table)
	}

	for _, table := range tables {
		buf.WriteString(fmt.Sprintf("TABLE: %s\n", table))
		colRows, err := db.Query(fmt.Sprintf("SHOW COLUMNS FROM `%s`", table))
		if err != nil {
			return "", err
		}
		for colRows.Next() {
			var field, colType, null, key string
			var defVal, extra sql.NullString
			if err := colRows.Scan(&field, &colType, &null, &key, &defVal, &extra); err != nil {
				colRows.Close()
				return "", err
			}
			buf.WriteString(fmt.Sprintf("  - %s %s", field, colType))
			if key == "PRI" {
				buf.WriteString(" PRIMARY KEY")
			}
			if extra.Valid && extra.String != "" {
				buf.WriteString(" " + extra.String)
			}
			buf.WriteString("\n")
		}
		colRows.Close()
		buf.WriteString("\n")
	}

	return buf.String(), nil
}

// --- Helpers ---

var jsonFenceRe = regexp.MustCompile("(?s)```(?:json)?\\s*\n?(.*?)```")

func extractJSONFromResponse(raw string) string {
	// Try raw string first
	trimmed := strings.TrimSpace(raw)
	if len(trimmed) > 0 && trimmed[0] == '{' {
		return trimmed
	}
	// Try extracting from markdown code fence
	matches := jsonFenceRe.FindStringSubmatch(raw)
	if len(matches) >= 2 {
		return strings.TrimSpace(matches[1])
	}
	return trimmed
}

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
