<p align="center">
  <img src="logo.png" alt="nodino" width="500">
</p>

---

A kanban board and todo list with an MCP server for Claude Code integration.

Manage tasks across four columns — To Do, In Progress, Waiting, Done — via drag-and-drop in the browser or MCP tools from the CLI. Data is stored in a local SQLite database.

## Architecture

| Component | Role |
|-----------|------|
| **backend** | Go server on host — task CRUD API with SQLite |
| **nodino-mcp** | MCP server exposing task tools to Claude Code |
| **nginx** | Static frontend + HTTPS reverse proxy |

## Quick Start

Prerequisites: Docker.

```bash
# Start Docker services
docker compose up --build -d

# Build the backend
docker run --rm -v ./backend:/app -w /app golang:latest \
  sh -c "go mod tidy && CGO_ENABLED=0 go build -o server main.go"

# Run the backend on the host
./backend/server
```

Open `https://<host>:8890` (self-signed cert) or `http://localhost:8889`.

## MCP Tools

The nodino MCP server (`:8003`) exposes task management to Claude Code:

- `list_tasks(limit)` — list all tasks
- `create_task(content, importance)` — create a new task (starts as todo)
- `update_status(task_id, status)` — change status (todo, in_progress, waiting, done)
- `delete_task(task_id)` — remove a task

Register with: `claude mcp add --scope user --transport http nodino http://localhost:8003/mcp`

## UI Features

- Full-screen kanban board with four columns (To Do, In Progress, Waiting, Done)
- Drag-and-drop cards between columns
- Create and delete tasks
- Todo list view (todo + in-progress only) with status picker
- Auto dark/light mode
