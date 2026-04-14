# AgentRemote Rewrite Plan

## Goal

Rewrite the codebase from first principles with these fixed layers:

1. `bridgev2` is the base lifecycle framework.
2. `sdk/` is AgentRemote SDK, a metaframework for agentic behavior on top of `bridgev2`.
3. `bridges/ai` is one concrete Beeper-facing agent harness built on AgentRemote SDK.
4. `bridges/openclaw`, `bridges/opencode`, and `bridges/codex` are source-specific `bridgev2` bridges that consume AgentRemote SDK for agentic behavior.
5. `bridges/dummybridge` is the minimal reference implementation for the final shape.

Non-goals:

- no backward compatibility
- no legacy code paths
- no compatibility wrappers kept after cutover
- no duplicate frameworks layered on top of each other

## Ownership Rules

Every behavior must have exactly one owner.

### `bridgev2` owns

- connector and login contracts
- `Portal` lifecycle and Matrix room ownership
- `NetworkAPI` runtime boundaries
- bridge-facing media and backfill contracts

### AgentRemote SDK owns

- agentic login helpers on top of `bridgev2`
- room/bootstrap/materialization helpers for agentic bridges
- turn lifecycle
- streaming state
- tool-call execution protocol
- approval broker and persistence
- agentic event transport helpers
- bridge-aware media helpers
- typed state storage for agentic flows

### `bridges/ai` owns

- provider and model selection
- prompt policy and system prompts
- concrete tool catalog and policy
- AI-specific room/session behavior
- heartbeat semantics
- image analysis and generation behavior
- AI identity, presence, and model-facing formatting

### Source-specific bridges own

- source login and provisioning behavior
- source session and transport lifecycle
- source event translation
- source backfill policy
- source portal/session binding

They do not own generic streaming, generic approvals, generic tool call lifecycle, or generic room/bootstrap behavior.

## Target AgentRemote SDK Modules

The final `sdk/` surface should be organized by behavior, not by historical file growth.

- `sdk/bridge`
- `sdk/login`
- `sdk/portal`
- `sdk/turn`
- `sdk/tools`
- `sdk/approval`
- `sdk/events`
- `sdk/media`
- `sdk/storage`
- `sdk/types`

The current `sdk/helpers.go` bucket must be deleted by the end of the rewrite.

## Mandatory Cross-Cutting Rewrites

These happen regardless of bridge cutover order.

1. Merge `pkg/search` and `pkg/fetch` into `pkg/retrieval`.
2. Collapse repeated state scoping and JSON persistence helpers into one storage layer.
3. Keep `pkg/shared/media` low-level and pure.
4. Keep `pkg/shared/*` and `pkg/runtime/*` as pure libraries, not hidden bridge frameworks.

## Execution Phases

### Phase 0: Freeze the target

- write the ownership map
- define the final `sdk/` module surface
- decide which files are temporary migration targets and which files must disappear

Exit condition:

- every major behavior has exactly one intended owner

### Phase 1: Foundation rewrites

- build the new `sdk/` module skeleton
- merge `pkg/search` and `pkg/fetch` into `pkg/retrieval`
- define the new typed state/storage boundary
- define the new approval and tool-call protocol boundaries
- collapse duplicated DM portal bootstrap/materialization into one SDK path
- collapse shared assistant snapshot/message metadata assembly into SDK

Exit condition:

- the SDK has a clear compile-time surface for agentic behavior

Current status:

