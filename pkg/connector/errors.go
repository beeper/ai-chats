package connector

import (
	"errors"
	"net/http"
	"regexp"
	"strconv"
	"strings"

	"github.com/openai/openai-go/v3"
	"maunium.net/go/mautrix/bridgev2"
	"maunium.net/go/mautrix/bridgev2/status"
)

// Bridge state error codes for AI-specific errors
const (
	AIRateLimited    status.BridgeStateErrorCode = "ai-rate-limited"
	AIAuthFailed     status.BridgeStateErrorCode = "ai-auth-failed"
	AIContextTooLong status.BridgeStateErrorCode = "ai-context-too-long"
	AIModelNotFound  status.BridgeStateErrorCode = "ai-model-not-found"
	AIProviderError  status.BridgeStateErrorCode = "ai-provider-error"
	AIBillingError   status.BridgeStateErrorCode = "ai-billing-error"
	AIOverloaded     status.BridgeStateErrorCode = "ai-overloaded"
	AITimeout        status.BridgeStateErrorCode = "ai-timeout"
	AIImageError     status.BridgeStateErrorCode = "ai-image-error"
)

// BridgeStateHumanErrors provides human-readable messages for AI bridge error codes
var BridgeStateHumanErrors = map[status.BridgeStateErrorCode]string{
	AIRateLimited:    "Rate limited by AI provider. Please wait before retrying.",
	AIAuthFailed:     "API key is invalid or has expired.",
	AIContextTooLong: "Conversation is too long for this model's context window.",
	AIModelNotFound:  "The requested model is not available.",
	AIProviderError:  "The AI provider returned an error.",
	AIBillingError:   "Billing issue with AI provider. Please check your account credits.",
	AIOverloaded:     "The AI service is temporarily overloaded. Please try again in a moment.",
	AITimeout:        "Request timed out. Please try again.",
	AIImageError:     "Image is too large or has invalid dimensions for this model.",
}

var (
	maxContextPattern        = regexp.MustCompile(`maximum context length is (\d+) tokens`)
	resultedTokensPattern    = regexp.MustCompile(`resulted in (\d+) tokens`)
	promptTooLongPattern     = regexp.MustCompile(`prompt is too long:\s*(\d+)\s*tokens\s*>\s*(\d+)\s*maximum`)
	overflowTokenPairPattern = regexp.MustCompile(`(\d+)\s*tokens\s*>\s*(\d+)\s*(?:maximum|max)`)
)

// Pre-defined bridgev2.RespError constants for consistent error responses
var (
	ErrAPIKeyRequired = bridgev2.RespError{
		ErrCode:    "IO.AI_BRIDGE.API_KEY_REQUIRED",
		Err:        "please enter an API key",
		StatusCode: http.StatusBadRequest,
	}
	ErrBaseURLRequired = bridgev2.RespError{
		ErrCode:    "IO.AI_BRIDGE.BASE_URL_REQUIRED",
		Err:        "please enter a base URL",
		StatusCode: http.StatusBadRequest,
	}
	ErrOpenAIOrOpenRouterRequired = bridgev2.RespError{
		ErrCode:    "IO.AI_BRIDGE.OPENAI_OR_OPENROUTER_REQUIRED",
		Err:        "please enter an OpenAI or OpenRouter API key",
		StatusCode: http.StatusBadRequest,
	}
	ErrAPIKeyInvalid = bridgev2.RespError{
		ErrCode:    "IO.AI_BRIDGE.INVALID_API_KEY",
		Err:        "the provided API key is invalid",
		StatusCode: http.StatusUnauthorized,
	}
	ErrProviderUnavailable = bridgev2.RespError{
		ErrCode:    "IO.AI_BRIDGE.PROVIDER_UNAVAILABLE",
		Err:        "the AI provider is currently unavailable",
		StatusCode: http.StatusServiceUnavailable,
	}
	ErrContextLengthExceeded = bridgev2.RespError{
		ErrCode:    "IO.AI_BRIDGE.CONTEXT_LENGTH_EXCEEDED",
		Err:        "message context too long, some messages were truncated",
		StatusCode: http.StatusRequestEntityTooLarge,
	}
	ErrUnsupportedMediaType = bridgev2.RespError{
		ErrCode:    "IO.AI_BRIDGE.UNSUPPORTED_MEDIA_TYPE",
		Err:        "this media type is not supported by the current model",
		StatusCode: http.StatusUnsupportedMediaType,
	}
	ErrModelNotFound = bridgev2.RespError{
		ErrCode:    "IO.AI_BRIDGE.MODEL_NOT_FOUND",
		Err:        "the requested model is not available",
		StatusCode: http.StatusNotFound,
	}
)

