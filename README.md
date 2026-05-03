# AgentRemote

AgentRemote securely brings model chats to Beeper with streaming and native tool UI where the model supports it.

AgentRemote can run on the same device as the bridge runtime and can work behind a firewall. It connects to Beeper directly and creates an E2EE tunnel.

**This repository is still experimental. Expect everything to be broken for now.
**

## Install

Install the latest release:

```bash
curl -fsSL https://raw.githubusercontent.com/beeper/agentremote/main/install.sh | sh
```

Other supported install paths:

- Download a release archive from [GitHub Releases](https://github.com/beeper/agentremote/releases)
- Install via Homebrew: `brew install --cask beeper/tap/agentremote`

The AgentRemote Manager stores profile state under `~/.config/agentremote/`.

## Included bridges

| Bridge | What it connects |
| --- | --- |
| [`AI Chats`](./bridges/ai/README.md) | Talk to any model on Beeper AI |

## Quick start

```bash
agentremote login
agentremote list
agentremote run ai
```

Useful commands:

- `agentremote start <bridge>` starts a bridge in the background
- `agentremote status` shows local and remote bridge state
- `agentremote logs <instance> --follow` tails logs
- `agentremote stop <instance>` stops a running instance

Instance state lives under `~/.config/agentremote/profiles/<profile>/instances/`.

## Docker

The AgentRemote Manager is also published as a multi-arch Linux container image:

```bash
docker run --rm -it \
  -v "$(pwd):/data" \
  ghcr.io/beeper/agentremote:latest help
```

The container sets `HOME=/data`, so mounted state is persisted under `/data/.config/agentremote/`. See [`docker/agentremote/README.md`](./docker/agentremote/README.md) for usage details.

## AgentRemote SDK

Custom bridges in this repo are built on [`sdk/`](./sdk), the AgentRemote SDK metaframework, using:

- `sdk.NewStandardConnectorConfig(...)`
- `sdk.NewConnectorBase(...)`
- `sdk.Config`, `sdk.Agent`, `sdk.Conversation`, and `sdk.Turn`

See [`bridges/ai`](./bridges/ai) for the model chat bridge.

## Docs

- AgentRemote Manager reference: [`docs/bridge-orchestrator.md`](./docs/bridge-orchestrator.md)
- Matrix transport surface: [`docs/matrix-ai-matrix-spec-v1.md`](./docs/matrix-ai-matrix-spec-v1.md)
- Streaming note: [`docs/msc/com.beeper.mscXXXX-streaming.md`](./docs/msc/com.beeper.mscXXXX-streaming.md)
- Command profile: [`docs/msc/com.beeper.mscXXXX-commands.md`](./docs/msc/com.beeper.mscXXXX-commands.md)
