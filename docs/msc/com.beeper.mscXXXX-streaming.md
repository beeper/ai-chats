# MSC: Message-Anchored AI Streaming

## Summary

This proposal defines an application-level streaming profile for real-time AI output in Matrix rooms.

Instead of broadcasting every token into room-scoped ephemeral events, the sender publishes a normal placeholder `m.room.message` that carries a `com.beeper.stream` descriptor. Clients that care about live progress subscribe to that descriptor over `to_device`, and the sender delivers buffered and incremental updates directly to those devices. The final assistant message still lands in the room timeline as a normal edit of the placeholder.

The profile covers transport, subscription, completion, and optional custom encryption. The authoritative chunk catalog for `com.beeper.llm` remains in the [AI Matrix Spec](../matrix-ai-matrix-spec-v1.md#streaming).

## Motivation

AI model responses are generated token-by-token and can take tens of seconds to complete. Users should see progress quickly, but room-wide streaming transport has a few practical problems:

- **Unnecessary fanout:** Most joined devices are not actively viewing the room.
- **Server support burden:** Custom room-ephemeral support is not universally available.
- **Per-room delivery overhead:** High-frequency token traffic does not need to be delivered to every client.

Anchoring the stream in a timeline placeholder solves those problems:

- **Timeline-first UX:** Clients can render a room preview such as "Generating response..." from the placeholder alone.
- **Opt-in live delivery:** Only actively viewing devices subscribe.
- **Strong completion signal:** The final `m.replace` edit removes the stream descriptor, so even non-subscribed clients can tell the stream ended.

## Proposal

### Placeholder Descriptor

The sender starts by sending a placeholder `m.room.message` in the room timeline. The message includes a `com.beeper.stream` object:

```json
{
  "type": "m.room.message",
  "room_id": "!meow",
  "event_id": "$foobar",
  "sender": "@ai_chatgpt:beeper.local",
  "content": {
    "msgtype": "m.text",
    "body": "Pondering...",
    "com.beeper.stream": {
      "user_id": "@aibot:beeper.local",
      "device_id": "ABCD1234",
      "type": "com.beeper.llm",
      "expiry": 1800000
    }
  }
}
```

Fields:

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| `user_id` | string | yes | Matrix user that accepts subscriptions and publishes updates. This may differ from the placeholder message sender when bridge bot/device identities differ. |
| `device_id` | string | yes | Device that accepts subscriptions and sends updates. |
| `type` | string | yes | Stream payload family. This proposal currently defines `com.beeper.llm`. |
| `expiry` | integer | no | Maximum age in milliseconds for treating the descriptor as live. Clients SHOULD ignore stale descriptors after this window. |
| `encryption` | object | no | Optional custom symmetric encryption parameters. See [Custom encryption](#custom-encryption). |

If a message containing `com.beeper.stream` is the latest relevant event in a room, clients MAY show a room-list or timeline preview such as "Generating response...".

### Subscription Request

When a client opens the room and sees an unexpired stream descriptor, it subscribes with a `to_device` event:

```json
{
  "type": "com.beeper.stream.subscribe",
  "sender": "@you:beeper.com",
  "to_user_id": "@aibot:beeper.local",
  "to_device_id": "ABCD1234",
  "content": {
    "room_id": "!meow",
    "event_id": "$foobar",
    "device_id": "4321EFGH",
    "expiry": 300000
  }
}
```

Fields:

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| `room_id` | string | yes | Room containing the placeholder message. |
| `event_id` | string | yes | Placeholder event ID being subscribed to. |
| `device_id` | string | yes | Subscriber device that should receive updates. |
| `expiry` | integer | no | Requested subscription lifetime in milliseconds. Clients SHOULD renew before expiry if still viewing the stream. |

The sender SHOULD verify that the subscription targets a live placeholder message it controls and SHOULD clamp the granted expiry to a sender-defined maximum.

### Stream Update Delivery

After receiving a valid subscription, the sender sends a buffered snapshot of stream state so far to the subscribing device, then continues sending incremental updates while the subscription is active:

```json
{
  "type": "com.beeper.stream.update",
  "sender": "@aibot:beeper.local",
  "to_user_id": "@you:beeper.com",
  "to_device_id": "4321EFGH",
  "content": {
    "room_id": "!meow",
    "event_id": "$foobar",
    "com.beeper.llm.deltas": [
      {
        "turn_id": "turn_123",
        "seq": 7,
        "part": {
          "type": "text-delta",
          "id": "text-turn_123",
          "delta": "hello"
        },
        "m.relates_to": {
          "rel_type": "m.reference",
          "event_id": "$foobar"
        }
      }
    ]
  }
}
```

For a descriptor with `type = X`, update content uses the field `X + ".deltas"`. This proposal defines `com.beeper.llm.deltas` for AI SDK-compatible streaming chunks.

Each entry in `com.beeper.llm.deltas` uses the stable envelope defined by the AI profile:

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| `turn_id` | string | yes | Identifier for the assistant turn. |
| `seq` | integer | yes | Monotonically increasing per `turn_id`. |
| `part` | object | yes | AI SDK-compatible streaming chunk. |
| `m.relates_to` | object | yes | `m.reference` pointing at the placeholder event. |
| `agent_id` | string | no | Multi-agent routing hint. |

For `com.beeper.llm`, producers SHOULD send buffered deltas in-order and receivers SHOULD ignore duplicates where `seq <= last_applied_seq`.

### Completion

When the stream is complete, the sender edits the original message:

```json
{
  "type": "m.room.message",
  "room_id": "!meow",
  "sender": "@ai_chatgpt:beeper.local",
  "content": {
    "m.relates_to": {
      "rel_type": "m.replace",
      "event_id": "$foobar"
    },
    "m.new_content": {
      "msgtype": "m.text",
      "body": "Result of pondering is here"
    }
  }
}
```

The terminal edit is authoritative. It SHOULD remove `com.beeper.stream` from the message content and include the finalized assistant state. Clients MUST treat the removal of `com.beeper.stream`, or the arrival of the final edit, as the end of the live stream.

### Client Behavior

1. Observe placeholder `m.room.message` events for `com.beeper.stream`.
2. If the descriptor is unexpired and the room is actively viewed, send `com.beeper.stream.subscribe` to the advertised `user_id` and `device_id`.
3. Apply the initial buffered `com.beeper.stream.update`, then subsequent incremental updates.
4. Re-subscribe before subscription expiry if the room remains active.
5. Stop rendering the stream when the placeholder is edited to remove `com.beeper.stream`, when the descriptor has expired, or when the client leaves the room.

## Custom Encryption

`to_device` updates can use normal Olm encryption. In encrypted rooms, that is the default and recommended transport.

As an optional optimization, the placeholder descriptor MAY expose a symmetric key:

```json
{
  "com.beeper.stream": {
    "user_id": "@aibot:beeper.local",
    "device_id": "ABCD1234",
    "type": "com.beeper.llm",
    "expiry": 1800000,
    "encryption": {
      "algorithm": "com.beeper.stream.v1.aes-gcm",
      "key": "57v+6jXy1NOiFzkrrg+nga0VN7+RURdrCEbm+8OrCDA"
    }
  }
}
```

When using this mode, the sender encrypts the `com.beeper.stream.update` payload once and sends the same ciphertext to every subscriber:

```json
{
  "type": "m.room.encrypted",
  "content": {
    "algorithm": "com.beeper.stream.v1.aes-gcm",
    "room_id": "!meow",
    "event_id": "$foobar",
    "iv": "svNAxzmSqyRdMU3O",
    "ciphertext": "vrKgF7jsQyd9CKnXLqVjAI9mSLH1okmtu0Puu4Tl4uh+HjrR4JhhD0DhT2ioxiUZMaqgYuERuXThAkpebpFFs0kwT0Bp8sC+NyCXHw8apLWxbUxMZ1FMUvyV5fIR6l6RXS50gA"
  }
}
```

Requirements:

- `key` is 32 random bytes encoded as unpadded standard base64.
- `room_id` and `event_id` are included in the encrypted event envelope so receivers can route the payload to the correct stream key without trial-decrypting every active stream.
- `iv` is 12 random bytes encoded as unpadded standard base64.
- `ciphertext` is AES-GCM ciphertext followed by the 16-byte authentication tag, encoded as unpadded standard base64.

This is an optimization, not the baseline transport.

## Potential Issues

- **Sender-side subscriber tracking:** The sender must keep short-lived subscriber state per placeholder event.
- **Metadata exposure:** The placeholder reveals that a stream exists and identifies the serving device.
- **Late subscribers:** Clients may receive only buffered state retained by the sender, not an authoritative replay log.
- **Descriptor staleness:** If the sender crashes and never edits the placeholder, clients rely on `expiry` to stop subscribing.

## Alternatives

### Room ephemerals

Room-scoped ephemeral events can broadcast updates to all joined clients, but they require homeserver support and deliver high-frequency traffic to devices that may not be viewing the room.

### Timeline edits only

Streaming entirely through `m.replace` edits would persist every intermediate state and create unnecessary room traffic. The placeholder-plus-subscription model keeps the timeline authoritative without persisting every token.

## Security Considerations

- **Authorization:** Senders SHOULD only honor subscriptions from users who are entitled to view the placeholder message.
- **Validation:** `room_id` and `event_id` in subscriptions and updates MUST match the anchored placeholder.
- **Expiry enforcement:** Senders SHOULD cap subscription lifetimes and discard expired subscribers.
- **Custom AES mode:** Anyone who can read the placeholder descriptor can decrypt stream updates when the symmetric key mode is used. This is acceptable only because anyone who can read the placeholder is also allowed to subscribe.
- **Key/IV reuse:** AES-GCM senders MUST generate a fresh random IV for every encrypted update. Implementations that approach AES-GCM limits for a single key MUST rotate keys.

## Unstable Prefix

While this proposal is not yet part of the Matrix specification, implementations MUST use the following unstable identifiers:

| Unstable | Stable (future) |
|----------|----------------|
| `com.beeper.stream` | `m.stream` |
| `com.beeper.stream.subscribe` | `m.stream.subscribe` |
| `com.beeper.stream.update` | `m.stream.update` |
| `com.beeper.stream.v1.aes-gcm` | `m.stream.v1.aes-gcm` |

## Dependencies

- Matrix timeline messaging (`m.room.message`, `m.replace`) for the placeholder and final state.
- Matrix `to_device` delivery for subscriptions and live updates.
- Standard Olm `to_device` encryption, or the optional AES-GCM mode defined above.
