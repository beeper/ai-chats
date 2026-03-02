# MSC: AI Streaming Profile

## Summary

This proposal defines an application-level streaming profile on top of [com.beeper.ephemeral](com.beeper.mscXXXX-ephemeral.md) for real-time AI output in Matrix rooms. It specifies an envelope convention for ordered, resumable streaming of AI assistant responses using ephemeral events.

The profile covers the transport envelope and delivery semantics. The authoritative chunk type catalog is maintained in the [AI Matrix Spec](../matrix-ai-matrix-spec-v1.md#streaming).

## Motivation

AI model responses are generated token-by-token and can take tens of seconds to complete. Without real-time streaming, users stare at a blank screen until the full response is ready — a poor experience that makes the assistant feel unresponsive.

Streaming addresses this with three key benefits:

- **Latency perception:** Users see output within milliseconds of generation starting, even when the full response takes 30+ seconds.
- **Progressive rendering:** Clients can render text, reasoning traces, and tool calls incrementally as they arrive.
- **Multi-step agent visibility:** When an AI assistant executes multiple tool calls in sequence, streaming lets users observe each step in real time rather than waiting for the entire chain to complete.

Matrix has no built-in mechanism for high-frequency, ordered, non-persisted event delivery within a room. This profile combines `com.beeper.ephemeral` transport with an application-level envelope to provide exactly that.

## Proposal

### Event Type

```
com.beeper.ai.stream_event
```

This event type is sent via the `com.beeper.ephemeral` transport endpoint.

### Envelope Schema

```json
{
  "turn_id": "turn_123",
  "seq": 7,
  "part": {
    "type": "text-delta",
    "id": "text-turn_123",
    "delta": "hello"
  },
  "target_event": "$initial_event",
  "agent_id": "researcher",
  "m.relates_to": {
    "rel_type": "m.reference",
    "event_id": "$initial_event"
  }
}
```

### Fields

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| `turn_id` | string | yes | Identifier for the conversation turn. All stream events for one assistant response share the same `turn_id`. |
| `seq` | integer | yes | Monotonically increasing sequence number (starts at 1). Used for ordering and gap detection. |
| `part` | object | yes | Streaming chunk object. Structure depends on the `type` field within. |
| `target_event` | string | no | Event ID of the timeline message being streamed into. Set once the first timeline event is sent. |
| `agent_id` | string | no | Identifier of the agent producing this stream (for multi-agent rooms). |
| `m.relates_to` | object | no | Standard Matrix relation. When `target_event` is set, includes `rel_type: "m.reference"`. |

### Part Types

The `part` field carries a streaming chunk. The complete list of supported chunk types is maintained in the [AI Matrix Spec](../matrix-ai-matrix-spec-v1.md#streaming). Key categories:

| Category | Chunk types |
|----------|-------------|
| Lifecycle | `start`, `start-step`, `finish-step`, `message-metadata`, `finish`, `abort`, `error` |
| Text | `text-start`, `text-delta`, `text-end` |
| Reasoning | `reasoning-start`, `reasoning-delta`, `reasoning-end` |
| Tool input | `tool-input-start`, `tool-input-delta`, `tool-input-available`, `tool-input-error` |
| Tool output | `tool-approval-request`, `tool-output-available`, `tool-output-error`, `tool-output-denied` |
| Sources | `source-url`, `source-document`, `file` |
| Bridge-specific | `data-tool-progress`, `data-tool-call-event`, `data-image_generation_partial`, `data-annotation` |

Consumers MUST accept all valid chunk types and MUST ignore unknown future types.

### Transaction ID Convention

Stream events MUST use the following transaction ID format:

```
ai_stream_{turn_id}_{seq}
```

This ensures idempotent delivery via the `com.beeper.ephemeral` deduplication mechanism (composite key on `room_id`, `sender`, `event_type`, `txn_id`).

### E2EE

Inherited from the transport layer. When the room is encrypted, clients MUST encrypt stream event content using the room's Megolm session per [MSC3673]. Receiving clients decrypt using shared room keys.

### Fallback: Debounced Timeline Edits

When ephemeral delivery is unavailable (server returns `404`, `405`, `501`, or `M_UNRECOGNIZED`), the sender SHOULD fall back to debounced `m.replace` edits on the timeline message. The sender SHOULD auto-detect this on first failure and switch to timeline edits for the remainder of the turn.

### Client Behavior

1. Subscribe to `com.beeper.ai.stream_event` in `/sync` ephemeral events.
2. Group events by `turn_id`.
3. Order by `seq` within each turn; ignore events with `seq <= last_applied_seq`.
4. Apply `part` content incrementally using the chunk type semantics.
5. When `target_event` appears, associate the stream with the timeline message.
6. Terminal chunks (`finish`, `abort`, `error`) signal end of stream.

### Resilience

- Gaps in `seq` indicate missed events (ephemeral events have no delivery guarantee).
- Clients SHOULD gracefully degrade: if stream events are missed, the finalized timeline message (`m.replace` edit) contains the complete content.
- `target_event` allows late-joining clients to skip the stream and read the persisted message directly.

## Potential Issues

- **No delivery guarantee:** Ephemeral events are best-effort. Clients that miss stream events rely on the final timeline edit for complete content. This means streaming is a progressive enhancement, not the authoritative source.
- **Sequence gaps:** Network issues or server load may cause `seq` gaps. Clients MUST handle missing sequence numbers gracefully rather than blocking on them.
- **Ordering across federation:** Federated servers may deliver ephemeral events out of order. The `seq` field allows receivers to reorder, but significant delays may cause visual jitter.

## Alternatives

### Timeline edits only

Using `m.replace` edits for every chunk would persist each intermediate state and generate excessive server load. Ephemeral events avoid this by keeping intermediate states transient — only the final content is persisted.

### `to_device` events

`to_device` messages could deliver stream chunks directly to specific devices. However, they bypass room membership semantics, cannot be aggregated by the server, and would require separate delivery logic per device rather than leveraging the existing `/sync` room ephemeral pipeline.

## Security Considerations

- **Stream events in encrypted rooms:** Stream events in encrypted rooms MUST be encrypted using Megolm per [MSC3673]. Implementations MUST NOT send plaintext stream events in encrypted rooms.
- **`agent_id` spoofing:** In multi-agent rooms, a malicious client could send stream events with a forged `agent_id`. Clients SHOULD verify that stream events originate from an expected sender (e.g. the bridge bot user) before rendering them.
- **Content size:** Individual stream events SHOULD be small (typically under 1KB). Servers MAY reject ephemeral events exceeding the 64KB transport limit.

## Unstable Prefix

While this proposal is not yet part of the Matrix specification, implementations MUST use the following unstable prefix:

| Unstable | Stable (future) |
|----------|----------------|
| `com.beeper.ai.stream_event` | `m.ai.stream_event` |

## Dependencies

- [com.beeper.ephemeral](com.beeper.mscXXXX-ephemeral.md): Custom room ephemeral events — the transport layer.
- [MSC2477]: User-defined ephemeral events — the upstream proposal that `com.beeper.ephemeral` implements.
- [MSC3673]: Encrypted ephemeral data units — E2EE for ephemeral events.

[MSC2477]: https://github.com/matrix-org/matrix-spec-proposals/pull/2477
[MSC3673]: https://github.com/matrix-org/matrix-spec-proposals/pull/3673
