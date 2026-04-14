# Duplication And Branching Audit

This document is a static structural review of duplicated code paths, branched implementations, and parallel mini-frameworks inside `ai-bridge`.

It is focused on cases where the codebase has more than one way to do the same job, or where simple branching has grown into hard-to-follow logic.

Tests were not run for this audit.

## Highest Leverage Findings

1. `pkg/search` and `pkg/fetch` are two copies of the same provider stack.
   Relevant files:
   - `pkg/fetch/config.go`
   - `pkg/search/config.go`
   - `pkg/fetch/env.go`
   - `pkg/search/env.go`
   - `pkg/fetch/router.go`
   - `pkg/search/router.go`
   - `pkg/fetch/provider_exa.go`
   - `pkg/search/provider_exa.go`
   Why this is duplicated:
   - Both packages define the same provider/fallback selection scaffold.
   - Both reapply the same Exa defaults and env merge logic.
   - Both wrap the same shared Exa transport layer with package-specific glue.
   - `search` reimplements provider routing instead of using the shared provider chain path that `fetch` already uses.
   Why this makes the code harder to follow:
   - Provider behavior changes need to be mirrored in two sibling packages.
   - Error behavior and defaulting can drift even when the intended policy is the same.
   Single-path direction:
   - One shared provider selection/env/routing helper for search/fetch-style capabilities.
   - One shared Exa provider scaffold that accepts endpoint-specific payload and response mapping callbacks.

2. `bridges/ai` streaming terminalization is split across multiple partially overlapping owners.
   Relevant files:
   - `bridges/ai/streaming_responses_api.go`
   - `bridges/ai/streaming_response_lifecycle.go`
   - `bridges/ai/streaming_success.go`
   - `bridges/ai/streaming_error_handling.go`
   - `bridges/ai/streaming_responses_finalize.go`
   Why this is branched:
   - Response lifecycle events update terminal fields.
   - Success and error paths both emit metadata and finalize turns.
   - Finalization logic is not owned by one terminal state machine.
   Why this makes the code harder to follow:
   - `finishReason`, `responseID`, `responseStatus`, persistence, metadata emission, and `turn.End(...)` are touched in multiple paths.
   - It is difficult to know which function is authoritative for terminal state.
   Single-path direction:
   - One terminalizer that owns the final state transition.
   - Event handlers should only record deltas and terminal signals.

3. Provider capability and token resolution in `bridges/ai` drift across separate subsystems.
   Relevant files:
   - `bridges/ai/client.go`
   - `bridges/ai/token_resolver.go`
   - `bridges/ai/media_understanding_runner.go`
   - `bridges/ai/image_generation_tool.go`
   - `bridges/ai/handleai.go`
   Why this is branched:
   - Provider compatibility is inferred independently for chat, media, image generation, and token sourcing.
   - `ProviderMagicProxy` and similar providers are treated differently depending on the entry point.
   Why this makes the code harder to follow:
   - The compatibility matrix is implicit and spread out.
   - Adding a provider or changing semantics requires editing several unrelated files.
   Single-path direction:
   - One provider capability table that owns compatibility flags, token sources, default model behavior, and media/image support.

4. Prompt/context assembly in `bridges/ai` is implemented as overlapping serializers and projections.
   Relevant files:
   - `bridges/ai/prompt_context_local.go`
   - `bridges/ai/prompt_projection_local.go`
   - `bridges/ai/canonical_prompt_messages.go`
   - `bridges/ai/prompt_builder.go`
   - `bridges/ai/streaming_continuation.go`
   Why this is duplicated:
   - The same prompt concepts are converted to Responses input, Chat Completions input, turn-data projections, and history views in separate code paths.
   - Tool calls, tool results, images, reasoning blocks, and text are all encoded and decoded in multiple directions.
   Why this makes the code harder to follow:
   - Any new prompt block type has to be implemented in several places.
   - There is no single canonical serializer.
   Single-path direction:
   - Keep one canonical `PromptMessage`/`PromptBlock` model.
   - Generate provider-specific and persistence-specific representations from shared walkers.

