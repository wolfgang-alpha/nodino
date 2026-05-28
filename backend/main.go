package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"
)

var mempalaceURL string

func main() {
	mempalaceURL = envOr("MEMPALACE_URL", "http://localhost:8002")

	waitForMempalace()

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

// --- Knots API ---

func handleKnots(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		handleGetKnots(w, r)
	case http.MethodPost:
		handleCreateKnot(w, r)
	case http.MethodPut:
		handleUpdateKnot(w, r)
	case http.MethodDelete:
		handleDeleteKnot(w, r)
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

func handleCreateKnot(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Content    string `json:"content"`
		Type       string `json:"type"`
		Importance int    `json:"importance"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.Content == "" {
		writeJSON(w, 400, map[string]string{"error": "content is required"})
		return
	}

	if req.Type == "" {
		req.Type = "task"
	}
	if req.Importance < 1 || req.Importance > 5 {
		req.Importance = 3
	}

	status := "todo"
	tagged := fmt.Sprintf("[importance:%d][status:%s] %s", req.Importance, status, req.Content)

	drawerID, err := mpStoreDrawer("nodino", req.Type, tagged)
	if err != nil {
		writeJSON(w, 500, map[string]string{"error": err.Error()})
		return
	}

	mpAddFact(drawerID, "has_status", status)

	writeJSON(w, 201, knotJSON{
		ID:         drawerID,
		Content:    req.Content,
		Type:       req.Type,
		Importance: req.Importance,
		Status:     &status,
	})
}

func handleUpdateKnot(w http.ResponseWriter, r *http.Request) {
	var req struct {
		ID     string `json:"id"`
		Status string `json:"status,omitempty"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.ID == "" {
		writeJSON(w, 400, map[string]string{"error": "id is required"})
		return
	}

	if req.Status == "" {
		writeJSON(w, 400, map[string]string{"error": "status is required"})
		return
	}

	// Find the current drawer
	results, err := mpSearch("*", "task", 200)
	if err != nil {
		writeJSON(w, 500, map[string]string{"error": err.Error()})
		return
	}

	var found *searchResult
	for _, sr := range results {
		if sr.ID == req.ID {
			found = &sr
			break
		}
	}
	if found == nil {
		writeJSON(w, 404, map[string]string{"error": "task not found"})
		return
	}

	knot := parseDrawerToKnot(*found)

	// Rebuild content with new status tag
	tagged := fmt.Sprintf("[importance:%d][status:%s]", knot.Importance, req.Status)
	if knot.OccursAt != nil {
		tagged += fmt.Sprintf("[occurs_at:%s]", *knot.OccursAt)
	}
	tagged += " " + knot.Content

	// Replace: delete old, create new
	mpDeleteDrawer(req.ID)
	newID, err := mpStoreDrawer("nodino", "task", tagged)
	if err != nil {
		writeJSON(w, 500, map[string]string{"error": err.Error()})
		return
	}

	mpAddFact(newID, "has_status", req.Status)

	writeJSON(w, 200, map[string]string{"status": "updated", "id": newID})
}

func handleDeleteKnot(w http.ResponseWriter, r *http.Request) {
	id := r.URL.Query().Get("id")
	if id == "" {
		writeJSON(w, 400, map[string]string{"error": "id is required"})
		return
	}

	if err := mpDeleteDrawer(id); err != nil {
		writeJSON(w, 500, map[string]string{"error": err.Error()})
		return
	}

	writeJSON(w, 200, map[string]string{"status": "deleted"})
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
		return nil, fmt.Errorf("parse error: %w", err)
	}
	return parsed.Results, nil
}

func mpStoreDrawer(wing, room, content string) (string, error) {
	payload := map[string]string{"wing": wing, "room": room, "content": content}
	body, _ := json.Marshal(payload)
	resp, err := http.Post(mempalaceURL+"/drawers", "application/json", bytes.NewReader(body))
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	respBody, _ := io.ReadAll(resp.Body)

	if resp.StatusCode != 200 {
		return "", fmt.Errorf("store error %d: %s", resp.StatusCode, string(respBody))
	}

	var result struct {
		DrawerID string `json:"drawer_id"`
	}
	if err := json.Unmarshal(respBody, &result); err != nil {
		return "", fmt.Errorf("parse error: %w", err)
	}
	return result.DrawerID, nil
}

func mpDeleteDrawer(drawerID string) error {
	req, _ := http.NewRequest(http.MethodDelete, mempalaceURL+"/drawers/"+drawerID, nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	resp.Body.Close()
	if resp.StatusCode != 200 {
		return fmt.Errorf("delete error %d", resp.StatusCode)
	}
	return nil
}

func mpAddFact(subject, predicate, object string) error {
	payload := map[string]string{"subject": subject, "predicate": predicate, "object": object}
	body, _ := json.Marshal(payload)
	resp, err := http.Post(mempalaceURL+"/kg/facts", "application/json", bytes.NewReader(body))
	if err != nil {
		return err
	}
	resp.Body.Close()
	return nil
}

func mpInvalidateFact(subject, predicate, object string) error {
	payload := map[string]string{"subject": subject, "predicate": predicate, "object": object}
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

// --- Types ---

type knotJSON struct {
	ID         string  `json:"id"`
	Content    string  `json:"content"`
	Type       string  `json:"type"`
	Importance int     `json:"importance"`
	OccursAt   *string `json:"occurs_at,omitempty"`
	Status     *string `json:"status,omitempty"`
	CreatedAt  string  `json:"created_at"`
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
