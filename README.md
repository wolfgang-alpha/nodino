<p align="center">
  <img src="logo.png" alt="nodino" width="500">
</p>

---

A voice-first second-brain agent that extracts structured knowledge from natural conversation, stores it in a semantic memory palace, and displays it as fading data cards in real time.

You talk (or type), the agent listens. Behind the scenes it creates knots (events, tasks, ideas, observations, ...), discovers entities (people, places, things), and weaves relationships (nodinos) between them. The UI shows only data — no chat transcript, just knowledge flowing in from the bottom and fading out over time. Semantic search lets you retrieve anything by meaning, not just keywords.

## Architecture

| Component | Role |
|-----------|------|
| **backend** | Go server on host, spawns `claude -p` for AI (Max subscription) |
| **mempalace-mcp** | MCP server exposing mempalace tools to Claude |
| **mempalace-api** | Semantic memory store + knowledge graph (REST) |
| **chroma** | ChromaDB vector database (internal to mempalace) |
| **whisper** | Speech-to-text via faster-whisper (medium.en) |
| **piper** | Text-to-speech via Piper TTS |
| **nginx** | Static frontend + HTTPS reverse proxy |

Claude Code is the AI backbone — the same tools available in the webui are available in your terminal Claude Code sessions via MCP.

## Quick Start

Prerequisites: Claude Code CLI installed with a Max subscription, Docker.

```bash
# Start Docker services
docker compose up --build -d

# Build the backend
docker run --rm -v ./backend:/app -w /app golang:1.22-alpine \
  sh -c "go mod tidy && CGO_ENABLED=0 go build -o server main.go"

# Run the backend on the host
MCP_CONFIG_PATH=./mcp.json ./backend/server
```

Open `https://<host>:8890` (self-signed cert) or `http://localhost:8889`.

### Nextcloud Calendar

If you have a CalDAV MCP server running (e.g., on port 8000), calendar tools are automatically available. Update `mcp.json` with your server's URL.

## How It Works

1. Speak into the microphone or type a message
2. Audio is transcribed by Whisper; text is sent to Claude via the CLI
3. Claude uses MCP tools to store knots, search knowledge, manage entities, link relationships, and query/create calendar events
4. New data cards appear in the center column, agent replies are spoken via Piper TTS
5. Cards age and fade out over time; drag to pin important ones

## MCP Tools

The same MCP servers power both the webui and your terminal Claude Code sessions:

- **mempalace** — store/search drawers, knowledge graph facts, entity queries
- **caldav** — list/create/update/delete calendar events (if configured)

Add `mcp.json` to your Claude Code project or pass `--mcp-config ./mcp.json` to use these tools from the terminal.

## Memory Palace

- **Drawers** — verbatim knowledge chunks stored in ChromaDB, searchable by meaning
- **Knowledge Graph** — entities and temporal relationships in SQLite
- **Semantic Search** — find anything by what it means, not just what it says

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

Semantic memory and knowledge graph powered by [MemPalace](https://github.com/MemPalace/mempalace) — a personal memory system for AI agents that stores verbatim knowledge in ChromaDB with a temporal knowledge graph in SQLite.
