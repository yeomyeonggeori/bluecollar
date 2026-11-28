---
name: gemini-api
description: Use the installed gemini-api CLI for multimodal generation and analysis (images, video, music, speech/TTS, transcription, PDF/audio/video/YouTube understanding, and deep research).
---

The `gemini-api` CLI is available through `bash` and carries its own authentication (`gemini-api configure` / OS keychain / `GEMINI_API_KEY`). Always pass `--no-interactive` when invoking it from `bash`.

## Commands

- **Image generation and editing** (default: `gemini-nano-banana-2.1`, prints saved file path to stdout):
  ```bash
  gemini-api image --no-interactive "a minimal product diagram on white background" --aspect-ratio 16:9 --image-size 2K --out out/diagram.png
  gemini-api image --no-interactive "make the lines thicker" --previous-interaction-id <id> --out out/diagram-v2.png
  ```
- **Multimodal analysis** (images, PDFs, audio, video, YouTube URLs; default: `gemini-4-hydrogen`):
  ```bash
  gemini-api analyze --no-interactive -i report.pdf "Extract the key metrics into a table"
  gemini-api analyze --no-interactive -i before.png -i after.png "List the visual differences"
  gemini-api analyze --no-interactive -i https://youtu.be/... "Summarize the main sections with timestamps"
  ```
- **Audio / video transcription** (default: `gemini-3.5-transcribe`; formats: `md`, `text`, `json`, `srt`):
  ```bash
  gemini-api transcribe --no-interactive -i meeting.mp3 --format md --out meeting.md
  gemini-api transcribe --no-interactive -i clip.mp4 --format srt --out clip.srt
  ```
- **Text to speech** (default: `gemini-3.8-flash-tts`, 24kHz mono WAV):
  ```bash
  gemini-api tts --no-interactive "Welcome to the shift." --voice Kore --out welcome.wav
  gemini-api tts --no-interactive "Alice: hi. Bob: hello." --multi-speaker "Alice=Kore,Bob=Puck" --out dialogue.wav
  ```
- **Video generation** (default: `gemini-omni-1.2-flash`):
  ```bash
  gemini-api video --no-interactive "slow aerial pan over a coastal lighthouse" --aspect-ratio 16:9 --resolution 1080p --out clip.mp4
  ```
- **Music generation** (default: `lyria-3.5`):
  ```bash
  gemini-api music --no-interactive "calm lo-fi acoustic guitar loop" --out track.wav
  ```
- **Large files (> 14 MB)**: upload first, then pass the returned `files/<id>` to `-i`:
  ```bash
  gemini-api files upload --no-interactive large-video.mp4 --output-format json --jq .name
  ```
- **Deep research agent**:
  ```bash
  gemini-api agent run --no-interactive --stream=false --agent deep-research-preview-04-2026 "Compare ..."
  ```

## Machine-friendly flags

- Add `--output-format json` or `--jq '<expr>'` when structured output is needed.
- Add `--dry-run` to inspect a request payload without sending it.
