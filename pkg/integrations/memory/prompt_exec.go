package memory

import (
	"context"
	"strings"
)

type PromptContextDeps struct {
	ShouldInjectContext   func(portal any, meta any) bool
	ShouldBootstrap       func(portal any, meta any) bool
	ResolveBootstrapPaths func(portal any, meta any) []string
	MarkBootstrapped      func(ctx context.Context, portal any, meta any)
	ReadSection           func(ctx context.Context, meta any, path string) string
}

func BuildPromptContextText(
	ctx context.Context,
	portal any,
	meta any,
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
