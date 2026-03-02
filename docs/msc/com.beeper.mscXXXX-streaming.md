# com.beeper.ai.stream_event — AI Streaming Profile

**Prior art:** [MSC2477](https://github.com/matrix-org/matrix-spec-proposals/pull/2477) (user-defined ephemeral events)

**Transport:** [com.beeper.ephemeral](com.beeper.mscXXXX-ephemeral.md) (our MSC2477 implementation)

**Status:** Implemented and running in ai-bridge.

## Summary

This document defines a profile on top of `com.beeper.ephemeral` for real-time AI streaming in Matrix rooms. The profile specifies an application-level envelope convention for ordered, resumable streaming of AI assistant output.

The authoritative chunk type catalog is in [matrix-ai-matrix-spec-v1.md](../matrix-ai-matrix-spec-v1.md#streaming) — this document covers only the transport envelope and delivery semantics.

## Event Type

```
com.beeper.ai.stream_event
```

Registered as `EphemeralEventType` in mautrix-go.

## Envelope Schema

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
| `turn_id` | string | yes | UUID identifying the conversation turn. All stream events for one assistant response share the same `turn_id`. |
| `seq` | integer | yes | Monotonically increasing sequence number (starts at 1). Used for ordering and gap detection. |
| `part` | object | yes | AI SDK `UIMessageChunk` payload. Structure depends on `part.type`. |
| `target_event` | string | no | Event ID of the timeline message being streamed into. Set once the first timeline event is sent. |
| `agent_id` | string | no | Identifier of the agent producing this stream (for multi-agent rooms). |
| `m.relates_to` | object | no | Standard Matrix relation. When `target_event` is set, includes `rel_type: "m.reference"`. |

### Part Types

The `part` field carries an AI SDK `UIMessageChunk`. The complete list of supported chunk types is maintained in the [main spec](../matrix-ai-matrix-spec-v1.md#streaming) under "Chunk Compatibility". Key categories:

| Category | Chunk types |
|----------|-------------|
| Lifecycle | `start`, `start-step`, `finish-step`, `message-metadata`, `finish`, `abort`, `error` |
| Text | `text-start`, `text-delta`, `text-end` |
| Reasoning | `reasoning-start`, `reasoning-delta`, `reasoning-end` |
| Tool input | `tool-input-start`, `tool-input-delta`, `tool-input-available`, `tool-input-error` |
| Tool output | `tool-approval-request`, `tool-output-available`, `tool-output-error`, `tool-output-denied` |
| Sources | `source-url`, `source-document`, `file` |
| Bridge-specific | `data-tool-progress`, `data-tool-call-event`, `data-image_generation_partial`, `data-annotation` |

Consumers MUST accept all valid AI SDK chunk types and MUST ignore unknown future types.

## Transaction ID Convention

```
ai_stream_{turn_id}_{seq}
```

Built by `BuildStreamEventTxnID()` in `pkg/matrixevents/matrixevents.go`. Ensures idempotent delivery via the `com.beeper.ephemeral` deduplication mechanism.

## E2EE

Inherited from the transport layer. When the room is encrypted, mautrix-go's `SendEphemeralEvent()` wraps the content with Megolm before sending. Clients decrypt using shared room keys.

## Fallback: Debounced Timeline Edits

When ephemeral delivery is unavailable (server returns 404/405/501/`M_UNRECOGNIZED`), the bridge falls back to debounced `m.replace` edits on the timeline message. The stream transport auto-detects this on first failure and switches for the remainder of the turn.

Implementation: `pkg/shared/streamtransport/session.go`, `pkg/shared/streamtransport/fallback.go`.

## Client Behavior

1. Subscribe to `com.beeper.ai.stream_event` in `/sync` ephemeral events
2. Group events by `turn_id`
3. Order by `seq` within each turn; ignore events with `seq <= last_applied_seq`
4. Apply `part` content incrementally using AI SDK `UIMessageChunk` semantics
5. When `target_event` appears, associate the stream with the timeline message
6. Terminal chunks (`finish`, `abort`, `error`) signal end of stream

## Resilience

- Gaps in `seq` indicate missed events (ephemeral events have no delivery guarantee)
- Clients should gracefully degrade: if stream events are missed, the finalized timeline message (`m.replace` edit) contains the complete content
- `target_event` allows late-joining clients to skip the stream and read the persisted message
