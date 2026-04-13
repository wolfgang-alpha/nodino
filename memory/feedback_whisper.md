---
name: whisper model too weak
description: The base Whisper model is insufficient for the user's speech recognition needs
type: feedback
---

The `base` Whisper ASR model (set in docker-compose.yml ASR_MODEL env var) is too weak for the user's use case.

**Why:** Transcription quality not good enough for natural conversation input.
**How to apply:** Next session, upgrade to `small` or `medium` model, or consider switching to whisper.cpp. Trade-off is speed vs accuracy — user prioritizes accuracy. This is a one-line change: `ASR_MODEL: base` → `ASR_MODEL: small` or `ASR_MODEL: medium` in docker-compose.yml.
