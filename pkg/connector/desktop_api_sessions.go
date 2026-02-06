package connector

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"sort"
	"strings"
	"time"

	beeperdesktopapi "github.com/beeper/desktop-api-go"
	"github.com/beeper/desktop-api-go/option"
	"github.com/beeper/desktop-api-go/shared"
)

const (
	channelDesktopAPI            = "desktop-api"
	desktopDefaultInstance       = "default"
	desktopSessionKeyPrefix      = channelDesktopAPI + ":"
	desktopSessionKeyAliasPrefix = "desktop:"
)

type desktopSessionListOptions struct {
	Limit         int
	ActiveMinutes int
	MessageLimit  int
	AllowedKinds  map[string]struct{}
}

type desktopFocusParams struct {
	ChatID              string
	MessageID           string
	DraftText           string
	DraftAttachmentPath string
}

type desktopMessageBuildOptions struct {
	IsGroup  bool
	Instance string
	BaseURL  string
	Accounts map[string]beeperdesktopapi.Account
}

func normalizeDesktopInstanceName(name string) string {
	trimmed := strings.TrimSpace(name)
	if trimmed == "" {
		return desktopDefaultInstance
	}
	return strings.ToLower(trimmed)
}

func normalizeDesktopSessionKeyWithInstance(instance, chatID string) string {
	trimmedChat := strings.TrimSpace(chatID)
	if trimmedChat == "" {
		return ""
	}
	inst := normalizeDesktopInstanceName(instance)
	return desktopSessionKeyPrefix + inst + ":" + trimmedChat
}

func escapeDesktopPathSegment(value string) string {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return ""
	}
	return url.PathEscape(trimmed)
}

func parseDesktopSessionKey(sessionKey string) (string, string, bool) {
	trimmed := strings.TrimSpace(sessionKey)
	if trimmed == "" {
		return "", "", false
	}
	var raw string
	if strings.HasPrefix(trimmed, desktopSessionKeyPrefix) {
		raw = strings.TrimPrefix(trimmed, desktopSessionKeyPrefix)
	} else if strings.HasPrefix(trimmed, desktopSessionKeyAliasPrefix) {
		raw = strings.TrimPrefix(trimmed, desktopSessionKeyAliasPrefix)
	} else {
		return "", "", false
	}
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", "", false
	}
	parts := strings.SplitN(raw, ":", 2)
	if len(parts) == 1 {
		return desktopDefaultInstance, strings.TrimSpace(parts[0]), strings.TrimSpace(parts[0]) != ""
	}
	instance := normalizeDesktopInstanceName(parts[0])
	chatID := strings.TrimSpace(parts[1])
	if chatID == "" {
		return "", "", false
	}
	return instance, chatID, true
}

func (oc *AIClient) desktopAPIInstances() map[string]DesktopAPIInstance {
	instances := map[string]DesktopAPIInstance{}
	if oc == nil || oc.UserLogin == nil {
		return instances
	}
	meta := loginMetadata(oc.UserLogin)
	if meta == nil || meta.ServiceTokens == nil {
		return instances
	}
	for name, instance := range meta.ServiceTokens.DesktopAPIInstances {
		key := normalizeDesktopInstanceName(name)
		if key == "" {
			continue
		}
		if strings.TrimSpace(instance.Token) == "" && strings.TrimSpace(instance.BaseURL) == "" {
			continue
		}
		instances[key] = instance
	}
	if token := strings.TrimSpace(meta.ServiceTokens.DesktopAPI); token != "" {
		if _, ok := instances[desktopDefaultInstance]; !ok {
			instances[desktopDefaultInstance] = DesktopAPIInstance{Token: token}
		}
	}
	return instances
}

func (oc *AIClient) desktopAPIInstanceConfig(instance string) (DesktopAPIInstance, bool) {
	instances := oc.desktopAPIInstances()
	key := normalizeDesktopInstanceName(instance)
	config, ok := instances[key]
	return config, ok
}

