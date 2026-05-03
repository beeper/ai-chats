# AI Chats

AI Chats securely brings model chats to Beeper with streaming and native tool UI where the model supports it.

AI Chats can run on the same device as the bridge runtime and can work behind a firewall. It connects to Beeper directly and creates an E2EE tunnel.

**This repository is still experimental. Expect everything to be broken for now.
**

## Included Bridge

| Bridge | What it connects |
| --- | --- |
| [`AI Chats`](./bridges/ai/README.md) | Talk to any model on Beeper AI |

## Running

```bash
./build.sh
./run.sh
```

Bridge lifecycle, local registration, and profile state are managed by
[`beeper/bridge-manager`](https://github.com/beeper/bridge-manager), the same
manager used by other standalone Beeper bridges.

## Shared Primitives

Standalone bridges can import shared AI chat primitives from this module:

- `pkg/shared/streamui` for streaming UI chunks and snapshots
- `sdk` for approvals, turns, and bridge helper primitives
- `sdk.NewConnectorBase(...)`
- `pkg/runtime`, `pkg/shared/*`, and `turns` for reusable model-chat behavior

## Docs

- Matrix transport surface: [`docs/matrix-ai-matrix-spec-v1.md`](./docs/matrix-ai-matrix-spec-v1.md)
- Streaming note: [`docs/msc/com.beeper.mscXXXX-streaming.md`](./docs/msc/com.beeper.mscXXXX-streaming.md)
- Command profile: [`docs/msc/com.beeper.mscXXXX-commands.md`](./docs/msc/com.beeper.mscXXXX-commands.md)
