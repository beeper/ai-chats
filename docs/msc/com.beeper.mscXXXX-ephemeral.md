# com.beeper.ephemeral — Custom Room Ephemeral Events

**Prior art:** [MSC2477](https://github.com/matrix-org/matrix-spec-proposals/pull/2477) (user-defined ephemeral events), [MSC3673](https://github.com/matrix-org/matrix-spec-proposals/pull/3673) (encrypted ephemeral data units)

**Status:** Implemented in hungryserv + mautrix-go. Ready to merge `batuhan/com-beeper-ephemeral` branch.

## Summary

`com.beeper.ephemeral` provides a transport for custom ephemeral events in Matrix rooms. This is our implementation of MSC2477 with a `com.beeper` unstable prefix, plus transparent E2EE support following the MSC3673 pattern.

Ephemeral events are short-lived, non-persisted events delivered via `/sync` to joined room members. They are useful for real-time features like AI streaming, live indicators, and collaborative cursors.

## Differences from MSC2477

| Aspect | MSC2477 | com.beeper.ephemeral |
|--------|---------|---------------------|
| Unstable prefix | `org.matrix.msc2477` | `com.beeper.ephemeral` |
| Endpoint | `PUT /_matrix/client/unstable/org.matrix.msc2477/rooms/{roomId}/ephemeral/{type}/{txnId}` | `PUT /_matrix/client/unstable/com.beeper.ephemeral/rooms/{roomId}/ephemeral/{type}/{txnId}` |
| Power levels key | `ephemeral` + `ephemeral_default` (default 50) | `GetEphemeralLevel()` (same concept) |
| TTL | Not specified | 2 minutes, server-side cleanup every 60s |
| Timestamp | `origin_server_ts` on event | `?ts=` query param on PUT, stored as `origin_server_ts` |
| Response | `{}` | `{}` (empty body) |
| Built-in type blocking | Rejects `m.*` types | No type restriction (power levels apply) |
| Sync delivery | `ephemeral` section of `/sync` rooms | Same — added to `joined.Ephemeral.Events` |

## Client-Server API

### Sending

```
PUT /_matrix/client/unstable/com.beeper.ephemeral/rooms/{roomId}/ephemeral/{eventType}/{txnId}
```

**Request body:** Arbitrary JSON content.

**Query parameters:**
- `ts` (optional): Unix millisecond timestamp for `origin_server_ts`.

**Auth:** Standard Matrix access token. Sender must be joined to the room.

**Power levels:** Checked via `GetEphemeralLevel(eventType)` on room power levels.

**Constraints:**
- Maximum content size: 64KB
- Deduplication: `INSERT OR IGNORE` on `(room_id, sender, event_type, txn_id)` composite PK

**Response:** `200 OK`
```json
{}
```

### Receiving (via /sync)

Events appear in `rooms.join.{roomId}.ephemeral.events[]`:

```json
{
  "type": "com.example.custom",
  "sender": "@user:server",
  "origin_server_ts": 1709123456000,
  "room_id": "!room:server",
  "content": { ... }
}
```

## E2EE (MSC3673 pattern)

mautrix-go encrypts ephemeral events transparently:

1. `Client.SendEphemeralEvent()` checks `IsEncrypted(roomID)`
2. If encrypted: wraps content with Megolm (`cli.Crypto.Encrypt()`), sets `eventType = m.room.encrypted`
3. Sends to endpoint as `PUT .../ephemeral/m.room.encrypted/{txnId}`
4. Server stores content-agnostically
5. `/sync` delivers the encrypted event; client decrypts with shared Megolm keys

Reuses existing room Megolm sessions — no separate session management.

## Server Implementation (hungryserv)

### Storage

```sql
CREATE TABLE ephemeral_events (
    room_id          TEXT    NOT NULL,
    sender           TEXT    NOT NULL,
    event_type       TEXT    NOT NULL,
    content          JSONB   NOT NULL,
    origin_server_ts INTEGER NOT NULL,
    txn_id           TEXT    NOT NULL,
    expire_ts        INTEGER NOT NULL,
    PRIMARY KEY (room_id, sender, event_type, txn_id)
);
```

### Lifecycle

- Events expire after 2 minutes (`ephemeralEventTTL`)
- Cleanup job runs every 60 seconds, deletes rows where `expire_ts <= now`
- `/sync` filters out expired events (`WHERE expire_ts > ?now`)

### Sync Integration

- `EphemeralEventsSyncStep` in `/sync` pipeline alongside `ReadReceiptsSyncStep` and `TypingNotificationsSyncStep`
- Tracks progress via `since.Ephemeral` rowid cursor
- Only delivers events in rooms where user has `membership: join`

### Appservice Transactions

Custom ephemeral events are NOT forwarded in appservice transactions. Appservices are senders, not receivers — they use the client-server PUT endpoint directly.

## mautrix-go Implementation

### Client layer (`client.go:1362-1401`)
- `Client.SendEphemeralEvent()` — txnID generation, timestamp, E2EE wrapping
- Calls `/_matrix/client/unstable/com.beeper.ephemeral/rooms/...`

### Bridge layer (`bridgev2/matrixinterface.go:221-225`)
- `EphemeralSendingMatrixAPI` interface — extends `MatrixAPI` with `SendEphemeralEvent()`
- `ASIntent` implements it (`bridgev2/matrix/intent.go:88`) — adds encryption + `EnsureJoined()`

### Appservice layer (`appservice/intent.go:225-231`)
- `IntentAPI.SendEphemeralEvent()` — ensures joined, adds double puppet value, delegates to `Client`

## Files (hungryserv branch: `batuhan/com-beeper-ephemeral`)

14 files, +402 lines:
- `routes/roomephemeral.go` — PUT route handler
- `controller/ephemeral.go` — `SendEphemeralEvent()` with room/power checks
- `controller/sync/ephemeralevents.go` — `EphemeralEventsSyncStep`
- `database/ephemeral_events.go` — DB layer with Insert/GetSince/GetSinceUntil/DeleteExpired
- `database/upgrades/83-ephemeral-events.sql` — schema migration
- `jobs/ephemeral.go` — cleanup job (every minute, deletes expired)
