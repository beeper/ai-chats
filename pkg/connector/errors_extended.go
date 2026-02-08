package connector

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

// ProxyError represents a structured error from the hungryserv proxy
type ProxyError struct {
	Code      string `json:"code"`
	Message   string `json:"message"`
	Details   string `json:"details"`
	Provider  string `json:"provider"`
	Retryable bool   `json:"retryable"`
	Type      string `json:"type"`
	Status    int    `json:"status"`
}

// ProxyErrorResponse is the wrapper for proxy errors
type ProxyErrorResponse struct {
	Error ProxyError `json:"error"`
}

// ParseProxyError attempts to parse a structured proxy error from an error message
func ParseProxyError(err error) *ProxyError {
	if err == nil {
		return nil
	}
	msg := err.Error()

	// Try to find JSON in the error message
	startIdx := strings.Index(msg, "{")
	if startIdx == -1 {
		return nil
	}

	var resp ProxyErrorResponse
	if jsonErr := json.Unmarshal([]byte(msg[startIdx:]), &resp); jsonErr == nil {
		if resp.Error.Type == "proxy_error" {
			return &resp.Error
		}
	}

	// Try parsing directly as ProxyError
	var proxyErr ProxyError
	if jsonErr := json.Unmarshal([]byte(msg[startIdx:]), &proxyErr); jsonErr == nil {
		if proxyErr.Type == "proxy_error" {
			return &proxyErr
		}
	}

	return nil
}

// IsProxyError checks if the error is a structured proxy error
func IsProxyError(err error) bool {
	return ParseProxyError(err) != nil
}

// FormatProxyError formats a proxy error for user display
func FormatProxyError(proxyErr *ProxyError) string {
	if proxyErr == nil {
		return ""
	}

	switch proxyErr.Code {
	case "timeout", "connection_timeout":
		return "Request timed out waiting for AI provider. Please try again."
	case "connection_refused":
		return "Could not connect to AI provider. The service may be down."
	case "connection_reset", "connection_closed":
		return "Connection to AI provider was lost. Please try again."
	case "dns_error":
		return "Could not reach AI provider. Please check your connection."
	case "request_cancelled":
		return "Request was cancelled."
	default:
		if proxyErr.Message != "" {
			return proxyErr.Message
		}
		return "Failed to reach AI provider. Please try again."
	}
}

// FallbackReasoningLevel returns a lower reasoning level to try when the current one fails.
// Returns empty string if there's no fallback available (already at "none" or unknown level).
func FallbackReasoningLevel(current string) string {
	// Reasoning level hierarchy: xhigh -> high -> medium -> low -> none
	switch current {
	case "xhigh":
		return "high"
	case "high":
		return "medium"
	case "medium":
		return "low"
	case "low":
		return "none"
	case "none", "":
		return "" // No fallback available
	default:
		return "medium" // Unknown level, try medium
	}
}

// containsAnyPattern checks if the lowercased error message contains any of the given patterns.
func containsAnyPattern(err error, patterns []string) bool {
	if err == nil {
		return false
	}
	msg := strings.ToLower(err.Error())
	for _, pattern := range patterns {
		if strings.Contains(msg, pattern) {
			return true
		}
	}
	return false
}

// IsCompactionFailureError checks if a context-length error originated from
// compaction itself (e.g., the summarisation prompt overflowed). This lets
// callers avoid re-attempting compaction when compaction was the thing that failed.
func IsCompactionFailureError(err error) bool {
	if ParseContextLengthError(err) == nil {
		return false
	}
	return containsAnyPattern(err, []string{
		"summarization failed",
		"auto-compaction",
		"compaction failed",
		"compaction",
	})
}

// IsBillingError checks if the error is a billing/payment error (402)
func IsBillingError(err error) bool {
	return containsAnyPattern(err, []string{
		"402",
		"payment required",
		"insufficient credits",
		"credit balance",
		"exceeded your current quota",
		"quota exceeded",
		"billing",
		"plans & billing",
		"resource has been exhausted",
	})
}

// IsOverloadedError checks if the error indicates the service is overloaded
func IsOverloadedError(err error) bool {
	return containsAnyPattern(err, []string{
		"overloaded_error",
		"\"overloaded_error\"",
		"overloaded",
		"resource_exhausted",
		"service unavailable",
		"503",
	})
}

// IsTimeoutError checks if the error is a timeout error
func IsTimeoutError(err error) bool {
	return containsAnyPattern(err, []string{
		"timeout",
		"timed out",
		"deadline exceeded",
		"context deadline exceeded",
		"etimedout",
		"esockettimedout",
		"econnreset",
		"econnaborted",
		"408",
		"504",
	})
}

// IsImageError checks if the error is related to image size or dimensions
func IsImageError(err error) bool {
	return containsAnyPattern(err, []string{
		"image exceeds",
		"image dimensions exceed",
		"image too large",
		"image size",
		"max allowed size",
	})
}

