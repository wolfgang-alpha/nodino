# Nodino

Kanban board and todo list with SQLite storage and MCP server for Claude Code.

## Task statuses

todo, in_progress, waiting, done

## Importance

1-5 (1=trivial, 2=minor, 3=normal, 4=important, 5=urgent)

## Architecture

- Backend (Go, runs on host :8085) — task CRUD API with SQLite
- nodino-mcp (Docker :8003) — MCP server wrapping the REST API
- nginx (Docker :8889/:8890) — frontend + reverse proxy

## Running

```bash
docker compose up --build -d
docker run --rm -v ./backend:/app -w /app golang:latest \
  sh -c "go mod tidy && CGO_ENABLED=0 go build -o server main.go"
./backend/server
```

## API

- `GET /api/knots?limit=N` — list tasks
- `POST /api/knots` — create task `{content, importance}`
- `PUT /api/knots` — update status `{id, status}`
- `DELETE /api/knots?id=ID` — delete task
