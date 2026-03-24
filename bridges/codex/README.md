# Codex Bridge

The Codex bridge connects Beeper to a local Codex CLI runtime.

It fits setups where Codex stays on the machine that already has the checkout, credentials, and tools.

## What it does

- starts or connects to `codex app-server`
- maps Codex conversations into Beeper rooms
- streams replies into chat
- carries approvals and tool activity through the same room

## Login modes

The bridge supports:

- ChatGPT login
- OpenAI API key login
- externally managed ChatGPT tokens
- host-auth auto-detection when Codex is already logged in on the machine

Managed logins use an isolated `CODEX_HOME` per login. Host-auth uses the machine's existing Codex auth state.

## Run

```bash
./tools/bridges run codex
```

Or:

```bash
./run.sh codex
```
