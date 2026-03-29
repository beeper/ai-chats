# Matrix AI Transport v1

Status: experimental and unstable.

## What the code emits

### 1. Canonical assistant messages

Assistant turns are stored as normal `m.room.message` events with:

- standard Matrix fallback fields such as `msgtype` and `body`
- `com.beeper.ai`, which carries an AI SDK-style `UIMessage`

Current shape:

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
  }
}
```

The final edit keeps `com.beeper.ai` as the canonical payload inside `m.new_content`. Streaming-only UI parts are compacted before final persistence.

### 2. Message-anchored live streaming

When live streaming is available, the placeholder message also carries `com.beeper.stream`.

The bridge code does not hardcode the transport backend. It asks a `BeeperStreamPublisher` for a descriptor, registers the placeholder event, and emits live deltas against that target.

Live delta payloads use the stable `com.beeper.llm` envelope:

```json
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
    "event_id": "$placeholder"
  }
}
```

Envelope fields:

- `turn_id`
- `seq`
- `part`
- `m.relates_to`
- optional `agent_id`

`part` follows the AI SDK `UIMessageChunk` model.

### 3. Finalization

When a turn completes, the placeholder is edited with the final assistant content. The final event is authoritative. The stream descriptor is no longer present after finalization.
For replacement events, Matrix fallback fields remain at the top level while canonical assistant fields such as `com.beeper.ai` and link previews are carried inside `m.new_content`.

### 4. Compaction status events

The AI bridge may emit `com.beeper.ai.compaction_status` timeline events while retrying after context compaction.

Current fields are:

- `type`
- `session_id`
- `messages_before`
- `messages_after`
- `tokens_before`
- `tokens_after`
- `summary`
- `will_retry`
- `error`

### 5. Command descriptions

AI rooms broadcast `org.matrix.msc4391.command_description` state events for the user-facing commands implemented by the bridge. See [`docs/msc/com.beeper.mscXXXX-commands.md`](./msc/com.beeper.mscXXXX-commands.md).

## Extra keys

These keys appear as metadata or rendering hints on Matrix events:

- `com.beeper.ai`
- `com.beeper.stream`
- `com.beeper.ai.model_id`
- `com.beeper.ai.agent`
- `com.beeper.ai.image_generation`
- `com.beeper.ai.tts`

## Notes

- Custom agents are stored in login metadata, not published as room state events.
- `com.beeper.ai.info` is registered as a known state type, but it is not actively broadcast.
- Room capability state is sent through standard Beeper room-feature state, not a custom AI state event.
