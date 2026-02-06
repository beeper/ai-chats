package connector

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/beeper/ai-bridge/pkg/shared/toolspec"
)

const defaultNexusTimeoutSeconds = 30

var nexusToolRoutes = map[string]string{
	toolspec.NexusSearchContactsName:       "/search",
	toolspec.NexusGetContactName:           "/get-contact",
	toolspec.NexusCreateContactName:        "/create-contact",
	toolspec.NexusUpdateContactName:        "/update-contact",
	toolspec.NexusArchiveContactName:       "/archive-contact",
	toolspec.NexusRestoreContactName:       "/restore-contact",
	toolspec.NexusCreateNoteName:           "/note",
	toolspec.NexusGetGroupsName:            "/get-groups",
	toolspec.NexusCreateGroupName:          "/create-group",
	toolspec.NexusUpdateGroupName:          "/update-group",
	toolspec.NexusGetNotesName:             "/moments/notes",
	toolspec.NexusGetEventsName:            "/moments/events",
	toolspec.NexusGetUpcomingEventsName:    "/moments/events/upcoming",
	toolspec.NexusGetEmailsName:            "/moments/emails",
	toolspec.NexusGetRecentEmailsName:      "/moments/emails/recent",
	toolspec.NexusGetRecentRemindersName:   "/moments/reminders/recent",
	toolspec.NexusGetUpcomingRemindersName: "/moments/reminders/upcoming",
	toolspec.NexusFindDuplicatesName:       "/find-duplicates",
	toolspec.NexusMergeContactsName:        "/merge-contacts",
}

func isNexusToolName(name string) bool {
	_, ok := nexusToolRoutes[strings.TrimSpace(name)]
	return ok
}

func makeNexusExecutor(route string) toolExecutor {
	return func(ctx context.Context, args map[string]any) (string, error) {
		return executeNexusRoute(ctx, route, args)
	}
}

func (oc *AIClient) isNexusConfigured() bool {
	if oc == nil || oc.connector == nil {
		return false
	}
	return nexusConfigured(oc.connector.Config.Tools.Nexus)
}

func nexusConfigured(cfg *NexusToolsConfig) bool {
	if cfg == nil {
		return false
	}
	if cfg.Enabled != nil && !*cfg.Enabled {
		return false
	}
	if strings.TrimSpace(cfg.BaseURL) == "" && strings.TrimSpace(cfg.MCPEndpoint) == "" {
		return false
	}
	return strings.TrimSpace(cfg.Token) != ""
}

func executeNexusRoute(ctx context.Context, route string, args map[string]any) (string, error) {
	btc := GetBridgeToolContext(ctx)
	if btc == nil || btc.Client == nil || btc.Client.connector == nil {
		return "", fmt.Errorf("nexus tool requires bridge context")
	}
	cfg := btc.Client.connector.Config.Tools.Nexus
	if !nexusConfigured(cfg) {
		return "", fmt.Errorf("nexus tools are not configured (set network.tools.nexus.base_url or mcp_endpoint and token)")
	}

	baseURL := strings.TrimRight(strings.TrimSpace(cfg.BaseURL), "/")
	if baseURL == "" {
		mcpEndpoint := strings.TrimRight(strings.TrimSpace(cfg.MCPEndpoint), "/")
		baseURL = strings.TrimSuffix(mcpEndpoint, nexusMCPDefaultPath)
		baseURL = strings.TrimRight(baseURL, "/")
	}
	if baseURL == "" {
		return "", fmt.Errorf("nexus tools require network.tools.nexus.base_url")
	}
	endpoint := baseURL + "/tools/v2" + route

	payload, err := json.Marshal(args)
	if err != nil {
		return "", fmt.Errorf("failed to encode nexus payload: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(payload))
	if err != nil {
		return "", fmt.Errorf("failed to build nexus request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	headerValue, err := nexusAuthorizationHeaderValue(cfg.AuthType, cfg.Token)
	if err != nil {
		return "", err
	}
	req.Header.Set("Authorization", headerValue)

	timeoutSeconds := cfg.TimeoutSeconds
	if timeoutSeconds <= 0 {
		timeoutSeconds = defaultNexusTimeoutSeconds
	}
	client := &http.Client{Timeout: time.Duration(timeoutSeconds) * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return "", fmt.Errorf("nexus request failed: %w", err)
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)
	trimmed := strings.TrimSpace(string(body))
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		snippet := trimmed
		if len(snippet) > 500 {
			snippet = snippet[:500] + "..."
		}
		if snippet == "" {
			snippet = http.StatusText(resp.StatusCode)
		}
		return "", fmt.Errorf("nexus request failed (%d): %s", resp.StatusCode, snippet)
	}
	if trimmed == "" {
		return "{}", nil
	}
	if json.Valid(body) {
		return trimmed, nil
	}
	wrapped, _ := json.Marshal(map[string]any{"raw": trimmed})
	return string(wrapped), nil
}
