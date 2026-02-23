# Engineering Refactor Plan: Hard-Cut Generic Plugin/Extension Host

## 1) Scope
Refactor the bridge so the connector is a generic plugin/extension host and feature behavior lives in optional modules.

Primary target:
- `cron` and `memory` become true modules with no connector-owned feature tree.

Secondary target:
- Prepare next optional modules (`linkpreview`, `media`, `heartbeat-session`) so features can be enabled only when reviewed.

## 1.1 Locked Hard-Cut Decisions
1. No migration layer.
2. No backward-compat aliases for old cron/memory config or metadata shapes.
3. No phased rollout logic in this refactor.
4. If a module is absent, commands/tools/hooks disappear completely.
5. Connector must have no cron/memory feature ownership after this cut.
6. Project is unreleased; no legacy compatibility obligations.
7. Do not lose existing feature behavior for compiled/enabled modules (parity required).

This plan is code-driven from current state in:
- `pkg/connector`
- `pkg/integrations/runtime`
- `pkg/integrations/modules`
- `pkg/integrations/cron`
- `pkg/integrations/memory`

## 2) Current State Snapshot (from code)

### 2.1 High-coupling hotspots
- `pkg/connector/integration_host.go` is 1939 lines and still owns most cron/memory behavior.
- `pkg/connector/client.go` is 3107 lines and wires lifecycle + heartbeat + integrations.
- `pkg/connector/integrations.go` is 867 lines and has partial generic registries but still module-specific branches.
- `pkg/connector/integrations_config.go` is 785 lines and still has typed cron/memory config.

### 2.2 Critical coupling that blocks “true module host”
- Direct imports in connector non-test code:
  - `pkg/connector/integration_host.go` imports `pkg/integrations/cron` and `pkg/integrations/memory`.
- Module-name switching in host:
  - `runtimeIntegrationHost.Module*` methods branch on `"cron"` / `"memory"` in `pkg/connector/integration_host.go`.
- Module wrappers still call host dispatch:
  - `pkg/integrations/cron/integration.go` and `pkg/integrations/memory/integration.go` currently depend on `runtime.Module*Host` dispatch interfaces.
- Connector schema still module-specific:
  - `Config.Cron`, `Config.Memory`, `Config.MemorySearch` in `pkg/connector/integrations_config.go`.
  - `PruningConfig.OverflowFlush` uses YAML key `memory_flush` in `pkg/connector/context_pruning.go`.
  - `PortalMetadata` fields `IsCronRoom`, `CronJobID`, `MemoryFlushAt`, `MemoryFlushCompactionCount`, `MemoryBootstrapAt` in `pkg/connector/metadata.go`.
- Connector still has module-specific tool fallback map:
  - `integrationToolModules` and `integrationModuleEnabled` in `pkg/connector/integrations.go`.

### 2.3 Existing pieces we should keep
- Generic registries are already in place:
  - tool/prompt/command/event/overflow/purge/approval registries in `pkg/connector/integrations.go`.
- Compile-time module list exists:
  - `pkg/integrations/modules/builtins.go` with `BuiltinFactories`.
- Module startup/stop orchestration already generic:
  - `startLifecycleIntegrations` / `stopLifecycleIntegrations` / `stopLoginLifecycleIntegrations`.

## 3) Refactor End-State Requirements

## 3.1 Hard invariants
1. No non-test imports of `pkg/integrations/cron` or `pkg/integrations/memory` from `pkg/connector`.
2. No connector branching by module name (`cron`, `memory`) in runtime host or registries.
3. Module inclusion is controlled only by `pkg/integrations/modules/builtins.go`.
4. Removing a module requires:
   - remove one import line in `builtins.go`
   - remove one factory line in `builtins.go`
   - delete module package directory
   - no connector edits
5. Connector config and metadata are module-agnostic.

## 3.2 Behavior constraints
- If a module is absent, its commands/tools/prompts/events/overflow/purge/lifecycle disappear.
- No connector fallback stubs for missing modules.
- Tool approval behavior remains deterministic and policy-safe.
- For modules that remain present, preserve behavior and user-visible outputs except strictly unavoidable wording differences.
- `!ai cron` and `!ai memory` must not be connector-owned commands; they must be exported by modules.
- `!ai help` must be generated from currently registered command definitions (core + loaded modules), not static hardcoded module command lists.

## 4) Work Breakdown (Detailed)

## WP0: Baseline & Safety Net
Objective:
- Freeze behavior before heavy movement.

Tasks:
1. Add baseline grep gates script (temporary local script or CI step):
   - `rg 'pkg/integrations/(cron|memory)' pkg/connector --glob '!**/*test.go'`
   - `rg 'case \"cron\"|case \"memory\"' pkg/connector/integration_host.go`
