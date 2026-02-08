package connector

import (
	"errors"
	"strings"
	"testing"

	"github.com/openai/openai-go/v3"
)

func TestParseContextLengthError_OpenAIStyle(t *testing.T) {
	err := &openai.Error{
		StatusCode: 400,
		Message:    "This model's maximum context length is 128000 tokens. However, your messages resulted in 130532 tokens.",
	}

	cle := ParseContextLengthError(err)
	if cle == nil {
		t.Fatal("expected context length error")
	}
	if cle.ModelMaxTokens != 128000 {
		t.Fatalf("expected max tokens 128000, got %d", cle.ModelMaxTokens)
	}
	if cle.RequestedTokens != 130532 {
		t.Fatalf("expected requested tokens 130532, got %d", cle.RequestedTokens)
	}
}

func TestParseContextLengthError_OpenRouterAnthropicStyle(t *testing.T) {
	err := errors.New(`POST "https://ai.bt.hn/openrouter/v1/chat/completions": 400 Bad Request {"message":"Provider returned error","code":400,"metadata":{"raw":"{\"type\":\"error\",\"error\":{\"type\":\"invalid_request_error\",\"message\":\"prompt is too long: 217779 tokens > 200000 maximum\"}}"}}`)

	cle := ParseContextLengthError(err)
	if cle == nil {
		t.Fatal("expected context length error")
	}
	if cle.ModelMaxTokens != 200000 {
		t.Fatalf("expected max tokens 200000, got %d", cle.ModelMaxTokens)
	}
	if cle.RequestedTokens != 217779 {
		t.Fatalf("expected requested tokens 217779, got %d", cle.RequestedTokens)
	}
}

func TestParseContextLengthError_NonContextError(t *testing.T) {
	err := errors.New("POST https://example.test/chat/completions: 400 Bad Request invalid schema")
	if cle := ParseContextLengthError(err); cle != nil {
		t.Fatal("expected nil for non-context error")
	}
}

func TestParseContextLengthError_RequestTooLarge(t *testing.T) {
	err := errors.New(`{"type":"error","error":{"type":"request_too_large","message":"Request too large"}}`)
	cle := ParseContextLengthError(err)
	if cle == nil {
		t.Fatal("expected context length error for request_too_large")
	}
}

func TestParseContextLengthError_413TooLarge(t *testing.T) {
	err := errors.New("413 too large: request body exceeds limit")
	cle := ParseContextLengthError(err)
	if cle == nil {
		t.Fatal("expected context length error for 413 too large")
	}
}

func TestParseContextLengthError_ExceedsMaximumSize(t *testing.T) {
	err := errors.New("request exceeds the maximum size allowed")
	cle := ParseContextLengthError(err)
	if cle == nil {
		t.Fatal("expected context length error for request exceeds the maximum size")
	}
}

func TestParseContextLengthError_ExceedsModelContextWindow(t *testing.T) {
	err := errors.New("the input exceeds model context window of 128000 tokens")
	cle := ParseContextLengthError(err)
	if cle == nil {
		t.Fatal("expected context length error for exceeds model context window")
	}
}

func TestIsRateLimitError_ResourceExhausted(t *testing.T) {
	err := errors.New("resource_exhausted: quota exceeded for project")
	if !IsRateLimitError(err) {
		t.Fatal("expected resource_exhausted to be classified as rate limit")
	}
}

func TestIsRateLimitError_QuotaExceeded(t *testing.T) {
	err := errors.New("quota exceeded for this billing period")
	if !IsRateLimitError(err) {
		t.Fatal("expected 'quota exceeded' to be classified as rate limit")
	}
}

func TestIsRateLimitError_UsageLimit(t *testing.T) {
	err := errors.New("usage limit reached for this model")
	if !IsRateLimitError(err) {
		t.Fatal("expected 'usage limit' to be classified as rate limit")
	}
}

func TestIsRateLimitError_429StatusCode(t *testing.T) {
	err := &openai.Error{StatusCode: 429, Message: "rate limit exceeded"}
	if !IsRateLimitError(err) {
		t.Fatal("expected 429 status to be classified as rate limit")
	}
}

