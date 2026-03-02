# com.beeper.action_hints — Action Hints (Buttons)

**Prior art:** [MSC1485](https://docs.google.com/document/d/1EgDkQMO_UEXsR7V4xFJYXrCf0FBz5Pzq-RFoojdqJk/) (tulir), [MSC3381](https://github.com/matrix-org/matrix-spec-proposals/pull/3381) (polls), [MSC4140](https://github.com/matrix-org/matrix-spec-proposals/pull/4140) (delayed events), [MSC4392](https://github.com/matrix-org/matrix-spec-proposals/pull/4392) (semantic markup)

**Status:** Implemented in mautrix-go + ai-bridge.

## Summary

`com.beeper.action_hints` adds structured button data to Matrix messages. Clients render the hints as clickable buttons. When a user clicks a button, an event is sent back to the room with a relation linking it to the original message.

Based on tulir's MSC1485 proposal with Beeper extensions for access control, expiry, exclusive selection, and opaque context passthrough.

## Content Structure

The `com.beeper.action_hints` key in event content contains a single object with the `hints` array and extension fields:

```json
{
  "type": "m.room.message",
  "content": {
    "msgtype": "m.text",
    "body": "Allow web_search tool?",
    "com.beeper.action_hints": {
      "hints": [
        {
          "body": "Allow",
          "event_type": "com.beeper.action_response",
          "event": { "action_id": "allow" }
        },
        {
          "body": "Always Allow",
          "event_type": "com.beeper.action_response",
          "event": { "action_id": "always" }
        },
        {
          "body": "Deny",
          "event_type": "com.beeper.action_response",
          "event": { "action_id": "deny" }
        }
      ],
      "exclusive": true,
      "allowed_senders": ["@owner:example.com"],
      "expires_at": 1738970600000,
      "context": {
        "approval_id": "abc123",
        "tool_name": "web_search",
        "tool_call_id": "call_456"
      }
    }
  }
}
```

### Hint Object

Each entry in `hints[]`:

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| `body` | string | yes | Button label text |
| `format` | string | no | Format of `formatted_body` (e.g. `org.matrix.custom.html`) |
| `formatted_body` | string | no | HTML-formatted label |
| `img` | mxc URI | no | Optional button image |
| `event_type` | string | no | Type of event sent on click. Default: `m.room.message` |
| `event` | object | no | Content of event sent on click. Default: `{ "msgtype": "m.text", "body": <body> }` |

### Extension Fields

Sibling fields alongside `hints[]` in the `com.beeper.action_hints` object:

| Field | Type | Description |
|-------|------|-------------|
| `exclusive` | boolean | Only one response allowed (like polls with `max_selections: 1`) |
| `allowed_senders` | string[] | User IDs who can press buttons (empty = anyone) |
| `expires_at` | integer | Unix ms timestamp; buttons disabled after expiry |
| `context` | object | Opaque data passed through to the response event |

## Response Event

When a user clicks a button, the client sends:

```json
{
  "type": "com.beeper.action_response",
  "content": {
    "m.relates_to": {
      "m.from_action_hint": {
        "event_id": "$original_message",
        "hint_key": 0
      }
    },
    "action_id": "allow",
    "context": {
      "approval_id": "abc123",
      "tool_call_id": "call_456"
    }
  }
}
```

### Response Fields

| Field | Type | Description |
|-------|------|-------------|
| `m.relates_to.m.from_action_hint.event_id` | string | Event ID of the message containing the hints |
| `m.relates_to.m.from_action_hint.hint_key` | integer | Index of the selected hint in `hints[]` |
| `action_id` | string | The action identifier from the hint's `event` content |
| `context` | object | Passthrough from the original `com.beeper.action_hints.context` |

### Implementation Note: `m.from_action_hint`

mautrix-go's `RelatesTo` struct does not yet have a dedicated field for the nested `m.from_action_hint` relation. The bridge's `ParseActionResponse()` extracts `event_id` from `RelatesTo.EventID` but cannot parse `hint_key` from the custom nesting. In practice, ai-bridge identifies the selected hint via the `action_id` in the response content + the `context` passthrough, so the `hint_key` index is not required for correct operation.

## State Update on Selection

After processing a response, the sender of the original message SHOULD edit it to:
1. Mark the selected hint (e.g. add `"selected": true` to the chosen hint)
2. Disable remaining hints (clients should stop rendering buttons)
3. Update the message body to reflect the selection

## AI SDK UIMessage Integration

For ai-bridge, the `com.beeper.ai` UIMessage includes an `action-hints` part that references the sibling `com.beeper.action_hints` data:

```json
{
  "com.beeper.ai": {
    "id": "turn_123",
    "role": "assistant",
    "parts": [
      {
        "type": "action-hints",
        "toolCallId": "call_456",
        "toolName": "web_search"
      }
    ]
  }
}
```

Single source of truth: the `action-hints` part tells clients to render buttons from `com.beeper.action_hints`. No duplication of button data in the AI parts.

## Older-Client Fallback

If a user replies to a message containing `com.beeper.action_hints` with `/respond <action_id>`, the bridge synthesizes a `com.beeper.action_response` event. This covers clients that don't render buttons natively.

## Why Timeline Events for Responses

`com.beeper.action_response` is a persisted timeline event because:
- Audit trail: users can see who approved what
- Server-side aggregation via `m.relates_to`
- Consistent with MSC1485 original design
- Survives client reconnects and app restarts
