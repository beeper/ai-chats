package sdk

import (
	"net/http"
	"strings"

	"maunium.net/go/mautrix/bridgev2"
)

const loginErrorCodePrefix = "COM.BEEPER.AGENTREMOTE"

func sanitizeLoginErrorCodePart(part string) string {
	part = strings.TrimSpace(strings.ToUpper(part))
	if part == "" {
		return ""
	}
	replacer := strings.NewReplacer(
		".", "_",
		"-", "_",
		" ", "_",
		"/", "_",
		":", "_",
	)
	return replacer.Replace(part)
}

func LoginErrorCode(parts ...string) string {
	filtered := make([]string, 0, len(parts)+1)
	filtered = append(filtered, loginErrorCodePrefix)
	for _, part := range parts {
		part = sanitizeLoginErrorCodePart(part)
		if part != "" {
			filtered = append(filtered, part)
		}
	}
	return strings.Join(filtered, ".")
}

func NewLoginRespError(statusCode int, message string, parts ...string) bridgev2.RespError {
	return bridgev2.RespError{
		ErrCode:    LoginErrorCode(parts...),
		Err:        strings.TrimSpace(message),
		StatusCode: statusCode,
	}
}

func WrapLoginRespError(err error, statusCode int, parts ...string) bridgev2.RespError {
	if err == nil {
		return NewLoginRespError(statusCode, http.StatusText(statusCode), parts...)
	}
	return NewLoginRespError(statusCode, err.Error(), parts...)
}