- complete: `pkg/retrieval` now owns the old `search` + `fetch` stack
- complete: large `sdk` helper buckets have been split by behavior
- complete: SDK approval flow has been split into core, pending store, routing, prompt store, and finalize layers
- complete: AI, Codex, and OpenClaw approval normalization now converge on shared SDK helpers
- complete: DM portal bootstrap now has a single SDK entrypoint
- complete: login lifecycle runtime now has a shared SDK display/wait loop
- in progress: Codex and OpenCode room/session bootstrap now converge on one bridge-local helper per bridge above the SDK bootstrap path
- in progress: canonical turn/message metadata assembly is moving into SDK, with OpenClaw live/history metadata now converging on shared SDK and bridge-local adapter helpers
- in progress: message metadata merge semantics now converge on shared SDK helpers instead of per-bridge merge ladders
- in progress: AI room materialization and terminal streaming finalization are being collapsed onto single local lifecycle/finalization entrypoints
- in progress: low-level blob-scope construction has moved into `pkg/aidb`, with Codex and OpenClaw storage helpers converging on shared scope plumbing
- in progress: AI chat creation/open flows and login-scoped identity plumbing now converge on shared local helpers instead of tuple-based DB identity wiring
- in progress: AI writer/lifecycle metadata now uses shared SDK UI metadata assembly with AI-specific extras layered on top
- complete: the standalone SDK portal lifecycle wrappers are gone; room create/update flows now call raw `bridgev2` portal operations directly
- complete: `sdk.BootstrapDMPortal` is gone; AI, Codex, OpenClaw, and OpenCode now own their bootstrap flow locally while still sharing low-level portal configuration helpers
- complete: thin SDK portal/status transport helpers are gone; bridges now share one low-level `pkg/shared/bridgeutil` path for DM room setup and Matrix status delivery
- in progress: AI portal-state and turn-store entrypoints now route through one scope-resolution path instead of split detached-vs-client persistence wrappers
- complete: Codex portal state no longer uses `codex_portal_state`; durable room state now lives in `PortalMetadata`, and room discovery now enumerates real `bridgev2` portals instead of a sidecar catalog
- complete: OpenClaw login credentials/session-sync markers no longer use `openclaw_login_state`; durable login state now lives in `UserLoginMetadata`
- complete: OpenClaw `PortalMetadata` is back to a minimal room marker; session identity, preview, history, and runtime state now live in the portal-state blob as the single durable owner
- complete: AI login config no longer uses `aichats_login_config`; durable login config now lives in `UserLoginMetadata`
- complete: AI Gravatar/profile supplement no longer uses `gravatar_json` in `aichats_login_state`; it now lives with the rest of durable login config in `UserLoginMetadata`
- complete: AI portal persistence no longer goes through a redundant `saveAIPortalState` wrapper; portal metadata writes now use the single `portal.Save(ctx)` path
- complete: `aichats_portal_state` no longer carries a dead `state_json` payload in fresh schema or writes; it is now only the epoch/turn-sequence ledger
- complete: unused AI portal metadata field `SessionBootstrappedAt` has been removed; `SessionBootstrapByAgent` is the only live bootstrap latch
- complete: AI internal-room classification and compaction snapshot ownership no longer route through the generic `ModuleMeta` bag; they now use typed `PortalMetadata` fields
- complete: AI heartbeat status no longer mirrors the last event in two in-memory stores; login runtime state is now the single persisted heartbeat source
- complete: OpenClaw room title/topic/type derivation now routes through one shared presentation path used by live room info, DM bootstrap, and session resync
- complete: OpenClaw no longer persists preview/catalog presentation caches in portal state; room topics now derive preview/tool/model summaries on demand from live session and catalog state
- complete: OpenClaw no longer persists history presentation/config fields in portal state or metadata; the one remaining visible history label is now a single presentation constant
- complete: OpenClaw no longer wraps `sdk.BuildUIMessageMetadata` behind a bridge-local helper just to inject session extras; callers now pass `Extras` directly to the shared SDK helper
- complete: OpenClaw no longer persists `OpenClawDMCreatedFromContact`; the synthetic-DM bootstrap path now derives that condition from `session_key` plus missing `session_id`
- complete: OpenClaw no longer wraps DM chat info creation behind `buildOpenClawDMChatInfo`; the DM call sites now use `bridgeutil.BuildLoginDMChatInfo(...)` directly
- complete: OpenCode portal setup/title branching no longer uses `AwaitingPath` plus `TitlePending` booleans; one `RoomState` now owns placeholder-vs-active-vs-title-pending behavior
- complete: OpenCode portal creation, managed setup handoff, title finalization, and reconnect toggles no longer mutate portal metadata through separate code paths; one portal-meta helper now owns `InstanceID` / `SessionID` / `ReadOnly` / `RoomState` / `Title` transitions
- complete: OpenCode callers no longer re-derive setup vs active vs title-pending behavior from raw `RoomState`; one explicit portal-phase layer now owns those read-side decisions
- complete: OpenCode per-message runtime ownership no longer splits across `seenMsg`, `partsByMessage`, and `turnState`; one `messageState` map now owns role, part membership, and turn lifecycle
- complete: OpenCode per-session cache and send-queue ownership no longer live in parallel top-level maps; one `sessionRuntime` owner now contains both cache and queue state
- complete: OpenCode no longer keeps separate top-level part-delivery maps beside the session runtime; remaining part/message runtime now hangs off the same session-scoped owner
- complete: OpenCode no longer mirrors message-to-part membership in both message and part runtime state; part ownership is now derived from the session-scoped part map
- complete: AI no longer persists the dead `CompactionLastUsageAt` timestamp, and internal-room integration classification no longer routes through an extra helper layer
- complete: AI no longer uses the fake-generic `integration_meta` bag; the memory integration now persists typed `memory_state` fields through the runtime boundary
- complete: AI memory lifecycle, overflow flush, and bootstrap checks no longer open-code repeated field mutations; typed `MemoryState` methods now own those transitions
- complete: AI login runtime state no longer open-codes heartbeat dedupe and provider health transitions in separate closure bodies; typed `loginRuntimeState` methods now own those mutations
- complete: AI managed heartbeat scheduling no longer open-codes config/due/run-result transitions across runtime helpers; `managedHeartbeatState` now owns those transition rules directly
- complete: AI heartbeat dedupe/scheduling ownership no longer straddles `aichats_sessions` and `aichats_managed_heartbeats`; managed heartbeat state now persists the last sent session/text/timestamp itself, and session rows are back to route/queue ownership only
- complete: AI session rows no longer carry dead `last_account_id` / `last_thread_id` baggage; route recovery and queue settings are the only remaining live session-store concerns
- complete: AI no longer mirrors Matrix route recovery into the main session row; real room sessions now own their own route state, and agent-level “last route” is derived from the latest real session instead of a shadow cache
- complete: AI session rows no longer carry dead route/queue override payloads; `aichats_sessions` is now just session identity plus timestamp, with route recovery derived from real room session keys and queue behavior owned only by config/inline inputs
- complete: AI session timestamp persistence no longer carries dead opaque `session_id` state or fake entry objects through heartbeat resolution; session lookup now resolves to a key plus timestamp only
- complete: AI session alias/global-scope routing no longer splits across tuple preambles, alias canonicalizers, and conflicting store-owner rules; one session-routing path now owns main-key construction, global-store selection, and heartbeat/session-key resolution
- complete: AI session reset/history visibility no longer splits between `PortalMetadata.SessionResetAt` and turn-store epochs; `aichats_portal_state` is now the single owner of history reset boundaries, and prompt/history replay reads only the current context epoch
- complete: AI turn sequencing/context-epoch persistence no longer routes through a dedicated portal-state store object; `turn_store.go` now owns the low-level `aichats_portal_state` SQL directly, and the extra `portal_state_db.go` layer is gone
- complete: AI portal canonicalization/scope resolution no longer forks into parallel client-vs-non-client helper stacks; one resolver path now owns portal hydration and scope derivation for AI-owned storage
- complete: AI turn-store persistence/replay no longer exposes duplicate package-level wrappers beside the `AIClient` methods; the remaining public entrypoints now route through one method surface over shared by-scope helpers
- complete: AI session-store persistence no longer carries a fake composite ref object with duplicated bridge/login identity; `store_agent_id` is now the only explicit session-store owner passed through heartbeat, route, and status paths
- complete: AI session-store persistence no longer exposes fake session row objects or duplicate scope wrappers; the store now owns one scalar `updated_at_ms` value behind direct `load/storeSessionUpdatedAt` helpers
- complete: `portalMeta(...)` no longer performs hidden portal canonicalization or DB work; metadata access is now a pure helper, with portal resolution kept at explicit storage/runtime boundaries
- complete: AI portal canonicalization no longer repeats across history replay, latest-assistant turn lookup, welcome/title generation, and system notices; each path now resolves the portal once and only asks for scope when it actually uses scope
- complete: AI chat entry no longer branches through separate ghost-vs-identifier resolution stacks; one chat-target resolver now owns model/agent normalization, alias redirects, and handoff into the existing response builders
- complete: AI streaming terminal success no longer carries a responses-only finish-reason prepass or a single-use terminal wrapper; final success fallback and terminal send ownership now live in one finalization path
- complete: AI heartbeat terminal delivery no longer fans out through repeated skip branches; one heartbeat decision helper now chooses the skip action and the remaining body is the single deliver path
- complete: AI new-chat command resolution no longer carries separate target representations or duplicated agent lookup branches; it now resolves straight into the shared chat target shape and one create/open path
- complete: AI streaming failure handling no longer duplicates cancel/timeout/context-length classification between chat-completions and responses paths; one terminal-error helper now owns that decision tree
- complete: AI scheduler/internal rooms no longer route durable portal updates through redundant save callbacks and post-save fixups; scheduler room materialization now uses one pre-save mutation path
- complete: AI room override/title/internal-room materialization paths no longer use `BeforeSave` just to persist portal mutations that `SaveBefore` already handles; the remaining callback cases are narrower and behavior-specific
- complete: AI subagent spawn and generated-title sync no longer route portal mutation through `MutatePortal`/`BeforeSave`; they now perform explicit metadata/save work before room materialization
- complete: AI `materializePortalRoom` no longer carries dead `BeforeSave` / `OnCreated` / `OnExisting` callback branches; the helper now only owns pre-save mutation, cleanup-on-create-error, and welcome behavior
- complete: AI created-chat room finalization no longer forks across normal new-chat flow, boss-store room creation, and subagent spawn; one helper now owns created-portal lookup plus room materialization
- complete: AI internal room bootstrap no longer duplicates portal lookup/materialization decisions between the integration host and scheduler; one `getOrMaterializePortalRoom` path now owns that create-or-update behavior
- complete: AI default-chat bootstrap and regular agent-chat creation no longer configure ghost/avatar/model-target state separately; one agent-portal helper now owns agent room metadata and member shaping
- complete: raw portal room materialization no longer forks across SDK conversation bootstrap, Codex welcome/session rooms, OpenClaw DMs, and OpenCode session rooms; one `bridgeutil.MaterializePortalRoom(...)` path now owns create-vs-update plus bridge-info/capability refresh
- complete: DM portal configure-plus-persist no longer forks across Codex, OpenClaw, OpenCode, and the dummy bridge; one `bridgeutil.ConfigureAndPersistDMPortal(...)` path now owns the shared pre-save bootstrap step above bridge-specific state persistence
- complete: Codex and OpenClaw session/DM bootstrap no longer perform redundant second `portal.Save(ctx)` writes after state persistence; the state-save owner is now the only durable portal write in that pre-room phase
- complete: the dummy bridge reference implementation no longer teaches bespoke DM room bootstrap logic; it now follows the same shared bridgeutil portal bootstrap/materialization path as the real bridges
- complete: Codex thread start and thread resume no longer duplicate post-RPC loaded-thread bookkeeping; one helper now owns recovered-turn restoration and room-info refresh
- complete: Codex login flow metadata no longer splits auth-mode/step-id/wait-deadline/display behavior across `Start`, `SubmitUserInput`, `spawnAndStartLogin`, and `buildStillWaitingStep`; one flow-spec table now owns that state-machine mapping
- complete: OpenCode permission request/reply handling no longer re-derive approval identifiers, owner MXID, and stream-event bootstrap in separate handlers; shared helpers now own approval request normalization and approval stream emission
- complete: Codex no longer wraps message-status sends or sandbox/path normalization behind trivial bridge-local helpers; the call sites now use `bridgeutil.SendMessageStatus(...)`, `sdk.NormalizeAbsolutePath(...)`, and the sandbox constant directly
- complete: Codex no longer routes room topic refresh through `syncCodexRoomTopic`; the three call sites now recompute `ChatInfo` and call `UpdateInfo(...)` directly
- pending: split AI storage into three real owners only: `LoginStorage`, `PortalRepository`, and `PortalTurnStore`
- pending: collapse `aichats_portal_state` so it owns only sequencing/reset infrastructure and no longer hydrates metadata-shaped state
- in progress: move durable portal/login state out of JSON sidecar tables and into bridge metadata wherever the data is connector metadata rather than runtime-only state
- pending: replace callback-driven portal mutation (`MutatePortal`, `BeforeSave`, `OnCreated`) with `ChatInfo.ExtraUpdates` / `UserInfo.ExtraUpdates` where the mutation is durable bridge state
- pending: replace AI poll-based welcome/autogreeting flow with one event-driven bootstrap turn flow
- complete: SDK login persistence/completion no longer forks across bridge-local “new login -> load client -> reconnect” tails; the shared helper now also covers bridge-specific post-persist setup and custom load params, so AI, OpenCode, and OpenClaw all use the same lifecycle owner
- complete: connector-level login creation no longer open-codes the same enabled/flow-id gating in each bridge; Codex, OpenClaw, and OpenCode now share one SDK login-flow validator
- complete: SDK approval reaction routing no longer reassembles user decision payloads in parallel match paths; one shared helper now owns reaction-option decision construction

