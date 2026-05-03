package ai

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"slices"
	"strings"
	"time"
)

const (
	gravatarAPIBaseURL    = "https://api.gravatar.com/v3"
	gravatarAvatarBaseURL = "https://0.gravatar.com/avatar"
)

func normalizeGravatarEmail(email string) (string, error) {
	normalized := strings.TrimSpace(strings.ToLower(email))
	if normalized == "" {
		return "", errors.New("email is required")
	}
	if !strings.Contains(normalized, "@") {
		return "", errors.New("invalid email address")
	}
	return normalized, nil
}

func gravatarHash(email string) string {
	hash := sha256.Sum256([]byte(email))
	return hex.EncodeToString(hash[:])
}

func formatGravatarMarkdown(profile *GravatarProfile, status string) string {
	if profile == nil {
		return ""
	}
	lines := []string{"User identity supplement (Gravatar):"}
	if status != "" {
		lines = append(lines, fmt.Sprintf("gravatar.status: %s", status))
	}
	if profile.Email != "" {
		lines = append(lines, fmt.Sprintf("gravatar.email: %s", profile.Email))
	}
	if profile.Hash != "" {
		lines = append(lines, fmt.Sprintf("gravatar.hash: %s", profile.Hash))
	}
	if profile.FetchedAt > 0 {
		lines = append(lines, fmt.Sprintf("gravatar.fetched_at: %s", time.Unix(profile.FetchedAt, 0).UTC().Format(time.RFC3339)))
	}
	var flattened []string
	flattenGravatarValue(profile.Profile, "gravatar.profile", &flattened)
	lines = append(lines, flattened...)
	return strings.Join(lines, "\n")
}

func flattenGravatarValue(value any, prefix string, out *[]string) {
	switch v := value.(type) {
	case map[string]any:
		if len(v) == 0 {
			return
		}
		keys := slices.Sorted(maps.Keys(v))
		for _, key := range keys {
			child := v[key]
			if isGravatarEmpty(child) {
				continue
			}
			nextPrefix := key
			if prefix != "" {
				nextPrefix = prefix + "." + key
			}
			flattenGravatarValue(child, nextPrefix, out)
		}
	case []any:
		if len(v) == 0 {
			return
		}
		for i, child := range v {
			if isGravatarEmpty(child) {
				continue
			}
			nextPrefix := fmt.Sprintf("%s[%d]", prefix, i)
			flattenGravatarValue(child, nextPrefix, out)
		}
	default:
		if isGravatarEmpty(v) {
			return
		}
		label := prefix
		if label == "" {
			label = "value"
		}
		*out = append(*out, fmt.Sprintf("%s: %s", label, formatGravatarScalar(v)))
	}
}

func isGravatarEmpty(value any) bool {
	if value == nil {
		return true
	}
	switch v := value.(type) {
	case string:
		return strings.TrimSpace(v) == ""
	case []any:
		return len(v) == 0
	case map[string]any:
		return len(v) == 0
	default:
		return false
	}
}

func formatGravatarScalar(value any) string {
	switch v := value.(type) {
	case string:
		return v
	case json.Number:
		return v.String()
	default:
		return fmt.Sprint(v)
	}
}

func (oc *AIClient) gravatarContext() string {
	loginConfig := oc.loginConfigSnapshot(context.Background())
	if loginConfig == nil || loginConfig.Gravatar == nil || loginConfig.Gravatar.Primary == nil {
		return ""
	}
	return formatGravatarMarkdown(loginConfig.Gravatar.Primary, "primary")
}
