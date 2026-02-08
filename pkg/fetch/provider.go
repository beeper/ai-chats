package fetch

import (
	"context"

	"github.com/beeper/ai-bridge/pkg/shared/registry"
)

// Provider fetches readable content for a given backend.
type Provider interface {
	Name() string
	Fetch(ctx context.Context, req Request) (*Response, error)
}

// Registry is an alias for a generic registry of fetch providers.
type Registry = registry.Registry[Provider]

// NewRegistry creates an empty registry.
func NewRegistry() *Registry {
	return registry.New[Provider]()
}
