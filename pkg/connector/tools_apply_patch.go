package connector

import (
	"context"
	"errors"
	"strings"

	"github.com/beeper/ai-bridge/pkg/textfs"
)

func executeApplyPatch(ctx context.Context, args map[string]any) (string, error) {
	store, err := textFSStore(ctx)
	if err != nil {
		return "", err
	}
	input, ok := args["input"].(string)
	if !ok || strings.TrimSpace(input) == "" {
		return "", errors.New("missing or invalid 'input' argument")
	}

	patchCtx, cancel := context.WithTimeout(ctx, textFSToolTimeout)
	defer cancel()
	result, err := textfs.ApplyPatch(patchCtx, store, input)
	if err != nil {
		return "", err
	}
	if result != nil {
		paths := make([]string, 0, len(result.Summary.Added)+len(result.Summary.Modified)+len(result.Summary.Deleted))
		paths = append(paths, result.Summary.Added...)
		paths = append(paths, result.Summary.Modified...)
		paths = append(paths, result.Summary.Deleted...)

		go func(paths []string) {
			bg, cancel := context.WithTimeout(detachedBridgeToolContext(ctx), textFSPostWriteTimeout)
			defer cancel()
			for _, path := range paths {
				notifyMemoryFileChanged(bg, path)
				maybeRefreshAgentIdentity(bg, path)
			}
		}(paths)

		if strings.TrimSpace(result.Text) != "" {
			return result.Text, nil
		}
	}
	return "Patch applied.", nil
}
