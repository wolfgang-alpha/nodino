---
name: nodino core decisions
description: Key architectural decisions for the nodino second-brain agent project
type: project
---

- Whisper STT: REMOVED — too early for voice input, using keyboard text input instead
- TTS: Piper via custom Flask HTTP wrapper (piper/serve.py), containerized — agent speaks replies aloud
- Frontend served at port 8889 (HTTP) / 8890 (HTTPS, self-signed cert for remote mic access)
- Ollama host: 192.168.0.132:11434, model: devstral-2:123b-cloud
- Markdown export: deferred, not in v1
- Data cards in center screen: interactive, clickable to edit, draggable to pin
- Conversation bubbles: green user bubbles float to bottom-right, blue agent bubbles float to bottom-left
- Auto dark/light mode via prefers-color-scheme
- Stack: Go backend, vanilla JS frontend, nginx proxy, MariaDB, all dockerized (4 services)
- Reference project: /home/me/ponder (similar architecture)

**Why:** User wants a text-input second-brain agent with TTS output, minimal but interactive UI with aesthetic bubble animations.
**How to apply:** Follow ponder's patterns for docker-compose, nginx proxy, Go+Ollama integration. UI: three-column layout (agent bubbles | data cards | user bubbles), fading over time.
