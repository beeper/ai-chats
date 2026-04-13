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

Exit condition:

- the SDK has a clear compile-time surface for agentic behavior

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

1. `pkg/retrieval`
2. `sdk` module map and skeleton
3. `bridges/dummybridge` vertical slice
4. `bridges/openclaw` and `bridges/opencode`
5. `bridges/codex`
6. `bridges/ai`
7. dead code deletion and compaction