// IsReasoningError checks if the error is related to unsupported reasoning/thinking levels
func IsReasoningError(err error) bool {
	return containsAnyPattern(err, []string{
		"reasoning",
		"thinking",
		"extended thinking",
		"reasoning_effort",
	})
}

// IsRoleOrderingError checks if the error is related to message role ordering conflicts
func IsRoleOrderingError(err error) bool {
	return containsAnyPattern(err, []string{
		"incorrect role information",
		"roles must alternate",
		"consecutive user",
		"consecutive assistant",
	})
}

// IsMissingToolCallInputError checks if the error indicates a corrupted session
// where tool call inputs are missing (e.g., from interrupted streaming).
func IsMissingToolCallInputError(err error) bool {
	return containsAnyPattern(err, []string{
		"tool_call.input",
		"tool_use.input",
		"input is a required property",
		"missing required field: input",
	})
}

// IsToolUseIDFormatError checks if the error is caused by an invalid tool_use ID format
// (e.g., when IDs from one provider are replayed to another).
func IsToolUseIDFormatError(err error) bool {
	return containsAnyPattern(err, []string{
		"tool_use_id",
		"tool_use.id",
		"tool_call_id",
		"invalid tool_use block",
		"tool_use block with id",
	})
}

// ImageDimensionError contains parsed details from image dimension errors.
type ImageDimensionError struct {
	MaxDimensionPx int
}

var imageDimensionPattern = regexp.MustCompile(`(\d+)\s*(?:px|pixels)`)

// ParseImageDimensionError extracts max dimension from an image error.
// Returns nil if the error is not an image dimension error.
func ParseImageDimensionError(err error) *ImageDimensionError {
	if err == nil || !IsImageError(err) {
		return nil
	}
	msg := strings.ToLower(err.Error())
	if !strings.Contains(msg, "dimension") && !strings.Contains(msg, "resolution") {
		return nil
	}
	if matches := imageDimensionPattern.FindStringSubmatch(msg); len(matches) > 1 {
		if px, parseErr := strconv.Atoi(matches[1]); parseErr == nil && px > 0 {
			return &ImageDimensionError{MaxDimensionPx: px}
		}
	}
	return nil
}

// ImageSizeError contains parsed details from image size errors.
type ImageSizeError struct {
	MaxMB float64
}

var imageSizeMBPattern = regexp.MustCompile(`(\d+(?:\.\d+)?)\s*(?:mb|megabytes)`)

// ParseImageSizeError extracts max size in MB from an image error.
// Returns nil if the error is not an image size error.
func ParseImageSizeError(err error) *ImageSizeError {
	if err == nil || !IsImageError(err) {
		return nil
	}
	msg := strings.ToLower(err.Error())
	if matches := imageSizeMBPattern.FindStringSubmatch(msg); len(matches) > 1 {
		if mb, parseErr := strconv.ParseFloat(matches[1], 64); parseErr == nil && mb > 0 {
			return &ImageSizeError{MaxMB: mb}
		}
	}
	return nil
}

// collapseConsecutiveDuplicateBlocks removes consecutive duplicate paragraphs
// from an error message. Paragraphs are separated by double newlines.
func collapseConsecutiveDuplicateBlocks(s string) string {
	blocks := strings.Split(s, "\n\n")
	if len(blocks) <= 1 {
		return s
	}
	deduped := []string{blocks[0]}
	for i := 1; i < len(blocks); i++ {
		if strings.TrimSpace(blocks[i]) != strings.TrimSpace(blocks[i-1]) {
			deduped = append(deduped, blocks[i])
		}
	}
	return strings.Join(deduped, "\n\n")
}

