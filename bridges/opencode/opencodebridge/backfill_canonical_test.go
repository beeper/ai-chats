package opencodebridge

import (
	"testing"

	"github.com/beeper/ai-bridge/bridges/opencode/opencode"
)

func TestBackfillTotalTokensIncludesPartCacheTokens(t *testing.T) {
	msg := opencode.MessageWithParts{
		Parts: []opencode.Part{{
			Type: "step-finish",
			Tokens: &opencode.TokenUsage{
				Input:  5,
				Output: 7,
				Cache: &opencode.TokenCache{
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