// MapErrorToStateCode maps an API error to a bridge state error code.
// Returns empty string if the error doesn't map to a known state code.
func MapErrorToStateCode(err error) status.BridgeStateErrorCode {
	if err == nil {
		return ""
	}
	if IsBillingError(err) {
		return AIBillingError
	}
	if IsOverloadedError(err) {
		return AIOverloaded
	}
	if IsTimeoutError(err) {
		return AITimeout
	}
	if IsImageError(err) {
		return AIImageError
	}
	if IsRateLimitError(err) {
		return AIRateLimited
	}
	if IsAuthError(err) {
		return AIAuthFailed
	}
	if ParseContextLengthError(err) != nil {
		return AIContextTooLong
	}
	if IsModelNotFound(err) {
		return AIModelNotFound
	}
	if IsServerError(err) {
		return AIProviderError
	}
	return ""
}

// ContextLengthError contains parsed details from context_length_exceeded errors
type ContextLengthError struct {
	ModelMaxTokens  int
	RequestedTokens int
	OriginalError   error
}

func (e *ContextLengthError) Error() string {
	return e.OriginalError.Error()
}

// PreDeltaError indicates a failure before any assistant output was streamed.
type PreDeltaError struct {
	Err error
}

func (e *PreDeltaError) Error() string {
	if e == nil || e.Err == nil {
		return "pre-delta error"
	}
	return e.Err.Error()
}

func (e *PreDeltaError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.Err
}

func IsPreDeltaError(err error) bool {
	var pde *PreDeltaError
	return errors.As(err, &pde)
}

func parseContextLengthTokenCounts(text string) (maxTokens, requestedTokens int) {
	if text == "" {
		return 0, 0
	}

	lower := strings.ToLower(text)
	if matches := maxContextPattern.FindStringSubmatch(lower); len(matches) > 1 {
		maxTokens, _ = strconv.Atoi(matches[1])
	}
	if matches := resultedTokensPattern.FindStringSubmatch(lower); len(matches) > 1 {
		requestedTokens, _ = strconv.Atoi(matches[1])
	}
	if matches := promptTooLongPattern.FindStringSubmatch(lower); len(matches) > 2 {
		requestedTokens, _ = strconv.Atoi(matches[1])
		maxTokens, _ = strconv.Atoi(matches[2])
	}
	if (maxTokens == 0 || requestedTokens == 0) && strings.Contains(lower, "prompt is too long") {
		if matches := overflowTokenPairPattern.FindStringSubmatch(lower); len(matches) > 2 {
			requestedTokens, _ = strconv.Atoi(matches[1])
			maxTokens, _ = strconv.Atoi(matches[2])
		}
	}

	return maxTokens, requestedTokens
}

func hasContextLengthSignal(text string) bool {
	lower := strings.ToLower(text)
	return strings.Contains(lower, "context length") ||
		strings.Contains(lower, "context_length") ||
		strings.Contains(lower, "prompt is too long") ||
		strings.Contains(lower, "request_too_large") ||
		strings.Contains(lower, "request too large") ||
		strings.Contains(lower, "413 too large") ||
		strings.Contains(lower, "request exceeds the maximum size") ||
		strings.Contains(lower, "exceeds model context window")
}

func safeErrorString(err error) (text string) {
	if err == nil {
		return ""
	}
	defer func() {
		if recover() != nil {
			text = ""
		}
	}()
	return err.Error()
}

