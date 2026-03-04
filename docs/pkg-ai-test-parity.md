# pkg/ai Test Parity Tracker

This document tracks parity between upstream `pi-mono/packages/ai/test/*.test.ts`
and the Go port in `pkg/ai`.

Legend:

- ✅ **Ported**: implemented as Go test(s) with runtime behavior coverage.
- 🧪 **Env-gated**: implemented as live provider test, skipped without credentials.
- 📝 **Scaffold**: placeholder test exists; behavior not fully ported yet.

## Current parity snapshot

### Core stream/runtime/e2e behavior

- `stream.test.ts` → ✅🧪 `pkg/ai/e2e/stream_test.go`, `pkg/ai/e2e/parity_provider_runtime_test.go`
- `abort.test.ts` → ✅🧪 `pkg/ai/e2e/abort_test.go`
- `context-overflow.test.ts` → ✅🧪 `pkg/ai/e2e/context_overflow_test.go`
- `tool-call-without-result.test.ts` → ✅🧪 `pkg/ai/e2e/parity_openai_test.go`
- `total-tokens.test.ts` → ✅🧪 `pkg/ai/e2e/parity_openai_test.go`
- `tokens.test.ts` → ✅🧪 `pkg/ai/e2e/abort_test.go` (OpenAI subset)
- `openai-responses-reasoning-replay-e2e.test.ts` → ✅🧪 `pkg/ai/e2e/openai_reasoning_replay_e2e_test.go` (+ deterministic conversion assertions in `pkg/ai/providers/openai_responses_shared_test.go`)
- `cross-provider-handoff.test.ts` → ✅🧪 `pkg/ai/e2e/cross_provider_handoff_e2e_test.go` (OpenAI↔Anthropic subset)
- `tool-call-id-normalization.test.ts` → ✅🧪 `pkg/ai/e2e/tool_call_id_normalization_e2e_test.go` (OpenAI subset) + deterministic ID normalization tests in providers
- `xhigh.test.ts` → ✅🧪 `pkg/ai/e2e/xhigh_e2e_test.go` (OpenAI subset) + deterministic support checks in `pkg/ai/models_test.go`
- `empty.test.ts` → ✅🧪 `pkg/ai/e2e/empty_e2e_test.go` (OpenAI subset)

### Provider/unit parity

- `openai-completions-tool-choice.test.ts` → ✅ `pkg/ai/providers/openai_completions_test.go`
- `openai-completions-tool-result-images.test.ts` → ✅ `pkg/ai/providers/openai_completions_test.go`
- `openai-codex-stream.test.ts` → ✅ `pkg/ai/providers/openai_codex_responses_test.go`
- `google-gemini-cli-retry-delay.test.ts` → ✅ `pkg/ai/providers/google_gemini_cli_test.go`
- `google-gemini-cli-empty-stream.test.ts` → ✅ `pkg/ai/providers/google_gemini_cli_test.go`
- `google-gemini-cli-claude-thinking-header.test.ts` → ✅ `pkg/ai/providers/google_gemini_cli_test.go`
- `google-tool-call-missing-args.test.ts` → ✅ `pkg/ai/providers/google_tool_call_missing_args_test.go`
- `google-shared-gemini3-unsigned-tool-call.test.ts` → ✅ `pkg/ai/providers/google_shared_test.go`
- `google-thinking-signature.test.ts` → ✅ `pkg/ai/providers/google_shared_test.go`
- `transform-messages-copilot-openai-to-anthropic.test.ts` → ✅ `pkg/ai/providers/transform_messages_test.go`
- `anthropic-tool-name-normalization.test.ts` → ✅ `pkg/ai/providers/anthropic_test.go`
- `cache-retention.test.ts` → ✅ `pkg/ai/providers/cache_retention_test.go`
- `image-tool-result.test.ts` → ✅🧪 `pkg/ai/e2e/image_tool_result_e2e_test.go` (OpenAI subset) + deterministic conversion tests in `pkg/ai/providers/openai_completions_test.go`, `pkg/ai/providers/openai_responses_shared_test.go`
- `unicode-surrogate.test.ts` → ✅ `pkg/ai/utils/sanitize_unicode_test.go`
- `supports-xhigh.test.ts` / `xhigh.test.ts` → ✅ `pkg/ai/models_test.go`
- `interleaved-thinking.test.ts` (deterministic parts) → ✅ `pkg/ai/providers/anthropic_test.go`, `pkg/ai/providers/amazon_bedrock_test.go`
- `bedrock-models.test.ts` (deterministic parts) → ✅ `pkg/ai/providers/amazon_bedrock_test.go`

### OAuth parity

- `oauth.ts` (provider/token helper semantics) → ✅ `pkg/ai/oauth/*_test.go`

### Remaining scaffolds in Go e2e suite

The following are currently kept as env-gated scaffolds in
`pkg/ai/e2e/parity_scaffolds_test.go`:

- 📝 `zen.test.ts`
- 📝 `github-copilot-anthropic.test.ts` (live)
