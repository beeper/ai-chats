package runtime

import "strings"

func ClassifyFallbackError(err error) FailureClass {
	if err == nil {
		return FailureClassUnknown
	}
	text := strings.ToLower(strings.TrimSpace(err.Error()))
	switch {
	case strings.Contains(text, "context") && strings.Contains(text, "length"):
		return FailureClassContextOverflow
	case strings.Contains(text, "rate") && strings.Contains(text, "limit"):
		return FailureClassRateLimit
	case strings.Contains(text, "auth"), strings.Contains(text, "unauthorized"), strings.Contains(text, "forbidden"):
		return FailureClassAuth
	case strings.Contains(text, "timeout"):
		return FailureClassTimeout
	case strings.Contains(text, "connection"), strings.Contains(text, "network"):
		return FailureClassNetwork
	case strings.Contains(text, "provider") || strings.Contains(text, "model"):
		return FailureClassProviderHard
	default:
		return FailureClassUnknown
	}
}

func ShouldTriggerFallback(class FailureClass) bool {
	switch class {
	case FailureClassAuth, FailureClassRateLimit, FailureClassTimeout, FailureClassNetwork, FailureClassContextOverflow, FailureClassProviderHard:
		return true
	default:
		return false
	}
}
