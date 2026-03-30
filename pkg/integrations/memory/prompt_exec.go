package memory

import (
	"context"
	"strings"

	"maunium.net/go/mautrix/bridgev2"

	iruntime "github.com/beeper/agentremote/pkg/integrations/runtime"
)

type PromptContextDeps struct {
	ShouldInjectContext   func(portal *bridgev2.Portal, meta iruntime.Meta) bool
	ShouldBootstrap       func(portal *bridgev2.Portal, meta iruntime.Meta) bool
	ResolveBootstrapPaths func(portal *bridgev2.Portal, meta iruntime.Meta) []string
	MarkBootstrapped      func(ctx context.Context, portal *bridgev2.Portal, meta iruntime.Meta)
	ReadSection           func(ctx context.Context, meta iruntime.Meta, path string) string
}

func BuildPromptContextText(
	ctx context.Context,
	portal *bridgev2.Portal,
	meta iruntime.Meta,
	deps PromptContextDeps,
) string {
	if deps.ShouldInjectContext == nil || !deps.ShouldInjectContext(portal, meta) {
		return ""
	}
	if deps.ReadSection == nil {
		return ""
	}

	sections := make([]string, 0, 3)
	if section := deps.ReadSection(ctx, meta, "MEMORY.md"); section != "" {
		sections = append(sections, section)
	} else if section := deps.ReadSection(ctx, meta, "memory.md"); section != "" {
		sections = append(sections, section)
	}

	if deps.ShouldBootstrap != nil && deps.ShouldBootstrap(portal, meta) {
		if deps.ResolveBootstrapPaths != nil {
			for _, path := range deps.ResolveBootstrapPaths(portal, meta) {
				if section := deps.ReadSection(ctx, meta, path); section != "" {
					sections = append(sections, section)
				}
			}
		}
		if deps.MarkBootstrapped != nil {
			deps.MarkBootstrapped(ctx, portal, meta)
		}
	}

	if len(sections) == 0 {
		return ""
	}
	return strings.Join(sections, "\n\n")
}
