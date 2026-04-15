package ai

import (
	"context"
	"strings"
)

func notifyTextFSFileChanges(ctx context.Context, paths ...string) {
	seen := make(map[string]struct{}, len(paths))
	for _, path := range paths {
		path = strings.TrimSpace(path)
		if path == "" {
			continue
		}
		if _, ok := seen[path]; ok {
			continue
		}
		seen[path] = struct{}{}
		notifyIntegrationFileChanged(ctx, path)
		maybeRefreshAgentIdentity(ctx, path)
	}
}
