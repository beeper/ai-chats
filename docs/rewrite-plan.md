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
- in progress: OpenClaw portal identity/history configuration is being moved out of the portal-state blob and into `PortalMetadata`, leaving the blob for operational preview/backfill/runtime state only
- complete: AI login config no longer uses `aichats_login_config`; durable login config now lives in `UserLoginMetadata`
- complete: AI Gravatar/profile supplement no longer uses `gravatar_json` in `aichats_login_state`; it now lives with the rest of durable login config in `UserLoginMetadata`
- complete: AI portal persistence no longer goes through a redundant `saveAIPortalState` wrapper; portal metadata writes now use the single `portal.Save(ctx)` path
- complete: `aichats_portal_state` no longer carries a dead `state_json` payload in fresh schema or writes; it is now only the epoch/turn-sequence ledger
- complete: unused AI portal metadata field `SessionBootstrappedAt` has been removed; `SessionBootstrapByAgent` is the only live bootstrap latch
- complete: AI internal-room classification and compaction snapshot ownership no longer route through the generic `ModuleMeta` bag; they now use typed `PortalMetadata` fields, while module-owned bookkeeping lives in a dedicated `integration_meta` bag
- complete: AI heartbeat status no longer mirrors the last event in two in-memory stores; login runtime state is now the single persisted heartbeat source
- in progress: OpenClaw room title/topic/type derivation is being collapsed into one shared presentation path used by live room info, DM bootstrap, and session resync
- pending: split AI storage into three real owners only: `LoginStorage`, `PortalRepository`, and `PortalTurnStore`
- pending: collapse `aichats_portal_state` so it owns only sequencing/reset infrastructure and no longer hydrates metadata-shaped state
- in progress: move durable portal/login state out of JSON sidecar tables and into bridge metadata wherever the data is connector metadata rather than runtime-only state
- pending: replace callback-driven portal mutation (`MutatePortal`, `BeforeSave`, `OnCreated`) with `ChatInfo.ExtraUpdates` / `UserInfo.ExtraUpdates` where the mutation is durable bridge state
- pending: replace AI poll-based welcome/autogreeting flow with one event-driven bootstrap turn flow

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
8. trim AI `integration_meta` usage down to true module-owned state only and keep bridge room classification/config out of that bag
9. collapse OpenClaw room title/topic/type derivation into one canonical path and trim portal blob fields to runtime-only state
10. collapse OpenCode phase flags and overlapping per-session caches into one runtime owner
11. delete any remaining dead per-bridge helper stacks and sidecar tables