2. Run and record baseline:
   - `go test ./...`
3. Snapshot existing command/tool outputs for:
   - `!ai cron status/list/runs/run/remove`
   - `!ai memory status/reindex/search/get/set/append`

Done when:
- Baseline tests and snapshots are captured for parity comparison.

---

## WP1: Runtime Contract Cleanup
Objective:
- Remove host-dispatch anti-pattern and make modules consume generic capabilities directly.

Files:
- `pkg/integrations/runtime/module_hooks.go`
- `pkg/integrations/runtime/interfaces.go`

Tasks:
1. Remove these dispatch interfaces from runtime package:
   - `ModuleToolHost`
   - `ModulePromptHost`
   - `ModuleCommandHost`
   - `ModuleEventHost`
   - `ModuleOverflowHost`
   - `ModuleLoginHost`
   - `ModuleLifecycleHost`
2. Keep seams:
   - `ToolIntegration`, `PromptIntegration`, `CommandIntegration`, `EventIntegration`, `OverflowIntegration`, `LoginPurgeIntegration`, lifecycle seams.
3. Expand `Host` only with generic capability interfaces modules can use directly.
4. Define capability interfaces with no module names in signatures.

Done when:
- No runtime interface mentions `"module string"` dispatch pattern.

---

## WP2: Connector Host Becomes Pure Capability Adapter
Objective:
- Strip connector host of cron/memory feature logic and keep only generic adapters.

Files:
- `pkg/connector/integration_host.go` (split recommended)
- new:
  - `pkg/connector/integration_host.go` (thin host wiring)
  - `pkg/connector/integration_host_capabilities.go` (generic adapters)
  - `pkg/connector/integration_events.go` (optional extraction)

Tasks:
1. Delete all `Module*` methods in `runtimeIntegrationHost` that switch by module name.
2. Remove connector-owned cron/memory execution methods from host:
   - Tool: `ToolDefinitions`, `ExecuteTool`, `ToolAvailability` (module-specific parts)
   - Command: `executeCronCommand`, memory command wrappers
   - Overflow/purge/login methods tied to memory implementation
   - cron service wrappers (`Status/List/Add/Update/Remove/Run/Wake/Runs`)
3. Keep host methods only for generic capabilities:
   - logger/clock
   - state store
   - portal/session/dispatch helpers
   - db/config access
   - generic tool execution entrypoints

Done when:
- `pkg/connector/integration_host.go` has no references to cron/memory package types.

---

## WP3: Rebuild Cron Module as Self-Owned Integration
Objective:
- Move cron ownership fully into `pkg/integrations/cron`.

Files:
- `pkg/integrations/cron/integration.go`
- `pkg/integrations/cron/runtime.go`
- `pkg/integrations/cron/tool_exec.go`
- `pkg/integrations/cron/command_format.go`
- `pkg/integrations/cron/rooms.go`
- `pkg/integrations/cron/sessions.go`
- `pkg/integrations/cron/isolated.go`

Tasks:
1. Rewrite `cron.Integration` so it implements seams directly (no host module-dispatch assertions).
2. Give cron module internal state:
   - service instance
   - lifecycle ownership (`Start/Stop`)
3. Build service inside module from generic host capabilities.
4. Move command execution (`!ai cron ...`) into module package.
5. Move tool execution wiring entirely into module.
6. Move cron event logging and run-log reading logic from connector into module.
7. Keep connector unaware of cron room creation details; module uses generic portal/state/dispatch capabilities.

Connector cleanup targets to remove:
- `buildCronService`
- `resolveCronJobTimeoutMs`
- `onCronEvent`
- `updateCronSessionEntry`
- `readCronRuns`
- `resolveCronDeliveryTarget`
- `getOrCreateCronRoom`
- `runCronIsolatedAgentJob`
- `buildCronDispatchMetadata`
- `mergeCronContext`

Done when:
- `cron` behavior parity is preserved without connector cron imports.

---

## WP4: Rebuild Memory Module as Self-Owned Integration
Objective:
- Move memory ownership fully into `pkg/integrations/memory`.

Files:
- `pkg/integrations/memory/integration.go`
- `pkg/integrations/memory/module_exec.go`
- `pkg/integrations/memory/prompt_exec.go`
- `pkg/integrations/memory/overflow_exec.go`
- `pkg/integrations/memory/manager.go`
- `pkg/integrations/memory/manager_close.go`
- `pkg/integrations/memory/login_purge.go`
- `pkg/integrations/memory/runtime.go` (replace with runtime.Host-based access)

