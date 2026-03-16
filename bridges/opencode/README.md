# OpenCode Companion

The OpenCode Companion bridge connects a self-hosted OpenCode server to Beeper through AgentRemote.

It is built for setups where OpenCode is already running on a machine you trust and you want Beeper to become the front end. That can be a local development machine, a lab box, or an office server that you reach from your phone.

## What It Does

- Connects to an OpenCode server over HTTP
- Subscribes to the OpenCode event stream for live updates
- Maps OpenCode sessions into Beeper rooms
- Streams responses, titles, and session events into chat
- Keeps the bridge usable even when the remote instance temporarily disconnects

## Login Model

The bridge asks for:

- Server URL
- Optional username
- Optional password for HTTP basic auth

Multiple OpenCode instances can be tracked per login, which is useful if you talk to different machines or environments.

## Best Fit

Use this bridge when:

- You run OpenCode yourself and want Beeper access from anywhere
- You want a simple remote interface for agent sessions without exposing a separate UI
- You want to keep the runtime and credentials on the host machine

## Run It

From the repo root:

```bash
./tools/bridges run opencode
```

Or:

```bash
./run.sh opencode
```

## Notes

- OpenCode uses an HTTP API plus event streaming rather than the local Codex app-server flow.
- In AgentRemote terms, this is the bridge for turning a private OpenCode deployment into a Beeper-accessible agent endpoint.