### Phase 2: Vertical slice

- rewrite `bridges/dummybridge` to consume the new SDK surface

Exit condition:

- one bridge proves login, room bootstrap, turn lifecycle, approvals, and event transport on the new SDK

### Phase 3: Source bridge cutover

- rewrite `bridges/openclaw`
- rewrite `bridges/opencode`
- rewrite `bridges/codex`

These can be executed in parallel once the SDK surface is stable.

Exit condition:

- all source-specific bridges use AgentRemote SDK instead of local agentic frameworks

### Phase 4: AI harness cutover

- rewrite `bridges/ai` to consume the new SDK surface
- collapse bridge-local state, queue, approval, and streaming duplication

Exit condition:

- `bridges/ai` is reduced to AI policy plus bridge wiring

### Phase 5: Deletion

- delete dead wrappers
- delete duplicate helper stacks
- delete deprecated file families

Exit condition:

- no old path remains reachable

## Immediate Order Of Attack

1. redesign AI storage around `LoginStorage`, `PortalRepository`, and `PortalTurnStore`
2. finish deleting metadata-shaped state from `aichats_portal_state`, leaving only turn sequencing/reset mechanics
3. trim `aichats_login_state` down to true runtime/cache fields, with heartbeat-status persistence as the next likely extraction
4. continue moving OpenClaw portal identity/config out of the portal blob and into `PortalMetadata`
5. collapse reset/history ownership so one turn-store boundary controls reset semantics
6. replace callback-driven portal mutation with `ExtraUpdates`
7. replace AI welcome/autogreeting polling with event-driven bootstrap turns
8. keep AI integration-owned state typed and minimal; do not reintroduce generic per-portal metadata bags
9. keep trimming OpenClaw portal blob fields down to true runtime/session ownership and avoid reintroducing mirrored metadata copies
10. collapse any remaining OpenCode runtime duplication around part/message caches after the `messageState` and `sessionRuntime` cuts
11. delete any remaining dead per-bridge helper stacks and sidecar tables
