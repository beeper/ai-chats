# Rewrite Plan

## Goal

Rewrite the remaining architecture from first principles into one canonical
shape that is:

- simple
- easy to follow
- non-duplicated
- unapologetically non-backward-compatible

The target is the taste level of Pi:

- one clean provider layer
- one clean agent/runtime loop
- thin integration edges

And the subsystem discipline of OpenClaw:

- session logic in one place
- media logic in one place
- product/channel wiring at the edge

## Upstream References

### Pi

- `pi-mono/packages/ai/src/types.ts`
- `pi-mono/packages/ai/src/api-registry.ts`
- `pi-mono/packages/ai/src/stream.ts`
- `pi-mono/packages/ai/src/providers/openai-responses.ts`
- `pi-mono/packages/agent/src/agent.ts`
- `pi-mono/packages/agent/src/agent-loop.ts`

### OpenClaw

- `openclaw/src/channels/session.ts`
- `openclaw/src/media/host.ts`

## Final Shape

### `bridgev2`

Owns:

- connector and login contracts
- portal lifecycle
- Matrix room ownership
- remote event transport boundaries
- generic bridge metadata/capability refresh

### `sdk`

Owns:

- one agent runtime contract
- one turn loop
- one turn persistence/replay model
- one approval subsystem
- one minimal login helper surface
- one shared event/send helper surface

It does not own:

- provider policy
- AI session policy
- AI room semantics
- AI product behavior

### `bridges/ai`

Owns:

- provider/model selection policy
- prompt policy and system prompts
- AI room semantics
- heartbeat product behavior
- AI tool catalog and policy
- AI session semantics
- AI-specific media/presentation policy

It does not own:

- second copies of runtime state machines
- second copies of approval machinery
- second copies of prompt serialization frameworks
- generic provider frameworks that do not buy anything

## Module Target

The intended long-term code organization is:

### `sdk/approval`

- approval request registration
- prompt/edit lifecycle
- wait/finalize/resolve

### `sdk/agent`

- runtime state
- event stream
- one turn loop

### `sdk/turn`

- canonical turn state
- final edit shaping
- replay/snapshot from canonical state

### `sdk/login`

- minimal process helpers
- command selection helpers

### `sdk/events`

- send/edit/status helpers that truly add value above `bridgev2`

### `bridges/ai/internal/prompt`

- canonical prompt model
- provider serialization
- turn projection/replay adapters

### `bridges/ai/internal/provider`

- provider capability/config table
- provider runtime construction
- auth/base URL/model defaults

### `bridges/ai/internal/session`

- canonical session keys
- session persistence
- routing/selection
- heartbeat session selection

### `bridges/ai/internal/runtime`

- queue/run state
- terminalization
- heartbeat/runtime execution

### `bridges/ai/internal/media`

- AI-specific media behavior only
- generic transport/normalization delegated to shared helpers

## Hard Rewrite Rules

1. One behavior, one owner.
2. One persisted field, one write path.
3. No compatibility shells.
4. No wrappers that only rename or forward.
5. No secondary framework inside `bridges/ai`.
6. Prefer deletion to abstraction.
7. If a subsystem cannot be explained in one screen, collapse it.

## Completed Passes

Already finished:

- SDK helper cleanup around runtime getters, cache lifecycle, approval request
  construction, bridge-info formatting, approval prompt formatting, and the
  embedded stream-state base layer
- AI helper cleanup around queue dispatching, continuation/finalization,
  portal send/edit, heartbeat/session routing, prompt assembly, contact
  resolution, retrieval token application, prompt/state constant shims, and
  one-use accessors
- Retrieval cleanup around env defaults, provider registration, provider
  constructors, Exa wrapper surfaces, and direct-fetch defaults
- Bridge-local wrapper deletion where `bridges/ai` and `bridges/codex` were
  just forwarding into shared SDK helpers

This means the rewrite should now focus on subsystem collapse, not more tiny
utility deletion as the primary workstream.

## Updated Priorities

The highest-value remaining work is now:

1. Streaming terminalizer
2. Prompt canonicalization
3. Session subsystem
4. Provider consolidation
5. Queue/runtime/heartbeat unification
6. `runtimeIntegrationHost` reduction
7. SDK runtime/loading collapse

## Execution Order

### Phase 1: Streaming Terminalizer

Target files:

- `bridges/ai/streaming_responses_api.go`
- `bridges/ai/streaming_success.go`
- `bridges/ai/streaming_error_handling.go`
- `bridges/ai/response_finalization.go`
- `bridges/ai/streaming_state.go`

Deliverable:

- one terminal state machine
- one finalization owner
- one path for `turn.End(...)`
- one place where provider finish/status becomes persisted/runtime state
- one place where final Matrix edits/messages are emitted
- the Responses stream parser only records lifecycle deltas; it does not own
  terminal timestamps
