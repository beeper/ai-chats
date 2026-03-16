package providerchain

import (
	"errors"
	"fmt"
)

// RunFirst invokes providers in order until one returns a non-nil response.
func RunFirst[P any, R any](
	order []string,
	get func(name string) (P, bool),
	call func(provider P) (*R, error),
	finalize func(name string, resp *R),
	unavailable error,
) (*R, error) {
	var lastErr error
	for _, name := range order {
		provider, ok := get(name)
		if !ok {
			continue
		}
		resp, err := call(provider)
		if err != nil {
			lastErr = err
			continue
		}
		if resp == nil {
			lastErr = fmt.Errorf("provider %s returned empty response", name)
			continue
		}
		if finalize != nil {
			finalize(name, resp)
		}
		return resp, nil
	}
	if lastErr != nil {
		return nil, lastErr
	}
	if unavailable == nil {
		unavailable = errors.New("no providers available")
	}
	return nil, unavailable
}