5. Tool approvals in `bridges/ai` are split across separate policy, normalization, persistence, and stream handling paths.
   Relevant files:
   - `bridges/ai/tool_approvals.go`
   - `bridges/ai/tool_approvals_rules.go`
   - `bridges/ai/tool_approvals_policy.go`
   - `bridges/ai/streaming_output_handlers.go`
   Why this is branched:
   - Builtin and MCP approvals share lifecycle semantics, but approval IDs, TTL, allow rules, normalization, and persistence are derived in separate helpers.
   Why this makes the code harder to follow:
   - Approval behavior is distributed across rule logic, runtime checks, streaming event handling, and approval-flow registration.
   - It is not obvious which layer owns the final decision.
   Single-path direction:
   - One approval descriptor and one approval lifecycle path.
   - Builtin and MCP differences should be data, not separate frameworks.

## Bridge Layer Findings

6. Connector/bootstrap skeletons are repeated across all bridges.
   Relevant files:
   - `bridges/codex/constructors.go`
   - `bridges/opencode/connector.go`
   - `bridges/openclaw/connector.go`
   - `bridges/dummybridge/connector.go`
   Why this is duplicated:
   - Each bridge rebuilds the same standard connector configuration pattern, login flow wiring, and startup hooks.
   Single-path direction:
   - A shared connector builder with bridge-specific hooks.

7. Login flow state machines are duplicated across bridges and internally branched within each bridge.
   Relevant files:
   - `bridges/codex/login.go`
   - `bridges/opencode/login.go`
   - `bridges/openclaw/login.go`
   - `bridges/dummybridge/login.go`
   Why this is duplicated:
   - Each bridge implements its own “collect credentials -> maybe wait -> complete login” machine.
   - Codex, OpenCode, and OpenClaw each add their own internal sub-branches for the same conceptual job.
   Single-path direction:
   - A shared login-state helper that owns transition mechanics, with bridge code only supplying validation and completion hooks.

8. Room provisioning and portal lifecycle are reimplemented per bridge.
   Relevant files:
   - `bridges/dummybridge/bridge.go`
   - `bridges/codex/directory_manager.go`
   - `bridges/codex/backfill.go`
   - `bridges/opencode/opencode_portal.go`
   - `bridges/openclaw/provisioning.go`
   Why this is duplicated:
   - Each bridge repeats DM/chat creation, portal setup, and system notice behavior.
   Single-path direction:
   - One shared DM/chat provisioning helper with bridge-specific title/topic metadata.

9. Client-boundary room dispatch is duplicated across bridges.
   Relevant files:
   - `bridges/codex/client.go`
   - `bridges/opencode/client.go`
   - `bridges/openclaw/client.go`
   - `bridges/dummybridge/runtime.go`
   Why this is duplicated:
   - Each client checks whether a room belongs to that bridge and then forwards to its own handlers.
   Single-path direction:
   - A shared room router with bridge-specific predicates and delegates.

10. Backfill/import/pagination logic is the same shape in three places, and Codex adds an extra managed-directory split over it.
    Relevant files:
    - `bridges/codex/backfill.go`
    - `bridges/codex/directory_manager.go`
    - `bridges/opencode/backfill.go`
    - `bridges/openclaw/manager.go`
    Why this is duplicated:
    - Load remote history, sort it, paginate it, convert it to Matrix backfill messages.
    - Codex additionally fans the same flow out across managed paths.
    Single-path direction:
    - One backfill adapter pattern with provider-specific fetch and conversion callbacks.

11. Streaming state, DB metadata, and final SDK metadata builders are effectively per-bridge copies.
    Relevant files:
    - `bridges/codex/client.go`
    - `bridges/codex/streaming_support.go`
    - `bridges/opencode/stream_metadata.go`
    - `bridges/opencode/stream_canonical.go`
    - `bridges/openclaw/stream.go`
    Why this is duplicated:
    - The same “remote stream -> Matrix turn state -> final metadata” pipeline has separate implementations in each bridge.
    Single-path direction:
    - One shared stream-state and metadata adapter layer, with bridge-specific field extraction only.

12. Approval adapters are duplicated across Codex, OpenCode, and OpenClaw.
    Relevant files:
    - `bridges/codex/client.go`
    - `bridges/opencode/opencode_manager.go`
    - `bridges/openclaw/manager.go`
    Why this is duplicated:
    - Register approval, build prompt, deliver to Matrix, wait, resolve remote decision.
    Single-path direction:
    - One provider-agnostic approval adapter, with bridge-specific presentation and resolution hooks.