Tasks:
1. Rewrite `memory.Integration` to use generic host capabilities directly.
2. Move manager lookup/runtime adapter behavior from connector into module.
3. Move prompt injection bootstrapping and section reading logic into module.
4. Move overflow flush orchestration and tool loop invocation ownership into module.
5. Move login purge + manager cache purge ownership into module.
6. Make memory module own search config merge/defaults fully.

Connector cleanup targets to remove:
- `resolveMemorySearchConfig`
- `mergeMemorySearchConfig`
- `convertMemorySearchDefaults`
- `resolve*EmbeddingConfig` helpers
- `memoryRuntimeAdapter`
- `getMemoryManager`
- `resolveMemoryFlushSettings`
- `runFlushToolLoop`
- `memoryFlushTools`
- memory prompt bootstrap helpers in host
- purge helpers wrappers for memory tables/vector rows

Done when:
- Memory commands/tools/prompt/overflow/purge run via module only.

---

## WP5: Config Genericization (Connector-Agnostic)
Objective:
- Remove typed cron/memory schema from connector config.

Files:
- `pkg/connector/integrations_config.go`
- `pkg/connector/context_pruning.go`
- `pkg/connector/integrations_example-config.yaml`
- `config.example.yaml`

Tasks:
1. Remove connector config fields:
   - `Config.Cron`
   - `Config.Memory`
   - `Config.MemorySearch`
2. Keep generic integration map:
   - `integrations.<module>.enabled`
   - `integrations.<module>.*`
3. Move memory overflow flush config key from core pruning:
   - from `pruning.memory_flush` to `integrations.memory.flush` (or equivalent module namespace).
4. Remove connector upgrade copy rules for old cron/memory trees (hard cut, no fallback).
5. Update example configs to only use clean names (no unreleased alias baggage).

Done when:
- Connector config struct has no cron/memory typed fields.

---

## WP6: Metadata Genericization
Objective:
- Remove cron/memory typed metadata fields from connector.

Files:
- `pkg/connector/metadata.go`
- `pkg/connector/handleai.go`
- `pkg/connector/sessions_tools.go`
- `pkg/connector/agent_activity.go`
- `pkg/connector/integrations.go`
- `pkg/connector/integration_host.go` (remaining references)

Tasks:
1. Replace typed fields in `PortalMetadata`:
   - `IsCronRoom`, `CronJobID`, `MemoryFlushAt`, `MemoryFlushCompactionCount`, `MemoryBootstrapAt`
2. Introduce generic metadata extension map:
   - e.g. `ModuleMeta map[string]map[string]any`
3. Add helper APIs for module metadata reads/writes.
4. Replace current checks:
   - `isInternalControlRoom` in `handleai.go`
   - session visibility filtering in `sessions_tools.go`
   - activity routing guard in `agent_activity.go`
   - `integrationPortalRoomType` / `integrationSessionKind` in `integrations.go`
5. Memory flush/bootstrap markers move to memory module-owned metadata extension fields.
6. Cron room markers move to cron module-owned metadata extension fields.

Done when:
- Connector has no cron/memory-specific metadata fields.

---

## WP7: Tool and Command Discovery Becomes Fully Generic
Objective:
- Remove hardcoded cron/memory tool mapping from connector.

Files:
- `pkg/connector/integrations.go`
- `pkg/connector/tool_registry.go`
- `pkg/connector/tools.go`
- `pkg/connector/tool_policy.go`

Tasks:
1. Remove `integrationToolModules` and special `integrationModuleEnabled` switch logic.
2. Replace availability logic with registry-driven source of truth:
   - if tool not registered, it is unavailable.
3. Keep `coreToolIntegration` only for core tools, without module-specific exclusions by name constants.
4. Remove tool-name special cases for cron/memory from connector.
5. Keep dynamic command registration from `CommandIntegration`.
6. Remove any static connector command ownership for module commands (`cron`, `memory`).
7. Ensure help text is registry-driven:
   - only commands from loaded modules appear
   - absent modules produce no command entries in help output.

Done when:
- Connector does not encode cron/memory tool names in behavior branches.

---

## WP8: Lifecycle + Events + Overflow + Purge Ownership
Objective:
- Ensure module hooks are the only path for lifecycle/event/overflow/purge behavior.

Files:
- `pkg/connector/client.go`
- `pkg/connector/integrations.go`
- `pkg/connector/response_retry.go`
- `pkg/connector/logout_cleanup.go`
- event callsites:
  - `pkg/connector/handlematrix.go`
  - `pkg/connector/internal_dispatch.go`
  - `pkg/connector/streaming_persistence.go`
  - `pkg/connector/chat.go`
  - `pkg/connector/tools.go`
  - `pkg/connector/tools_apply_patch.go`

