package commandregistry

import (
	"cmp"
	"slices"
	"sync"

	"maunium.net/go/mautrix/bridgev2/commands"
	"maunium.net/go/mautrix/event"
)

// Definition describes a chat command in a tool-like schema.
type Definition struct {
	Name        string
	Description string
	Args        string
	Section     commands.HelpSection

	RequiresPortal bool
	RequiresLogin  bool
	HasLogin       func(*commands.Event) bool
	Handler        func(*commands.Event)
}

// FullHandler returns the maunium command handler for this definition.
func (d Definition) FullHandler() *commands.FullHandler {
	run := d.Handler
	if run == nil {
		return nil
	}
	return &commands.FullHandler{
		Func: func(ce *commands.Event) {
			switch {
			case d.RequiresPortal && ce.Portal == nil:
				if ce.MessageStatus != nil {
					ce.MessageStatus.Status = event.MessageStatusFail
					ce.MessageStatus.ErrorReason = event.MessageStatusUnsupported
					ce.MessageStatus.Message = "That command can only be run in portal rooms."
					ce.MessageStatus.IsCertain = true
				}
				ce.Reply("That command can only be ran in portal rooms.")
			case d.RequiresLogin && (ce.User == nil || (d.HasLogin != nil && !d.HasLogin(ce)) || (d.HasLogin == nil && ce.User.GetDefaultLogin() == nil)):
				if ce.MessageStatus != nil {
					ce.MessageStatus.Status = event.MessageStatusFail
					ce.MessageStatus.ErrorReason = event.MessageStatusNoPermission
					ce.MessageStatus.Message = "That command requires you to be logged in."
					ce.MessageStatus.IsCertain = true
				}
				ce.Reply("That command requires you to be logged in.")
			default:
				run(ce)
			}
		},
		Name: d.Name,
		Help: commands.HelpMeta{
			Section:     d.Section,
			Description: d.Description,
			Args:        d.Args,
		},
		RequiresPortal: false,
		RequiresLogin:  false,
	}
}

// Registry collects command handlers for registration.
type Registry struct {
	mu       sync.RWMutex
	handlers map[string]*commands.FullHandler
}

// NewRegistry creates an empty registry.
func NewRegistry() *Registry {
	return &Registry{
		handlers: make(map[string]*commands.FullHandler),
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
	if handler == nil || handler.Name == "" || handler.Func == nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()

	r.handlers[handler.Name] = handler
}

// Get retrieves a handler by canonical name.
func (r *Registry) Get(name string) *commands.FullHandler {
	r.mu.RLock()
	defer r.mu.RUnlock()
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