// FormatUserFacingError transforms an API error into a user-friendly message.
// Returns a sanitized message suitable for display to end users.
func FormatUserFacingError(err error) string {
	if err == nil {
		return "An unknown error occurred."
	}

	// Check specific error types and return user-friendly messages
	if IsBillingError(err) {
		return "Billing issue with AI provider. Please check your account credits or upgrade your plan."
	}

	if IsOverloadedError(err) {
		return "The AI service is temporarily overloaded. Please try again in a moment."
	}

	if IsRateLimitError(err) {
		return "Rate limited by AI provider. Please wait a moment before retrying."
	}

	if IsTimeoutError(err) {
		return "Request timed out. The server took too long to respond. Please try again."
	}

	if IsAuthError(err) {
		return "Authentication failed. Please check your API key or re-login."
	}

	if cle := ParseContextLengthError(err); cle != nil {
		if cle.ModelMaxTokens > 0 {
			return "Context overflow: prompt too large for the model. Try again with less input or a larger-context model."
		}
		return "Your message is too long for this model's context window. Please try a shorter message."
	}

	if IsImageError(err) {
		if dimErr := ParseImageDimensionError(err); dimErr != nil && dimErr.MaxDimensionPx > 0 {
			return fmt.Sprintf("Image exceeds %dpx dimension limit. Please resize the image and try again.", dimErr.MaxDimensionPx)
		}
		if sizeErr := ParseImageSizeError(err); sizeErr != nil && sizeErr.MaxMB > 0 {
			return fmt.Sprintf("Image exceeds %.0fMB size limit. Please use a smaller image.", sizeErr.MaxMB)
		}
		return "Image is too large or has invalid dimensions. Please resize the image and try again."
	}

	if IsRoleOrderingError(err) {
		return "Message ordering conflict - please try again. If this persists, start a new conversation."
	}

	if IsReasoningError(err) {
		return "This model doesn't support the requested reasoning level. Try reducing reasoning effort in settings."
	}

	if IsModelNotFound(err) {
		return "The requested model is not available. Please select a different model."
	}

	if IsMissingToolCallInputError(err) {
		return "Session data is corrupted (missing tool call input). Please start a new conversation to recover."
	}

	if IsToolUseIDFormatError(err) {
		return "Tool call ID format error. Please start a new conversation to recover."
	}

	// Check for structured proxy errors (from hungryserv)
	if proxyErr := ParseProxyError(err); proxyErr != nil {
		return FormatProxyError(proxyErr)
	}

	if IsServerError(err) {
		return "The AI provider encountered an error. Please try again later."
	}

	// For unknown errors, try to extract a sensible message
	msg := err.Error()

	// Strip <final> tags that may leak from internal processing
	msg = stripFinalTags(msg)

	// Strip common error prefixes
	prefixes := []string{
		"error:",
		"api error:",
		"openai error:",
		"anthropic error:",
	}
	lower := strings.ToLower(msg)
	for _, prefix := range prefixes {
		if strings.HasPrefix(lower, prefix) {
			msg = strings.TrimSpace(msg[len(prefix):])
			break
		}
	}

	// Truncate very long error messages
	if len(msg) > 600 {
		msg = msg[:600] + "..."
	}

	// If the message looks like raw JSON, try to extract a readable error
	if strings.HasPrefix(strings.TrimSpace(msg), "{") {
		if parsed := parseJSONErrorMessage(msg); parsed != "" {
			return collapseConsecutiveDuplicateBlocks(parsed)
		}
		return "The AI provider returned an error. Please try again."
	}

	return collapseConsecutiveDuplicateBlocks(msg)
}

// FailoverReason is a typed enum for classifying why a model failover happened.
type FailoverReason string

const (
	FailoverAuth      FailoverReason = "auth"
	FailoverBilling   FailoverReason = "billing"
	FailoverRateLimit FailoverReason = "rate_limit"
	FailoverTimeout   FailoverReason = "timeout"
	FailoverFormat    FailoverReason = "format"
	FailoverOverload  FailoverReason = "overload"
	FailoverServer    FailoverReason = "server"
	FailoverUnknown   FailoverReason = "unknown"
)

// ClassifyFailoverReason returns a structured reason for why a model failover
// should occur. Wraps the existing Is*Error functions into a single classifier.
func ClassifyFailoverReason(err error) FailoverReason {
	if err == nil {
		return FailoverUnknown
	}
	if IsAuthError(err) {
		return FailoverAuth
	}
	if IsBillingError(err) {
		return FailoverBilling
	}
	if IsRateLimitError(err) {
		return FailoverRateLimit
	}
	if IsTimeoutError(err) {
		return FailoverTimeout
	}
	if IsOverloadedError(err) {
		return FailoverOverload
	}
	if IsToolSchemaError(err) || IsRoleOrderingError(err) {
		return FailoverFormat
	}
	if IsServerError(err) {
		return FailoverServer
	}
	return FailoverUnknown
}

// stripFinalTags removes <final>...</final> tags from text.
func stripFinalTags(s string) string {
	for {
		start := strings.Index(s, "<final>")
		if start == -1 {
			break
		}
		end := strings.Index(s, "</final>")
		if end == -1 {
			// Unclosed tag — strip from <final> to end
			s = strings.TrimSpace(s[:start])
			break
		}
		s = strings.TrimSpace(s[:start] + s[end+len("</final>"):])
	}
	return s
}

// parseJSONErrorMessage attempts to extract a human-readable message from a JSON error payload.
func parseJSONErrorMessage(raw string) string {
	// Try nested {"error": {"type": ..., "message": ...}} format (Anthropic style)
	var nested struct {
		Error struct {
			Type    string `json:"type"`
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal([]byte(raw), &nested); err == nil && nested.Error.Message != "" {
		if nested.Error.Type != "" {
			return nested.Error.Type + ": " + nested.Error.Message
		}
		return nested.Error.Message
	}

	// Try flat {"type": ..., "message": ...} format (OpenAI style)
	var flat struct {
		Type    string `json:"type"`
		Message string `json:"message"`
	}
	if err := json.Unmarshal([]byte(raw), &flat); err == nil && flat.Message != "" {
		if flat.Type != "" {
			return flat.Type + ": " + flat.Message
		}
		return flat.Message
	}

	return ""
}