// ParseContextLengthError checks if err is a context length exceeded error
// and extracts the token counts from the error message
func ParseContextLengthError(err error) *ContextLengthError {
	if err == nil {
		return nil
	}

	var cle *ContextLengthError
	if errors.As(err, &cle) {
		return cle
	}

	var sources []string
	if text := safeErrorString(err); text != "" {
		sources = append(sources, text)
	}
	var apiErr *openai.Error
	if errors.As(err, &apiErr) {
		if apiErr.Message != "" {
			sources = append(sources, apiErr.Message)
		}
		if raw := apiErr.RawJSON(); raw != "" {
			sources = append(sources, raw)
		}
	}

	matched := false
	maxTokens := 0
	requestedTokens := 0
	for _, source := range sources {
		if !hasContextLengthSignal(source) {
			continue
		}
		matched = true
		parsedMax, parsedRequested := parseContextLengthTokenCounts(source)
		if parsedMax > 0 {
			maxTokens = parsedMax
		}
		if parsedRequested > 0 {
			requestedTokens = parsedRequested
		}
	}
	if !matched {
		return nil
	}

	if apiErr != nil && apiErr.StatusCode != 0 && apiErr.StatusCode != 400 && apiErr.StatusCode != 413 {
		return nil
	}

	return &ContextLengthError{
		ModelMaxTokens:  maxTokens,
		RequestedTokens: requestedTokens,
		OriginalError:   err,
	}
}

// IsRateLimitError checks if the error is a rate limit (429) error
func IsRateLimitError(err error) bool {
	var apiErr *openai.Error
	if errors.As(err, &apiErr) {
		if strings.EqualFold(apiErr.Code, "rate_limit_exceeded") {
			return true
		}
		if apiErr.StatusCode == 429 {
			return true
		}
	}
	return containsAnyPattern(err, []string{
		"resource_exhausted",
		"quota exceeded",
		"usage limit",
	})
}

// IsServerError checks if the error is a server-side (5xx) error
func IsServerError(err error) bool {
	var apiErr *openai.Error
	if errors.As(err, &apiErr) {
		if strings.EqualFold(apiErr.Code, "server_error") {
			return true
		}
		return apiErr.StatusCode >= 500
	}
	return false
}

// IsAuthError checks if the error is an authentication error.
// Checks openai.Error status codes first, then falls back to string pattern matching.
func IsAuthError(err error) bool {
	var apiErr *openai.Error
	if errors.As(err, &apiErr) {
		if apiErr.StatusCode == 401 || apiErr.StatusCode == 403 {
			return true
		}
	}
	return containsAnyPattern(err, []string{
		"invalid api key",
		"invalid_api_key",
		"incorrect api key",
		"invalid token",
		"unauthorized",
		"forbidden",
		"access denied",
		"token has expired",
		"no credentials found",
		"no api key found",
		"re-authenticate",
		"oauth token refresh failed",
	})
}

// IsModelNotFound checks if the error is a model not found (404) error
func IsModelNotFound(err error) bool {
	var apiErr *openai.Error
	if errors.As(err, &apiErr) {
		return apiErr.StatusCode == 404
	}
	return false
}

// IsToolSchemaError checks if the error indicates a tool schema validation failure.
func IsToolSchemaError(err error) bool {
	var apiErr *openai.Error
	if errors.As(err, &apiErr) {
		lowerMsg := strings.ToLower(apiErr.Message)
		if strings.EqualFold(apiErr.Code, "invalid_function_parameters") {
			return true
		}
		if strings.Contains(apiErr.Message, "Invalid schema for function") {
			return true
		}
		if strings.Contains(lowerMsg, "input_schema") &&
			(strings.Contains(lowerMsg, "oneof") || strings.Contains(lowerMsg, "allof") || strings.Contains(lowerMsg, "anyof")) {
			return true
		}
		raw := apiErr.RawJSON()
		if raw != "" {
			lowerRaw := strings.ToLower(raw)
			if strings.Contains(raw, "invalid_function_parameters") || strings.Contains(raw, "Invalid schema for function") {
				return true
			}
			if strings.Contains(lowerRaw, "input_schema") &&
				(strings.Contains(lowerRaw, "oneof") || strings.Contains(lowerRaw, "allof") || strings.Contains(lowerRaw, "anyof")) {
				return true
			}
		}
	}
	return false
}

// IsToolUniquenessError checks if the error indicates duplicate tool names.
func IsToolUniquenessError(err error) bool {
	var apiErr *openai.Error
	if errors.As(err, &apiErr) {
		if strings.Contains(apiErr.Message, "tools: Tool names must be unique") {
			return true
		}
		raw := apiErr.RawJSON()
		if raw != "" && strings.Contains(raw, "tools: Tool names must be unique") {
			return true
		}
	}
	return false
}

// IsNoResponseChunksError checks if the Responses streaming returned no chunks.
func IsNoResponseChunksError(err error) bool {
	for err != nil {
		if strings.Contains(err.Error(), "No response chunks received") {
			return true
		}
		err = errors.Unwrap(err)
	}
	return false
}
