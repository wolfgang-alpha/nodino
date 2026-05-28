# Nodino

Task and knowledge CRUD webui backed by mempalace (standalone, see ~/docker/mempalace).

## Mempalace conventions

All nodino data uses `wing="nodino"`. The `room` is the knot type:
event, appointment, reminder, observation, mood, log, anecdote, idea, project, decision, contact, task

## Prefix tag encoding

Metadata is encoded as prefix tags in the drawer content:

```
[importance:N] where N is 1-5 (1=trivial, 2=minor, 3=normal, 4=important, 5=urgent)
[status:VALUE]  for tasks: backlog, todo, in_progress, done
[occurs_at:DATETIME] for time-sensitive items (ISO 8601)
```

Example: `store_drawer(wing="nodino", room="task", content="[importance:4][status:todo] Review the design mockups")`

Tasks MUST include a `[status:]` tag. Default to `[status:todo]` if not specified.

## Entities

Record entities as knowledge graph facts:
- `add_fact(subject="Alice", predicate="is_a", object="person")`
- `add_fact(subject="Alice", predicate="described_as", object="Project lead")`

Entity kinds: person, animal, place, organization, thing

## Relationships

Link related knowledge: `add_fact(subject="knot_id", predicate="related", object="other_id")`

Predicates: same_thread, follow_up, caused_by, related

## Task status updates

Invalidate the old status fact and add the new one:
```
invalidate_fact(subject="drawer_id", predicate="has_status", object="todo")
add_fact(subject="drawer_id", predicate="has_status", object="in_progress")
```

## Architecture

- Backend (Go, runs on host :8085) — knots CRUD API via mempalace REST
- whisper (Docker :9001) — speech-to-text
- piper (Docker :9000) — text-to-speech
- nginx (Docker :8889/:8890) — frontend + reverse proxy
- mempalace (standalone at ~/docker/mempalace, :8001 MCP / :8002 REST)

## Running

```bash
docker compose up --build -d
docker run --rm -v ./backend:/app -w /app golang:1.22-alpine \
  sh -c "go mod tidy && CGO_ENABLED=0 go build -o server main.go"
MEMPALACE_URL=http://localhost:8002 ./backend/server
```
