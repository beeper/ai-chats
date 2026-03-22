# OpenClaw Bridge

The OpenClaw bridge connects Beeper to a self-hosted OpenClaw gateway.

## What it does

- connects to a gateway over `ws`, `wss`, `http`, or `https`
- syncs OpenClaw sessions into Beeper rooms
- streams replies, approvals, and session updates into chat

## Login flow

The bridge asks for:

- gateway URL
- auth mode: none, token, or password
- optional label

If the gateway requires device pairing, the login waits for approval and surfaces the request ID.

## Run

```bash
./tools/bridges run openclaw
```

Or:

```bash
./run.sh openclaw
```
