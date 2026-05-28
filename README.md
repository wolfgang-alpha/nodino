<p align="center">
  <img src="logo.png" alt="nodino" width="500">
</p>

---

A task and knowledge management webui backed by [MemPalace](https://github.com/MemPalace/mempalace) — a semantic memory store with a temporal knowledge graph.

Nodino displays structured knowledge as data cards: tasks, events, observations, ideas, and more. Cards age and fade over time; drag to pin important ones. A kanban board and todo list provide focused task views.

## Architecture

| Component | Role |
|-----------|------|
| **backend** | Go server on host — knots CRUD API via mempalace REST |
| **whisper** | Speech-to-text via faster-whisper (medium.en) |
| **piper** | Text-to-speech via Piper TTS |
| **nginx** | Static frontend + HTTPS reverse proxy |

Mempalace runs as a standalone service (see [mempalace-docker-compose](https://github.com/wolfgang-alpha/mempalace-docker-compose)). Claude Code orchestrates everything via MCP tools.

## Quick Start

Prerequisites: Docker, standalone mempalace running on `:8002`.

```bash
# Start Docker services
docker compose up --build -d

# Build the backend
docker run --rm -v ./backend:/app -w /app golang:1.22-alpine \
  sh -c "go mod tidy && CGO_ENABLED=0 go build -o server main.go"

# Run the backend on the host
MEMPALACE_URL=http://localhost:8002 ./backend/server
```

Open `https://<host>:8890` (self-signed cert) or `http://localhost:8889`.

## Knowledge Types

event, appointment, reminder, observation, mood, log, anecdote, idea, project, decision, contact, task

## UI Features

- Three-column layout: agent bubbles (left) | data cards (center) | user bubbles (right)
- Voice input via microphone button + text input
- Drag-to-pin cards to prevent fade-out
- Double-click cards to edit inline
- Kanban board for task management
- Todo list view (todo + in-progress only)
- Auto dark/light mode
- TTS playback of agent responses

## Credits

Semantic memory and knowledge graph powered by [MemPalace](https://github.com/MemPalace/mempalace).
