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

Recent cleanup kept pushing in that direction:

- SDK provider identity normalization now uses the single normalization
  primitive directly instead of another config wrapper
- SDK client session access no longer routes through `getSession()` /
  `setSession()`: the handful of real callers now read/write `sessionMu`
  directly, and `Turn.Writer()` no longer bounces through a one-callsite
  `turnPortal(...)` accessor
- Queue rejection and run launch no longer bounce through local wrappers:
  `sendQueueRejectedStatus(...)` and `dispatchCompletionInternal(...)` are
  gone, so queue-stop / queue-overflow rejection and queued / heartbeat run
  launch now happen directly at the real callsites
- Pending prompt assembly now has one queue/runtime owner:
  `buildPromptContextForPendingMessage(...)` rebuilds text/media/regenerate
  prompts from `pendingMessage`, and the duplicate queue-only
  `pendingQueueItem.rawEventContent` copy is gone
- Queue admission now has one flatter accepted tail inside
  `dispatchOrQueueCore(...)`: direct-run, steer-only, and queue branches no
  longer each carry their own save/notify return path
- Heartbeat no longer owns a global inflight gate:
  `hasInflightRequests()` is gone, and heartbeat now checks and locks only the
  specific session/delivery rooms it would touch
- Room occupancy no longer has a second registry:
  `roomLocks` is gone, and `activeRoomRuns` now owns both room admission and
  active-run state
- Queue interrupt admission no longer bounces through a generic policy helper:
  `dispatchOrQueueCore(...)` now owns its interrupt-mode branch directly
- Dead SDK media helper overlap is gone:
  `sdk/media_helpers.go` was unused and duplicated bridge-owned media download
  behavior, so it has been deleted
- `runtimeIntegrationHost` lost three more non-canonical layers:
  module enablement and module-config lookup now stay in `AIClient`, the dead
  host-only `ExecuteBuiltinTool(...)` wrapper is gone, and assistant-turn
  waiting now reuses `aiTurnRecord` instead of a second checkpoint adapter
  type
- Dead SDK replay/apply helpers are gone:
  `sdk/stream_replay.go`, `sdk/part_apply.go`, `sdk/stream_part_state.go`, and
  the unused `sdk/canonical_assistant_metadata.go` path were all test-only and
  have been deleted so turn lifecycle work can focus on the live owner paths
- AI turn canonicalization no longer round-trips through a second projection:
  `buildCanonicalTurnData(...)` now uses one `BuildTurnDataFromUIMessage(...)`
  pass with the full assistant metadata/file/artifact inputs, and the extra
  merge helper path is gone
- TextFS post-write side effects now have one owner:
  tool writes, edit/apply-patch writes, and integration-host writes all funnel
  through `notifyTextFSFileChanges(...)` instead of each re-spelling the
  notify-plus-identity-refresh pair
- Integration-host completions no longer bypass the bridge model mapper:
  `runtimeIntegrationHost.NewCompletion(...)` now reuses
  `AIClient.modelIDForAPI(...)` instead of sending a second raw model string
  path to the provider
- Web search and fetch config no longer each own their own merge pipeline:
  connector config, login-derived Exa tokens, env overlays, and defaults now
  flow through one `applyRetrievalConfigRuntimeDefaults(...)` path instead of
  being duplicated in both `effectiveSearchConfig(...)` and
  `effectiveFetchConfig(...)`
- SDK/bridge assistant metadata no longer round-trip through transient
  snapshots:
  `sdk.BuildAssistantMetadataBundle(...)` now consumes canonical `TurnData`
  directly, `sdk.BuildTurnSnapshot(...)` / `sdk.SnapshotFromTurnData(...)` are
  gone, and the AI / Codex / SDK final-metadata paths no longer build extra UI
  message projections just to flatten them back into message metadata
- Current-user prompt persistence no longer reparses prompt messages back into
  canonical turn data:
  `buildPromptContextForTurn(...)` now carries user `sdk.TurnData` directly in
  `PromptContext`, the reverse `turnDataFromUserPromptMessages(...)` adapter is
  gone, and persistence writers reuse the canonical turn record instead of
  rebuilding it from the final user prompt message
