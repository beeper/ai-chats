package commandregistry

import (
	"cmp"
	"slices"
	"sync"

	"maunium.net/go/mautrix/bridgev2/commands"
)

// Definition describes a chat command in a tool-like schema.
type Definition struct {
	Name        string
	Description string
	Args        string
	Aliases     []string
	Section     commands.HelpSection

	RequiresPortal bool
	RequiresLogin  bool
	Handler        func(*commands.Event)
}

// FullHandler returns the maunium command handler for this definition.
func (d Definition) FullHandler() *commands.FullHandler {
	return &commands.FullHandler{
		Func:    d.Handler,
		Name:    d.Name,
		Aliases: d.Aliases,
		Help: commands.HelpMeta{
			Section:     d.Section,
			Description: d.Description,
			Args:        d.Args,
		},
		RequiresPortal: d.RequiresPortal,
		RequiresLogin:  d.RequiresLogin,
	}
}

// Registry collects command handlers for registration.
type Registry struct {
	mu       sync.RWMutex
	handlers map[string]*commands.FullHandler
	aliases  map[string]string
}

// NewRegistry creates an empty registry.
func NewRegistry() *Registry {
	return &Registry{
		handlers: make(map[string]*commands.FullHandler),
		aliases:  make(map[string]string),
	}
}

// Register adds a command definition to the registry and returns its handler.
func (r *Registry) Register(def Definition) *commands.FullHandler {
	handler := def.FullHandler()
	r.RegisterHandler(handler)
	return handler
}

// RegisterHandler adds an already-constructed handler to the registry.
func (r *Registry) RegisterHandler(handler *commands.FullHandler) {
	if handler == nil || handler.Name == "" {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()

	r.handlers[handler.Name] = handler
	for _, alias := range handler.Aliases {
		r.aliases[alias] = handler.Name
	}
}

// Get retrieves a handler by name or alias.
func (r *Registry) Get(name string) *commands.FullHandler {
	r.mu.RLock()
	defer r.mu.RUnlock()

	if canonical, ok := r.aliases[name]; ok {
		name = canonical
	}
	return r.handlers[name]
}

// All returns all handlers sorted by name.
func (r *Registry) All() []*commands.FullHandler {
	r.mu.RLock()
	defer r.mu.RUnlock()

	handlers := make([]*commands.FullHandler, 0, len(r.handlers))
	for _, handler := range r.handlers {
		handlers = append(handlers, handler)
	}

	slices.SortFunc(handlers, func(a, b *commands.FullHandler) int {
		return cmp.Compare(a.Name, b.Name)
	})
	return handlers
}

// Names returns all registered handler names.
func (r *Registry) Names() []string {
	r.mu.RLock()
	defer r.mu.RUnlock()

	names := make([]string, 0, len(r.handlers))
	for name := range r.handlers {
		names = append(names, name)
	}
	slices.Sort(names)
	return names
}
