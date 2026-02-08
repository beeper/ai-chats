package registry

// Named is a constraint for providers that can identify themselves by name.
type Named interface {
	Name() string
}

// Registry is a generic store for named providers.
// It is not safe for concurrent use; callers that need
// concurrency should add their own synchronisation.
type Registry[T Named] struct {
	providers map[string]T
}

// New creates an empty Registry.
func New[T Named]() *Registry[T] {
	return &Registry[T]{providers: make(map[string]T)}
}

// Register adds or replaces a provider, keyed by its Name().
func (r *Registry[T]) Register(provider T) {
	if r.providers == nil {
		r.providers = make(map[string]T)
	}
	r.providers[provider.Name()] = provider
}

// Get returns the provider for the given name, or the zero value of T
// and false when no provider is registered under that name.
func (r *Registry[T]) Get(name string) (T, bool) {
	p, ok := r.providers[name]
	return p, ok
}
