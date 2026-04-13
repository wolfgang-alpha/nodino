<p align="center">
  <img src="logo.png" alt="nodino" width="200">
</p>

<h1 align="center">nodino</h1>

<p align="center"><em>"little knot" in Italian</em></p>

---

A second-brain agent that extracts structured knowledge from natural conversation, stores it as a connected graph of "knots" in MariaDB, and displays them as fading data cards in real time.

You talk, the agent listens. Behind the scenes it creates knots (events, tasks, ideas, observations, ...), discovers entities (people, places, things), and weaves relationships (nodinos) between them. The UI shows only data — no chat transcript, just knowledge flowing in from the bottom and fading out over time.

## Architecture

| Service | Role |
|---------|------|
| **backend** | Go API server, Ollama orchestration, structured JSON actions |
| **mariadb** | Knowledge store (knots, entities, nodinos, conversations) |
| **piper** | Text-to-speech via Piper TTS |
| **nginx** | Static frontend + HTTPS reverse proxy |
| **Ollama** | External LLM (devstral-2) |

## Quick Start

```bash
docker compose up --build
```

Open `https://<host>:8890` (self-signed cert) or `http://localhost:8889`.

## How It Works

1. Type a message in natural language
2. The backend sends it to Ollama with full context (recent knots, known entities, schema)
3. Ollama returns structured JSON actions: `create_knot`, `create_entity`, `link_entity`, `create_nodino`, `retrieve`
4. The backend executes these as parameterized SQL — never raw LLM-generated queries
5. New data cards appear in the center column, agent replies are spoken via Piper TTS
6. Cards age and fade out over time; drag to pin important ones

## Knowledge Graph

- **Knots** — atomic pieces of knowledge (events, tasks, reminders, ideas, moods, ...)
- **Entities** — people, places, organizations, animals, things
- **Nodinos** — relationships between knots (same_thread, follow_up, caused_by, related)

## UI Features

- Three-column layout: agent bubbles (left) | data cards (center) | user bubbles (right)
- Drag-to-pin cards to prevent fade-out
- Double-click cards to edit inline
- Kanban board for task management (toggle via top-right button)
- Auto dark/light mode
- TTS playback of agent responses
