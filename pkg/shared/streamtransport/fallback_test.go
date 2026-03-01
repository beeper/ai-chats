package streamtransport

import (
	"errors"
	"net/http"
	"testing"

	"maunium.net/go/mautrix"
)

func TestShouldFallbackToDebounced(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want bool
	}{
		{
			name: "resp error unrecognized",
			err:  &mautrix.RespError{ErrCode: "M_UNRECOGNIZED", Err: "unknown endpoint"},
			want: true,
		},
		{
			name: "http error unknown code",
			err: mautrix.HTTPError{
				Response:  &http.Response{StatusCode: 400},
				RespError: &mautrix.RespError{ErrCode: "M_UNKNOWN"},
			},
			want: true,
		},
		{
			name: "http 501 no code",
			err: mautrix.HTTPError{
				Response: &http.Response{StatusCode: 501},
				Message:  "not implemented",
			},
			want: true,
		},
		{
			name: "generic ephemeral unsupported text",
			err:  errors.New("ephemeral events unsupported by homeserver"),
			want: true,
		},
		{
			name: "generic unrelated error",
			err:  errors.New("temporary network timeout"),
			want: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ShouldFallbackToDebounced(tt.err)
			if got != tt.want {
				t.Fatalf("ShouldFallbackToDebounced() = %v, want %v", got, tt.want)
			}
		})
	}
}