13. Attachment/media loading is duplicated in OpenCode and OpenClaw.
    Relevant files:
    - `bridges/opencode/opencode_media.go`
    - `bridges/openclaw/media.go`
    Why this is duplicated:
    - Decode source, infer filename/MIME, upload to Matrix, build message content.
    Single-path direction:
    - One shared attachment/media loader, with bridge-specific source parsers.

14. Identifier and portal-key construction are repeated with bridge-specific string formats.
    Relevant files:
    - `bridges/codex/portal_keys.go`
    - `bridges/codex/identifiers.go`
    - `bridges/opencode/opencode_identifiers.go`
    - `bridges/openclaw/identifiers.go`
    Why this is duplicated:
    - The escape/hash/parse mechanics are similar, but implemented separately in each bridge.
    Single-path direction:
    - Shared key-builder/parser utilities with bridge-specific prefixes and layouts.

## Core Infrastructure Findings

15. Message sending and formatting in `bridges/ai` are duplicated across normal text, tool messages, finalization, and media.
    Relevant files:
    - `bridges/ai/message_send.go`
    - `bridges/ai/media_send.go`
    - `bridges/ai/tools.go`
    - `bridges/ai/chat.go`
    - `bridges/ai/response_finalization.go`
    Why this is duplicated:
    - Markdown rendering, reply/thread setup, upload/send wiring, and payload shaping repeat with slight variations.
    Single-path direction:
    - One message payload builder and one send helper that take explicit send options.

16. Heartbeat execution is a large branched decision tree with separate dedupe and session-resolution helpers around it.
    Relevant files:
    - `bridges/ai/response_finalization.go`
    - `bridges/ai/heartbeat_session.go`
    - `bridges/ai/heartbeat_state.go`
    Why this is branched:
    - Delivery target, dedupe, alert gating, reasoning send, main-content send, and session recording are mixed together.
    Single-path direction:
    - Produce one heartbeat outcome object, then execute that outcome in one place.

17. Session/login storage and key canonicalization are fragmented.
    Relevant files:
    - `bridges/ai/session_store.go`
    - `bridges/ai/session_keys.go`
    - `bridges/ai/login_state_db.go`
    - `bridges/ai/login_config_db.go`
    - `bridges/ai/heartbeat_session.go`
    Why this is branched:
    - Key normalization, aliasing, and scope resolution live in several abstractions at once.
    Single-path direction:
    - One storage/scope abstraction with typed persistence methods.

18. `sdk` turn lifecycle is split across multiple partially overlapping paths for start, end, abort, final edit, and replay.
    Relevant files:
    - `sdk/turn.go`
    - `turns/session.go`
    - `sdk/final_edit.go`
    - `sdk/turn_data.go`
    - `sdk/stream_replay.go`
    Why this is duplicated:
    - Start/end/finalize/replay logic shares state concepts, but there is no single canonical state machine.
    Single-path direction:
    - One authoritative turn lifecycle owner, with final edit and replay consuming the same canonical state.

19. `sdk` cleanup and runtime adapter infrastructure are duplicated.
    Relevant files:
    - `sdk/base_stream_state.go`
    - `sdk/stream_turn_host.go`
    - `sdk/runtime.go`
    - `sdk/client.go`
    Why this is duplicated:
    - Two registries manage active stream cleanup differently.
    - The runtime interface is implemented twice with overlapping logic.
    Single-path direction:
    - Shared lifecycle registry and shared runtime adapter implementation.

20. Memory-path semantics are encoded in too many places.
    Relevant files:
    - `pkg/textfs/path.go`
    - `pkg/integrations/memory/approval.go`
    - `pkg/integrations/memory/prompt_exec.go`
    - `pkg/agents/workspace_bootstrap.go`
    - `pkg/agents/system_prompt_openclaw.go`
    - `pkg/integrations/memory/manager.go`
    Why this is duplicated:
    - The rules for “what counts as memory” and “which paths are managed” are rederived in several layers.
    Single-path direction:
    - One exported memory-path policy helper and canonical filename set.

21. Tool membership and tool policy are represented in multiple overlapping taxonomies.
    Relevant files:
    - `pkg/agents/toolpolicy/policy.go`
    - `pkg/agents/tools/core.go`
    - `pkg/agents/tools/builtin.go`
    - `pkg/agents/tools/registry.go`
    - `pkg/agents/beeper.go`
    - `pkg/agents/beeper_search.go`
    - `pkg/agents/beeper_help.go`
    - `pkg/agents/boss.go`
    Why this is duplicated:
    - `Tool.Group`, registry group state, preset tool lists, and policy config all describe overlapping inventories.
    Single-path direction:
    - One canonical tool membership source, with other views derived from it.

