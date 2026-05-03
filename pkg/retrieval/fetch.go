package retrieval

import (
	"context"
	"errors"
	"strings"

	"github.com/beeper/agentremote/pkg/shared/stringutil"
)

// Fetch executes a fetch using the configured provider chain.
func Fetch(ctx context.Context, req FetchRequest, cfg *FetchConfig) (*FetchResponse, error) {
	if strings.TrimSpace(req.URL) == "" {
		return nil, errors.New("missing url")
	}
	cfg = cfg.WithDefaults()
	if req.ExtractMode == "" {
		req.ExtractMode = "markdown"
	}
	if req.MaxChars < 0 {
		req.MaxChars = 0
	}

	var provider FetchProvider
	providerName := cfg.Provider
	switch providerName {
	case ProviderExa:
		if stringutil.BoolPtrOr(cfg.Exa.Enabled, true) && strings.TrimSpace(cfg.Exa.APIKey) != "" {
			provider = &exaFetchProvider{cfg: cfg.Exa}
		}
	case ProviderDirect:
		if stringutil.BoolPtrOr(cfg.Direct.Enabled, true) {
			provider = &directFetchProvider{cfg: cfg.Direct}
		}
	}
	if provider == nil {
		return nil, errors.New("no fetch providers available")
	}
	resp, err := provider.Fetch(ctx, req)
	if err != nil {
		return nil, err
	}
	if resp.Provider == "" {
		resp.Provider = providerName
	}
	return resp, nil
}