func (oc *AIClient) desktopAPIClient(instance string) (*beeperdesktopapi.Client, error) {
	config, ok := oc.desktopAPIInstanceConfig(instance)
	if !ok || strings.TrimSpace(config.Token) == "" {
		return nil, nil
	}
	options := []option.RequestOption{option.WithAccessToken(strings.TrimSpace(config.Token))}
	if baseURL := strings.TrimSpace(config.BaseURL); baseURL != "" {
		options = append(options, option.WithBaseURL(baseURL))
	}
	client := beeperdesktopapi.NewClient(options...)
	return &client, nil
}

func (oc *AIClient) desktopAPIInstanceNames() []string {
	instances := oc.desktopAPIInstances()
	if len(instances) == 0 {
		return nil
	}
	names := make([]string, 0, len(instances))
	for name := range instances {
		names = append(names, name)
	}
	sort.Strings(names)
	for i, name := range names {
		if name == desktopDefaultInstance {
			if i > 0 {
				names = append([]string{desktopDefaultInstance}, append(names[:i], names[i+1:]...)...)
			}
			break
		}
	}
	return names
}

func (oc *AIClient) listDesktopSessions(ctx context.Context, instance string, opts desktopSessionListOptions, accounts map[string]beeperdesktopapi.Account) ([]sessionListEntry, error) {
	client, err := oc.desktopAPIClient(instance)
	if err != nil {
		return nil, err
	}
	if client == nil {
		return nil, nil
	}

	limit := opts.Limit
	if limit <= 0 {
		limit = 50
	}

	params := beeperdesktopapi.ChatSearchParams{
		IncludeMuted: beeperdesktopapi.Bool(true),
		Type:         beeperdesktopapi.ChatSearchParamsTypeAny,
	}
	if opts.ActiveMinutes > 0 {
		cutoff := time.Now().Add(-time.Duration(opts.ActiveMinutes) * time.Minute)
		params.LastActivityAfter = beeperdesktopapi.Time(cutoff)
	}

	pageLimit := limit
	if pageLimit > 200 {
		pageLimit = 200
	}
	params.Limit = beeperdesktopapi.Int(int64(pageLimit))

	page, err := client.Chats.Search(ctx, params)
	if err != nil {
		return nil, err
	}

	chats := make([]beeperdesktopapi.Chat, 0, limit)
	for page != nil {
		for _, chat := range page.Items {
			chats = append(chats, chat)
			if len(chats) >= limit {
				break
			}
		}
		if len(chats) >= limit {
			break
		}
		page, err = page.GetNextPage()
		if err != nil {
			return nil, err
		}
	}

	entries := make([]sessionListEntry, 0, len(chats))
	baseURL := ""
	if config, ok := oc.desktopAPIInstanceConfig(instance); ok {
		baseURL = strings.TrimSpace(config.BaseURL)
	}
	for _, chat := range chats {
		kind := "other"
		if chat.Type == beeperdesktopapi.ChatTypeGroup {
			kind = "group"
		}
		if len(opts.AllowedKinds) > 0 {
			if _, ok := opts.AllowedKinds[kind]; !ok {
				continue
			}
		}

		updatedAt := int64(0)
		if !chat.LastActivity.IsZero() {
			updatedAt = chat.LastActivity.UnixMilli()
		}

		sessionKey := normalizeDesktopSessionKeyWithInstance(instance, chat.ID)
		entry := map[string]any{
			"key":       sessionKey,
			"kind":      kind,
			"channel":   channelDesktopAPI,
			"sessionId": sessionKey,
			"chatId":    chat.ID,
			"instance":  instance,
			"chat":      chat,
		}
		if baseURL != "" {
			entry["baseUrl"] = baseURL
		}
		if title := strings.TrimSpace(chat.Title); title != "" {
			entry["label"] = title
			entry["displayName"] = title
		}
		if updatedAt > 0 {
			entry["updatedAt"] = updatedAt
		}
		if accountID := strings.TrimSpace(chat.AccountID); accountID != "" {
			entry["accountId"] = accountID
			if account, ok := accounts[accountID]; ok {
				entry["account"] = account
				if network := strings.TrimSpace(account.Network); network != "" {
					entry["network"] = network
				}
				entry["accountUser"] = account.User
			}
		}
		if chat.Type != "" {
			entry["chatType"] = string(chat.Type)
		}

		if opts.MessageLimit > 0 {
			messages, msgErr := oc.listDesktopMessages(ctx, client, chat.ID, opts.MessageLimit)
			if msgErr == nil && len(messages) > 0 {
				entry["messages"] = buildDesktopSessionMessages(messages, desktopMessageBuildOptions{
					IsGroup:  chat.Type == beeperdesktopapi.ChatTypeGroup,
					Instance: instance,
					BaseURL:  baseURL,
					Accounts: accounts,
				})
			}
		}

		entries = append(entries, sessionListEntry{updatedAt: updatedAt, data: entry})
	}

	return entries, nil
}

