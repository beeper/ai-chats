# MSC: Action Hints

## Summary

This proposal adds structured button data to Matrix messages via a `com.beeper.action_hints` content block. Clients render hints as clickable buttons attached to a message. When a user clicks a button, a `com.beeper.action_response` event is sent to the room with a relation linking it to the original message.

The design is based on [MSC1485] with extensions for access control, expiry, exclusive selection, and opaque context passthrough.

## Motivation

Matrix currently lacks a standard mechanism for attaching interactive buttons to messages. Several use cases require structured, one-shot selection UI:

- **Tool approval:** AI assistants need to present Allow / Deny / Always Allow buttons when requesting permission to execute tools. Text-based approval (`!approve allow`) is fragile and undiscoverable.
- **Interactive bots:** Bots that present menus, confirmations, or multi-choice prompts benefit from structured buttons rather than relying on users to type exact command strings.
- **Polls-like selection:** [MSC3381] defines polls, but many scenarios need lightweight single-message selection without the overhead of a full poll event flow.

Without a standard button mechanism, each bot reinvents its own text-parsing scheme, leading to inconsistent UX and fragile integrations.

## Proposal

### Content Structure

The `com.beeper.action_hints` key in `m.room.message` content contains a single object with a `hints` array and optional extension fields:

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

Clients MUST render hints as buttons when the `com.beeper.action_hints` key is present. Clients that do not support action hints MUST still display the message `body` as plain text.

### Hint Object

Each entry in `hints[]`:

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| `body` | string | yes | Button label text. |
| `format` | string | no | Format of `formatted_body` (e.g. `org.matrix.custom.html`). |
| `formatted_body` | string | no | HTML-formatted label. |
| `img` | mxc URI | no | Optional button image. |
| `event_type` | string | no | Type of event sent on click. Default: `m.room.message`. |
| `event` | object | no | Content of event sent on click. Default: `{ "msgtype": "m.text", "body": <body> }`. |

### Extension Fields

Sibling fields alongside `hints[]` in the `com.beeper.action_hints` object:

| Field | Type | Description |
|-------|------|-------------|
| `exclusive` | boolean | Only one response allowed (like polls with `max_selections: 1`). When `true`, clients MUST disable all buttons after one is clicked. |
| `allowed_senders` | string[] | Matrix user IDs permitted to click buttons. Empty array or omitted means anyone in the room MAY respond. |
| `expires_at` | integer | Unix millisecond timestamp. Clients MUST disable buttons after this time. Servers SHOULD reject responses received after expiry. |
| `context` | object | Opaque data passed through to the response event. Clients MUST include this in the response unchanged. |

### Response Event

When a user clicks a button, the client sends a `com.beeper.action_response` event:

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

#### Response Fields

| Field | Type | Description |
|-------|------|-------------|
| `m.relates_to.m.from_action_hint.event_id` | string | Event ID of the message containing the hints. |
| `m.relates_to.m.from_action_hint.hint_key` | integer | Index of the selected hint in `hints[]`. |
| `action_id` | string | The action identifier from the hint's `event` content. |
| `context` | object | Passthrough from the original `com.beeper.action_hints.context`. |

### State Update on Selection

After processing a response, the sender of the original message SHOULD edit it to:

1. Mark the selected hint (e.g. add `"selected": true` to the chosen hint object).
2. Disable remaining hints (clients SHOULD stop rendering buttons).
3. Update the message `body` to reflect the selection.

## Fallback

Clients that do not render action hints MAY allow users to respond via a text reply containing the `action_id`. For example, replying `/respond allow` to a message with action hints. The bot SHOULD synthesize a `com.beeper.action_response` event from such text replies.

## Potential Issues

- **Button rendering divergence:** Different clients may render hints with varying visual fidelity. The `body` field ensures a minimum text representation.
- **Race conditions with `exclusive`:** If two users click simultaneously before the edit disabling buttons propagates, duplicate responses may arrive. Servers SHOULD aggregate responses and the hint sender SHOULD handle duplicates gracefully.

## Alternatives

### Ephemeral vs Timeline for Responses

`com.beeper.action_response` is a persisted timeline event rather than an ephemeral event because:

- **Audit trail:** Users can see who approved what in room history.
- **Server-side aggregation:** The `m.relates_to` relation enables bundled aggregations.
- **Consistency with [MSC1485]:** The original proposal uses timeline events for responses.
- **Durability:** Responses survive client reconnects, app restarts, and late-joining members.

Ephemeral responses were considered but rejected due to the lack of delivery guarantees and the inability to aggregate them server-side.

### MSC3381 Polls

[MSC3381] polls provide similar selection UX but are designed as standalone events with their own lifecycle (start, response, end). Action hints are intentionally lightweight — they attach directly to an existing message and require no separate lifecycle management.

## Security Considerations

- **`allowed_senders` enforcement:** Clients MUST check `allowed_senders` before rendering buttons as clickable. Servers receiving a `com.beeper.action_response` from a user not in `allowed_senders` SHOULD reject the event. If `allowed_senders` is empty or absent, any joined room member MAY respond.
- **`expires_at` validation:** Clients MUST NOT render expired buttons as clickable. The hint sender SHOULD reject responses arriving after `expires_at`, accounting for reasonable clock skew.
- **`context` tampering:** The `context` field is opaque and passed through unchanged. The hint sender MUST NOT trust `context` values in responses without validating them against the original hint. A malicious client could modify `context` to reference a different approval or tool call.
- **Power levels:** Sending a `com.beeper.action_response` event requires the standard power level for that event type. Room administrators MAY restrict who can send responses via power levels.

## Unstable Prefix

While this proposal is not yet part of the Matrix specification, implementations MUST use the following unstable prefixes:

| Unstable | Stable (future) |
|----------|----------------|
| `com.beeper.action_hints` | `m.action_hints` |
| `com.beeper.action_response` | `m.action_response` |
| `m.from_action_hint` | No change expected |

## Dependencies

- [MSC1485]: Action hints (buttons) — original proposal by tulir. This MSC extends the design with access control, expiry, and context passthrough.
- [MSC3381]: Polls — prior art for exclusive selection semantics.

[MSC1485]: https://docs.google.com/document/d/1EgDkQMO_UEXsR7V4xFJYXrCf0FBz5Pzq-RFoojdqJk/
[MSC3381]: https://github.com/matrix-org/matrix-spec-proposals/pull/3381
