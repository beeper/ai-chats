package search

import (
	"context"

	"github.com/beeper/ai-bridge/pkg/shared/registry"
)

// Provider performs web searches for a given backend.
type Provider interface {
	Name() string
	Search(ctx context.Context, req Request) (*Response, error)
}

// Registry is an alias for a generic registry of search providers.
type Registry = registry.Registry[Provider]

// NewRegistry creates an empty registry.
func NewRegistry() *Registry {
	return registry.New[Provider]()
}
