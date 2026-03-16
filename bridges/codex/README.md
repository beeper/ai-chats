# Codex Companion

The Codex Companion bridge connects a local Codex CLI runtime to Beeper through AgentRemote.

This is the bridge for people who want to run Codex on a workstation, laptop, or remote machine and use Beeper as the chat client. It exposes Codex conversations in Beeper with streaming responses, history, and tool approval flows, while keeping the actual runtime close to the code and credentials it needs.

## What It Does

- Starts or connects to a local `codex app-server` process
- Bridges Codex threads into Beeper rooms
- Streams assistant output into chat as it is generated
- Preserves conversation history
- Surfaces tool calls and approval requests in Beeper

## Login Model

The bridge supports Codex-backed logins through:

- ChatGPT-based auth
- OpenAI API key auth
- Externally managed ChatGPT tokens

If Codex is already authenticated on the host, the bridge can auto-provision a login from the existing local Codex state.

## Best Fit

Use this bridge when:

- Your agent already runs through the Codex CLI
- You want a phone-friendly interface for coding agents
- You want to keep execution on your own machine or behind your own network boundary

## Run It

From the repo root:

```bash
./tools/bridges run codex
```

Or:

```bash
./run.sh codex
```

For local Beeper environments:

```bash
./tools/bridges login --env local
./tools/bridges run codex
```

## Notes

- The bridge uses a dedicated Codex surface rather than the generic AI connector.
- Auth tokens are managed by Codex itself when using the local Codex home flow.
- This bridge is part of the self-hosted AgentRemote story: Beeper is the remote control, Codex stays where the work happens.
