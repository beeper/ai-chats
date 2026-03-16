package opencode

import (
	"testing"

	"github.com/beeper/agentremote/bridges/opencode/api"
)

func TestBackfillTotalTokensIncludesPartCacheTokens(t *testing.T) {
	msg := api.MessageWithParts{
		Parts: []api.Part{{
			Type: "step-finish",
			Tokens: &api.TokenUsage{
				Input:  5,
				Output: 7,
				Cache: &api.TokenCache{
					Read:  11,
					Write: 13,
				},
			},
		}},
	}

	if got := backfillTotalTokens(msg); got != 36 {
		t.Fatalf("expected part cache tokens to be included, got %d", got)
	}
}