Tasks:
1. Keep lifecycle start/stop as registry iteration only.
2. Ensure session/file events are emitted once at each callsite.
3. Keep overflow pipeline generic and module-ordered.
4. Keep login purge generic over `LoginPurgeIntegration`.
5. Remove direct connector memory/cron cleanup logic paths.

Done when:
- Hook calls are generic and module implementations own behavior.

---

## WP9: Additional Optional Modules for Derisking
Objective:
- Isolate high-risk features to support phased release with many features OFF.

Priority order:
1. `linkpreview` module
2. `media` module
3. `heartbeat-session` module
4. `streaming-engine` extraction
5. `tooling-governance` extraction

Initial file targets:
- Link preview:
  - `pkg/connector/linkpreview.go`
  - callsites in `pkg/connector/client.go` and `pkg/connector/response_finalization.go`
- Media:
  - `pkg/connector/tools_analyze_image.go`
  - `pkg/connector/image_generation*.go`
  - `pkg/connector/audio_generation.go`
  - `pkg/connector/media_understanding_*`
- Heartbeat/session:
  - `pkg/connector/heartbeat_*`
  - `pkg/connector/agent_activity.go`
- Streaming engine:
  - `pkg/connector/streaming_*`

Done when:
- Each extracted feature can be disabled by module exclusion/toggle with no connector edits.

---

## WP10: Module Optionality and Removal Guarantees
Objective:
- Enforce clean optionality with compile-time registry and runtime enable flags only.

Tasks:
1. Keep one compile-time module list in `pkg/integrations/modules/builtins.go`.
2. Ensure runtime activation only uses `integrations.<module>.enabled`.
3. Enforce disappearance behavior:
   - no module -> no command/tool/prompt/event hook presence.
4. Prove two-line removal + package deletion for `cron` and `memory`.

Done when:
- Module presence is fully registry-driven and removal requires no connector edits.

---

## WP11: Tests & CI Gates
Objective:
- Prevent regressions and re-coupling.

Core tests to add/update:
1. Registry ordering and duplicate handling.
2. Command registration parity (`cron`, `memory`) through module definitions.
3. Event hook dispatch exactly once per callsite.
4. Overflow hook behavior parity for memory flush path.
5. Login purge hook execution once.
6. Module absence tests:
   - memory absent
   - cron absent
   - both absent
7. Two-line removal proof tests/scripts.

Mandatory CI gates:
```bash
go test ./...
rg 'pkg/integrations/(cron|memory)' /Users/batuhan/.codex/worktrees/3a2b/ai-bridge/pkg/connector --glob '!**/*test.go'
rg 'case \"cron\"|case \"memory\"' /Users/batuhan/.codex/worktrees/3a2b/ai-bridge/pkg/connector/integration_host.go
rg '^integrations_(cron|memory)_' /Users/batuhan/.codex/worktrees/3a2b/ai-bridge/pkg/connector
```

Expected:
- tests pass
- grep gates return no matches

---

## WP12: Execution Order and Commit Slices
Recommended commit slices:
1. `refactor(runtime): remove Module*Host dispatch and finalize generic host capabilities`
2. `refactor(connector): convert runtimeIntegrationHost to generic capability adapter`
3. `refactor(cron): move connector-owned cron logic into integrations/cron`
4. `refactor(memory): move connector-owned memory logic into integrations/memory`
5. `refactor(schema): remove typed cron/memory config and metadata from connector`
6. `refactor(tooling): remove hardcoded cron/memory tool availability branches`
7. `test(isolation): add module absence and grep gate tests`
8. `refactor(optional): extract linkpreview module`
9. `refactor(optional): extract media module`
10. `refactor(optional): extract heartbeat-session module`

## 5) Risk Register

### High risk
1. Metadata migration breakage (`IsCronRoom` / memory flush markers moved to generic map).
2. Tool availability regressions when hardcoded module map is removed.
3. Memory overflow flush behavior drift (prompt rewrite + retry ordering).
4. Cron isolated-room creation/delivery behavior drift.

Mitigations:
- behavior snapshots before move
- parity tests per command/tool flow
- staged refactor with strict grep gates

### Medium risk
1. Config migration and example YAML drift.
2. Strict hard-cut can break stale local configs.

Mitigations:
- add strict config parse tests for new schema
- fail fast with clear errors on stale keys

## 6) Definition of Done
All items below must be true:
1. Connector has no non-test import of `pkg/integrations/cron` or `pkg/integrations/memory`.
2. Connector has no branch by module name for cron/memory.
3. Cron/memory packages own their behavior via runtime seams.
4. Connector config/metadata are module-agnostic.
5. Module removal works via `builtins.go` two-line edit + module directory deletion.
6. `go test ./...` passes.
7. Isolation grep gates pass.
8. Connector is a generic plugin/extension mechanism only (no cron/memory special-case logic).
