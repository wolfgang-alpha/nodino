package main

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"strconv"
	"time"

	_ "modernc.org/sqlite"
)

var db *sql.DB

func main() {
	dbPath := envOr("DB_PATH", "./nodino.db")
	initDB(dbPath)
	defer db.Close()

	http.HandleFunc("/api/knots", handleKnots)

	port := envOr("PORT", "8085")
	log.Printf("backend listening on :%s", port)
	log.Fatal(http.ListenAndServe(":"+port, nil))
}

func initDB(path string) {
	var err error
	db, err = sql.Open("sqlite", path)
	if err != nil {
		log.Fatal("failed to open database:", err)
	}

	_, err = db.Exec(`CREATE TABLE IF NOT EXISTS tasks (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		content TEXT NOT NULL,
		importance INTEGER NOT NULL DEFAULT 3,
		status TEXT NOT NULL DEFAULT 'todo',
		occurs_at TEXT,
		created_at TEXT NOT NULL
	)`)
	if err != nil {
		log.Fatal("failed to create table:", err)
	}

	log.Printf("database ready at %s", path)
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
	limitStr := q.Get("limit")
	limit := 50
	if limitStr != "" {
		if l, err := strconv.Atoi(limitStr); err == nil && l > 0 {
			limit = l
		}
	}

	rows, err := db.Query(
		"SELECT id, content, importance, status, occurs_at, created_at FROM tasks ORDER BY created_at DESC LIMIT ?",
		limit,
	)
	if err != nil {
		writeJSON(w, 500, map[string]string{"error": err.Error()})
		return
	}
	defer rows.Close()

	tasks := []taskJSON{}
	for rows.Next() {
		var t taskJSON
		var occursAt sql.NullString
		if err := rows.Scan(&t.ID, &t.Content, &t.Importance, &t.Status, &occursAt, &t.CreatedAt); err != nil {
			continue
		}
		if occursAt.Valid {
			t.OccursAt = &occursAt.String
		}
		tasks = append(tasks, t)
	}

	writeJSON(w, 200, tasks)
}

func handleCreateKnot(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Content    string  `json:"content"`
		Importance int     `json:"importance"`
		OccursAt   *string `json:"occurs_at,omitempty"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.Content == "" {
		writeJSON(w, 400, map[string]string{"error": "content is required"})
		return
	}

	if req.Importance < 1 || req.Importance > 5 {
		req.Importance = 3
	}

	now := time.Now().Format(time.RFC3339)
	result, err := db.Exec(
		"INSERT INTO tasks (content, importance, status, occurs_at, created_at) VALUES (?, ?, 'todo', ?, ?)",
		req.Content, req.Importance, req.OccursAt, now,
	)
	if err != nil {
		writeJSON(w, 500, map[string]string{"error": err.Error()})
		return
	}

	id, _ := result.LastInsertId()
	status := "todo"
	writeJSON(w, 201, taskJSON{
		ID:         fmt.Sprintf("%d", id),
		Content:    req.Content,
		Importance: req.Importance,
		Status:     status,
		OccursAt:   req.OccursAt,
		CreatedAt:  now,
	})
}

func handleUpdateKnot(w http.ResponseWriter, r *http.Request) {
	var req struct {
		ID     string `json:"id"`
		Status string `json:"status"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.ID == "" || req.Status == "" {
		writeJSON(w, 400, map[string]string{"error": "id and status are required"})
		return
	}

	res, err := db.Exec("UPDATE tasks SET status = ? WHERE id = ?", req.Status, req.ID)
	if err != nil {
		writeJSON(w, 500, map[string]string{"error": err.Error()})
		return
	}

	n, _ := res.RowsAffected()
	if n == 0 {
		writeJSON(w, 404, map[string]string{"error": "task not found"})
		return
	}

	writeJSON(w, 200, map[string]string{"status": "updated"})
}

func handleDeleteKnot(w http.ResponseWriter, r *http.Request) {
	id := r.URL.Query().Get("id")
	if id == "" {
		writeJSON(w, 400, map[string]string{"error": "id is required"})
		return
	}

	res, err := db.Exec("DELETE FROM tasks WHERE id = ?", id)
	if err != nil {
		writeJSON(w, 500, map[string]string{"error": err.Error()})
		return
	}

	n, _ := res.RowsAffected()
	if n == 0 {
		writeJSON(w, 404, map[string]string{"error": "task not found"})
		return
	}

	writeJSON(w, 200, map[string]string{"status": "deleted"})
}

// --- Types ---

type taskJSON struct {
	ID         string  `json:"id"`
	Content    string  `json:"content"`
	Importance int     `json:"importance"`
	Status     string  `json:"status"`
	OccursAt   *string `json:"occurs_at,omitempty"`
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
