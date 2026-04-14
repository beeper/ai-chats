# AgentRemote Rewrite Plan

## Goal

Rewrite the remaining code from first principles around two layers only:

1. `sdk/` is AgentRemote SDK, a thin metaframework on top of `bridgev2` for agentic behavior.
2. `bridges/ai` is the production AI Chats bridge that consumes the SDK and owns only AI-specific policy and product behavior.

Out of scope for this plan:

- deleted bridge experiments
- compatibility shells
- legacy migration shims
- preserving old module boundaries just because they already exist

## Fixed Ownership

Every behavior must have exactly one owner.

### `bridgev2` owns

- login and connector contracts
- `Portal` lifecycle and Matrix room creation
- room metadata transport
- generic Matrix capability updates
- ghost/contact resolution boundaries

### `sdk/` owns

- agentic login lifecycle helpers
- turn lifecycle primitives
- approval routing and persistence
- tool-call protocol helpers
- shared room/bootstrap helpers that are actually generic
- common UI/system-message helpers
- path normalization and low-level bridge utilities

### `bridges/ai` owns

- provider/model selection
- AI prompt policy and system prompt construction
- AI-specific room semantics
- welcome and auto-greeting product behavior
- heartbeat semantics
- AI-specific integration state
- AI-visible room titles, notices, and responder formatting

## Rewrite Rules

- One behavior, one write path.
- One persisted shape, one source of truth.
- No sidecar metadata bags for fields that already have a typed owner.
- No bridge-local wrappers around raw `bridgev2` or SDK helpers unless they add real AI policy.
- If a helper only forwards arguments, delete it.
- If two flows differ only because one was added later, collapse them into one lifecycle owner.

## Current State

The repo has already shed a large amount of duplicate ownership. The important completed cuts in scope are:

- `pkg/retrieval` now owns the old search/fetch stack.
- SDK helper buckets have been split by behavior instead of one catch-all helper file.
- SDK approval routing has one shared decision path.
- SDK approval request-start choreography now has one shared owner for resolve/register/emit/send.
- SDK approval wait/respond/finalize handle flow now has one shared owner across SDK, AI, and Codex.
- AI approval wait now returns the shared SDK approval response directly; the bridge-local approval result type and state remapping are gone.
- SDK login validation and post-persist completion are shared.
- AI portal canonicalization now has one resolver path instead of client/non-client forks.
- AI session routing now has one main-key/global-store owner.
- AI turn reset/history ownership now lives in turn-store epoch mechanics instead of split metadata fields.
- AI portal-state SQL access now lives directly in the turn-store boundary instead of an extra DB wrapper layer.
- AI created-chat materialization now has one helper across normal chat creation, boss-store rooms, and subagent spawn.
- AI chat creation now has one constructor path for model and agent chats, and one prepare/save/materialize path for newly created portals.
- AI identifier resolution now has one response-building path for model and agent targets.
- AI contact listing and contact search now share one contact-collection path.
- AI parsed chat-target resolution now has one shared branch for ghost-derived model/agent targets.
- AI named internal-room creation now has one shared load/mutate/save/materialize path across scheduler and integration host flows.
- AI internal room bootstrap now has one create-or-materialize path for scheduler and integration host flows.
- AI agent/default-chat portal configuration now has one owner.
- AI welcome/bootstrap no longer splits between direct post-create sends and provisioning polling; one portal-based room-created bootstrap path now owns welcome delivery and auto-greeting kickoff.
- Responses API continuation no longer carries a synthetic fallback approval handle branch; pending approvals now require the real registered handle.
- AI approval continuation and builtin-tool gating now use the shared SDK approval response directly instead of rehydrating a second runtime decision wrapper.
- AI room projection now has one chat-portal owner for `Portal -> ChatInfo`; `GetChatInfo` and generated-title sync use the same projector instead of fallback/name-only chat shapes.
- AI portal target mutation now has one helper for writing `OtherUserID` and derived target identity across chat creation, room mutation, scheduler rooms, and cron execution.
- AI new-chat creation no longer routes through `createAndOpenResolvedChat` / `createAndOpenChat`; `handleNewChat` now owns the resolved-target create/materialize/announce flow directly.
- AI room materialization now uses the shared `bridgeutil.MaterializePortalRoom` owner instead of a bridge-local reimplementation.
- AI room bootstrap now has one shared owner for created-chat rooms, existing default-chat rooms, named internal rooms, boss-created rooms, and subagent rooms; the `prepareCreatedChatPortal` / `ensureChatPortalReady` split is gone.
- AI DM portal initialization now uses the shared `bridgeutil.ConfigureAndPersistDMPortal` helper instead of hand-writing the same room-field bootstrap logic in bridge code.
- SDK default approval-option wrapper is gone; callers use `ApprovalPromptOptions(true)` directly.
- SDK one-off path-expansion wrapper is gone; absolute-path normalization owns its only `~` expansion behavior directly.
- SDK `LoginHandle` façade is gone; the unused login-scoped conversation shell was deleted outright.
- SDK room-features helper wrappers are gone; the only remaining call site now uses `event.RoomFeatures` directly.
- SDK metadata-builder wrappers are gone; connectors and tests now use `database.MetaTypes` directly instead of `BuildStandardMetaTypes` / `BuildMetaTypes`.
- SDK login-completion convenience wrappers are gone; bridge login flows now call `PersistAndCompleteLoginWithOptions` directly and build the completion step there.
- `aichats_portal_state` now stores turn/reset ownership only; the leftover reset timestamp sidecar field is gone.
- AI internal-room setup no longer hides durable portal writes behind `MutatePortal` / `SaveBefore`; scheduler and integration host now mutate and save portals explicitly before materialization.
- Shared DM portal bootstrap/materialization moved down to `pkg/shared/bridgeutil` where it was truly generic.

