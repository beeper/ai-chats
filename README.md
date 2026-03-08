# AI Bridge

AI Bridge is a Matrix-ish bridge for Beeper that brings AI Chats into your favorite chat app.

Batteries included - one click setup (for [Beeper Plus](https://www.beeper.com/plus)), all models. It also comes with a faithful Go port of [OpenClaw](https://github.com/openclaw/openclaw) (formerly knows as Moltbot (formerly known as Clawdbot)) called Beep.

Coming soon to Beeper Desktop as an experiment. Join the [Developer Community](beeper://connect) on [Matrix](https://matrix.to/#/#beeper-developers:beeper.com?via=beeper.com) for early access.

Connect all your chats with one click and manage your inbox with agents. Supports image generation, reminders, web search, and memory. Create direct model chats for simple conversations or agent chats for richer workflows.

Made by humans using agentic coding.

## Status

Experimental Matrix ↔ AI bridge for Beeper, built on top of [mautrix/bridgev2](https://pkg.go.dev/maunium.net/go/mautrix/bridgev2). Works best with alpha versions of Beeper Desktop. Supports any OpenAI-compatible provider (including OpenRouter).

## Highlights

- Multi-provider routing with prefixed model IDs (e.g. `openai/...`, `anthropic/...`)
- Per-model chats (each model shows up as its own contact)
- Streaming responses
- Multimodal input (images, PDFs, audio, video) when supported by the model
- Ghost-based chat targeting for models and agents
- Login flows for Beeper, Magic Proxy, or custom (BYOK)
- OpenClaw-style memory search (stored in the bridge DB)

## Docs

- `docs/matrix-ai-matrix-spec-v1.md`: Full Matrix transport spec (events, streaming, approvals, state, and schema examples).
- `docs/bridge-orchestrator.md`: One-command bridge management in this repo.

## Development note

The shared AI bridge schema now uses a single `ai_bridge_version` table. If you have a local dev SQLite database created before this refactor, delete and recreate it instead of expecting an automatic migration from the old version-table layout.

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

To log into a local Beeper env instead of production:

```bash
./tools/bridges login --env local
./tools/bridges whoami
./tools/bridges run ai
```

`local` maps to `beeper.localtest.me`. Other supported envs are `prod`, `staging`, and `dev`.

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

### Reset everything

If you want to wipe the current login session and fully reset the built-in bridge manager state:

1. Stop and delete each bridge instance, including its remote Beeper registration:

```bash
./tools/bridges delete --remote ai
./tools/bridges delete --remote codex
./tools/bridges delete --remote opencode
```

2. Remove the saved login session:

```bash
./tools/bridges logout
```

3. Log back into the env you actually want:

```bash
./tools/bridges login --env local
./tools/bridges whoami
```

4. Start the bridge again:

```bash
./tools/bridges run ai
```

Notes:

- `delete --remote <instance>` removes the local instance state under `~/.local/share/ai-bridge-manager/instances/<instance>` and also deletes the remote Beeper bridge record.
- `logout` removes the saved auth config at `~/.config/ai-bridge-manager/config.json`.
- If you only want to reset the login session, `logout` followed by `login --env ...` is enough.
- If you want the absolute manual nuke after the commands above, you can also remove `~/.local/share/ai-bridge-manager/` and `~/.config/ai-bridge-manager/`.

## Build

Requires libolm for encryption support.

```bash
./build.sh
```

Or use Docker:

```bash
docker build -t ai .
```
