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
7. SDK turn lifecycle consolidation
8. SDK runtime/loading collapse
9. Final dead-code deletion sweep

Recent progress also removed one more SDK runtime wrapper: provider identity
normalization now calls the shared primitive directly.

Recent progress also collapsed heartbeat session routing into one owner:
`resolveHeartbeatRoute(...)` now owns both session selection and delivery
selection, and heartbeat main-key alias handling now uses the same canonical
session rules as the session store.

Recent progress also moved canonical session read/write operations into
`session_store.go`, while `resolveHeartbeatRoute(...)` now owns heartbeat route
selection end-to-end: heartbeat no longer bounces through a second single-use
session selector helper before delivery selection.

Recent progress also collapsed immediate and queued prompt execution onto one
dispatch launcher: there is no queued-only run starter anymore, and both paths
now attach room-run/status/inbound/typing context through the same entrypoint.

Recent progress also removed the cron forwarding chain from
`runtimeIntegrationHost`: cron now wires directly to the scheduler, and the
old builtin-module registry layer is gone.

Recent progress also removed the SDK command-path runtime downcast: commands now
build a plain `Conversation` snapshot instead of reaching through `login.Client`
for SDK-private runtime state.

Recent progress also removed the `conversationRuntimeState` bag entirely:
runtime-owned agent/catalog/feature/approval/provider fields now live directly
on `Conversation`, `sdk/runtime.go` is gone, and SDK entrypoints construct the
conversation shape directly instead of rebuilding a separate runtime layer.

Recent progress also removed the one-message `promptTail(...)` wrapper from
prompt canonicalization: callers now slice the final prompt message directly at
the persistence boundary.

Recent progress also removed the metadata-to-prompt adapter and the extra
history replay helper layer: prompt replay now reconstructs directly from
canonical turn data inside `replayHistoryMessages(...)`, and
`bridges/ai/canonical_history.go` is gone.

Recent progress also removed the media-turn wrapper, the OpenRouter image-ref
wrapper, the media-service-config adapter, and the provider-specific OpenAI /
OpenRouter API-key helpers: media/image flows now call the canonical prompt and
service-config paths directly instead of passing through helper shells.

Recent progress also removed the provider-specific OpenAI / OpenRouter
base-URL helpers: provider initialization, media understanding, and retrieval
config now read base URLs straight from provider config or the shared
service-config map instead of routing through convenience shims.

Recent progress also flattened retrieval provider mutation further:
`applyLoginTokensToRetrievalConfig(...)` now owns Exa proxy-base/API-key
mutation directly instead of delegating to `applyExaProxyDefaultsTo(...)`.

Recent progress also flattened media auto-selection: active-model selection,
CLI fallback, and provider-key fallback now live directly in
`resolveAutoMediaEntries(...)` instead of bouncing through separate
`resolveActiveMediaEntry(...)`, `resolveKeyMediaEntry(...)`,
`resolveAutoAudioEntry(...)`, and `hasMediaProviderAuth(...)` helpers.

Recent progress also flattened image-generation provider selection:
`generateImagesForRequest(...)` now owns the OpenAI/Gemini/OpenRouter
service-config branching directly instead of routing through
`buildOpenAIImagesBaseURL(...)`, `buildGeminiBaseURL(...)`, and
`resolveOpenRouterImageGenEndpoint(...)`.

Recent progress also removed the generic `effectiveToolConfig[T]` wrapper:
`effectiveSearchConfig(...)` and `effectiveFetchConfig(...)` now read their
tool config, login-derived overrides, and env/default merge directly.

Recent progress also removed the memory runtime policy helper layer:
prompt-context injection and citation-mode selection now read the memory
module config directly at the real wiring points instead of routing through
local wrapper/parsing helpers first.

Recent progress also collapsed Matrix session lookup into one owner:
`resolveMatrixSessionTarget(...)` now owns `"main"` / room-id / portal-id
resolution for both `sessions_history` and `sessions_send`, so session tools
no longer carry two copies of the same Matrix-session branch.

