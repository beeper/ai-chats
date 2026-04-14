# Duplication Audit

This document is a current-state audit of the remaining duplicated ownership,
wrapper layers, and branchy logic in `ai-bridge`.

It is intentionally scoped to the code that still matters:

- `sdk/`
- `bridges/ai`

It does not optimize for deleted bridge experiments or already-finished
retrieval cleanup. The goal is to finish the architecture we actually want:

- one thin runtime/metaframework layer in `sdk/`
- one AI product harness in `bridges/ai`
- no compatibility shells
- no historical helper stacks
- no more than one way to do any behavior

## Upstream Shape We Want

### Pi references

- `pi-mono/packages/ai/src/types.ts`
- `pi-mono/packages/ai/src/api-registry.ts`
- `pi-mono/packages/ai/src/stream.ts`
- `pi-mono/packages/ai/src/providers/openai-responses.ts`
- `pi-mono/packages/agent/src/agent.ts`
- `pi-mono/packages/agent/src/agent-loop.ts`

Why Pi matters:

- one canonical provider contract
- one canonical agent loop
- one explicit event stream
- application wiring at the edge, not in the middle

### OpenClaw references

- `openclaw/src/channels/session.ts`
- `openclaw/src/media/host.ts`

Why OpenClaw matters:

- session logic is a bounded subsystem
- media logic is a bounded subsystem
- channel/product wiring does not become a hidden framework

## Canonical Shape

The final shape should be:

1. `bridgev2`
   - connector/login/portal contracts
   - Matrix room lifecycle
   - remote event transport boundaries

2. `sdk`
   - one runtime state model
   - one turn loop
   - one approval subsystem
   - one login helper surface
   - one event/send helper surface
   - one turn persistence/replay model

3. `bridges/ai`
   - provider/model policy
   - prompt/system prompt policy
   - AI room semantics
   - heartbeat product behavior
   - AI tool catalog/policy
   - AI session semantics

Everything else should be deleted or collapsed into those owners.

## Completed Simplifications

These wrapper/helper classes are already gone and should not return:

- SDK runtime/getter bag, cache removal shells, message construction wrappers,
  broken-login constructor shell, bridge-info helper leftovers, approval
  prompt formatting wrappers, and the embedded stream-state base layer
- AI queue dispatch shells, continuation/finalization wrappers, portal
  send/edit wrappers, heartbeat/session routing wrappers, current-turn prompt
  assembly wrappers, contact-resolution wrappers, retrieval token helper
  chains, prompt/state constant shims, and several one-use accessors
- Retrieval env/provider-registration/provider-constructor wrappers, direct
  fetch default wrappers, and the Exa wrapper layer
- Bridge-local status wrappers in `bridges/ai` and `bridges/codex`

What remains is now mostly subsystem-shape duplication rather than isolated
forwarders.

## Highest-Value Remaining Problems

### 1. Streaming terminalization still has multiple owners

Files:

- `bridges/ai/streaming_responses_api.go`
- `bridges/ai/streaming_success.go`
- `bridges/ai/streaming_error_handling.go`
- `bridges/ai/response_finalization.go`
- `bridges/ai/streaming_state.go`

Why this still violates the goal:

- `finishReason`, `responseStatus`, `responseID`, `completedAtMs`,
  persistence, final Matrix edit shaping, and `turn.End(...)` are still spread
  across several files.
- The Responses event parser no longer stamps `completedAtMs` directly, but
  terminal ownership is still split between lifecycle parsing, error
  normalization, response-final shaping, and the final success/error handlers.
- Terminal timestamps are now written directly at the real success/failure/flush
  sites; the remaining duplication is higher-level terminal shaping, not a
  separate timestamp helper.
- There is no single terminal state machine.

Desired owner:

- one `terminalizer` for all terminal transitions
- event handlers only record deltas and emit terminal signals
- no split between stream event handling, persistence shaping, and final
  Matrix output

### 2. Prompt handling still has too many representations

Files:

- `bridges/ai/prompt_builder.go`
- `bridges/ai/prompt_context_local.go`
- `bridges/ai/canonical_prompt_messages.go`
- `bridges/ai/streaming_continuation.go`
- `bridges/ai/turn_store.go`

Why this still violates the goal:

- The `buildCurrentTurnWithLinks` and `fetchHistoryRowsWithExtra` prompt
  wrappers are gone; remaining duplication is now in representation and
  projection ownership rather than trivial call-through helpers.
- Canonical turn-data persistence now calls `turnDataFromUserPromptMessages`
  directly; the remaining spread is the number of representations, not another
  persistence adapter.
- Steering-prompt continuation input is now serialized directly for the
  Responses loop instead of round-tripping through another prompt helper.
- prompt assembly, provider serialization, replay projection, and turn-data
  projection still overlap
- new prompt block behavior still requires changes in multiple places

Desired owner:

- one canonical prompt model
- provider serialization and replay derived from that model only
- no distinct local-context/projection/continuation helper stacks with
  overlapping semantics

### 3. Provider capability and auth resolution are still split

Files:

- `bridges/ai/provider.go`
- `bridges/ai/provider_openai.go`
- `bridges/ai/provider_openai_responses.go`
- `bridges/ai/token_resolver.go`
- `bridges/ai/media_understanding_runner.go`
- `bridges/ai/media_understanding_providers.go`
- `bridges/ai/image_generation_tool.go`
- `bridges/ai/client.go`

