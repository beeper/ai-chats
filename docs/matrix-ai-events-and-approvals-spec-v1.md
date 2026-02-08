# Matrix AI Events and Approvals v1

## Status
- Proposed v1 (implementation describes current behavior)
- Requires homeserver and client support (custom event types, ephemeral events, and rendering/consumption).
- Beeper is building experimental support for this profile. It is highly experimental and might never get a public release.

## Scope
This document specifies the `com.beeper.ai*` surface used by this bridge:
- Custom Matrix event types under `com.beeper.ai.*`.
- Content keys under `com.beeper.ai*` used inside standard Matrix events (for example `m.room.message`, `m.room.member`).
- The tool approval flow (MCP approvals + selected builtin tools).

Streaming chunks and the canonical `UIMessage` payload are specified in `docs/matrix-ai-uimessage-spec-v1.md`.

## Compatibility Requirements
- Homeserver support for custom event types and ephemeral events is required.
- Clients must explicitly implement rendering/consumption of these custom types; non-supporting clients should fall back to `m.room.message.body`.

## Terminology
- `turn_id`: Unique ID for a single assistant response “turn”. Used to key streaming and correlate related events.
- `agent_id`: Optional identifier for the agent/persona that produced the turn.
- `call_id` / `toolCallId`: Identifier for a tool invocation.
- `target_event`: Matrix event ID that a stream or projection relates to.

## Inventory
Authoritative identifiers are defined in `pkg/matrixevents/matrixevents.go`.

Event types:
- Timeline message events: `com.beeper.ai.*` with Matrix event class “message”.
- Ephemeral message events: `com.beeper.ai.*` with Matrix event class “ephemeral”.
- State events: `com.beeper.ai.*` with Matrix event class “state”.

Content keys:
- `com.beeper.ai` (canonical assistant content: AI SDK `UIMessage`)
- `com.beeper.ai.tool_call` (tool call projection)
- `com.beeper.ai.tool_result` (tool result projection)
- `com.beeper.ai.error` (AI error payload)
- `com.beeper.ai.agent_handoff` (handoff payload)
- `com.beeper.ai.agent` (routing/display or agent definition depending on event)
- `com.beeper.ai.model_id` (routing/display)
- `com.beeper.ai.image_generation` (generated-image metadata tag)
- `com.beeper.ai.tts` (generated-audio metadata tag)
- `com.beeper.ai.approval_decision` (inbound approval decision payload)

Capability IDs:
- `com.beeper.ai.capabilities.2026_02_05` (room “features” capability ID used for AI rooms; see `pkg/connector/client.go`).

HTTP namespace (unstable):
- Beeper provider base URL normalization uses `/_matrix/client/unstable/com.beeper.ai` (see `pkg/connector/login.go` and `pkg/connector/token_resolver.go`).

## Canonical Assistant Messages (`m.room.message` + `com.beeper.ai`)
The canonical assistant message is carried in a standard `m.room.message` event with a `com.beeper.ai` field containing an AI SDK-compatible `UIMessage`.

See `docs/matrix-ai-uimessage-spec-v1.md`.

## Streaming Deltas (`com.beeper.ai.stream_event`)
Streaming uses ephemeral `com.beeper.ai.stream_event` events containing an envelope (`turn_id`, `seq`, `target_event`) and a `part` that is an AI SDK `UIMessageChunk`.

See `docs/matrix-ai-uimessage-spec-v1.md`.

## Timeline Projection Events
These events are sent as separate timeline items (custom message event types) to support richer UI and/or non-streaming clients.

### `com.beeper.ai.tool_call`
Purpose:
- A timeline-visible projection of a tool invocation.
- Emitted when a tool call starts (see `pkg/connector/tool_execution.go`).

Schema (event content):
- `body: string` (fallback)
- `msgtype: "m.notice"` (fallback)
- `com.beeper.ai.tool_call: object`
  - `call_id: string` (required)
  - `turn_id: string` (required)
  - `agent_id?: string`
  - `tool_name: string` (required)
  - `tool_type: "builtin"|"provider"|"function"|"mcp"` (required)
  - `status: "running"|...` (required)
  - `input?: object`
  - `display?: { title?: string, icon?: string, collapsed?: boolean }`
  - `timing?: { started_at?: number, first_token_at?: number, completed_at?: number }` (unix ms)
  - `result_event?: string` (optional reference to result event)
  - `requires_approval?: boolean`
  - `approval?: { reason?: string, actions?: string[] }`

Relations:
- If the turn has an initial placeholder event, the tool call event SHOULD include `m.relates_to = { rel_type: "m.reference", event_id: <placeholder> }`.