22. Memory execution is duplicated between the MCP tool path and the `!ai memory` command path, and the manager itself repeats scan/filter pipelines.
    Relevant files:
    - `pkg/integrations/memory/module_exec.go`
    - `pkg/integrations/memory/manager.go`
    Why this is duplicated:
    - Search/get/list all repeat manager lookup, truncation, normalization, and scan behavior.
    Single-path direction:
    - Shared memory execution helpers plus a generic scan/filter abstraction.

## Summary

The main structural problem is not isolated copy-paste at leaf functions. The codebase repeatedly grows new local mini-frameworks for:

- provider selection
- login state machines
- portal lifecycle
- backfill adapters
- stream terminalization
- approval adapters
- prompt serialization
- storage/key canonicalization
- tool taxonomy

If the goal is to have one way to do anything, those are the seams to collapse first.

## External Alignment Review

This section compares `ai-bridge` against:

- `~/Projects/texts/beeper-workspace/mautrix/go/bridgev2`
- `~/Projects/texts/beeper-workspace/mautrix/whatsapp/pkg/connector`
- `~/Projects/texts/beeper-workspace/mautrix/signal`

The goal is not to blindly port `ai-bridge` onto `bridgev2`. The goal is to delete local wrapper code whenever the ownership boundary already exists upstream, and to follow the conventions that keep mature bridges readable.

### `bridgev2` Alignment Opportunities

1. Portal lifecycle should be owned by one portal object, not split across helper files.
   External references:
   - `mautrix/go/bridgev2/portal.go`
   - `mautrix/go/bridgev2/portalinternal.go`
   - `mautrix/go/bridgev2/portalreid.go`
   Local cleanup targets:
   - `sdk/portal_lifecycle.go`
   - `sdk/login_handle.go`
   - portal setup and cleanup helpers in `sdk/helpers.go`
   Why this matters:
   - `bridgev2` keeps create, save, delete, MXID removal, and re-ID logic on the portal lifecycle itself.
   - `ai-bridge` still spreads room lifecycle policy across helper functions and bridge-local setup code.
   Delete or align direction:
   - Move toward one portal owner that exposes room creation, metadata refresh, archive/delete, and rebind operations.
   - Keep only AI-specific policy locally, such as `ConversationSpec`, agent-selection rules, and archive-on-completion semantics.

2. Room metadata refresh should use one path for name, topic, bridge info, and capabilities.
   External references:
   - `mautrix/go/bridgev2/portal.go` around `UpdateInfo`, `UpdateBridgeInfo`, `UpdateCapabilities`, and `sendRoomMeta`
   Local cleanup targets:
   - `sdk/matrix_actions.go`
   - room-info helpers in `sdk/helpers.go`
   Why this matters:
   - `bridgev2` treats room metadata as one coherent refresh flow instead of separate “set name”, “set topic”, and “broadcast capabilities” helpers.
   Delete or align direction:
   - Collapse `SetRoomName`, `SetRoomTopic`, `BroadcastCapabilities`, and related wrappers behind one room-refresh entry point.
   - Keep a small policy layer that decides what the desired room metadata should be for AI DMs, shared rooms, and archived rooms.

3. Login flow scaffolding should match the `bridgev2` step model instead of maintaining a parallel mini-framework.
   External references:
   - `mautrix/go/bridgev2/login.go`
   - `mautrix/go/bridgev2/networkinterface.go`
   - `mautrix/go/bridgev2/commands/login.go`
   Local cleanup targets:
   - `sdk/base_login_process.go`
   - `sdk/login_helpers.go`
   - login command ceremony in `sdk/command_login.go`
   Why this matters:
   - `bridgev2` already has typed step kinds, default input validation, QR/display steps, completion steps, and command orchestration.
   - `ai-bridge` recreates a lot of the same ceremony in local helper layers.
   Delete or align direction:
   - Standardize on one shared login step protocol, then let each bridge only define its actual steps and validation.
   - Prefer `sdk.ValidateLoginState` and `sdk.PersistAndCompleteLoginWithOptions` for simple “persist login, load client, connect, finish” flows.

