# Ralph Prompt: Hard-Cut Generic Plugin/Extension Refactor

Use this directly with Ralph:

```bash
/ralph-loop:ralph-loop "You are refactoring /Users/batuhan/.codex/worktrees/3a2b/ai-bridge into a hard-cut generic plugin/extension host.

Non-negotiable constraints:
1) Hard cut only. No migration layer. No backward compatibility aliases.
2) Connector must not own cron/memory feature behavior.
3) If module absent, commands/tools/prompts/events/hooks disappear fully (no placeholders).
4) Compile-time inclusion only via pkg/integrations/modules/builtins.go.
5) Do not stop until all checks pass.
6) Project is unreleased: no backward-compat obligations for old schemas/keys.
7) No feature loss for compiled/enabled modules: preserve current behavior parity.
8) `!ai cron` and `!ai memory` must be module-exported commands only (no connector-owned handlers).
9) `!ai help` must be generated from the active command registry (loaded modules only).

Primary objective:
Implement docs/refactor-engineering-plan.md end-to-end, with cron and memory fully isolated modules.

Detailed execution requirements:

A) Runtime contracts:
- Remove runtime Module*Host dispatch anti-pattern from pkg/integrations/runtime/module_hooks.go.
- Keep only generic seams and generic host capabilities.

B) Connector host cleanup:
- Refactor pkg/connector/integration_host.go into generic capability adapter only.
- Remove all module-name branching and all connector imports of:
  - github.com/beeper/ai-bridge/pkg/integrations/cron
  - github.com/beeper/ai-bridge/pkg/integrations/memory

C) Cron module ownership:
- Move all remaining cron service/command/tool/runtime/event logic out of connector and into pkg/integrations/cron.
- Keep behavior parity for status/list/add/update/remove/run/wake/runs.

D) Memory module ownership:
- Move all remaining memory manager/tool/prompt/overflow/purge logic out of connector and into pkg/integrations/memory.
- Preserve behavior parity for memory search/get/status/reindex/set/append and overflow flush.

E) Hard schema cut:
- Remove typed connector config ownership for cron/memory/memory_search and module-specific upgrade copies.
- Remove connector-owned memory_flush key ownership and move under module namespace.
- Remove typed connector portal metadata fields for cron/memory and use module-generic extension state.

F) Generic tool/command wiring:
- Remove hardcoded cron/memory tool module maps and special-casing in connector registries/policy.
- Registry-driven behavior only.
- Remove static connector ownership of module commands; module commands must come from CommandIntegration exports.
- Ensure help output is dynamic from registered commands (no static module command docs).

G) Verification and cleanup:
- Remove obsolete connector-local cron/memory logic/files.
- Add/adjust tests for deterministic ordering, module absence, lifecycle/event/overflow/purge behavior.

Required acceptance checks (must all pass):
1) go test ./...
2) rg 'pkg/integrations/(cron|memory)' /Users/batuhan/.codex/worktrees/3a2b/ai-bridge/pkg/connector --glob '!**/*test.go'
   -> must return zero matches
3) rg 'case \"cron\"|case \"memory\"' /Users/batuhan/.codex/worktrees/3a2b/ai-bridge/pkg/connector/integration_host.go
   -> must return zero matches
4) rg '^integrations_(cron|memory)_' /Users/batuhan/.codex/worktrees/3a2b/ai-bridge/pkg/connector
   -> must return zero matches

Two-line removal proof (must perform for both modules):
- For cron:
  1) remove cron import + factory line in pkg/integrations/modules/builtins.go
  2) delete pkg/integrations/cron
  3) go test ./...
  4) restore afterward if needed for subsequent proof
- For memory:
  1) remove memory import + factory line in pkg/integrations/modules/builtins.go
  2) delete pkg/integrations/memory
  3) go test ./...
  4) restore afterward only if required by requested final state

Failure handling:
- If stuck after 10 iterations on the same issue:
  1) print blocker summary
  2) list attempted fixes
  3) choose simpler implementation that still satisfies hard constraints
  4) continue
- Never stop at analysis-only state.

Output requirements when complete:
1) List changed files
2) Summarize how each acceptance check passed
3) Confirm two-line removal proof outcomes
4) Output exact token: <promise>HARD_CUT_COMPLETE</promise>
" --max-iterations 120 --completion-promise "HARD_CUT_COMPLETE"
```

## Notes
- Canonical engineering source remains: `/Users/batuhan/.codex/worktrees/3a2b/ai-bridge/docs/refactor-engineering-plan.md`
- This file is the Ralph-executable wrapper.