- Login-scoped TextFS storage no longer has branched constructor paths:
  `AIClient.textFSStoreForAgent(...)` now owns the storage tuple, the host-only
  `textStoreForAgent(...)` helper is gone, and tools / bootstrap / heartbeat /
  agent-display reads all delegate to the same store-construction rule
- Agent module config no longer round-trips the entire agent through JSON:
  `runtimeIntegrationHost.AgentModuleConfig(...)` now delegates to a typed
  module selector, memory module lookup is aligned with `memory_search`, and
  agent hydration normalizes memory config back into a typed shape instead of
  rediscovering it through generic maps later
- SDK turn-part schema no longer has duplicated field maps in both directions:
  `sdk.TurnDataFromUIMessage(...)` and `sdk.UIMessageFromTurnData(...)` now
  share dedicated `decodeTurnPart(...)` / `encodeTurnPart(...)` helpers and one
  reserved-key list, so new part fields no longer require two separate schema
  edits
- Final-edit payload assembly no longer has split packaging conventions:
  SDK now owns final payload construction end to end, AI no longer stages a
  mixed top-level map only to unpack it again, and the wrapper helpers around
  default extra packing / finish-reason stamping are gone
- Memory runtime policy config no longer bounces through helper wrappers:
  prompt-context injection and citation-mode wiring now read
  `host.ModuleConfig("memory")` directly at the real callsites instead of
  staging another local parser / accessor layer first
- Matrix session lookup no longer has two separate resolver branches:
  `sessions_history` and `sessions_send` now both use
  `resolveMatrixSessionTarget(...)` for `"main"`, room-id, and portal-id
  resolution instead of each open-coding the same Matrix-session search rules
- Chat ghost-target lookup no longer repeats agent-vs-model branching:
  identifier and ghost resolution now both reuse
  `resolveParsedChatGhostTarget(...)` so parsed ghost IDs go through one model /
  agent resolver and one `model not found` error-shaping path
- SDK visible text no longer reimplements turn-text projection:
  `Turn.VisibleText()` now falls back to canonical `TurnText(td)` instead of
  rebuilding text-part concatenation in a second place
- SDK approval flow no longer carries a private send/login/sender/status
  wrapper layer:
  approval prompt send, resolved-status emission, and reaction redaction now
  use direct `SendViaPortal(...)`, sender resolution, and
  `bridgeutil.SendMessageStatus(...)` logic at the real callsites instead of
  bouncing through `loginOrNil(...)`, `senderOrEmpty(...)`, `send(...)`, and
  `sendMessageStatus(...)`
- Continuation steering prompts no longer have a second Responses serialization
  path:
  continuation input now reuses `promptContextToResponsesInput(...)` for
  steering prompts instead of manually rebuilding user text items inline
- Scheduled internal-room creation no longer routes through a one-use portal
  key helper:
  `scheduler_rooms.go` now constructs the `networkid.PortalKey` directly and
  the dead `portalKeyFromParts(...)` wrapper is gone
- The integration runtime host no longer exposes a fake clock abstraction for
  one caller:
  `integrationruntime.Host.Now()` and `runtimeIntegrationHost.Now()` are gone,
  and cron now uses `time.Now()` directly instead of forcing a host-surface
  method that had no real ownership value
- SDK turn/final-edit surface is smaller now:
  dead exported accessors `Turn.Agent()`, `Turn.Emitter()`, and
  `Turn.Session()` are gone, and the one-callsite
  `BuildTextOnlyFinalEditPayload(...)` adapter has been deleted in favor of
  direct fallback shaping at the real final-edit callsite
- Session routing no longer round-trips through a temporary routing bag:
  `resolveSessionRouting(...)` and the `sessionRouting` struct are gone, and
  heartbeat/session activity/status paths now read the canonical session
  primitives directly via `sessionStoreAgentID(...)`, `sessionMainKey(...)`,
  `sessionScope(...)`, and `normalizedSessionAgentID(...)`
- Chat-completions prompt serialization no longer carries a dead capability
  flag:
  the unused `supportsVideoURL` parameter has been deleted from
  `promptContextToChatCompletionMessages(...)`, and its callers now use the
  one canonical serializer signature