func (oc *AIClient) listDesktopMessages(ctx context.Context, client *beeperdesktopapi.Client, chatID string, limit int) ([]shared.Message, error) {
	if client == nil {
		return nil, errors.New("desktop API client not available")
	}
	trimmed := strings.TrimSpace(chatID)
	if trimmed == "" {
		return nil, errors.New("chat ID is required")
	}
	if limit <= 0 {
		return nil, nil
	}

	page, err := client.Messages.List(ctx, escapeDesktopPathSegment(trimmed), beeperdesktopapi.MessageListParams{})
	if err != nil || page == nil {
		return nil, err
	}
	items := page.Items
	if len(items) == 0 {
		return nil, nil
	}

	sort.Slice(items, func(i, j int) bool {
		return items[i].Timestamp.Before(items[j].Timestamp)
	})
	if len(items) > limit {
		items = items[len(items)-limit:]
	}
	return items, nil
}

func buildDesktopSessionMessages(messages []shared.Message, opts desktopMessageBuildOptions) []map[string]any {
	result := make([]map[string]any, 0, len(messages))
	for _, msg := range messages {
		content := strings.TrimSpace(msg.Text)
		if content == "" {
			if len(msg.Attachments) == 0 {
				continue
			}
			content = fmt.Sprintf("[attachment: %s]", strings.ToLower(string(msg.Attachments[0].Type)))
		}
		if opts.IsGroup && !msg.IsSender && msg.SenderName != "" {
			content = msg.SenderName + ": " + content
		}
		role := "user"
		if msg.IsSender {
			role = "assistant"
		}
		entry := map[string]any{
			"role":         role,
			"content":      content,
			"text":         msg.Text,
			"timestamp":    msg.Timestamp.UnixMilli(),
			"timestampUtc": msg.Timestamp.UTC().Format(time.RFC3339Nano),
			"chatId":       msg.ChatID,
			"accountId":    msg.AccountID,
			"senderId":     msg.SenderID,
			"senderName":   msg.SenderName,
			"isSender":     msg.IsSender,
			"isUnread":     msg.IsUnread,
			"messageType":  msg.Type,
			"sortKey":      msg.SortKey,
		}
		if msg.ID != "" {
			entry["id"] = msg.ID
		}
		if msg.LinkedMessageID != "" {
			entry["linkedMessageId"] = msg.LinkedMessageID
		}
		if len(msg.Attachments) > 0 {
			entry["attachments"] = msg.Attachments
		}
		if len(msg.Reactions) > 0 {
			entry["reactions"] = msg.Reactions
		}
		if opts.Instance != "" {
			entry["instance"] = opts.Instance
		}
		if opts.BaseURL != "" {
			entry["baseUrl"] = opts.BaseURL
		}
		if msg.AccountID != "" && len(opts.Accounts) > 0 {
			if account, ok := opts.Accounts[msg.AccountID]; ok {
				entry["account"] = account
				if network := strings.TrimSpace(account.Network); network != "" {
					entry["network"] = network
				}
				entry["accountUser"] = account.User
			}
		}
		result = append(result, entry)
	}
	return result
}

func (oc *AIClient) listDesktopAccounts(ctx context.Context, instance string) (map[string]beeperdesktopapi.Account, error) {
	client, err := oc.desktopAPIClient(instance)
	if err != nil {
		return nil, err
	}
	if client == nil {
		return nil, fmt.Errorf("desktop API token is not set")
	}
	accounts, err := client.Accounts.List(ctx)
	if err != nil || accounts == nil {
		return nil, err
	}
	result := make(map[string]beeperdesktopapi.Account, len(*accounts))
	for _, account := range *accounts {
		if account.AccountID == "" {
			continue
		}
		result[account.AccountID] = account
	}
	return result, nil
}