Recent progress also collapsed parsed chat ghost target resolution:
identifier and ghost lookup now share `resolveParsedChatGhostTarget(...)`
instead of each re-spelling the same parsed model-vs-agent branching and
model-not-found shaping.

Recent progress also removed a second SDK visible-text projection:
`Turn.VisibleText()` now reuses canonical `TurnText(td)` rather than keeping a
separate fallback loop over text parts.

Recent progress also deleted the SDK approval-flow helper shell:
approval prompt send, resolved-status emission, and reaction redaction now
perform direct login/sender/send/status work at the real callsites instead of
flowing through `loginOrNil(...)`, `senderOrEmpty(...)`, `send(...)`, and
`sendMessageStatus(...)`.

Recent progress also removed a second continuation-input builder:
steering prompts now reuse `promptContextToResponsesInput(...)` for Responses
serialization instead of manually rebuilding the same user text items inside
continuation assembly.

Recent progress also deleted a one-use portal-key helper from the AI bridge:
scheduled internal-room creation now constructs its `networkid.PortalKey`
inline, and the dead `portalKeyFromParts(...)` adapter is gone.

Recent progress also trimmed the integration host surface itself:
`integrationruntime.Host.Now()` and the matching bridge-host implementation are
gone, and cron now uses `time.Now()` directly instead of keeping a fake
host-owned clock wrapper for one caller.

Recent progress also reduced SDK turn/final-edit API surface:
dead exported turn accessors `Turn.Agent()`, `Turn.Emitter()`, and
`Turn.Session()` are gone, and the one-callsite
`BuildTextOnlyFinalEditPayload(...)` wrapper has been deleted in favor of
direct fallback payload shaping where final edits are actually built.

Recent progress also removed a transient session-routing representation:
the `sessionRouting` bag and `resolveSessionRouting(...)` helper are gone, and
heartbeat/session activity/status logic now reads the canonical session
primitives directly (`sessionStoreAgentID(...)`, `sessionMainKey(...)`,
`sessionScope(...)`, `normalizedSessionAgentID(...)`).

Recent progress also deleted a dead prompt-serialization parameter:
`promptContextToChatCompletionMessages(...)` no longer carries an unused
`supportsVideoURL` flag, so chat-completions and compaction paths now share
one direct serializer signature.

Recent progress also collapsed user prompt projection into one canonical path:
prompt builder, regenerate/rewrite context assembly, and transcript user-edit
repair now all derive the bridge-local user `PromptMessage` and canonical
`CurrentTurnData` from the same `buildUserPromptTurn(...)` projection instead
of assembling one shape by hand and the other separately.

Recent progress also removed stale regenerate-path API shape:
`buildContextForRegenerate(...)` no longer accepts an unused `latestUserID`
parameter, so queued regenerate dispatch only passes the prompt data that the
builder actually consumes.

Recent progress also deleted another batch of historical wrappers:
`sdk/client.go` no longer hides plain session state behind `getSession()` /
`setSession()`, `Turn.Writer()` no longer routes through `turnPortal(...)`,
and `bridges/ai` no longer launches queued/heartbeat runs or queue rejection
statuses through `dispatchCompletionInternal(...)` /
`sendQueueRejectedStatus(...)`.

Recent progress also made `pendingMessage` the canonical queued/immediate
prompt input: `buildPromptContextForPendingMessage(...)` now rebuilds
text/media/regenerate prompts from that one shape, and the duplicate
`pendingQueueItem.rawEventContent` field is gone.

Recent progress also flattened queue acceptance inside
`dispatchOrQueueCore(...)`: direct-run, steer-only, and queued acceptance now
share one post-accept tail for persistence/session mutation instead of three
separate return shapes.

Recent progress also removed heartbeat's global inflight admission branch:
`hasInflightRequests()` is gone, and heartbeat now checks and locks only the
specific session/delivery rooms it would touch before launch.