4. Login loading and client reconstruction should follow one cached `UserLogin` path.
   External references:
   - `mautrix/go/bridgev2/userlogin.go`
   - `mautrix/go/bridgev2/networkinterface.go`
   Local cleanup targets:
   - `sdk/load_user_login.go`
   - `sdk/client_loader_builder.go`
   - `sdk/client_cache.go`
   - cache and loader glue in `sdk/connector_builder.go`
   Why this matters:
   - `bridgev2` draws a clean line between connector startup, cached login objects, and per-login runtime loading.
   - `ai-bridge` still has multiple thin layers that mostly forward cached-client and load-or-create decisions.
   Delete or align direction:
   - Remove trivial forwarding methods and keep one canonical client-loading path.
   - Preserve only the genuinely custom behavior, such as `BrokenLoginClient` if “visible but disabled” logins remain a requirement.

5. Backfill should use a single fetch model and queue model when the project needs real remote-history sync.
   External references:
   - `mautrix/go/bridgev2/networkinterface.go`
   - `mautrix/go/bridgev2/portalbackfill.go`
   - `mautrix/go/bridgev2/backfillqueue.go`
   Local cleanup targets:
   - `pkg/shared/backfillutil/*`
   - `sdk/types.go` fetch and replay interfaces
   - bridge-local backfill entry points
   Why this matters:
   - `bridgev2` models fetch params, fetch responses, forward/backward pagination, thread backfill, dedupe, and batch send as one pipeline.
   - `ai-bridge` has lighter utilities and several bridge-local backfill interpretations.
   Delete or align direction:
   - If backfill stays shallow, keep the local utilities.
   - If backfill grows into persistent history sync, adopt the `bridgev2` queue/task pattern instead of growing another local mini-framework.

6. Remote message conversion should use one model for message, edit, reaction, and status transport.
   External references:
   - `mautrix/go/bridgev2/networkinterface.go`
   - `mautrix/go/bridgev2/portal.go`
   Local cleanup targets:
   - `sdk/helpers.go`
   - `sdk/remote_events.go`
   - `sdk/base_reaction_handler.go`
   - parts of `sdk/status_helpers.go`
   Why this matters:
   - `bridgev2` centers transport on `ConvertedMessage`, `ConvertedEdit`, `MatrixMessageResponse`, `EventSender`, and the `RemoteEvent*` interfaces.
   - `ai-bridge` has accumulated several local send-via-portal wrappers and relation bookkeeping helpers.
   Delete or align direction:
   - Keep the AI-specific streaming and turn semantics.
   - Delete thin wrappers that only translate between equivalent send/edit/reaction abstractions.

7. Matrix media addressing should follow one direct-media or public-media convention.
   External references:
   - `mautrix/go/bridgev2/matrix/directmedia.go`
   - `mautrix/go/bridgev2/matrix/publicmedia.go`
   Local cleanup targets:
   - Matrix-facing portions of `sdk/media_helpers.go`
   - bridge-facing media-address helpers in `pkg/shared/media/*`
   Why this matters:
   - `bridgev2` clearly separates direct-media downloads from public signed media URLs and MXC generation.
   - `ai-bridge` still mixes generic file decoding concerns with Matrix-facing address-generation helpers.
   Delete or align direction:
   - Keep low-level file and data-URI decoding in `pkg/shared/media`.
   - Standardize one Matrix-facing content-addressing layer instead of per-bridge wrappers.

8. Identifier handling should treat IDs as opaque typed strings as much as possible.
   External references:
   - `mautrix/go/bridgev2/networkid/bridgeid.go`
   - `mautrix/go/bridgev2/networkinterface.go`
   Local cleanup targets:
   - `sdk/identifier_helpers.go`
   - ID-heavy helper code in `sdk/helpers.go`
   Why this matters:
   - `bridgev2` intentionally avoids forcing every caller to manually parse and reformat identifiers.
   - `ai-bridge` still has multiple places that rebuild or normalize ID strings by hand.
   Delete or align direction:
   - Keep policy-generating helpers such as `NextUserLoginID` and `NewTurnID`.
   - Delete reformatting and parsing helpers that only compensate for inconsistent internal conventions.

