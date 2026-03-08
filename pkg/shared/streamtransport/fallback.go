package streamtransport

import (
	"errors"
	"strings"

	"maunium.net/go/mautrix"
)

// ShouldFallbackToDebounced returns true when an ephemeral stream send failure
// looks like server/client unsupported behavior (unknown/unrecognized/etc).
func ShouldFallbackToDebounced(err error) bool {
	if err == nil {
		return false
	}
	if isFallbackRespErrorCode(respErrorCode(err)) {
		return true
	}
	var httpErr mautrix.HTTPError
	if errors.As(err, &httpErr) {
		if code := strings.TrimSpace(httpErrCode(&httpErr)); isFallbackRespErrorCode(code) {
			return true
		}
		switch httpErrStatus(&httpErr) {
		case 404, 405, 501:
			return true
		}
		combined := strings.ToLower(strings.TrimSpace(httpErr.Message + " " + httpErr.ResponseBody))
		if looksLikeUnknownUnsupported(combined) {
			return true
		}
	}
	combined := strings.ToLower(strings.TrimSpace(err.Error()))
	return looksLikeUnknownUnsupported(combined)
}

func respErrorCode(err error) string {
	var respErrPtr *mautrix.RespError
	if errors.As(err, &respErrPtr) && respErrPtr != nil {
		return strings.TrimSpace(respErrPtr.ErrCode)
	}
	return ""
}

func httpErrCode(err *mautrix.HTTPError) string {
	if err == nil || err.RespError == nil {
		return ""
	}
	return strings.TrimSpace(err.RespError.ErrCode)
}

func httpErrStatus(err *mautrix.HTTPError) int {
	if err == nil || err.Response == nil {
		return 0
	}
	return err.Response.StatusCode
}

func isFallbackRespErrorCode(code string) bool {
	upper := strings.ToUpper(code)
	if upper == "" {
		return false
	}
	return strings.Contains(upper, "UNKNOWN") ||
		strings.Contains(upper, "UNRECOGNIZED") ||
		strings.Contains(upper, "UNSUPPORTED") ||
		strings.Contains(upper, "NOT_IMPLEMENTED")
}

func looksLikeUnknownUnsupported(s string) bool {
	if s == "" {
		return false
	}
	if strings.Contains(s, "ephemeral") {
		return strings.Contains(s, "unknown") ||
			strings.Contains(s, "unrecognized") ||
			strings.Contains(s, "unsupported") ||
			strings.Contains(s, "not implemented")
	}
	return strings.Contains(s, "m_unrecognized") ||
		strings.Contains(s, "m_unknown")
}
