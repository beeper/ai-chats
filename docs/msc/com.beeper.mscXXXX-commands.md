# Command Descriptions (MSC4391)

**Spec:** [MSC4391](https://github.com/matrix-org/matrix-spec-proposals/pull/4391) (bot command descriptions)

**Status:** Already implemented in mautrix-go and gomuks. Adopted as-is by ai-bridge.

## Summary

ai-bridge uses MSC4391 `org.matrix.msc4391.command_description` state events to advertise available commands to clients. Instead of users memorizing `!ai status`, `!ai model`, etc., clients discover commands from state events and render them as slash commands with autocomplete and typed parameters.

MSC4391 is already used in gomuks (web + TUI) and deeply integrated into mautrix-go's command processor, so we adopt it directly rather than creating a `com.beeper.` variant.

## State Event

Type: `org.matrix.msc4391.command_description`

ai-bridge broadcasts one state event per command on room join:

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

## Structured Invocation

When a client sends a command, it includes the structured field:

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

The `body` field contains the text fallback for clients without MSC4391 support.

## Relationship with Action Hints (MSC1485)

MSC4391 and `com.beeper.action_hints` serve complementary roles:

| Aspect | MSC4391 Commands | Action Hints (MSC1485) |
|--------|-----------------|----------------------|
| Discovery | State events in room | Inline on messages |
| Initiation | User-initiated (slash commands) | System-prompted (buttons) |
| Invocation | `org.matrix.msc4391.command` in message | `com.beeper.action_response` event |
| Use case | `!ai model`, `!ai reset`, etc. | Tool approval Allow/Deny |

Both could be unified in the future (action hints as an alternate invocation path for commands), but currently they serve distinct UX patterns.

## Command List

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

Dynamic commands from integrations/modules are also broadcast.

## Implementation

### ai-bridge (`command_registry.go`)

`BroadcastCommandDescriptions()`:
1. Iterates `aiCommandRegistry.All()`
2. Maps each `commandregistry.Definition` to an MSC4391 command description
3. Sends `org.matrix.msc4391.command_description` state events via the bot intent
4. Called from `BroadcastRoomState()` on room join

### Text Fallback

The `!ai` text prefix parsing is kept as a fallback for older clients. When `org.matrix.msc4391.command` is present in the message, it takes precedence.

### mautrix-go

Already has full support:
- `StateMSC4391BotCommand` event type (`event/type.go`)
- `MSC4391BotCommandInput` struct in `MessageEventContent` (`event/message.go`)
- `cmdschema` package for parsing command schemas
- `commands.Processor` routes structured commands
- gomuks renders slash commands from state events
