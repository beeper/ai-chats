# AgentRemote

AgentRemote connects Beeper to self-hosted agent runtimes.

It gives Matrix/Beeper chats a bridge layer for full history, live streaming, approvals, and remote access, while the actual runtime stays on your machine or network.

This repository is still experimental.

## Included bridges

| Bridge | What it connects |
| --- | --- |
| `ai` | The built-in Beeper AI chat surface in this repo |
| [`codex`](./bridges/codex/README.md) | A local `codex app-server` runtime |
| [`opencode`](./bridges/opencode/README.md) | A remote OpenCode server or a bridge-managed local OpenCode process |
| [`openclaw`](./bridges/openclaw/README.md) | A self-hosted OpenClaw gateway |

## Quick start

```bash
./tools/bridges login --env prod
./tools/bridges list
./tools/bridges run codex
```

Useful commands:

- `./tools/bridges up <bridge>` starts a bridge in the background
- `./tools/bridges status` shows local and remote bridge state
- `./tools/bridges logs <instance> --follow` tails logs
- `./tools/bridges stop <instance>` stops a running instance

Instance state lives under `~/.config/agentremote/profiles/<profile>/instances/`.

## SDK

Custom bridges in this repo are built on [`sdk/`](./sdk), using:

- `bridgesdk.NewStandardConnectorConfig(...)`
- `bridgesdk.NewConnectorBase(...)`
- `sdk.Config`, `sdk.Agent`, `sdk.Conversation`, and `sdk.Turn`

See [`bridges/dummybridge`](./bridges/dummybridge) for a minimal bridge example.

## Docs

- CLI reference: [`docs/bridge-orchestrator.md`](./docs/bridge-orchestrator.md)
- Matrix transport surface: [`docs/matrix-ai-matrix-spec-v1.md`](./docs/matrix-ai-matrix-spec-v1.md)
- Streaming note: [`docs/msc/com.beeper.mscXXXX-streaming.md`](./docs/msc/com.beeper.mscXXXX-streaming.md)
- Command profile: [`docs/msc/com.beeper.mscXXXX-commands.md`](./docs/msc/com.beeper.mscXXXX-commands.md)