- User prompt projection no longer splits prompt-message and turn-data owners:
  regenerate, rewrite, prompt-builder, and transcript-edit paths now all use
  one `buildUserPromptTurn(...)` projection so the bridge-local user
  `PromptMessage` and canonical `CurrentTurnData` are derived from the same
  filtered block list instead of being assembled separately

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
- Natural final Matrix delivery now happens directly inside
  `finalizeStreamingTurn(...)`; the extra `sendFinalAssistantTurn(...)` wrapper
  is gone.
- The Responses event parser no longer stamps `completedAtMs` directly, but
  terminal ownership is still split between lifecycle parsing, error
  normalization, response-final shaping, and the final success/error handlers.
- Terminal timestamps are now written directly at the real success/failure/flush
  sites; the remaining duplication is higher-level terminal shaping, not a
  separate timestamp helper.
- Responses and chat-completions step errors now enter the same terminal-error
  finalization helper; remaining streaming duplication is above that boundary.
- Heartbeat early-return handling no longer bounces through
  `heartbeatSkipParams`/`skipHeartbeatRun(...)`; those branches now terminate
  directly inside `sendFinalHeartbeatTurn(...)`.
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
- Prompt replay now reconstructs directly from canonical turn data inside
  `replayHistoryMessages(...)`; the metadata-to-prompt adapter and
  `canonical_history.go` helper layer are gone.
- Steering-prompt continuation input is now serialized directly for the
  Responses loop instead of round-tripping through another prompt helper.
- Base-context history loading now enters `replayHistoryMessages` directly; the
  remaining prompt duplication is no longer about separate history-loader
  scaffolding.
- Local prompt projection no longer bounces through single-use wrappers for
  block filtering, image extra lookup, tool-argument normalization, or
  internal prompt turn upsert packaging.
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

- simple constructor shells continue to disappear; remaining provider
  duplication is in capability/auth/media behavior, not the old base-URL
  convenience path
- image generation now resolves provider service endpoints through the shared
  service-config path; remaining provider duplication is the broader auth/media
  policy branching, not these endpoint-specific rebuilds
- media understanding now also reads OpenAI/OpenRouter endpoint+auth config from
  the shared service-config path instead of re-deriving those service values
- media prompt building and OpenRouter image-input preparation no longer route
  through single-callsite wrapper helpers; the remaining provider/media debt is
  policy branching, not those local adapter shells
- retrieval Exa proxy defaults no longer bounce through a second helper layer:
  `applyLoginTokensToRetrievalConfig(...)` now owns proxy-base/API-key mutation
  directly instead of routing through `applyExaProxyDefaultsTo(...)`
- media auto-selection no longer climbs a helper ladder for active-model,
  key-based fallback, and audio-provider fallback selection:
  `resolveAutoMediaEntries(...)` now owns that decision directly
- image generation no longer routes provider/service endpoint selection through
  separate OpenAI/Gemini/OpenRouter wrapper helpers: `generateImagesForRequest`
  now owns that provider-config branching directly
- search/fetch config loading no longer routes through the generic
  `effectiveToolConfig[T]` helper; `effectiveSearchConfig(...)` and
  `effectiveFetchConfig(...)` now own their direct load/login/default merge
  flow
- OpenRouter media generation no longer routes through
  `resolveOpenRouterMediaConfig(...)`; `generateWithOpenRouter(...)` now owns
  its auth/header/base-URL/pdf-engine shaping directly
- provider initialization, media understanding, and retrieval config no longer
  route through provider-specific OpenAI / OpenRouter base-URL shims
- media provider capability, auth-header shape, env-key lookup, and optional
  service binding now come from one provider-spec table instead of separate
  maps/switches
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

- status/session readers and heartbeat routing now enter through one route
  selection path; the remaining fragmentation is in write-side ownership and
  how different features touch session state