Why this still violates the goal:

- token lookup, base URL routing, capability flags, media/image support, and
  provider-specific behavior are still derived in multiple subsystems
- the current `AIProvider` abstraction does not buy enough to justify the extra
  layer

Desired owner:

- one provider capability/config table
- one concrete provider runtime shape
- data-driven differences instead of scattered branching
- media/image/tool code should consume the same provider table instead of
  re-deriving provider behavior

### 4. Session routing and session persistence are still fragmented

Files:

- `bridges/ai/sessions_tools.go`
- `bridges/ai/session_store.go`
- `bridges/ai/agent_activity.go`
- `bridges/ai/heartbeat_state.go`
- `bridges/ai/login_state_db.go`
- `bridges/ai/login_config_db.go`

Why this still violates the goal:

- status/session readers now enter through `session_store.go`; the remaining
  fragmentation is in write-side ownership, heartbeat selection, and route
  resolution
- last-routed-room lookup now also lives in `session_store.go`; remaining
  fragmentation is not consumer-side DB querying, but how different features
  choose and touch sessions
- canonical key rules, store routing, heartbeat selection, timestamp touching,
  still live in separate places
- there is not one obvious entrypoint for “resolve the session”

Desired owner:

- one session subsystem
- one canonical session key function
- one persistence surface
- one selection/routing surface
- heartbeat, tools, and room lookup should all enter through the same session
  resolution boundary

### 5. Queue/runtime/heartbeat state are still not one pipeline

Files:

- `bridges/ai/pending_queue.go`
- `bridges/ai/pending_event.go`
- `bridges/ai/queue_runtime.go`
- `bridges/ai/queue_resolution.go`
- `bridges/ai/streaming_state.go`
- `bridges/ai/heartbeat_execute.go`
- `bridges/ai/heartbeat_state.go`

Why this still violates the goal:

- queueing, execution, streaming, heartbeat delivery, and terminal state still
  form multiple partial runtimes instead of one run pipeline

Desired owner:

- one run state model
- one queue/execution boundary
- one terminalization boundary
- heartbeat should become one caller of the same run pipeline, not an adjacent
  runtime

### 6. `runtimeIntegrationHost` is still too large

Files:

- `bridges/ai/integration_host.go`

Why this still violates the goal:

- it bundles portal access, session routing, cron, memory DB access,
  workspace resolution, provider/runtime helpers, and integration-facing APIs
- it can become a second hidden framework under `bridges/ai`

Desired owner:

- either a much smaller boundary adapter
- or explicit subsystem services consumed by integrations directly
- integrations should not discover unrelated runtime/session/provider behavior
  through one god object

### 7. SDK runtime/loading still has too many layers

Files:

- `sdk/runtime.go`
- `sdk/client.go`
- `sdk/client_base.go`
- `sdk/client_cache.go`
- `sdk/load_user_login.go`
- `sdk/connector.go`
- `sdk/connector_builder.go`

Why this still violates the goal:

- the runtime surface is still split between `sdkClient`,
  stream host/state helpers, and client-cache/login helpers
- the SDK still reads like a local bridge framework rather than a thin runtime
  layer

Desired owner:

- one runtime adapter shape
- one client-loading path
- one stream host/state model

## Current Next Cuts

The highest-value remaining architectural cuts are:

1. Streaming terminalizer
2. Prompt canonicalization
3. Session subsystem
4. Provider consolidation
5. `runtimeIntegrationHost` reduction

Those are the places where duplication still changes how the system thinks,
not just how it is spelled.

### 8. SDK turn lifecycle is still distributed

Files:

- `sdk/turn.go`
- `sdk/final_edit.go`
- `sdk/turn_data.go`
- `sdk/turn_data_builder.go`
- `sdk/turn_snapshot.go`
- `sdk/stream_replay.go`

Why this still violates the goal:

- start state, persisted turn data, final edit shaping, snapshots, and replay
  are still split across several overlapping files

Desired owner:

- one turn lifecycle owner
- replay/final edit derived from the same canonical state

### 9. SDK login helpers still deserve one final hard trim

Files:

- `sdk/base_login_process.go`
- `sdk/login_helpers.go`
- `sdk/command_login.go`

Why this still matters:

- these are much cleaner now, but they still need to prove they are the
  thinnest useful layer on top of `bridgev2`
- anything that only restates step/process semantics should be deleted

## Lowest-Value Targets

These are not the next focus unless they fall out naturally:

- tiny getters or builder naming cleanup
- test-only helpers
- purely cosmetic file moves

The remaining architecture problem is not leaf wrappers. It is overlapping
owners for runtime, prompt, provider, session, and terminal state.

## Rewrite Order

1. streaming terminalization
2. prompt canonicalization
3. provider capability/auth consolidation
4. session subsystem consolidation
5. queue/runtime/heartbeat consolidation
6. SDK runtime thinning
7. SDK turn lifecycle consolidation
8. final dead-code deletion sweep

## Exit Condition

The rewrite is complete when:

- there is one runtime loop
- there is one terminalizer
- there is one prompt model
- there is one provider capability/config surface
- there is one session subsystem
- `sdk` is a thin runtime layer, not a second bridge framework
- `bridges/ai` reads like product policy and wiring only
