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

## Notes

- Full integration remains feature-gated.
- Fallback behavior is intentional and required for incremental rollout safety.
