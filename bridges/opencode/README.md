# OpenCode Bridge

The OpenCode bridge connects Beeper to OpenCode.

It supports two modes:

- remote: connect to an existing OpenCode server over HTTP
- managed: let the bridge launch `opencode` locally and keep a default working directory

## What it does

- maps OpenCode sessions into Beeper rooms
- streams replies and session updates into chat
- keeps reconnect logic inside the bridge instead of requiring a separate UI

## Login flow

Remote mode asks for:

- server URL
- optional basic-auth username
- optional basic-auth password

Managed mode asks for:

- path to the `opencode` binary
- default working directory

## Run

```bash
./tools/bridges run opencode
```

Or:

```bash
./run.sh opencode
```