func (oc *AIClient) resolveDesktopSessionByLabel(ctx context.Context, instance, label string) (string, string, error) {
	client, err := oc.desktopAPIClient(instance)
	if err != nil {
		return "", "", err
	}
	if client == nil {
		return "", "", fmt.Errorf("desktop API token is not set")
	}
	trimmed := strings.TrimSpace(label)
	if trimmed == "" {
		return "", "", errors.New("label is required")
	}

	params := beeperdesktopapi.ChatSearchParams{
		Query:        beeperdesktopapi.String(trimmed),
		IncludeMuted: beeperdesktopapi.Bool(true),
		Limit:        beeperdesktopapi.Int(10),
		Type:         beeperdesktopapi.ChatSearchParamsTypeAny,
	}
	page, err := client.Chats.Search(ctx, params)
	if err != nil || page == nil {
		return "", "", err
	}
	lower := strings.ToLower(trimmed)
	for _, chat := range page.Items {
		title := strings.ToLower(strings.TrimSpace(chat.Title))
		if title == lower {
			key := normalizeDesktopSessionKeyWithInstance(instance, chat.ID)
			return chat.ID, key, nil
		}
	}
	if len(page.Items) > 0 {
		chat := page.Items[0]
		key := normalizeDesktopSessionKeyWithInstance(instance, chat.ID)
		return chat.ID, key, nil
	}
	return "", "", fmt.Errorf("no session found for label '%s'", trimmed)
}

func (oc *AIClient) resolveDesktopSessionByLabelAnyInstance(ctx context.Context, label string) (string, string, string, error) {
	instances := oc.desktopAPIInstanceNames()
	if len(instances) == 0 {
		return "", "", "", fmt.Errorf("desktop API token is not set")
	}
	var lastErr error
	for _, instance := range instances {
		chatID, key, err := oc.resolveDesktopSessionByLabel(ctx, instance, label)
		if err == nil {
			return instance, chatID, key, nil
		}
		lastErr = err
	}
	if lastErr != nil {
		return "", "", "", lastErr
	}
	return "", "", "", fmt.Errorf("no session found for label '%s'", strings.TrimSpace(label))
}

func (oc *AIClient) sendDesktopMessage(ctx context.Context, instance, chatID string, message string) (string, error) {
	client, err := oc.desktopAPIClient(instance)
	if err != nil {
		return "", err
	}
	if client == nil {
		return "", fmt.Errorf("desktop API token is not set")
	}
	trimmed := strings.TrimSpace(chatID)
	if trimmed == "" {
		return "", errors.New("chat ID is required")
	}
	body := strings.TrimSpace(message)
	if body == "" {
		return "", errors.New("message is required")
	}
	resp, err := client.Messages.Send(ctx, escapeDesktopPathSegment(trimmed), beeperdesktopapi.MessageSendParams{Text: beeperdesktopapi.String(body)})
	if err != nil {
		return "", err
	}
	if resp == nil {
		return "", nil
	}
	return strings.TrimSpace(resp.PendingMessageID), nil
}

func (oc *AIClient) focusDesktop(ctx context.Context, instance string, params desktopFocusParams) (*beeperdesktopapi.FocusResponse, error) {
	client, err := oc.desktopAPIClient(instance)
	if err != nil {
		return nil, err
	}
	if client == nil {
		return nil, fmt.Errorf("desktop API token is not set")
	}

	body := beeperdesktopapi.FocusParams{}
	if chatID := strings.TrimSpace(params.ChatID); chatID != "" {
		body.ChatID = beeperdesktopapi.String(chatID)
	}
	if messageID := strings.TrimSpace(params.MessageID); messageID != "" {
		body.MessageID = beeperdesktopapi.String(messageID)
	}
	if draftText := strings.TrimSpace(params.DraftText); draftText != "" {
		body.DraftText = beeperdesktopapi.String(draftText)
	}
	if attachmentPath := strings.TrimSpace(params.DraftAttachmentPath); attachmentPath != "" {
		body.DraftAttachmentPath = beeperdesktopapi.String(attachmentPath)
	}

	return client.Focus(ctx, body)
}