9. Database access should be explicit typed stores if JSON blob wrappers stop being enough.
   External references:
   - `mautrix/go/bridgev2/database/database.go`
   - `mautrix/go/bridgev2/database/portal.go`
   - `mautrix/go/bridgev2/database/message.go`
   - `mautrix/go/bridgev2/database/userlogin.go`
   - `mautrix/go/bridgev2/database/kvstore.go`
   Local cleanup targets:
   - ad hoc upsert/load/delete code around bridge-local JSON state tables
   - especially repeated state access in `bridges/*/*_db.go`
   Why this matters:
   - `bridgev2` uses typed query helpers per table instead of letting table semantics leak into random callers.
   - `ai-bridge` is still in a middle state: shared blob tables exist, but bridge-local state access often rewraps the same scoping and CRUD semantics.
   Delete or align direction:
   - If JSON blobs remain the long-term storage model, then the action item is not to port `bridgev2` tables, but to push more logic into one typed scope/store wrapper per state area.
   - If state becomes more relational, use the `bridgev2` pattern rather than inventing a second storage style.

10. Connector/client boundaries should map directly to one-time connector lifecycle and per-login runtime lifecycle.
    External references:
    - `mautrix/go/bridgev2/networkinterface.go`
    - `mautrix/go/bridgev2/bridge.go`
    Local cleanup targets:
    - `sdk/connector.go`
    - `sdk/connector_builder.go`
    - `sdk/client.go`
    - `sdk/client_base.go`
    - `sdk/client_cache.go`
    Why this matters:
    - `bridgev2` is strict about “connector config/bootstrap” versus “live client behavior”.
    - `ai-bridge` still carries several local abstraction layers that mainly forward lifecycle calls.
    Delete or align direction:
    - Keep the generic `Config[SessionT, ConfigDataT]` design.
    - Delete pass-through methods that exist only to restate what `bridgev2` already models.

### WhatsApp Conventions Worth Copying

1. Keep `connector.go` thin and declarative.
   Relevant external file:
   - `mautrix/whatsapp/pkg/connector/connector.go`
   Local targets:
   - `bridges/openclaw/connector.go`
   - `bridges/opencode/connector.go`
   - `bridges/dummybridge/connector.go`
   Direction:
   - Startup files should wire config, DB, commands, and runtime factories.
   - Feature logic should live in client, login, backfill, media, and conversion files.

2. Keep login flows as explicit state machines with named flow IDs and named step IDs.
   Relevant external file:
   - `mautrix/whatsapp/pkg/connector/login.go`
   Local targets:
   - `bridges/openclaw/login.go`
   - `bridges/opencode/login.go`
   - `bridges/dummybridge/login.go`
   Direction:
   - Each bridge should expose a small, readable process object with `Start`, optional `StartWithOverride`, and `SubmitUserInput`.
   - Shared helpers should handle boilerplate validation and completion behavior.

3. Keep start-chat and portal provisioning on one path.
   Relevant external file:
   - `mautrix/whatsapp/pkg/connector/startchat.go`
   Local targets:
   - `bridges/openclaw/provisioning.go`
   - `bridges/opencode/opencode_portal.go`
   - `bridges/opencode/sdk_catalog.go`
   - `bridges/dummybridge/bridge.go`
   Direction:
   - Standardize on one helper stack for identifier resolution, DM room creation, and initial portal metadata.
   - Prefer `sdk.BuildLoginDMChatInfo`, `sdk.ConfigureDMPortal`, `sdk.EnsurePortalLifecycle`, and `sdk.RefreshPortalLifecycle`.

4. Keep metadata files tiny and typed.
   Relevant external file:
   - `mautrix/whatsapp/pkg/connector/dbmeta.go`
   Local targets:
   - `bridges/openclaw/metadata.go`
   - `bridges/opencode/metadata.go`
   Direction:
   - Metadata files should define structures, constructors, and merge rules.
   - Persistence helpers and blob-table wiring should move into dedicated state files.

5. Keep client logic inside the client, not in connector helpers.
   Relevant external files:
   - `mautrix/whatsapp/pkg/connector/client.go`
   - `mautrix/whatsapp/pkg/connector/mclient.go`
   Local targets:
   - `bridges/openclaw/client.go`
   - `bridges/openclaw/manager.go`
   - `bridges/opencode/client.go`
   - `bridges/opencode/host.go`
   - `bridges/dummybridge/bridge.go`
   Direction:
   - Transport, reconnect, runtime state, and protocol event handling should be concentrated in the per-login runtime object.
   - Matrix adapters should stay as small boundaries, not secondary runtimes.

