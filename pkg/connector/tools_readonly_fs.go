package connector

import (
	"context"
	"fmt"
	"path"
	"regexp"
	"strings"
)

func executeLS(ctx context.Context, args map[string]any) (string, error) {
	store, err := textFSStore(ctx)
	if err != nil {
		return "", err
	}
	opCtx, cancel := context.WithTimeout(ctx, textFSToolTimeout)
	defer cancel()
	dir := ""
	if raw, ok := args["path"]; ok {
		if s, ok := raw.(string); ok {
			dir = strings.TrimSpace(s)
		}
	}
	entries, err := store.ListWithPrefix(opCtx, dir)
	if err != nil {
		return "", err
	}
	names, hasDir := store.DirEntries(entries, dir)
	if len(names) == 0 && hasDir {
		return "(empty)", nil
	}
	if len(names) == 0 && dir != "" {
		// Check if it's a file
		if _, found, _ := store.Read(opCtx, dir); found {
			return dir, nil
		}
		return "", fmt.Errorf("path not found: %s", dir)
	}
	if len(names) == 0 {
		return "(empty)", nil
	}
	return strings.Join(names, "\n"), nil
}

func executeFind(ctx context.Context, args map[string]any) (string, error) {
	store, err := textFSStore(ctx)
	if err != nil {
		return "", err
	}
	opCtx, cancel := context.WithTimeout(ctx, textFSToolTimeout)
	defer cancel()
	patternRaw, ok := args["pattern"].(string)
	if !ok || strings.TrimSpace(patternRaw) == "" {
		return "", fmt.Errorf("missing or invalid 'pattern' argument")
	}
	pattern := strings.TrimSpace(patternRaw)
	base := ""
	if raw, ok := args["path"]; ok {
		if s, ok := raw.(string); ok {
			base = strings.Trim(strings.TrimSpace(s), "/")
		}
	}
	entries, err := store.ListWithPrefix(opCtx, base)
	if err != nil {
		return "", err
	}
	matches := []string{}
	for _, entry := range entries {
		rel := entry.Path
		if base != "" {
			if !strings.HasPrefix(rel, base+"/") && rel != base {
				continue
			}
			rel = strings.TrimPrefix(rel, base+"/")
		}
		ok, err := path.Match(pattern, rel)
		if err != nil {
			return "", fmt.Errorf("invalid pattern: %w", err)
		}
		if ok {
			if base != "" {
				matches = append(matches, path.Join(base, rel))
			} else {
				matches = append(matches, rel)
			}
		}
	}
	if len(matches) == 0 {
		return "No matches.", nil
	}
	return strings.Join(matches, "\n"), nil
}

func executeGrep(ctx context.Context, args map[string]any) (string, error) {
	store, err := textFSStore(ctx)
	if err != nil {
		return "", err
	}
	opCtx, cancel := context.WithTimeout(ctx, textFSToolTimeout)
	defer cancel()
	patternRaw, ok := args["pattern"].(string)
	if !ok || strings.TrimSpace(patternRaw) == "" {
		return "", fmt.Errorf("missing or invalid 'pattern' argument")
	}
	pattern := patternRaw
	pathArg := ""
	if raw, ok := args["path"]; ok {
		if s, ok := raw.(string); ok {
			pathArg = strings.TrimSpace(s)
		}
	}
	useRegex := false
	if raw, ok := args["regex"]; ok {
		if v, ok := raw.(bool); ok {
			useRegex = v
		}
	}
	caseSensitive := false
	if raw, ok := args["caseSensitive"]; ok {
		if v, ok := raw.(bool); ok {
			caseSensitive = v
		}
	}
	limit := 20
	if raw, ok := args["limit"]; ok {
		if v, ok := raw.(float64); ok && v > 0 {
			limit = int(v)
		}
	}

	var re *regexp.Regexp
	if useRegex {
		flags := ""
		if !caseSensitive {
			flags = "(?i)"
		}
		var err error
		re, err = regexp.Compile(flags + pattern)
		if err != nil {
			return "", fmt.Errorf("invalid regex: %w", err)
		}
	}

	entries, err := store.ListWithPrefix(opCtx, pathArg)
	if err != nil {
		return "", err
	}

	matches := []string{}
	for _, entry := range entries {
		lines := strings.Split(entry.Content, "\n")
		for idx, line := range lines {
			if len(matches) >= limit {
				break
			}
			hit := false
			if useRegex {
				hit = re.MatchString(line)
			} else if caseSensitive {
				hit = strings.Contains(line, pattern)
			} else {
				hit = strings.Contains(strings.ToLower(line), strings.ToLower(pattern))
			}
			if hit {
				matches = append(matches, fmt.Sprintf("%s:%d:%s", entry.Path, idx+1, line))
			}
		}
		if len(matches) >= limit {
			break
		}
	}
	if len(matches) == 0 {
		return "No matches.", nil
	}
	if len(matches) >= limit {
		matches = append(matches, fmt.Sprintf("... truncated at %d matches", limit))
	}
	return strings.Join(matches, "\n"), nil
}
