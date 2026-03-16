package agentremote

import (
	"context"
	"sync"
)

// BaseLoginProcess provides background context management for login flows.
// Embed this in bridge-specific login structs to get free Cancel() and
// BackgroundProcessContext() implementations.
type BaseLoginProcess struct {
	bgMu     sync.Mutex
	bgCtx    context.Context
	bgCancel context.CancelFunc
}

// BackgroundProcessContext returns a long-lived context for background operations.
// The context is lazily initialized on first call and reused for subsequent calls.
func (p *BaseLoginProcess) BackgroundProcessContext() context.Context {
	p.bgMu.Lock()
	defer p.bgMu.Unlock()
	if p.bgCtx == nil || p.bgCancel == nil {
		p.bgCtx, p.bgCancel = context.WithCancel(context.Background())
	}
	return p.bgCtx
}

// Cancel cancels the background context and clears all references.
func (p *BaseLoginProcess) Cancel() {
	p.bgMu.Lock()
	cancel := p.bgCancel
	p.bgCancel = nil
	p.bgCtx = nil
	p.bgMu.Unlock()
	if cancel != nil {
		cancel()
	}
}
