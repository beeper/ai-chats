# pkg/ai Runtime Migration Notes

This repository now includes a standalone `pkg/ai` Go port of `pi-mono/packages/ai`,
plus controlled connector bridge paths that can route runtime execution to `pkg/ai`.

## Feature flags

### Connector runtime selector

- `PI_USE_PKG_AI_RUNTIME=1`
  - Enables connector runtime selection path (`streamWithPkgAIBridge`).
  - Keeps safe fallback to existing Responses/Chat Completions code paths.

- `PI_USE_PKG_AI_RUNTIME_DRY_RUN=1`
  - Runs optional `pkg/ai` dry-run stream consumption for diagnostics while still
    executing the existing connector runtime path.

### Provider runtime bridge

- `PI_USE_PKG_AI_PROVIDER_RUNTIME=1`
  - Enables `OpenAIProvider` bridging for:
    - `GenerateStream(...)` via `tryGenerateStreamWithPkgAI(...)`
    - `Generate(...)` via `tryGenerateWithPkgAI(...)`
  - Includes guarded fallback for unresolved/stubbed provider APIs.

## Current bridge behavior

- Streaming (`PI_USE_PKG_AI_RUNTIME`):
  - Controlled live pkg/ai event consumption is enabled for safe non-tool
    scenarios.
  - Falls back to legacy streaming runtime when bridge conditions are not met.

- Provider abstraction (`PI_USE_PKG_AI_PROVIDER_RUNTIME`):
  - Routes both streaming and non-streaming provider calls through pkg/ai where
    possible.
  - Preserves existing connector behavior on fallback-class errors.

## High-signal test commands

```bash
go test ./pkg/ai/...
CGO_ENABLED=0 go test ./pkg/connector -run "TestPkgAIProviderRuntimeEnabled|TestInferProviderNameFromBaseURL|TestBuildPkgAIModelFromGenerateParams|TestShouldFallbackFromPkgAIEvent|TestShouldFallbackFromPkgAIError|TestTryGenerateStreamWithPkgAIReturnsRuntimeErrorEventsWhenProviderResolved|TestTryGenerateWithPkgAIFallsBackOnStubbedProviders|TestTryGenerateWithPkgAIReturnsRuntimeErrorWhenProviderResolved|TestGenerateResponseFromAIMessage|TestParseThinkingLevel|TestOpenAIProviderGenerate_UsesPkgAIBridgeWhenEnabled|TestPkgAIRuntimeEnabledFromEnv|TestChooseStreamingRuntimePath|TestPromptContainsToolCalls|TestShouldUsePkgAIBridgeStreaming|TestBuildPkgAIBridgeGenerateParams|TestPkgAIProviderBridgeCredentials|TestAIEventToStreamEvent_Mapping|TestStreamEventsFromAIStream|TestToAIContext_MapsMessagesAndTools"
```

## Connector bridge env-gated provider validation

To validate real provider happy paths for connector bridge routing (OpenAI, Anthropic, Google), set credentials and:

```bash
PI_AI_E2E=1 CGO_ENABLED=0 go test ./pkg/connector -run "TestPkgAIProviderBridgeE2E_"
```

Optional model overrides:

- `PI_AI_E2E_OPENAI_MODEL`
- `PI_AI_E2E_ANTHROPIC_MODEL`
- `PI_AI_E2E_GOOGLE_MODEL`

## pkg/ai env-gated provider parity e2e tests

The `pkg/ai/e2e` suite now includes live provider parity checks for:

- OpenAI basic complete/stream flows (`stream.test.ts` parity subset),
- stream cancel behavior (`abort.test.ts` parity subset),
- orphan tool-call recovery (`tool-call-without-result.test.ts` parity subset),
- usage total-token accounting (`total-tokens.test.ts` parity subset).
- context-overflow detection (`context-overflow.test.ts` parity subset).
- Anthropic and Google complete/stream smoke coverage.

Run with:

```bash
PI_AI_E2E=1 OPENAI_API_KEY=... ANTHROPIC_API_KEY=... GEMINI_API_KEY=... \
  go test ./pkg/ai/e2e -run "TestGenerateE2E_OpenAI|TestAbortE2E_OpenAIStream|TestToolCallWithoutResultE2E_OpenAI|TestTotalTokensE2E_OpenAI|TestContextOverflowE2E_OpenAI|TestGenerateE2E_Anthropic|TestGenerateE2E_Google"
```

Optional overrides:

- `PI_AI_E2E_OPENAI_MODEL` (default: `gpt-4o-mini`)
- `PI_AI_E2E_OPENAI_BASE_URL` (for OpenAI-compatible endpoints)
- `PI_AI_E2E_OPENAI_CONTEXT_WINDOW` (default: `128000`, or `400000` for `gpt-5*` models)
- `PI_AI_E2E_ANTHROPIC_MODEL` / `PI_AI_E2E_ANTHROPIC_BASE_URL`
- `PI_AI_E2E_GOOGLE_MODEL` / `PI_AI_E2E_GOOGLE_BASE_URL`

## Notes

- Full integration remains feature-gated.
- Fallback behavior is intentional and required for incremental rollout safety.
