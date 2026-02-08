# Matrix AI UIMessage Profile v1

## Status
- Proposed v1 (implementation target)
- Scope: Matrix transport profile for Vercel AI SDK `UIMessage` and `UIMessageChunk`
- Requires homeserver and client support (custom ephemeral events + rendering/consumption).
- Beeper is building experimental support for this profile. It is highly experimental and might never get a public release.
- See also: `docs/matrix-ai-events-and-approvals-spec-v1.md` for the broader `com.beeper.ai.*` event/approval surface.

## Upstream Reference
- Vercel AI SDK source inspected at commit `ff7dd528f3933f67bf4568126db0a81cd4a47a96` (2026-02-06 UTC)
- Core types:
  - `packages/ai/src/ui/ui-messages.ts`
  - `packages/ai/src/ui-message-stream/ui-message-chunks.ts`
  - `packages/ai/src/ui-message-stream/json-to-sse-transform-stream.ts`

## 1. Goals
- Keep one canonical assistant message shape in Matrix timeline (`UIMessage`).
- Stream incremental updates in AI SDK-native chunk format (`UIMessageChunk`).
- Matrixify AI SDK SSE shape without inventing a second chunk protocol.
- Preserve `m.room.message` fallback fields (`msgtype`, `body`) for non-AI clients.

## 2. Event Model

### 2.1 Timeline carrier (canonical message)
- Event type: `m.room.message`
- Required fallback fields: `msgtype`, `body`
- AI field: `com.beeper.ai`
- `com.beeper.ai` MUST be an AI SDK-compatible `UIMessage`.

### 2.2 Streaming carrier (ephemeral deltas)
- Event type: `com.beeper.ai.stream_event`
- Event class: ephemeral
- Content:
  - `turn_id: string` (REQUIRED)
  - `seq: integer` (REQUIRED, starts at 1, strictly increasing per `turn_id`)
  - `part: UIMessageChunk` (REQUIRED)
  - `target_event?: string` (RECOMMENDED; timeline event being updated)
  - `agent_id?: string` (OPTIONAL routing/display metadata)
  - `m.relates_to?: { rel_type: "m.reference", event_id: string }` (RECOMMENDED when `target_event` is present)

### 2.3 Optional timeline projections
- `com.beeper.ai.tool_call`
- `com.beeper.ai.tool_result`
- These are optional denormalized projections. Canonical assistant content remains `com.beeper.ai` (`UIMessage`).

## 3. Matrixified SSE Mapping

AI SDK UI streams emit SSE frames of the form:
- `data: <JSON UIMessageChunk>`
- terminal sentinel `data: [DONE]`

This profile maps them as:
1. For each SSE JSON chunk, send one `com.beeper.ai.stream_event` with:
   - `part = <that JSON chunk>`
   - `seq = next per-turn sequence`
   - `turn_id`, `target_event` envelope fields
2. `data: [DONE]` is transport-level termination and does not require a Matrix event.

Implication:
- Producers MUST NOT remap chunk payload schemas; `part` stays AI SDK-compatible.
- Consumers MUST process `part` as AI SDK `UIMessageChunk`.

## 4. Full UIMessageChunk Compatibility

v1 adopts the full AI SDK chunk union. Producers MAY emit any valid member:
- `start`
- `start-step`
- `finish-step`
- `message-metadata`
- `text-start`
- `text-delta`
- `text-end`
- `reasoning-start`
- `reasoning-delta`
- `reasoning-end`
- `tool-input-start`
- `tool-input-delta`
- `tool-input-available`
- `tool-input-error`
- `tool-approval-request`
- `tool-output-available`
- `tool-output-error`
- `tool-output-denied`
- `source-url`
- `source-document`
- `file`
- `data-*`
- `finish`
- `abort`
- `error`

Consumer requirements:
- MUST accept and safely handle all valid AI SDK chunk types.
- MUST ignore unknown future chunk types without failing the turn.
- MUST NOT persist `data-*` chunks with `transient: true`.

### 4.1 Bridge-specific `data-*` chunks

This bridge emits some `data-*` chunks in `com.beeper.ai.stream_event.part` for UI coordination. Clients that do not recognize them SHOULD ignore them.

- `data-tool-progress` (transient)
  - `data.call_id: string`
  - `data.tool_name: string`
  - `data.status: string`
  - `data.progress?: { message?: string, percent?: number }`
  - `transient: true`
