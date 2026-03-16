# OpenClaw Gateway

The OpenClaw Gateway bridge connects a self-hosted OpenClaw gateway to Beeper through AgentRemote.

This is the most direct way to expose OpenClaw sessions in Beeper while keeping the agent runtime on infrastructure you control. Run the gateway on a local machine, server, or private network, then use Beeper from mobile or desktop to talk to those agents remotely.

## What It Does

- Connects to an OpenClaw gateway over `ws`, `wss`, `http`, or `https`
- Syncs OpenClaw sessions into Beeper rooms
- Streams responses and updates live
- Carries tool calls, approvals, and agent state into chat
- Preserves per-session metadata, usage, and history context

## Login Model

The bridge asks for:

- Gateway URL
- Optional gateway token
- Optional gateway password
- Optional label for distinguishing multiple gateways

That makes it a good fit for private deployments where the gateway is reachable only on a LAN, VPN, Tailscale network, or internal hostname.

## Best Fit

Use this bridge when:

- You already run OpenClaw and want Beeper as the client
- Your agents live behind a firewall and should stay there
- You want streaming and approvals without building a separate mobile UI

## Run It

From the repo root:

```bash
./tools/bridges run openclaw
```

Or:

```bash
./run.sh openclaw
```

## Notes

- The bridge is intentionally focused on OpenClaw as a remote runtime, not a hosted SaaS workflow.
- It is a core example of the AgentRemote model: keep the gateway private, use Beeper as the interface.