6. Keep Matrix-to-remote conversion separate from lifecycle and queue management.
   Relevant external file:
   - `mautrix/whatsapp/pkg/connector/handlewhatsapp.go`
   Local targets:
   - `bridges/opencode/opencode_messages.go`
   - `bridges/opencode/opencode_parts.go`
   - `bridges/openclaw/manager.go`
   Direction:
   - Parsing, canonicalization, transport dispatch, and portal mutation should not sit in the same large functions.

7. Treat backfill as a first-class subsystem.
   Relevant external file:
   - `mautrix/whatsapp/pkg/connector/backfill.go`
   Local targets:
   - `bridges/openclaw/manager.go`
   - `bridges/opencode/backfill.go`
   - `bridges/opencode/backfill_canonical.go`
   Direction:
   - Queueing, pagination, conversion, and send should be visibly separate concerns.
   - Avoid mixing history sync with live event handling in the same file.

8. Keep direct media isolated and helper-backed.
   Relevant external file:
   - `mautrix/whatsapp/pkg/connector/directmedia.go`
   Local targets:
   - `bridges/openclaw/media.go`
   - `bridges/opencode/opencode_media.go`
   - `bridges/opencode/host.go`
   Direction:
   - Bridge files should only parse source-specific media references and call shared helpers.
   - Shared layers should own retries, byte limits, MIME fallback, and Matrix send mechanics.

### Signal Conventions Worth Copying

1. Interactive login or provisioning should be a dedicated process object, not a connector-adjacent blob of logic.
   Local targets:
   - `bridges/ai/login.go`
   - `bridges/ai/login_loaders.go`
   - `bridges/ai/login_config_db.go`
   - `bridges/ai/login_state_db.go`
   Direction:
   - If the AI bridge keeps evolving interactive auth or provisioning flows, model them as explicit `Start`, `Wait`, `Cancel` process objects with their own state channel.

2. Connector orchestration should remain thin while protocol behavior sits in the client runtime.
   Local targets:
   - `bridges/ai/connector.go`
   - `bridges/ai/client.go`
   Direction:
   - `bridges/ai/connector.go` should wire stores, reconstruct logins, and register hooks.
   - Queueing, runtime state transitions, and protocol behavior should continue moving out of connector-side helpers and into focused client/runtime units.

3. Database scope wrappers should be explicit, typed, and reusable.
   Local targets:
   - `bridges/ai/bridge_db.go`
   - `bridges/ai/login_state_db.go`
   - `bridges/ai/login_config_db.go`
   - `bridges/ai/portal_state_db.go`
   - `bridges/ai/session_store.go`
   - `bridges/ai/system_events_db.go`
   - `bridges/ai/logout_cleanup.go`
   Direction:
   - Consolidate repeated `bridge_id` / `login_id` / `portal_id` scoping and transaction boilerplate behind typed store wrappers.
   - Avoid repeating scope resolution in every state file.

4. Buffered receive pipelines should own idempotency and cleanup in one place.
   Local targets:
   - `bridges/ai/debounce.go`
   - `bridges/ai/pending_queue.go`
   - `bridges/ai/pending_event.go`
   - `bridges/ai/reaction_feedback.go`
   - `bridges/ai/streaming_persistence.go`
   Direction:
   - Build one buffered-event abstraction that covers dedupe, retry, pending persistence, and TTL cleanup, instead of letting those semantics drift across several files.

5. Connection and streaming status should be modeled as one typed status pipeline.
   Local targets:
   - `bridges/ai/client.go`
   - `bridges/ai/streaming_state.go`
   - `bridges/ai/streaming_error_handling.go`
   - `bridges/ai/heartbeat_*`
   Direction:
   - Collapse the current scattered mix of queue state, heartbeat state, streaming state, and login state into one typed event/status model.

6. Attachment and media helpers should keep memory and file-backed paths aligned.
   Local targets:
   - `bridges/ai/media_download.go`
   - `bridges/ai/media_helpers.go`
   - `bridges/ai/media_send.go`
   - `bridges/ai/media_understanding_*`
   Direction:
   - Push normalization, size checks, local-file handling, and MIME fallback into shared helpers so bridge-local code only describes source-specific extraction.

7. Lifecycle state should be more strongly typed.
   Local targets:
   - `bridges/ai/pending_queue.go`
   - `bridges/ai/streaming_state.go`
   - `bridges/ai/reply_policy.go`
   - `bridges/ai/queue_resolution.go`
   Direction:
   - Replace stringly-typed modes and ad hoc flags with small enums and narrow status objects wherever possible.