- `data-tool-call-event`
  - `id: "tool-call-event:<toolCallId>"`
  - `data.toolCallId: string`
  - `data.callEventId: string` (Matrix event ID for the `com.beeper.ai.tool_call` projection)
- `data-image_generation_partial` (transient)
  - `data.item_id: string` (provider stream item id)
  - `data.index: number` (partial index)
  - `data.image_b64: string` (base64 image bytes; may be large)
  - `transient: true`
- `data-annotation` (transient)
  - `data.annotation: any` (provider annotation payload)
  - `data.index: number`
  - `transient: true`

## 5. UIMessage Canonical Shape

`com.beeper.ai` canonical payload:
- `id: string`
- `role: "assistant"` for assistant responses
- `metadata?: object`
- `parts: UIMessagePart[]`

Recommended metadata keys:
- `turn_id`
- `agent_id`
- `model`
- `finish_reason`
- `usage` (`prompt_tokens`, `completion_tokens`, `reasoning_tokens`, `total_tokens?`)
- `timing` (`started_at`, `first_token_at`, `completed_at`, unix ms)

Canonical persistence recommendation (bridge internal):
- `canonical_schema = "ai-sdk-ui-message-v1"`
- `canonical_ui_message = <full UIMessage>`
- Persist the full `parts` array (do not down-convert to a reduced subset).

## 6. Ordering and Idempotency

Per turn (`turn_id`):
- `seq` MUST be strictly increasing.
- Duplicate or stale events (`seq <= last_applied_seq`) MUST be ignored.
- Out-of-order events SHOULD be buffered briefly and applied in `seq` order.

Turn identity:
- `turn_id` is the primary stream key.
- `target_event` binds a stream to the placeholder/replaced timeline event.

## 7. Lifecycle

Recommended sender lifecycle:
1. Emit initial placeholder `m.room.message` with fallback text and seed `com.beeper.ai` (`id=turn_id`, empty or partial `parts`).
2. Emit `com.beeper.ai.stream_event` chunks (full AI SDK shape in `part`) with monotonic `seq`.
3. Emit final timeline edit (`m.replace`) containing final fallback text + full final `com.beeper.ai`.

Terminal chunks:
- Turn SHOULD end with one of:
  - `finish`
  - `abort`
  - `error` (optionally followed by `finish` depending producer behavior)

## 8. Relations

- Stream events SHOULD include `target_event` and `m.relates_to` reference.
- Final timeline update MUST use `m.relates_to.rel_type = "m.replace"` against the initial placeholder event.
- Thread/reply relations MAY coexist with `m.replace`.

## 9. JSON Examples

### 9.1 Initial timeline message
```json
{
  "msgtype": "m.text",
  "body": "Thinking...",
  "com.beeper.ai": {
    "id": "turn_123",
    "role": "assistant",
    "metadata": { "turn_id": "turn_123" },
    "parts": []
  }
}
```

### 9.2 Matrixified SSE chunk
```json
{
  "turn_id": "turn_123",
  "seq": 7,
  "target_event": "$initial_event",
  "m.relates_to": {
    "rel_type": "m.reference",
    "event_id": "$initial_event"
  },
  "part": {
    "type": "text-delta",
    "id": "text-turn_123",
    "delta": "hello"
  }
}
```

### 9.3 Final edited timeline message
```json
{
  "msgtype": "m.text",
  "body": "* hello world",
  "m.new_content": {
    "msgtype": "m.text",
    "body": "hello world"
  },
  "m.relates_to": {
    "rel_type": "m.replace",
    "event_id": "$initial_event"
  },
  "com.beeper.ai": {
    "id": "turn_123",
    "role": "assistant",
    "metadata": {
      "turn_id": "turn_123",
      "model": "openai/gpt-5",
      "finish_reason": "stop"
    },
    "parts": [
      { "type": "text", "text": "hello world", "state": "done" }
    ]
  }
}
```

## 10. Implementation Notes

- Desktop consumes `event.part` as `UIMessageChunk` and reconstructs live `UIMessage`.
- Matrix envelope concerns (`turn_id`, `seq`, `target_event`) remain bridge/client responsibilities.
- Prefer AI SDK-compatible stream parsing behavior for chunk semantics (tool partial JSON, metadata merge, data-part replacement by `(type,id)`, step boundaries).