func TestIsOverloadedError_ResourceExhausted(t *testing.T) {
	// resource_exhausted should still match overloaded too
	err := errors.New("resource_exhausted: service overloaded")
	if !IsOverloadedError(err) {
		t.Fatal("expected resource_exhausted to still match overloaded")
	}
}

func TestIsMissingToolCallInputError(t *testing.T) {
	tests := []struct {
		msg  string
		want bool
	}{
		{"tool_call.input is required", true},
		{"tool_use.input must be provided", true},
		{"input is a required property", true},
		{"missing required field: input", true},
		{"some other error", false},
	}
	for _, tt := range tests {
		if got := IsMissingToolCallInputError(errors.New(tt.msg)); got != tt.want {
			t.Errorf("IsMissingToolCallInputError(%q) = %v, want %v", tt.msg, got, tt.want)
		}
	}
}

func TestIsToolUseIDFormatError(t *testing.T) {
	tests := []struct {
		msg  string
		want bool
	}{
		{"invalid tool_use_id format", true},
		{"tool_use.id must be alphanumeric", true},
		{"tool_call_id is invalid", true},
		{"invalid tool_use block", true},
		{"tool_use block with id xyz is malformed", true},
		{"some other error", false},
	}
	for _, tt := range tests {
		if got := IsToolUseIDFormatError(errors.New(tt.msg)); got != tt.want {
			t.Errorf("IsToolUseIDFormatError(%q) = %v, want %v", tt.msg, got, tt.want)
		}
	}
}

func TestFormatUserFacingError_MissingToolCallInput(t *testing.T) {
	err := errors.New("tool_call.input is required but was not provided")
	msg := FormatUserFacingError(err)
	if msg != "Session data is corrupted (missing tool call input). Please start a new conversation to recover." {
		t.Fatalf("unexpected message: %s", msg)
	}
}

func TestFormatUserFacingError_ToolUseIDFormat(t *testing.T) {
	err := errors.New("invalid tool_use_id format: must be alphanumeric")
	msg := FormatUserFacingError(err)
	if msg != "Tool call ID format error. Please start a new conversation to recover." {
		t.Fatalf("unexpected message: %s", msg)
	}
}

func TestFormatUserFacingError_JSONPayloadParsing(t *testing.T) {
	// Anthropic-style nested error
	err := errors.New(`{"error":{"type":"invalid_request_error","message":"prompt is too long"}}`)
	msg := FormatUserFacingError(err)
	// This should match context length first since "prompt is too long" triggers it
	if msg == "The AI provider returned an error. Please try again." {
		t.Fatal("expected JSON to be parsed, not treated as generic")
	}
}

func TestFormatUserFacingError_FlatJSONPayload(t *testing.T) {
	err := errors.New(`{"type":"server_error","message":"internal failure"}`)
	msg := FormatUserFacingError(err)
	// String-based checks don't match server_error (requires openai.Error),
	// so it falls through to JSON parsing
	expected := "server_error: internal failure"
	if msg != expected {
		t.Fatalf("expected %q, got %q", expected, msg)
	}
}

func TestFormatUserFacingError_UnknownJSONPayload(t *testing.T) {
	// JSON that doesn't match known error types but has parseable type+message
	err := errors.New(`{"type":"custom_error","message":"something specific happened"}`)
	msg := FormatUserFacingError(err)
	expected := "custom_error: something specific happened"
	if msg != expected {
		t.Fatalf("expected %q, got %q", expected, msg)
	}
}

func TestFormatUserFacingError_TruncationAt600(t *testing.T) {
	// Create an error with exactly 601 chars
	longMsg := ""
	for len(longMsg) < 601 {
		longMsg += "a"
	}
	err := errors.New(longMsg)
	msg := FormatUserFacingError(err)
	if len(msg) != 603 { // 600 + "..."
		t.Fatalf("expected truncation at 600 chars + ..., got len %d", len(msg))
	}
}

func TestFormatUserFacingError_StripsFinalTags(t *testing.T) {
	err := errors.New("some error <final>internal details</final>happened")
	msg := FormatUserFacingError(err)
	if msg != "some error happened" {
		t.Fatalf("expected final tags stripped, got: %q", msg)
	}
}