### Concrete Code-Removal Targets

These are the places where upstream conventions most clearly imply local deletions or consolidations.

1. `pkg/search` and `pkg/fetch`
   Why this is first:
   - The code is already near-duplicate.
   - Nothing in `bridgev2`, WhatsApp, or Signal argues for keeping separate provider stacks here.
   Direction:
   - Merge toward one provider/runtime/env/router stack with different operation modes.

2. `sdk` portal lifecycle wrappers
   Files:
   - `sdk/portal_lifecycle.go`
   - `sdk/login_handle.go`
   - parts of `sdk/helpers.go`
   Why this is next:
   - `bridgev2` already provides the conceptual shape.
   - Current helper layers make room setup and cleanup harder to trace.
   Direction:
   - Replace small wrapper helpers with one authoritative portal lifecycle owner.

3. `sdk` login scaffolding
   Files:
   - `sdk/base_login_process.go`
   - `sdk/login_helpers.go`
   - parts of `sdk/command_login.go`
   Why this is next:
   - `bridgev2` and WhatsApp both use the same shape: one process object, one step protocol, one command path.
   Direction:
   - Delete local step ceremony that only restates shared login process semantics.

4. Per-bridge portal setup paths
   Files:
   - `bridges/openclaw/provisioning.go`
   - `bridges/opencode/opencode_portal.go`
   - `bridges/opencode/sdk_catalog.go`
   - `bridges/dummybridge/bridge.go`
   Why this is next:
   - WhatsApp’s start-chat flow is much more centralized.
   - The current local spread makes DM room creation and portal refresh inconsistent.
   Direction:
   - Standardize one start-chat and portal-creation path for all bridges.

5. Per-bridge metadata persistence helpers
   Files:
   - `bridges/openclaw/metadata.go`
   - `bridges/openclaw/portal_state_db.go`
   - `bridges/ai/portal_state_db.go`
   - `bridges/opencode/metadata.go`
   Why this is next:
   - WhatsApp keeps metadata definitions separate from persistence behavior.
   - `ai-bridge` still lets metadata types and storage helpers bleed together.
   Direction:
   - Keep typed metadata definitions, but move persistence and scoping into explicit state stores.

6. `bridges/ai` queue and status machinery
   Files:
   - `bridges/ai/pending_queue.go`
   - `bridges/ai/pending_event.go`
   - `bridges/ai/streaming_state.go`
   - `bridges/ai/streaming_error_handling.go`
   - `bridges/ai/heartbeat_*`
   Why this is next:
   - Signal is very clear that one status model and one buffered pipeline makes the system easier to follow.
   Direction:
   - Delete duplicated state transitions and keep one typed pipeline for pending, running, failed, canceled, and replayed work.

7. Media wrappers that only restate shared helpers
   Files:
   - `sdk/media_helpers.go`
   - `bridges/openclaw/media.go`
   - `bridges/opencode/opencode_media.go`
   - `bridges/ai/media_helpers.go`
   Why this is next:
   - Both WhatsApp and Signal keep media logic disciplined: bridge-local code parses source data, shared code handles the actual transfer and validation semantics.
   Direction:
   - Delete bridge-local helpers that simply repackage download, decode, or send operations without adding source-specific logic.

8. Connector and client pass-through layers
   Files:
   - `sdk/connector.go`
   - `sdk/connector_builder.go`
   - `sdk/client.go`
   - `sdk/client_base.go`
   - `sdk/client_cache.go`
   Why this is next:
   - `bridgev2` is already explicit about one-time connector lifecycle versus per-login runtime lifecycle.
   Direction:
   - Keep the generic config surface.
   - Delete forwarding methods that do not add real project-specific behavior.

### Best-Practice Summary

The mature bridge pattern is consistent across `bridgev2`, WhatsApp, and Signal:

- one thin connector for bootstrap and registration
- one explicit login process model
- one client/runtime owner for live behavior
- one portal lifecycle owner
- one path for room metadata refresh
- one path for start-chat and portal provisioning
- one path for backfill
- one path for media download and Matrix media addressing
- one typed store/scoping layer
- one typed status pipeline

`ai-bridge` already has many of the right pieces. The problem is that it often adds one more local abstraction layer on top of those pieces. The highest-leverage deletions are the wrappers and bridge-local helper stacks that restate lifecycle, login, media, room metadata, and state-store behavior that should already have one owner.