- heartbeat route selection now keeps main-key alias checks, agent-room lookup,
  fallback-room lookup, and delivery-target shaping inside
  `resolveHeartbeatRoute(...)`; the extra `sessionUsesMainKey(...)`,
  `resolveAgentPortal(...)`, `resolveFallbackPortal(...)`, and
  `deliveryTargetForPortal(...)` wrappers are gone
- canonical stored-session read/write operations now live in
  `session_store.go`, while `resolveHeartbeatRoute(...)` owns route selection
  end-to-end; the remaining debt is mostly which callers still speak in
  store-agent/session primitives instead of one higher-level session API
- session tool entrypoints no longer bounce through local
  `resolveSessionPortal(...)`, `resolveSessionPortalByLabel(...)`,
  `resolveSessionLabel(...)`, `resolveSessionDisplayName(...)`, and
  `lastMessageTimestamp(...)` helpers; that routing/display logic now lives
  directly where history/send behavior is decided
- last-routed-room lookup now also lives in `session_store.go`; remaining
  fragmentation is not consumer-side DB querying, but how different features
  choose and touch sessions
- canonical key rules, store routing, timestamp touching, and tool/status
  entrypoints still live in separate places
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

- heartbeat and queued runs now share the same low-level launch primitive
  (`withAgentLoopInactivityTimeout(...)` + `runAgentLoopWithRetry(...)`), and
  queued/immediate Matrix inputs now rebuild prompts from the same
  `pendingMessage` owner instead of carrying a second queue-only raw-event copy
- heartbeat no longer blocks on unrelated work in other rooms; it now uses the
  same room-scoped busy/lock primitives as queue/runtime admission
- room occupancy no longer bounces between `roomLocks` and `activeRoomRuns`;
  the run map is now the only room-busy state owner
- heartbeat route selection now walks one session resolver and one delivery
  resolver instead of repeating portal validation and `channel-not-ready`
  branches
- queued runs and heartbeats now share the same async launch wrapper around
  `withAgentLoopInactivityTimeout(...)` + `runAgentLoopWithRetry(...)`
- the remaining duplication is now mostly heartbeat-specific preflight/result
  policy around that shared runtime path

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

- it still bundles portal access, session routing, provider/runtime helpers,
  and integration-facing APIs
- cron now wires directly to the scheduler instead of proxying through the host,
  so the remaining problem is the broader god-object surface, not the scheduler
  forwarding chain
- memory no longer reads DB/login/workspace identity through the shared host;
  those are now explicit constructor deps
- it can become a second hidden framework under `bridges/ai`

Desired owner:

- either a much smaller boundary adapter
- or explicit subsystem services consumed by integrations directly
- integrations should not discover unrelated runtime/session/provider behavior
  through one god object

### 7. SDK runtime/loading still has too many layers

Files:

- `sdk/conversation.go`
- `sdk/client.go`
- `sdk/client_base.go`
- `sdk/client_cache.go`
- `sdk/load_user_login.go`
- `sdk/connector.go`
- `sdk/connector_builder.go`

Why this still violates the goal:

- the separate `conversationRuntimeState` layer is gone; the remaining SDK
  runtime debt is the broader client-loading split and the stream-host/state
  surface around it
- commands no longer downcast `login.Client` to recover SDK-private runtime
  state, and entrypoints now build `Conversation` directly instead of routing
  through a second runtime bag
- the SDK still reads like a local bridge framework rather than a thin runtime
  layer

Desired owner:

- no separate runtime bag between client, conversation, and turn
- one direct conversation/runtime owner shape
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

Why this still violates the goal:

- start state, persisted turn data, final edit shaping, and snapshots are still
  split across several overlapping files even after the dead replay/apply layer
  was removed

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
3. session subsystem consolidation
4. provider capability/auth consolidation
5. queue/runtime/heartbeat consolidation
6. `runtimeIntegrationHost` reduction
7. SDK turn lifecycle consolidation
8. SDK runtime/loading collapse
9. final dead-code deletion sweep

## Exit Condition

The rewrite is complete when:

- there is one runtime loop
- there is one terminalizer
- there is one prompt model
- there is one provider capability/config surface
- there is one session subsystem
- `sdk` is a thin runtime layer, not a second bridge framework
- `bridges/ai` reads like product policy and wiring only