Recent progress also collapsed duplicate room-busy state: `roomLocks` is gone,
and `activeRoomRuns` now owns both room admission and active-run tracking.

Recent progress also deleted two more low-value layers:
`dispatchOrQueueCore(...)` now owns its interrupt-mode branch directly instead
of routing through `DecideQueueAction(...)`, and the dead overlapping
`sdk/media_helpers.go` file is gone.

Recent progress also removed the one-callsite
`resolveOpenRouterMediaConfig(...)` wrapper: `generateWithOpenRouter(...)` now
owns its auth/header/base-URL/pdf-engine shaping directly, and tests assert
those primitive owners instead of the deleted aggregate helper.

Recent progress also pulled natural final-send shaping directly into
`finalizeStreamingTurn(...)`: the extra `sendFinalAssistantTurn(...)` wrapper
is gone, and heartbeat skip/early-return branches now terminate directly
inside `sendFinalHeartbeatTurn(...)` instead of bouncing through
`heartbeatSkipParams` / `skipHeartbeatRun(...)`.

Recent progress also flattened heartbeat route selection further:
`resolveHeartbeatRoute(...)` now uses one session resolver plus one delivery
resolver, so agent-room validation and `channel-not-ready` handling no longer
repeat across explicit target, session-room, last-active, and default-chat
branches.

Recent progress also removed one more split execution entrypoint: heartbeat now
uses the same low-level run launch primitive as queued/immediate execution
(`withAgentLoopInactivityTimeout(...)` + `runAgentLoopWithRetry(...)`) even
though the surrounding queue/runtime/heartbeat pipeline is still not fully
unified.

Recent progress also collapsed the duplicated async launch wrapper itself:
queued runs and heartbeats now both enter the shared `launchAgentLoopRun(...)`
primitive, so only their exit policy remains separate.

Recent progress also trimmed `runtimeIntegrationHost` further: module
enablement and module-config lookup now stay with `AIClient`, the dead
host-only `ExecuteBuiltinTool(...)` wrapper is gone, and assistant-turn waits
now compare against canonical `aiTurnRecord` rows instead of a second
checkpoint adapter type.

Recent progress also deleted the dead SDK replay/apply side path:
`sdk/stream_replay.go`, `sdk/part_apply.go`, `sdk/stream_part_state.go`, and
unused `sdk/canonical_assistant_metadata.go` had no production callers, so the
remaining turn-lifecycle work is now concentrated on the live `Turn` /
snapshot / final-edit owners.

Recent progress also collapsed AI bridge turn canonicalization to one pass:
`buildCanonicalTurnData(...)` no longer bounces through
`UIMessageFromTurnData(...)` and a second merge step, and the extra
`turnDataFromStreamingState(...)` detour is gone.

Recent progress also removed the duplicated TextFS post-write branch:
tool writes, edit/apply-patch writes, and integration-host writes now all call
`notifyTextFSFileChanges(...)`, so notify-plus-identity-refresh behavior has
one owner.

Recent progress also removed one more provider-model fork from
`runtimeIntegrationHost`: completion requests now reuse
`AIClient.modelIDForAPI(...)` instead of keeping a second raw model-string
path.

Recent progress also collapsed duplicated retrieval-config assembly:
`effectiveSearchConfig(...)` and `effectiveFetchConfig(...)` now share one
runtime merge path for connector config, login-derived Exa credentials, env
overlays, and defaults instead of carrying two separate branches.

Recent progress also collapsed the SDK/bridge snapshot-to-metadata path:
assistant message metadata now derives directly from canonical `TurnData`,
`BuildTurnSnapshot(...)` / `SnapshotFromTurnData(...)` are gone, and the SDK /
AI / Codex metadata writers no longer build transient snapshot wrappers just to
flatten them back into metadata.

Recent progress also deleted the reverse user-prompt adapter:
`buildPromptContextForTurn(...)` now carries current-user `sdk.TurnData`
directly in `PromptContext`, and persistence writers no longer reconstruct
canonical turn data from the final user `PromptMessage`.