- terminal timestamps are written only at the real success/failure/flush sites

Why first:

- biggest reduction in ambiguity
- unlocks later queue/runtime simplification

### Phase 2: Prompt Canonicalization

Target files:

- `bridges/ai/prompt_builder.go`
- `bridges/ai/prompt_context_local.go`
- `bridges/ai/canonical_prompt_messages.go`
- `bridges/ai/streaming_continuation.go`
- `bridges/ai/turn_store.go`

Deliverable:

- one canonical prompt representation
- one-way serialization to provider formats
- no current-turn option shims or single-use history loaders around the prompt
  builder
- no production helper layer around canonical turn-data persistence
- one-way projection from persisted/runtime state
- no separate local-context/projection/continuation helper stacks

Why second:

- currently the most duplicated semantic layer after streaming

### Phase 3: Session Subsystem

Target files:

- `bridges/ai/session_store.go`
- `bridges/ai/sessions_tools.go`
- `bridges/ai/agent_activity.go`
- `bridges/ai/heartbeat_state.go`
- `bridges/ai/login_state_db.go`
- `bridges/ai/login_config_db.go`

Deliverable:

- one session-routing owner
- one session timestamp owner
- one session lookup path for heartbeat, status, and tools
- no re-derived store-agent identity at consumers
- high-level readers call session lookups by `agentID`; raw store IDs stay
  internal to the session subsystem
- last-routed-room lookup lives in the same subsystem as timestamp reads

Why third:

- this is the closest remaining mismatch with OpenClaw's bounded session shape

### Phase 4: Provider Consolidation

Target files:

- `bridges/ai/provider.go`
- `bridges/ai/provider_openai.go`
- `bridges/ai/provider_openai_responses.go`
- `bridges/ai/token_resolver.go`
- `bridges/ai/media_understanding_runner.go`
- `bridges/ai/media_understanding_providers.go`
- `bridges/ai/image_generation_tool.go`
- `bridges/ai/client.go`

Deliverable:

- one provider capability/config table
- one provider runtime construction path
- one auth/base URL resolution path
- media/image/tool policy reads from the same provider table

Why fourth:

- provider behavior is still scattered across chat/media/image subsystems

### Phase 4: Session Subsystem

Target files:

- `bridges/ai/session_store.go`
- `bridges/ai/session_keys.go`
- `bridges/ai/heartbeat_session.go`
- `bridges/ai/sessions_tools.go`
- `bridges/ai/login_state_db.go`
- `bridges/ai/login_config_db.go`

Deliverable:

- one canonical session subsystem
- one keying/routing model
- one persistence surface
- heartbeat and tool-session lookup reuse that exact surface

Why fourth:

- fixes a large amount of behavior duplication without changing user-visible
  semantics

### Phase 5: Queue/Runtime/Heartbeat Collapse

Target files:

- `bridges/ai/pending_queue.go`
- `bridges/ai/pending_event.go`
- `bridges/ai/queue_runtime.go`
- `bridges/ai/queue_resolution.go`
- `bridges/ai/streaming_state.go`
- `bridges/ai/heartbeat_execute.go`
- `bridges/ai/heartbeat_delivery.go`
- `bridges/ai/heartbeat_state.go`

Deliverable:

- one run pipeline
- one queue/execution boundary
- one heartbeat/runtime boundary
- heartbeat reduced to one caller of the same runtime pipeline

### Phase 6: SDK Thinning

Target files:

- `sdk/runtime.go`
- `sdk/client.go`
- `sdk/client_base.go`
- `sdk/client_cache.go`
- `sdk/load_user_login.go`
- `sdk/connector.go`
- `sdk/connector_builder.go`
- `sdk/stream_turn_host.go`
- `sdk/base_stream_state.go`

Deliverable:

- one runtime adapter shape
- one client-loading path
- one stream host/state boundary

### Phase 7: Turn Lifecycle Consolidation

Target files:

- `sdk/turn.go`
- `sdk/final_edit.go`
- `sdk/turn_data.go`
- `sdk/turn_data_builder.go`
- `sdk/turn_snapshot.go`
- `sdk/stream_replay.go`

Deliverable:

- one canonical turn lifecycle
- replay/final edit derived from the same state

### Phase 8: Deletion Sweep

Deliverable:

- remove leftover wrappers
- remove dead files
- remove stale doc claims

## Success Criteria

The rewrite is done when:

- `sdk` can be described as a thin agent runtime on top of `bridgev2`
- `bridges/ai` can be described as AI product policy and wiring
- there is one obvious path for runtime execution
- there is one obvious path for prompt handling
- there is one obvious path for provider selection/capability/auth
- there is one obvious path for session routing/storage
- there are no historical helper layers left “just because they already exist”
