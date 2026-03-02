# MSC: Custom Room Ephemeral Events

## Summary

`com.beeper.ephemeral` provides a transport for custom ephemeral events in Matrix rooms. This is an implementation of [MSC2477] with a `com.beeper` unstable prefix, plus transparent E2EE support following the [MSC3673] pattern.

Ephemeral events are short-lived, non-persisted events delivered via `/sync` to joined room members. They are useful for real-time features like AI streaming, live indicators, and collaborative cursors.

## Motivation

Matrix currently provides only a limited set of built-in ephemeral events — primarily typing indicators (`m.typing`) and read receipts (`m.receipt`). Applications that need real-time, non-persisted data delivery within a room have no standard mechanism available.

Use cases that require custom ephemeral events include:

- **AI streaming:** Token-by-token delivery of AI model output for progressive rendering (see [com.beeper.ai.stream_event](com.beeper.mscXXXX-streaming.md)).
- **Collaborative cursors:** Real-time cursor position sharing in shared editing contexts.
- **Custom presence:** Application-specific presence or activity indicators beyond `m.presence`.

[MSC2477] proposes user-defined ephemeral events but has not yet been merged into the Matrix specification. This proposal implements the same concept with a `com.beeper` unstable prefix to unblock real-time features today.

## Proposal

### Differences from MSC2477

| Aspect | MSC2477 | com.beeper.ephemeral |
|--------|---------|---------------------|
| Unstable prefix | `org.matrix.msc2477` | `com.beeper.ephemeral` |
| Endpoint | `PUT /_matrix/client/unstable/org.matrix.msc2477/rooms/{roomId}/ephemeral/{type}/{txnId}` | `PUT /_matrix/client/unstable/com.beeper.ephemeral/rooms/{roomId}/ephemeral/{type}/{txnId}` |
| Power levels key | `ephemeral` + `ephemeral_default` (default 50) | Same concept — checked via power levels |
| TTL | Not specified | Servers SHOULD expire events. Recommended TTL: 2 minutes. |
| Timestamp | `origin_server_ts` on event | `?ts=` query param on PUT, stored as `origin_server_ts` |
| Response | `{}` | `{}` (empty body) |
| Built-in type blocking | Rejects `m.*` types | No type restriction (power levels apply) |
| Sync delivery | `ephemeral` section of `/sync` rooms | Same — delivered in `rooms.join.{roomId}.ephemeral.events[]` |

### Client-Server API

#### Sending

```
PUT /_matrix/client/unstable/com.beeper.ephemeral/rooms/{roomId}/ephemeral/{eventType}/{txnId}
```

**Request body:** Arbitrary JSON content.

**Query parameters:**

| Parameter | Type | Required | Description |
|-----------|------|----------|-------------|
| `ts` | integer | no | Unix millisecond timestamp for `origin_server_ts`. If omitted, the server MUST use the current time. |

**Authentication:** Standard Matrix access token. The sender MUST be joined to the room.

**Power levels:** The server MUST check the sender's power level against the room's ephemeral event power level for the given `eventType`.

**Constraints:**
- Maximum content size: 64KB. Servers MUST reject requests exceeding this limit with `M_TOO_LARGE`.
- Deduplication: Servers MUST deduplicate on the composite key `(room_id, sender, event_type, txn_id)`. Duplicate sends MUST be silently accepted and return `200 OK`.

**Response:** `200 OK`
```json
{}
```

#### Receiving via /sync

Ephemeral events appear in the `/sync` response under `rooms.join.{roomId}.ephemeral.events[]`:

```json
{
  "type": "com.example.custom",
  "sender": "@user:server",
  "origin_server_ts": 1709123456000,
  "room_id": "!room:server",
  "content": { ... }
}
```

Servers MUST only deliver ephemeral events to users with `membership: join` in the room.

#### TTL and Expiry

Servers SHOULD expire ephemeral events after a configured TTL. The recommended TTL is 2 minutes. Servers SHOULD run periodic cleanup to remove expired events. The `/sync` endpoint MUST NOT deliver expired events.

### E2EE

When a room is encrypted, clients MUST encrypt ephemeral event content using the room's Megolm session before sending:

1. The client checks whether the room is encrypted.
2. If encrypted: the client wraps the content with Megolm encryption and sets `eventType` to `m.room.encrypted`.
3. The encrypted event is sent via `PUT .../ephemeral/m.room.encrypted/{txnId}`.
4. The server stores the event content-agnostically.
5. `/sync` delivers the encrypted event. Receiving clients decrypt with shared Megolm room keys.

This reuses existing room Megolm sessions — no separate key management is required. This follows the [MSC3673] pattern for encrypted ephemeral data units.

## Potential Issues

- **No delivery guarantee:** Ephemeral events are best-effort. Clients MUST NOT rely on ephemeral events as the sole delivery mechanism for critical data. Applications SHOULD provide a persisted fallback (e.g. timeline edits for streaming).
- **TTL semantics are server-defined:** The TTL is a server implementation detail, not a client-controlled parameter. Different servers MAY use different TTL values, which could affect applications that assume a specific event lifetime.
- **Dedup key constraints:** The composite dedup key `(room_id, sender, event_type, txn_id)` means that two different senders MAY use the same `txn_id` for the same `event_type` without conflict, but a single sender reusing a `txn_id` will have the second event silently dropped.

## Alternatives

### `to_device` events

`to_device` events provide direct device-to-device messaging but bypass room semantics entirely. They require the sender to enumerate target devices, do not benefit from server-side room membership filtering, and cannot be delivered to all room members via a single API call.

### Reusing `m.typing`

The existing `m.typing` mechanism is limited to a single boolean per user per room. It cannot carry arbitrary payloads, custom types, or per-event content. Extending `m.typing` to support custom data would be a breaking change to a well-established API.

### MSC2477 directly

Adopting [MSC2477] with its `org.matrix.msc2477` prefix is the eventual goal. The `com.beeper.ephemeral` prefix is used in the interim because MSC2477 has not yet been merged, and we need to ship real-time features today. The protocol semantics are intentionally aligned to make migration straightforward.

## Security Considerations

- **Power level enforcement:** Servers MUST check the sender's power level before accepting ephemeral events. Without power level checks, any joined user could flood a room with ephemeral events.
- **Content size limits:** Servers MUST enforce the 64KB content size limit. Unbounded content could be used for denial-of-service attacks on the `/sync` pipeline.
- **E2EE requirement for sensitive data:** Applications sending sensitive data (e.g. tool call parameters, user input) via ephemeral events in encrypted rooms MUST encrypt the content per the E2EE section above. Sending plaintext ephemeral events in encrypted rooms leaks data to the server.
- **Rate limiting:** Servers SHOULD apply rate limits to the ephemeral event endpoint. High-frequency streaming use cases (e.g. AI token-by-token output) can generate significant load.

## Unstable Prefix

While this proposal is not yet part of the Matrix specification, implementations MUST use the following unstable prefix:

| Unstable | Stable (future) |
|----------|----------------|
| `com.beeper.ephemeral` (endpoint path) | Aligned with [MSC2477] — `org.matrix.msc2477` or future `m.ephemeral` |

## Dependencies

- [MSC2477]: User-defined ephemeral events — the upstream proposal this implementation is based on.
- [MSC3673]: Encrypted ephemeral data units — the pattern for E2EE ephemeral events.

[MSC2477]: https://github.com/matrix-org/matrix-spec-proposals/pull/2477
[MSC3673]: https://github.com/matrix-org/matrix-spec-proposals/pull/3673
