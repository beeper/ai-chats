package runtime

import "strings"

func ClassifyFallbackError(err error) FailureClass {
	if err == nil {
		return FailureClassUnknown
	}
	text := strings.ToLower(strings.TrimSpace(err.Error()))
	switch {
	case strings.Contains(text, "model_not_found"),
		strings.Contains(text, "this model is not available"),
		(strings.Contains(text, "model") && strings.Contains(text, "not found")),
		(strings.Contains(text, "model") && strings.Contains(text, "not available")):
		return FailureClassProviderHard
	case strings.Contains(text, "access_denied"),
		strings.Contains(text, "feature flag"),
		strings.Contains(text, "require a subscription"),
		strings.Contains(text, "requires a subscription"),
		strings.Contains(text, "permission_error"):
		return FailureClassProviderHard
	case strings.Contains(text, "api key"), strings.Contains(text, "invalid_api_key"),
		strings.Contains(text, "authentication"), strings.Contains(text, "unauthorized"),
		strings.Contains(text, "forbidden"), strings.Contains(text, "permission"):
		return FailureClassAuth
	case strings.Contains(text, "context") && strings.Contains(text, "length"):
		return FailureClassContextOverflow
	case strings.Contains(text, "rate") && strings.Contains(text, "limit"),
		strings.Contains(text, "429"), strings.Contains(text, "resource_exhausted"),
		strings.Contains(text, "quota"), strings.Contains(text, "overloaded"),
		strings.Contains(text, "too many requests"):
		return FailureClassRateLimit
	case strings.Contains(text, "timeout"), strings.Contains(text, "deadline exceeded"),
		strings.Contains(text, "timed out"), strings.Contains(text, "request_timeout"):
		return FailureClassTimeout
	case strings.Contains(text, "connection"), strings.Contains(text, "network"),
		strings.Contains(text, "connection reset"), strings.Contains(text, "econnreset"):
		return FailureClassNetwork
	case strings.Contains(text, "provider"), strings.Contains(text, "model"),
		strings.Contains(text, "not found"), strings.Contains(text, "404"),
		strings.Contains(text, "payment"), strings.Contains(text, "billing"),
		strings.Contains(text, "402"), strings.Contains(text, "500"),
		strings.Contains(text, "502"), strings.Contains(text, "503"), strings.Contains(text, "504"):
		return FailureClassProviderHard
	default:
		return FailureClassUnknown
	}
}

// DecideFallback converts raw errors into runtime-standard retry/failover behavior.
func DecideFallback(err error) FallbackDecision {
	class := ClassifyFallbackError(err)
	switch class {
	case FailureClassAuth:
		return FallbackDecision{
			Class:       class,
			Action:      FallbackActionAbort,
			Reason:      "auth_or_permission_error",
			StatusText:  "Authentication/permission failed. Check credentials or access.",
			ShouldRetry: false,
		}
	case FailureClassRateLimit:
		return FallbackDecision{
			Class:       class,
			Action:      FallbackActionFailover,
			Reason:      "rate_limited",
			StatusText:  "Provider rate limit reached. Trying fallback model.",
			ShouldRetry: true,
		}
	case FailureClassTimeout, FailureClassNetwork:
		return FallbackDecision{
			Class:       class,
			Action:      FallbackActionRetry,
			Reason:      "transient_network_failure",
			StatusText:  "Temporary network/provider issue. Retrying.",
			ShouldRetry: true,
		}
	case FailureClassContextOverflow:
		return FallbackDecision{
			Class:       class,
			Action:      FallbackActionTrimRetry,
			Reason:      "context_overflow",
			StatusText:  "Context window exceeded. Compacting and retrying.",
			ShouldRetry: true,
		}
	case FailureClassProviderHard:
		return FallbackDecision{
			Class:       class,
			Action:      FallbackActionFailover,
			Reason:      "provider_hard_failure",
			StatusText:  "Provider/model failure. Trying fallback model.",
			ShouldRetry: true,
		}
	default:
		return FallbackDecision{
			Class:       FailureClassUnknown,
			Action:      FallbackActionNone,
			Reason:      "unknown_error",
			StatusText:  "Unknown error.",
			ShouldRetry: false,
		}
	}
}
