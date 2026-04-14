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
- SDK login validation and post-persist completion are shared.
- AI portal canonicalization now has one resolver path instead of client/non-client forks.
- AI session routing now has one main-key/global-store owner.
- AI turn reset/history ownership now lives in turn-store epoch mechanics instead of split metadata fields.
- AI portal-state SQL access now lives directly in the turn-store boundary instead of an extra DB wrapper layer.
- AI created-chat materialization now has one helper across normal chat creation, boss-store rooms, and subagent spawn.
- AI chat creation now has one constructor path for model and agent chats, and one prepare/save/materialize path for newly created portals.
- AI internal room bootstrap now has one create-or-materialize path for scheduler and integration host flows.
- AI agent/default-chat portal configuration now has one owner.
- AI welcome/bootstrap no longer splits between direct post-create sends and provisioning polling; one portal-based room-created bootstrap path now owns welcome delivery and auto-greeting kickoff.
- `aichats_portal_state` now stores turn/reset ownership only; the leftover reset timestamp sidecar field is gone.
- AI internal-room setup no longer hides durable portal writes behind `MutatePortal` / `SaveBefore`; scheduler and integration host now mutate and save portals explicitly before materialization.
- Shared DM portal bootstrap/materialization moved down to `pkg/shared/bridgeutil` where it was truly generic.

## Remaining High-Value Targets

These are the remaining rewrite targets that still matter for SDK + AI.

### 1. SDK approval transaction ownership

Problem:

- approval lifecycle orchestration is mostly converged, but AI still carries some approval-specific translation and policy adapters above the shared SDK path

Target:

- keep SDK as the only owner of approval transaction flow
- leave AI responsible only for approval policy, presentation, and AI-specific side effects like always-allow persistence
- remove bridge-local approval lifecycle shells that only restate the same state machine

### 2. SDK surface tightening

Problem:

- SDK still has a few helpers that are “shared because we copied them twice once”, not because they are true framework primitives

Target:

- keep only helpers that are genuinely reusable across agentic bridges
- leave AI-specific policy in `bridges/ai`
- avoid rebuilding a second framework inside `sdk/`

### 3. AI bridge-local branching

Problem:

- a few AI flows still branch by historical entrypoint instead of behavior
- common examples: new chat vs default chat vs subagent room vs provisioning-created room

Target:

- branch only on product semantics
- never branch purely because the room was created through a different code path

## Execution Order

### Phase 1: Finish lifecycle convergence

1. collapse AI welcome/bootstrap onto one portal-based owner
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

1. converge approval orchestration onto one SDK-owned transaction path
2. delete helpers that are just pass-through wrappers
3. keep shared helpers only where AI and future agentic bridges would genuinely benefit
4. avoid pushing AI-specific concepts down into the SDK

Exit condition:

- SDK reads like a small metaframework, not a storage dump of old bridge code

### Phase 4: Delete leftovers

1. remove dead helper stacks
2. remove dead state fields
3. remove stale comments and planning notes that refer to deleted bridges

Exit condition:

- the remaining architecture matches the ownership rules above

## Immediate Attack List

1. keep trimming the remaining AI-specific approval adapter layer where it only translates the shared SDK result
2. trim SDK helpers that are no longer meaningfully shared after the deleted bridge experiments are gone
3. keep deleting any remaining AI entrypoint-specific branches where the behavior is actually the same