Example:
```json
{
  "body": "Calling Web Search...",
  "msgtype": "m.notice",
  "m.relates_to": { "rel_type": "m.reference", "event_id": "$turn_placeholder" },
  "com.beeper.ai.tool_call": {
    "call_id": "call_123",
    "turn_id": "turn_123",
    "agent_id": "researcher",
    "tool_name": "web_search",
    "tool_type": "provider",
    "status": "running",
    "input": { "query": "matrix event types" },
    "display": { "title": "Web Search", "collapsed": false },
    "timing": { "started_at": 1738970000000 }
  }
}
```

### `com.beeper.ai.tool_result`
Purpose:
- A timeline-visible projection of the tool result.
- Emitted when a tool call finishes (see `pkg/connector/tool_execution.go`).

Schema (event content):
- `body: string` (fallback)
- `msgtype: "m.notice"` (fallback)
- `com.beeper.ai.tool_result: object`
  - `call_id: string` (required)
  - `turn_id: string` (required)
  - `agent_id?: string`
  - `tool_name: string` (required)
  - `status: "success"|"error"|"partial"` (required)
  - `output?: object`
  - `artifacts?: { type: "file"|"image", mxc_uri?: string, filename?: string, mimetype?: string, size?: number }[]`
  - `display?: { format?: string, expandable?: boolean, default_expanded?: boolean, show_stdout?: boolean, show_artifacts?: boolean }`

Relations:
- Tool results SHOULD reference the tool call event with `m.relates_to = { rel_type: "m.reference", event_id: <tool_call_event_id> }`.

Example:
```json
{
  "body": "Search completed",
  "msgtype": "m.notice",
  "m.relates_to": { "rel_type": "m.reference", "event_id": "$tool_call_event" },
  "com.beeper.ai.tool_result": {
    "call_id": "call_123",
    "turn_id": "turn_123",
    "tool_name": "web_search",
    "status": "success",
    "output": { "status": "completed", "results": [] },
    "display": { "expandable": true, "default_expanded": false }
  }
}
```

### `com.beeper.ai.compaction_status`
Purpose:
- Status events emitted during context compaction/retry.
- Emitted by `pkg/connector/response_retry.go`.

Schema (event content):
- `type: "compaction_start"|"compaction_end"` (required)
- `session_id?: string`
- `messages_before?: number`
- `messages_after?: number`
- `tokens_before?: number`
- `tokens_after?: number`
- `summary?: string`
- `will_retry?: boolean`
- `error?: string`
- `duration_ms?: number`

Example:
```json
{
  "type": "compaction_end",
  "session_id": "main",
  "messages_before": 50,
  "messages_after": 20,
  "tokens_before": 80000,
  "tokens_after": 30000,
  "summary": "...",
  "will_retry": true,
  "duration_ms": 742
}
```

### Reserved / Future Timeline Events
The bridge defines (but does not necessarily emit in current builds) additional message event types with schemas in `pkg/connector/events.go`:
- `com.beeper.ai.assistant_turn` (`AssistantTurnContent` / `AssistantTurnAI`)
- `com.beeper.ai.error` (`AIErrorContent` / `AIErrorData`)
- `com.beeper.ai.turn_cancelled` (`TurnCancelledContent`)
- `com.beeper.ai.agent_handoff` (`AgentHandoffContent` / `AgentHandoffData`)
- `com.beeper.ai.step_boundary` (`StepBoundaryContent`)
- `com.beeper.ai.generation_status` (`GenerationStatusContent`)
- `com.beeper.ai.tool_progress` (`ToolProgressContent`)
- `com.beeper.ai.approval_request` (`ApprovalRequestContent`)

These are part of the v1 surface area and may be emitted by future bridge/client implementations.

## State Events
State events are used to broadcast room configuration and capabilities.

### `com.beeper.ai.room_capabilities`
Purpose:
- Bridge-controlled room capabilities and effective settings.
- Emitted by `broadcastCapabilities` in `pkg/connector/chat.go`.
- SHOULD be protected by power levels so only the bridge bot can set it.

Schema: `RoomCapabilitiesEventContent` in `pkg/connector/events.go`.

Example:
```json
{
  "capabilities": {
    "supports_reasoning": true,
    "supports_tool_calling": true
  },
  "available_tools": [
    {"name": "web_search", "display_name": "Web Search", "type": "provider", "enabled": true, "available": true}
  ],
  "reasoning_effort_options": [
    {"value": "low", "label": "Low"},
    {"value": "medium", "label": "Medium"},
    {"value": "high", "label": "High"}
  ],
  "provider": "beeper",
  "effective_settings": {
    "model": {"value": "openai/gpt-5", "source": "room_override"},
    "system_prompt": {"value": "", "source": "global_default"},
    "temperature": {"value": 0.7, "source": "room_override"},
    "reasoning_effort": {"value": "medium", "source": "room_override"}
  }
}
```

### `com.beeper.ai.room_settings`
Purpose:
- User-editable room settings.
- Emitted by `broadcastSettings` in `pkg/connector/chat.go`.

Schema: `RoomSettingsEventContent` in `pkg/connector/events.go`.