Recent progress also centralized login-scoped TextFS store construction:
`AIClient.textFSStoreForAgent(...)` now owns the storage tuple, and host /
tool / bootstrap / heartbeat / agent-display code no longer rebuilds separate
`textfs.NewStore(...)` paths.

Recent progress also removed the host-side agent-module JSON round-trip:
`runtimeIntegrationHost.AgentModuleConfig(...)` now uses a typed module
selector, and memory module config is normalized on agent hydration instead of
serializing the whole agent just to discover one field.

Recent progress also centralized SDK turn-part schema mapping:
`TurnDataFromUIMessage(...)` and `UIMessageFromTurnData(...)` now share
dedicated part encode/decode helpers and one reserved-key list instead of
maintaining the same `TurnPart` field schema twice by hand.

Recent progress also collapsed final-edit payload construction:
SDK now owns payload assembly end to end, AI no longer repacks top-level extra
into `m.new_content`, and the tiny wrappers for default final-edit extra
packing and finish-reason stamping are gone.

Recent progress also removed the remaining stringly memory runtime-config
branch: `inject_context` and `citations` now go through one local parser in
`pkg/integrations/memory` instead of being read from raw maps in multiple
helpers.

Recent progress also removed the local session-tool helper layer:
`executeSessionsList(...)`, `executeSessionsHistory(...)`, and
`executeSessionsSend(...)` now own their session lookup/display logic directly
instead of routing through `resolveSessionPortal(...)`,
`resolveSessionPortalByLabel(...)`, `resolveSessionLabel(...)`,
`resolveSessionDisplayName(...)`, and `lastMessageTimestamp(...)`.

Recent progress also removed the single-callsite internal prompt turn upsert
wrapper and the local prompt projection helpers around block filtering, image
payload lookup, and tool-argument normalization: canonical prompt projection
now stays inside `promptMessagesFromTurnData(...)` and
`persistAIInternalPromptTurn(...)`.

Recent progress also removed memory-specific DB/login/workspace identity from
the shared integration host surface: memory now takes explicit constructor deps
for that state instead of type-asserting the host.

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
- adapter step errors share one terminal-error finalization path
- heartbeat skip/early-return decisions live in `sendFinalHeartbeatTurn`, not a
  second selector helper

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
- no continuation-only steering serialization helper
- base context history replay calls the canonical history replayer directly
- no one-message prompt-tail wrapper around latest-user persistence
- no metadata-to-prompt adapter or extra history replay helper file
- no local block-filter / image-extra / tool-argument wrappers inside canonical
  prompt projection
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
- no trivial constructor shells between caller intent and provider creation
- one provider runtime construction path
- one auth/base URL resolution path
- media/image/tool policy reads from the same provider table
- image-generation endpoint resolution uses the shared service-config path
- media OpenAI/OpenRouter endpoint+auth resolution uses that same service-config
  path
- media provider capability/auth/env/service metadata uses one canonical spec
  table

Why fourth:

- provider behavior is still scattered across chat/media/image subsystems

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
- no separate queued-only prompt dispatch launcher

### Phase 6: SDK Thinning

Target files:

- `sdk/client.go`
- `sdk/conversation.go`
- `sdk/client_base.go`
- `sdk/client_cache.go`
- `sdk/load_user_login.go`
- `sdk/connector.go`
- `sdk/connector_builder.go`
- `sdk/stream_turn_host.go`
- `sdk/base_stream_state.go`

Deliverable:

- no separate runtime bag between the SDK client, conversation, and turn
- one direct conversation/runtime owner shape
- one client-loading path
- one stream host/state boundary

### Phase 7: Turn Lifecycle Consolidation

Target files:

- `sdk/turn.go`
- `sdk/final_edit.go`
- `sdk/turn_data.go`
- `sdk/turn_data_builder.go`
- `sdk/turn_snapshot.go`

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
