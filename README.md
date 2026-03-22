# AgentRemote

AgentRemote connects Beeper to self-hosted agent runtimes.

It gives Matrix/Beeper chats a bridge layer for full history, live streaming, approvals, and remote access, while the actual runtime stays on your machine or network.

This repository is still experimental.

## Install

Install the latest release with the one-liner:

```bash
curl -fsSL https://raw.githubusercontent.com/beeper/agentremote/main/install.sh | sh
```

Other supported install paths:

- Download a release archive from [GitHub Releases](https://github.com/beeper/agentremote/releases)
- Install via Homebrew: `brew install --cask beeper/tap/agentremote`

To pin a version or choose the install directory:

```bash
curl -fsSL https://raw.githubusercontent.com/beeper/agentremote/main/install.sh | VERSION=v0.1.0 BINDIR="$HOME/.local/bin" sh
```

The installed CLI stores profile state under `~/.config/agentremote/`.

## Included bridges

| Bridge | What it connects |
| --- | --- |
| `ai` | The built-in Beeper AI chat surface in this repo |
| [`codex`](./bridges/codex/README.md) | A local `codex app-server` runtime |
| [`opencode`](./bridges/opencode/README.md) | A remote OpenCode server or a bridge-managed local OpenCode process |
| [`openclaw`](./bridges/openclaw/README.md) | A self-hosted OpenClaw gateway |

## Quick start

```bash
agentremote login --env prod
agentremote list
agentremote run codex
```

Useful commands:

- `agentremote up <bridge>` starts a bridge in the background
- `agentremote status` shows local and remote bridge state
- `agentremote logs <instance> --follow` tails logs
- `agentremote stop <instance>` stops a running instance

For local development from a checkout, `./tools/bridges ...` remains a thin wrapper around `go run ./cmd/agentremote`.

Instance state lives under `~/.config/agentremote/profiles/<profile>/instances/`.

## Docker

The CLI is also published as a multi-arch Linux container image:

```bash
docker run --rm -it \
  -v "$(pwd):/data" \
  ghcr.io/beeper/agentremote:latest help
```

The container sets `HOME=/data`, so mounted state is persisted under `/data/.config/agentremote/`. See [`docker/agentremote/README.md`](./docker/agentremote/README.md) for usage details.

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
