package connector

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
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

func ensureGravatarState(meta *UserLoginMetadata) *GravatarState {
	if meta.Gravatar == nil {
		meta.Gravatar = &GravatarState{}
	}
	return meta.Gravatar
}

func fetchGravatarProfile(ctx context.Context, email string) (*GravatarProfile, error) {
	normalized, err := normalizeGravatarEmail(email)
	if err != nil {
		return nil, err
	}
	hash := gravatarHash(normalized)

	reqURL := fmt.Sprintf("%s/profiles/%s", gravatarAPIBaseURL, hash)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, reqURL, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}
	req.Header.Set("Accept", "application/json")

	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch Gravatar profile: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusNotFound {
		return nil, fmt.Errorf("gravatar profile not found for %s", normalized)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("gravatar profile request failed: status %d", resp.StatusCode)
	}

	var profile map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&profile); err != nil {
		return nil, fmt.Errorf("failed to decode Gravatar profile: %w", err)
	}

	if _, ok := profile["hash"]; !ok {
		profile["hash"] = hash
	}
	if _, ok := profile["avatar_url"]; !ok {
		profile["avatar_url"] = fmt.Sprintf("%s/%s", gravatarAvatarBaseURL, hash)
	}

	return &GravatarProfile{
		Email:     normalized,
		Hash:      hash,
		Profile:   profile,
		FetchedAt: time.Now().Unix(),
	}, nil
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
		keys := make([]string, 0, len(v))
		for key := range v {
			keys = append(keys, key)
		}
		slices.Sort(keys)
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
	loginMeta := loginMetadata(oc.UserLogin)
	if loginMeta == nil || loginMeta.Gravatar == nil || loginMeta.Gravatar.Primary == nil {
		return ""
	}
	return formatGravatarMarkdown(loginMeta.Gravatar.Primary, "primary")
}