Example:
```json
{
  "model": "openai/gpt-5",
  "system_prompt": "",
  "temperature": 0.7,
  "max_context_messages": 50,
  "max_completion_tokens": 2048,
  "reasoning_effort": "medium",
  "conversation_mode": "responses",
  "agent_id": "boss",
  "emit_thinking": true,
  "emit_tool_args": false
}
```

### `com.beeper.ai.model_capabilities` (defined; currently not emitted)
Schema: `ModelCapabilitiesEventContent` in `pkg/connector/events.go`.

### `com.beeper.ai.agents` (defined; currently not emitted)
Schema: `AgentsEventContent` in `pkg/connector/events.go`.

## Other `com.beeper.ai*` Keys in Standard Events

### Routing/Display Hints on `m.room.message`
The bridge may set the following top-level keys on `m.room.message` events:
- `com.beeper.ai.model_id: string` (model identifier used for routing/selection)
- `com.beeper.ai.agent: string` (agent identifier used for routing/selection)

### Agent Definitions in `m.room.member` (Builder room)
In Builder rooms, agent definitions can be persisted in `m.room.member` state events (see `AgentMemberContent` in `pkg/connector/events.go`):
- `com.beeper.ai.agent: AgentDefinitionContent`

Example (member state content excerpt):
```json
{
  "membership": "join",
  "displayname": "Researcher",
  "avatar_url": "mxc://example.org/abc",
  "com.beeper.ai.agent": {
    "id": "researcher",
    "name": "Researcher",
    "description": "Finds sources",
    "model": "openai/gpt-5",
    "created_at": 1738970000000,
    "updated_at": 1738970000000
  }
}
```

### AI-Generated Media Tags
AI-generated media messages may carry additional metadata under:
- `com.beeper.ai.image_generation` (for generated images)
- `com.beeper.ai.tts` (for TTS-generated audio)

These keys are used as “tags”/metadata carriers when sending `m.image`/`m.audio` events (see `pkg/connector/image_generation.go` and `pkg/connector/audio_generation.go`).

## Approvals
Tool approvals are an owner-only gate for:
- MCP approvals (OpenAI Responses `mcp_approval_request` items).
- Selected builtin tool actions, configured via `network.tool_approvals.requireForTools`.

Runtime config is under `network.tool_approvals` (see `pkg/connector/example-config.yaml` and `pkg/connector/config.go`):
- `enabled` (default true)
- `ttlSeconds` (default 600)
- `requireForMcp` (default true)
- `requireForTools` (default list in code)

### When Approval Is Required
- MCP: required when `enabled=true`, `requireForMcp=true`, and the tool is not already always-allowed.
- Builtins: required when `enabled=true` and the tool name is in `requireForTools`, subject to per-tool action allowlists (see `pkg/connector/tool_approvals_policy.go`).

### Approval Request Emission
When approval is needed, the bridge emits:
1. An ephemeral stream chunk (`com.beeper.ai.stream_event`) where `part.type = "tool-approval-request"` containing:
   - `approvalId: string`
   - `toolCallId: string`
2. A timeline-visible fallback notice for clients that drop/ignore ephemeral events.
   - The fallback is an `m.room.message` with `msgtype = m.notice` and a `com.beeper.ai` `UIMessage` that includes a `dynamic-tool` part with `state = "approval-requested"`.

### Approving / Denying
Approvals can be resolved via:
- Command: `/approve <approvalId> <allow|always|deny> [reason]` (owner-only).
- Message payload: send an `m.room.message` whose raw content includes a `com.beeper.ai.approval_decision` object.

Approval decision payload schema:
- `approvalId: string` (required)
- `decision: "allow"|"always"|"deny"` (required)
- `reason?: string`

Example:
```json
{
  "com.beeper.ai.approval_decision": {
    "approvalId": "abc123",
    "decision": "always",
    "reason": "ok"
  }
}
```

Owner-only enforcement:
- The bridge rejects approvals from non-owner senders (see `pkg/connector/inbound_command_handlers.go` and `pkg/connector/handlematrix.go`).

### Always-Allow Persistence
If the user approves with `always`, the bridge persists an always-allow rule in the login metadata:
- MCP: `(serverLabel, toolName)`
- Builtin: `(toolName, action)` where applicable

### TTL and Timeouts
- Approval requests expire after `ttlSeconds`.
- On expiry, the pending approval is dropped and may be treated as denied/timeout depending on the caller.

### Outcome UI
After a decision, the bridge edits the timeline fallback notice to reflect the result:
- Approved: `dynamic-tool` part `state = "output-available"`
- Denied: `dynamic-tool` part `state = "output-denied"` (with optional error text)

## Forward Compatibility
- Clients MUST ignore unknown `com.beeper.ai.*` event types and unknown fields.
- Clients MUST ignore unknown future streaming chunk types (AI SDK-compatible forward extension).
