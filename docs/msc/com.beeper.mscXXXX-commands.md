# MSC: ai-bridge MSC4391 Command Profile

## Summary

This document defines the specific command set that ai-bridge advertises via [MSC4391] bot command descriptions. Rather than introducing a custom `com.beeper.*` command system, ai-bridge adopts MSC4391 directly — broadcasting `org.matrix.msc4391.command_description` state events so that supporting clients can render slash commands with autocomplete and typed parameters.

This is a profile document, not a new MSC. It specifies which commands ai-bridge publishes and how they interact with the action hints system defined in [com.beeper.action_hints](com.beeper.mscXXXX-actions.md).

## Motivation

Text-based bot commands (`!ai model gpt-4o`, `!ai reset`) have several problems:

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
{
  "type": "org.matrix.msc4391.command_description",
  "state_key": "model",
  "content": {
    "description": "Get or set the AI model",
    "arguments": {
      "model_id": {
        "description": "Model identifier (e.g. gpt-4o, claude-sonnet)",
        "required": false,
        "type": "string"
      }
    }
  }
}
```

### Structured Invocation

When a client sends a command, it MUST include the `org.matrix.msc4391.command` field in the message content:

```json
{
  "type": "m.room.message",
  "content": {
    "msgtype": "m.text",
    "body": "!ai model gpt-4o",
    "org.matrix.msc4391.command": {
      "command": "model",
      "arguments": {
        "model_id": "gpt-4o"
      }
    }
  }
}
```

The `body` field MUST contain a text fallback for clients without MSC4391 support. When `org.matrix.msc4391.command` is present, the bot MUST use the structured field and ignore the `body` for command parsing.

### Relationship with Action Hints

MSC4391 and `com.beeper.action_hints` serve complementary roles:

| Aspect | MSC4391 Commands | Action Hints |
|--------|-----------------|--------------|
| Discovery | State events in room | Inline on messages |
| Initiation | User-initiated (slash commands) | System-prompted (buttons) |
| Invocation | `org.matrix.msc4391.command` in message | `com.beeper.action_response` event |
| Use case | `model`, `reset`, `status`, etc. | Tool approval Allow/Deny |

Both MAY be unified in the future (action hints as an alternate invocation path for commands), but currently they serve distinct UX patterns.

### Command List

Commands broadcast by ai-bridge:

| Command | Description | Arguments |
|---------|-------------|-----------|
| `status` | Show current session status | — |
| `model` | Get or set the AI model | `model_id?: string` |
| `reset` | Start a new session/thread | — |
| `stop` | Abort current run and clear queue | — |
| `think` | Get or set thinking level | `level?: off\|minimal\|low\|medium\|high\|xhigh` |
| `verbose` | Get or set verbosity | `level?: off\|on\|full` |
| `reasoning` | Get or set reasoning visibility | `level?: off\|on\|low\|medium\|high\|xhigh` |
| `elevated` | Get or set elevated access | `level?: off\|on\|ask\|full` |
| `activation` | Set group activation policy | `policy: mention\|always` |
| `send` | Allow/deny sending messages | `mode: on\|off\|inherit` |
| `queue` | Inspect or configure message queue | `action?: status\|reset\|<mode>` |
| `whoami` | Show your Matrix user ID | — |
| `last-heartbeat` | Show last heartbeat event | — |

Dynamic commands from integrations and modules are also broadcast as state events.

## Fallback

Clients without MSC4391 support MAY send commands as `!ai <command>` text messages. The bot MUST parse `!ai` prefixed text as a fallback when the `org.matrix.msc4391.command` field is absent.

When both are present, the structured `org.matrix.msc4391.command` field takes precedence over the text `body`.

## Security Considerations

- **Command authorization:** The bot SHOULD check room power levels before executing commands that modify room or session state. Commands like `reset`, `model`, and `elevated` affect all users in the room.
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