func TestStripFinalTags(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"no tags here", "no tags here"},
		{"before <final>secret</final> after", "before  after"},
		{"<final>all secret</final>", ""},
		{"unclosed <final>tag", "unclosed"},
		{"multiple <final>a</final> and <final>b</final> tags", "multiple  and  tags"},
	}
	for _, tt := range tests {
		if got := stripFinalTags(tt.input); got != tt.want {
			t.Errorf("stripFinalTags(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}
}

func TestParseJSONErrorMessage(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		// Anthropic-style nested
		{`{"error":{"type":"invalid_request_error","message":"prompt is too long"}}`, "invalid_request_error: prompt is too long"},
		// Flat style
		{`{"type":"rate_limit_error","message":"too many requests"}`, "rate_limit_error: too many requests"},
		// Message only
		{`{"message":"something went wrong"}`, "something went wrong"},
		// No message field
		{`{"status":"error"}`, ""},
		// Invalid JSON
		{`not json`, ""},
	}
	for _, tt := range tests {
		if got := parseJSONErrorMessage(tt.input); got != tt.want {
			t.Errorf("parseJSONErrorMessage(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}
}

func TestStripThinkTags(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"no think tags", "no think tags"},
		{"<think>reasoning here</think> actual response", "actual response"},
		{"<think>line1\nline2\nline3</think>\nresponse", "response"},
		{"<think>first</think> middle <think>second</think> end", "middle end"},
		{"<think>everything is thinking</think>", ""},
		{"response without think", "response without think"},
	}
	for _, tt := range tests {
		if got := stripThinkTags(tt.input); got != tt.want {
			t.Errorf("stripThinkTags(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}
}

func TestIsCompactionFailureError(t *testing.T) {
	tests := []struct {
		msg  string
		want bool
	}{
		{"context_length_exceeded: compaction failed due to overflow", true},
		{"prompt is too long: auto-compaction exceeded limits", true},
		{"prompt is too long: summarization failed for session", true},
		{"context_length_exceeded: normal overflow", false}, // "compaction" not present
		{"compaction failed but not a context error", false}, // no context signal
		{"just a normal error", false},
	}
	for _, tt := range tests {
		if got := IsCompactionFailureError(errors.New(tt.msg)); got != tt.want {
			t.Errorf("IsCompactionFailureError(%q) = %v, want %v", tt.msg, got, tt.want)
		}
	}
}

func TestIsBillingError_ResourceHasBeenExhausted(t *testing.T) {
	err := errors.New("resource has been exhausted for project XYZ")
	if !IsBillingError(err) {
		t.Fatal("expected 'resource has been exhausted' to be classified as billing error")
	}
}

func TestIsOverloadedError_JSONOverloadedError(t *testing.T) {
	err := errors.New(`{"type":"overloaded_error","message":"server is overloaded"}`)
	if !IsOverloadedError(err) {
		t.Fatal("expected JSON overloaded_error to be classified as overloaded")
	}
}

func TestIsAuthError_StringFallback(t *testing.T) {
	tests := []struct {
		msg  string
		want bool
	}{
		{"invalid api key provided", true},
		{"invalid_api_key: check your credentials", true},
		{"incorrect api key", true},
		{"invalid token for endpoint", true},
		{"unauthorized access", true},
		{"forbidden: insufficient permissions", true},
		{"access denied for resource", true},
		{"token has expired, please refresh", true},
		{"no credentials found in request", true},
		{"no api key found", true},
		{"please re-authenticate", true},
		{"oauth token refresh failed", true},
		{"just a normal error", false},
	}
	for _, tt := range tests {
		if got := IsAuthError(errors.New(tt.msg)); got != tt.want {
			t.Errorf("IsAuthError(%q) = %v, want %v", tt.msg, got, tt.want)
		}
	}
}

func TestParseImageDimensionError(t *testing.T) {
	err := errors.New("image dimensions exceed maximum: image exceeds 2000 px limit")
	result := ParseImageDimensionError(err)
	if result == nil {
		t.Fatal("expected image dimension error")
	}
	if result.MaxDimensionPx != 2000 {
		t.Fatalf("expected 2000px, got %d", result.MaxDimensionPx)
	}
}

func TestParseImageDimensionError_NotImageError(t *testing.T) {
	err := errors.New("some random error with 2000 px")
	if ParseImageDimensionError(err) != nil {
		t.Fatal("expected nil for non-image error")
	}
}

func TestParseImageSizeError(t *testing.T) {
	err := errors.New("image too large: max allowed size is 20 MB")
	result := ParseImageSizeError(err)
	if result == nil {
		t.Fatal("expected image size error")
	}
	if result.MaxMB != 20 {
		t.Fatalf("expected 20MB, got %f", result.MaxMB)
	}
}

func TestParseImageSizeError_NotImageError(t *testing.T) {
	err := errors.New("file is 20 MB")
	if ParseImageSizeError(err) != nil {
		t.Fatal("expected nil for non-image error")
	}
}

func TestCollapseConsecutiveDuplicateBlocks(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"single block", "single block"},
		{"block1\n\nblock2", "block1\n\nblock2"},
		{"same\n\nsame", "same"},
		{"a\n\na\n\nb\n\nb\n\nc", "a\n\nb\n\nc"},
		{"", ""},
	}
	for _, tt := range tests {
		if got := collapseConsecutiveDuplicateBlocks(tt.input); got != tt.want {
			t.Errorf("collapseConsecutiveDuplicateBlocks(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}
}

func TestFormatUserFacingError_ImageDimensionLimit(t *testing.T) {
	err := errors.New("image dimensions exceed maximum: image exceeds 4096 pixels resolution limit")
	msg := FormatUserFacingError(err)
	if msg != "Image exceeds 4096px dimension limit. Please resize the image and try again." {
		t.Fatalf("unexpected message: %s", msg)
	}
}

func TestFormatUserFacingError_ImageSizeLimit(t *testing.T) {
	err := errors.New("image too large: max allowed size is 10 MB")
	msg := FormatUserFacingError(err)
	if msg != "Image exceeds 10MB size limit. Please use a smaller image." {
		t.Fatalf("unexpected message: %s", msg)
	}
}

func TestClassifyFailoverReason(t *testing.T) {
	tests := []struct {
		name   string
		err    error
		expect FailoverReason
	}{
		{"nil", nil, FailoverUnknown},
		{"auth", errors.New("unauthorized access"), FailoverAuth},
		{"billing", errors.New("payment required"), FailoverBilling},
		{"rate_limit", errors.New("resource_exhausted: rate limit hit"), FailoverRateLimit},
		{"timeout", errors.New("context deadline exceeded"), FailoverTimeout},
		{"overloaded", errors.New("service unavailable 503"), FailoverOverload},
		{"unknown", errors.New("something random"), FailoverUnknown},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := ClassifyFailoverReason(tt.err); got != tt.expect {
				t.Errorf("ClassifyFailoverReason() = %q, want %q", got, tt.expect)
			}
		})
	}
}

func TestSanitizeHistoryImages(t *testing.T) {
	// Small image should be preserved
	smallB64 := "data:image/png;base64," + strings.Repeat("A", 200)
	if got := sanitizeHistoryImages(smallB64); got != smallB64 {
		t.Error("expected small image to be preserved")
	}

	// Large image (over 1MB decoded) should be stripped
	// 1MB base64 = ~1.37M chars
	largeB64 := "data:image/png;base64," + strings.Repeat("A", 1500000)
	got := sanitizeHistoryImages(largeB64)
	if strings.Contains(got, "AAAA") {
		t.Error("expected large image to be stripped")
	}
	if got != "[image removed: too large for history]" {
		t.Errorf("unexpected replacement: %q", got[:50])
	}

	// Text without images should be unchanged
	plain := "hello world, no images here"
	if sanitizeHistoryImages(plain) != plain {
		t.Error("expected plain text to be unchanged")
	}
}

func TestHasContextLengthSignal_NewPatterns(t *testing.T) {
	tests := []struct {
		text string
		want bool
	}{
		{"context_length_exceeded", true},
		{"context length is too long", true},
		{"prompt is too long", true},
		{"request_too_large", true},
		{"Request Too Large error", true},
		{"413 Too Large", true},
		{"request exceeds the maximum size", true},
		{"exceeds model context window", true},
		{"just a normal error", false},
	}
	for _, tt := range tests {
		if got := hasContextLengthSignal(tt.text); got != tt.want {
			t.Errorf("hasContextLengthSignal(%q) = %v, want %v", tt.text, got, tt.want)
		}
	}
}