## Remaining High-Value Targets

These are the remaining rewrite targets that still matter for SDK + AI.

### 1. SDK surface tightening

Problem:

- SDK still has a few helpers that are “shared because we copied them twice once”, not because they are true framework primitives

Target:

- keep only helpers that are genuinely reusable across agentic bridges
- leave AI-specific policy in `bridges/ai`
- avoid rebuilding a second framework inside `sdk/`

### 2. AI bridge-local branching

Problem:

- a few AI flows still branch by historical entrypoint instead of behavior
- the remaining concrete seams are the few room-update paths and SDK helpers that still exist because history created them, not because they are needed now

Target:

- branch only on product semantics
- never branch purely because the room was created through a different code path

## Execution Order

### Phase 1: Finish lifecycle convergence

1. keep checking AI room/bootstrap entrypoints for behavior-only branching
2. remove any remaining duplicated create-room post-processing branches
3. keep auto-greeting chained off the same owner as welcome delivery

Exit condition:

- every AI room gets its welcome/bootstrap behavior from one lifecycle path only

### Phase 2: Finish storage convergence

1. audit every field in login state, portal metadata, and `aichats_portal_state`
2. move misplaced metadata-shaped fields out of turn/reset storage
3. leave `aichats_portal_state` with turn/reset/sequence ownership only

Exit condition:

- each persisted field has one obvious owner and one write path

### Phase 3: Tighten SDK

1. delete helpers that are just pass-through wrappers
2. keep shared helpers only where AI and future agentic bridges would genuinely benefit
3. avoid pushing AI-specific concepts down into the SDK

Exit condition:

- SDK reads like a small metaframework, not a storage dump of old bridge code

### Phase 4: Delete leftovers

1. remove dead helper stacks
2. remove dead state fields
3. remove stale comments and planning notes that refer to deleted bridges

Exit condition:

- the remaining architecture matches the ownership rules above

## Immediate Attack List

1. trim SDK helpers that are no longer meaningfully shared after the deleted bridge experiments are gone
2. keep deleting any remaining AI entrypoint-specific branches where the behavior is actually the same
3. keep collapsing the remaining room-update and helper seams where the behavior is already settled
