# MSC: ai-bridge MSC4391 Command Profile

## Summary

This document defines the specific command set that ai-bridge advertises via [MSC4391] bot command descriptions. Rather than introducing a custom `com.beeper.*` command system, ai-bridge adopts MSC4391 directly — broadcasting `org.matrix.msc4391.command_description` state events so that supporting clients can render slash commands with autocomplete and typed parameters.

This is a profile document, not a new MSC. It specifies which commands ai-bridge publishes via MSC4391.

## Motivation

Text-based bot commands (`!ai status`, `!ai reset`) have several problems:

- **Undiscoverable:** Users must read documentation or type `!ai help` to learn available commands. There is no in-client autocomplete or parameter hinting.
- **Fragile parsing:** Free-text command parsing leads to ambiguous inputs and poor error messages. Typed parameters eliminate this class of bugs.
- **No validation:** Without structured schemas, clients cannot validate arguments before sending. Invalid commands waste a round-trip.

[MSC4391] solves these problems by letting bots advertise commands as room state events. Clients that support MSC4391 render them as slash commands with autocomplete. ai-bridge adopts this directly.

## Proposal

### State Event

Type: `org.matrix.msc4391.command_description`

The bot MUST broadcast one state event per command when it joins a room. The `state_key` is the command name.

```json
{
  "type": "org.matrix.msc4391.command_description",
  "state_key": "status",
  "content": {
    "description": "Show current session status",
    "arguments": {}
  }
}
```

```json
### Structured Invocation

When a client sends a command, it MUST include the `org.matrix.msc4391.command` field in the message content:

```json
{
  "type": "m.room.message",
  "content": {
    "msgtype": "m.text",
    "body": "!ai status",
    "org.matrix.msc4391.command": {
      "command": "status",
      "arguments": {}
    }
  }
}
```

The `body` field MUST contain a text fallback for clients without MSC4391 support. When `org.matrix.msc4391.command` is present, the bot MUST use the structured field and ignore the `body` for command parsing.

### Command List

Commands broadcast by ai-bridge:

| Command | Description | Arguments |
|---------|-------------|-----------|
| `new` | Create a new chat of the same type | `agent?: string` |
| `status` | Show current session status | — |
| `reset` | Start a new session/thread | — |
| `stop` | Abort current run and clear queue | — |

Dynamic commands from integrations and modules are also broadcast as state events.

## Fallback

Clients without MSC4391 support MAY send commands as `!ai <command>` text messages. The bot MUST parse `!ai` prefixed text as a fallback when the `org.matrix.msc4391.command` field is absent.

When both are present, the structured `org.matrix.msc4391.command` field takes precedence over the text `body`.

## Security Considerations

- **Command authorization:** The bot SHOULD check room power levels before executing commands that modify room or session state.
- **Argument validation:** The bot MUST validate structured arguments against the published schema before execution. Malformed arguments MUST be rejected with an error message.

## Unstable Prefix

This profile uses the MSC4391 unstable prefix directly:

| Unstable | Stable (future) |
|----------|----------------|
| `org.matrix.msc4391.command_description` | `m.command_description` |
| `org.matrix.msc4391.command` | `m.command` |

No `com.beeper.*` variant is needed — MSC4391 is adopted as-is.

## Dependencies

- [MSC4391]: Bot command descriptions — the underlying protocol this profile builds on.

[MSC4391]: https://github.com/matrix-org/matrix-spec-proposals/pull/4391
