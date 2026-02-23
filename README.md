# AI Bridge

AI Bridge is a Matrix-ish bridge for Beeper that brings AI Chats into your favorite chat app.

Batteries included - one click setup (for [Beeper Plus](https://www.beeper.com/plus)), all models. It also comes with a faithful Go port of [OpenClaw](https://github.com/openclaw/openclaw) (formerly knows as Moltbot (formerly known as Clawdbot)) called Beep.

Coming soon to Beeper Desktop as an experiment. Join the [Developer Community](beeper://connect) on [Matrix](https://matrix.to/#/#beeper-developers:beeper.com?via=beeper.com) for early access.

Connect all your chats with one click and manage your inbox with agents. Supports image generation, reminders, web search, and memory. Create basic AI Chats to talk to models with no tools and customizable system prompt.

Made by humans using agentic coding.

## Status

Experimental Matrix ↔ AI bridge for Beeper, built on top of [mautrix/bridgev2](https://pkg.go.dev/maunium.net/go/mautrix/bridgev2). Works best with alpha versions of Beeper Desktop. Supports any OpenAI-compatible provider (including OpenRouter).

## Highlights

- Multi-provider routing with prefixed model IDs (e.g. `openai/...`, `anthropic/...`)
- Per-model chats (each model shows up as its own contact)
- Streaming responses
- Multimodal input (images, PDFs, audio, video) when supported by the model
- Per-room settings (model, temperature, system prompt, context limits, tools)
- Login flows for Beeper, Magic Proxy, or custom (BYOK)
- OpenClaw-style memory search (stored in the bridge DB)

## Docs

- `docs/matrix-ai-matrix-spec-v1.md`: Full Matrix transport spec (events, streaming, approvals, state, and schema examples).
- `docs/bridge-orchestrator.md`: One-command bridge management in this repo.

## Bridge Orchestrator

Use `tools/bridges` to manage isolated bridge instances for Beeper.

### Configured bridge instances

- `ai`
- `codex`
- `opencode`

Instances are defined in `bridges.manifest.yml`.

### Login and run

```bash
./tools/bridges login --env prod
./tools/bridges run ai
```

### Run other bridges

```bash
./tools/bridges run codex
./tools/bridges run opencode
```

### Simple wrapper

```bash
./run.sh ai
./run.sh codex
./run.sh opencode
```

`run.sh` checks login first and prompts you to login if needed, then runs the selected bridge.

### Useful commands

```bash
./tools/bridges list
./tools/bridges status
./tools/bridges logs ai --follow
./tools/bridges down ai
./tools/bridges restart ai
./tools/bridges whoami
```

## Build

Requires libolm for encryption support.

```bash
./build.sh
```

Or use Docker:

```bash
docker build -t ai .
```
