# AI Chats

AI Chats securely brings model chats to Beeper with streaming and native tool UI where the model supports it.

AI Chats can run on the same device as the bridge runtime and can work behind a firewall. It connects to Beeper directly and creates an E2EE tunnel.

**This repository is still experimental. Expect everything to be broken for now.
**

## Included Bridge

| Bridge | What it connects |
| --- | --- |
| [`AI Chats`](./pkg/connector/README.md) | Talk to any model on Beeper AI |

## Running

```bash
./build.sh
./ai -c config.yaml
```

Bridge lifecycle, local registration, and profile state live outside this repo.

## Shared Primitives

Standalone bridges can import shared AI chat primitives from this module:

- `pkg/shared/streamui` for streaming UI chunks and snapshots
- `pkg/shared/aihelpers` for approvals, turns, Matrix message helpers, and AI bridge UI glue
- `pkg/runtime`, `pkg/shared/*`, and `pkg/shared/turns` for reusable model-chat behavior

## Docs

- Matrix transport surface: [`docs/matrix-ai-matrix-spec-v1.md`](./docs/matrix-ai-matrix-spec-v1.md)
- Streaming note: [`docs/msc/com.beeper.mscXXXX-streaming.md`](./docs/msc/com.beeper.mscXXXX-streaming.md)
- Command profile: [`docs/msc/com.beeper.mscXXXX-commands.md`](./docs/msc/com.beeper.mscXXXX-commands.md)
