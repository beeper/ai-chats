# MSC: Message-Anchored AI Streaming

Status: experimental.

## Current model

The bridge starts a turn with a normal placeholder `m.room.message`.

That placeholder may include:

- `com.beeper.ai` for canonical assistant state
- `com.beeper.stream` for live-stream attachment

While the turn is active, the bridge emits `com.beeper.llm` delta envelopes anchored to the placeholder event.

When the turn finishes, the placeholder is replaced by a final edit and the live stream is considered complete.

## Placeholder shape

```json
{
  "msgtype": "m.text",
  "body": "...",
  "com.beeper.ai": {
    "id": "turn_123",
    "role": "assistant",
    "metadata": {
      "turn_id": "turn_123"
    },
    "parts": []
  },
  "com.beeper.stream": {
    "...": "publisher-defined descriptor"
  }
}
```

The descriptor comes from the active `BeeperStreamPublisher`. Transport details are publisher-defined.

## Delta envelope

Each streamed delta is wrapped as:

```json
{
  "turn_id": "turn_123",
  "seq": 7,
  "part": {
    "type": "text-delta",
    "delta": "hello"
  },
  "m.relates_to": {
    "rel_type": "m.reference",
    "event_id": "$placeholder"
  }
}
```

Envelope rules:

- `turn_id` is required
- `seq` is strictly positive and monotonic per turn
- `part` is required
- `m.relates_to.event_id` must point at the placeholder event
- `agent_id` may be included when the sender wants multi-agent routing hints

## Final message

The final timeline edit is the canonical result.

The final `com.beeper.ai` payload is compacted before it is attached to the edit, dropping live-only parts that are useful during streaming but not in the stored message.
In the replacement event, the canonical final payload lives in `m.new_content`; only Matrix edit fallback fields and the `m.replace` relation stay at the top level.

## Out of scope

This document does not define the wire protocol behind the stream publisher abstraction. For the broader Matrix event surface, see [`docs/matrix-ai-matrix-spec-v1.md`](../matrix-ai-matrix-spec-v1.md).
