# TODO

## Speech-to-Text

Add speech-to-text input via a microphone button alongside the text input. The Piper service (`./piper`) currently handles text-to-speech only.

Options to evaluate:
- **Whisper** (OpenAI) — tested with `whisper-base`, but accuracy was too low for practical use. Revisit with `whisper-small` or `whisper-medium` models.
- **whisper.cpp** — lighter alternative, could run as a sidecar container.
- **Browser Web Speech API** — zero-infrastructure option, but browser support varies.

The original UI concept had a sphere button for push-to-talk with a voice-stop keyword ("over"). This could be revisited once a reliable STT backend is in place.
