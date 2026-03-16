package ai

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
	AIRateLimited   status.BridgeStateErrorCode = "ai-rate-limited"
	AIAuthFailed    status.BridgeStateErrorCode = "ai-auth-failed"
	AIProviderError status.BridgeStateErrorCode = "ai-provider-error"
	AIBillingError  status.BridgeStateErrorCode = "ai-billing-error"
)

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
		Err:        "Enter an API key.",
		StatusCode: http.StatusBadRequest,
	}
	ErrBaseURLRequired = bridgev2.RespError{
		ErrCode:    "IO.AI_BRIDGE.BASE_URL_REQUIRED",
		Err:        "Enter a base URL.",
		StatusCode: http.StatusBadRequest,
	}
	ErrOpenAIOrOpenRouterRequired = bridgev2.RespError{
		ErrCode:    "IO.AI_BRIDGE.OPENAI_OR_OPENROUTER_REQUIRED",
		Err:        "Enter an OpenAI or OpenRouter API key.",
		StatusCode: http.StatusBadRequest,
	}
)

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

// authPatterns are string signals that indicate an authentication/authorization error.
var authPatterns = []string{
	"invalid api key",
	"invalid_api_key",
	"incorrect api key",
	"invalid token",
	"unauthorized",
	"token has expired",
	"no credentials found",
	"no api key found",
	"re-authenticate",
	"oauth token refresh failed",
	"authentication failed",
	"authentication_error",
}

var permissionDeniedPatterns = []string{
	"access_denied",
	"feature flag",
	"subscription",
	"requires the",
	"permission_error",
}

var permissionFallbackPatterns = []string{
	"forbidden",
	"access denied",
	"insufficient permission",
	"insufficient_permission",
	"permission denied",
}

// IsPermissionDeniedError checks if the error is a non-auth permission or
// entitlement failure, such as a missing feature flag or subscription.
func IsPermissionDeniedError(err error) bool {
	if err == nil || IsModelNotFound(err) {
		return false
	}

	var apiErr *openai.Error
	if errors.As(err, &apiErr) {
		if apiErr.StatusCode == 403 {
			if containsAnyInFields(permissionDeniedPatterns,
				apiErr.Code, apiErr.Type, apiErr.Message, apiErr.RawJSON()) {
				return true
			}
			if !containsAnyInFields(authPatterns,
				apiErr.Code, apiErr.Type, apiErr.Message, apiErr.RawJSON()) &&
				containsAnyInFields(permissionFallbackPatterns,
					apiErr.Code, apiErr.Type, apiErr.Message, apiErr.RawJSON()) {
				return true
			}
		}
	}

	return containsAnyPattern(err, permissionDeniedPatterns)
}

// IsAuthError checks if the error is an authentication error.
// Checks openai.Error status codes first, then falls back to string pattern matching.
func IsAuthError(err error) bool {
	if IsModelNotFound(err) || IsPermissionDeniedError(err) {
		return false
	}

	var apiErr *openai.Error
	if errors.As(err, &apiErr) {
		if apiErr.StatusCode == 401 {
			return true
		}
		if apiErr.StatusCode == 403 {
			if containsAnyInFields(authPatterns,
				apiErr.Code, apiErr.Type, apiErr.Message, apiErr.RawJSON()) {
				return true
			}
			return true
		}
	}
	return containsAnyPattern(err, authPatterns) || containsAnyPattern(err, permissionFallbackPatterns)
}

// IsModelNotFound checks if the error is a model not found (404) error
func IsModelNotFound(err error) bool {
	var apiErr *openai.Error
	if errors.As(err, &apiErr) {
		if apiErr.StatusCode == 404 {
			return true
		}
		if strings.EqualFold(strings.TrimSpace(apiErr.Code), "model_not_found") {
			return true
		}
		if strings.EqualFold(strings.TrimSpace(apiErr.Type), "invalid_request_error") &&
			containsAnyInFields([]string{"model is not available", "model not found", "\"code\":\"model_not_found\""},
				apiErr.Message, apiErr.RawJSON()) {
			return true
		}
	}
	return containsAnyPattern(err, []string{
		"model_not_found",
		"this model is not available",
		"model not found",
	})
}
