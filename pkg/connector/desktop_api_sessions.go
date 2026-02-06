package connector

import (
	"context"
	"encoding/base64"
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
	AccountIDs    map[string]struct{}
	Networks      map[string]struct{}
	MultiInstance bool
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

type desktopSendMessageRequest struct {
	Text             string
	ReplyToMessageID string
	Attachment       *desktopSendAttachment
}

type desktopSendAttachment struct {
	UploadID string
	Type     string
}

type desktopLabelResolveOptions struct {
	AccountID string
	Network   string
}

var (
	errDesktopLabelNotFound  = errors.New("desktop label not found")
	errDesktopLabelAmbiguous = errors.New("desktop label ambiguous")
)

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
	for _, chat := range chats {
		accountID := strings.TrimSpace(chat.AccountID)
		if len(opts.AccountIDs) > 0 {
			if _, ok := opts.AccountIDs[accountID]; !ok {
				continue
			}
		}
		account := accounts[accountID]
		if strings.TrimSpace(account.AccountID) == "" && accountID != "" {
			account.AccountID = accountID
		}
		if len(opts.Networks) > 0 {
			if !desktopNetworkFilterMatches(opts.Networks, account.Network) {
				continue
			}
		}
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
		channelName := desktopSessionChannelForNetwork(account.Network)
		entry := map[string]any{
			"key":       sessionKey,
			"kind":      kind,
			"channel":   channelName,
			"sessionId": sessionKey,
		}
		if title := strings.TrimSpace(chat.Title); title != "" {
			entry["label"] = title
			entry["displayName"] = title
		}
		if updatedAt > 0 {
			entry["updatedAt"] = updatedAt
		}
		if desktopAccountID := desktopSessionAccountID(opts.MultiInstance, instance, account); desktopAccountID != "" {
			entry["accountId"] = desktopAccountID
		}

		if opts.MessageLimit > 0 {
			messages, msgErr := oc.listDesktopMessages(ctx, client, chat.ID, opts.MessageLimit)
			if msgErr == nil && len(messages) > 0 {
				entry["messages"] = buildOpenClawDesktopSessionMessages(messages, desktopMessageBuildOptions{
					IsGroup:  chat.Type == beeperdesktopapi.ChatTypeGroup,
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
	items := make([]shared.Message, 0, limit)
	for page != nil {
		items = append(items, page.Items...)
		if len(items) >= limit {
			break
		}
		page, err = page.GetNextPage()
		if err != nil {
			return nil, err
		}
	}
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

func renderDesktopSessionMessageText(msg shared.Message, isGroup bool) (string, bool) {
	content := strings.TrimSpace(msg.Text)
	if content == "" {
		if len(msg.Attachments) == 0 {
			return "", false
		}
		attachmentType := strings.ToLower(strings.TrimSpace(string(msg.Attachments[0].Type)))
		if attachmentType == "" {
			attachmentType = "unknown"
		}
		content = fmt.Sprintf("[attachment: %s]", attachmentType)
	}
	if isGroup && !msg.IsSender {
		senderName := strings.TrimSpace(msg.SenderName)
		if senderName != "" {
			content = senderName + ": " + content
		}
	}
	return content, true
}

func buildOpenClawDesktopSessionMessages(messages []shared.Message, opts desktopMessageBuildOptions) []map[string]any {
	result := make([]map[string]any, 0, len(messages))
	for _, msg := range messages {
		contentText, ok := renderDesktopSessionMessageText(msg, opts.IsGroup)
		if !ok {
			continue
		}
		role := "user"
		if msg.IsSender {
			role = "assistant"
		}
		entry := map[string]any{
			"role": role,
			"content": []map[string]any{
				{
					"type": "text",
					"text": contentText,
				},
			},
			"timestamp": msg.Timestamp.UnixMilli(),
		}
		if msg.ID != "" {
			entry["id"] = msg.ID
		}
		result = append(result, entry)
	}
	return result
}

func buildDesktopSessionMessages(messages []shared.Message, opts desktopMessageBuildOptions) []map[string]any {
	result := make([]map[string]any, 0, len(messages))
	for _, msg := range messages {
		content, ok := renderDesktopSessionMessageText(msg, opts.IsGroup)
		if !ok {
			continue
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

func (oc *AIClient) resolveDesktopSessionByLabelWithOptions(ctx context.Context, instance, label string, opts desktopLabelResolveOptions) (string, string, error) {
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
		Limit:        beeperdesktopapi.Int(50),
		Type:         beeperdesktopapi.ChatSearchParamsTypeAny,
	}
	page, err := client.Chats.Search(ctx, params)
	if err != nil || page == nil {
		return "", "", err
	}

	chats := make([]beeperdesktopapi.Chat, 0, 100)
	for page != nil {
		chats = append(chats, page.Items...)
		if len(chats) >= 100 {
			break
		}
		page, err = page.GetNextPage()
		if err != nil {
			return "", "", err
		}
	}

	accounts := map[string]beeperdesktopapi.Account{}
	if accountMap, accountErr := oc.listDesktopAccounts(ctx, instance); accountErr == nil && accountMap != nil {
		accounts = accountMap
	}
	exactMatches, partialMatches := matchDesktopChatsByLabel(chats, trimmed, accounts)
	exactMatches = filterDesktopChatsByResolveOptions(exactMatches, accounts, opts)
	partialMatches = filterDesktopChatsByResolveOptions(partialMatches, accounts, opts)
	if len(exactMatches) == 1 {
		key := normalizeDesktopSessionKeyWithInstance(instance, exactMatches[0].ID)
		return exactMatches[0].ID, key, nil
	}
	if len(exactMatches) > 1 {
		titles := make([]string, 0, len(exactMatches))
		for i, chat := range exactMatches {
			if i >= 5 {
				break
			}
			titles = append(titles, describeDesktopChatForLabel(chat, accounts[strings.TrimSpace(chat.AccountID)]))
		}
		return "", "", fmt.Errorf("%w: label '%s' matched multiple chats (%s)", errDesktopLabelAmbiguous, trimmed, strings.Join(titles, ", "))
	}
	if len(partialMatches) > 0 {
		suggestions := make([]string, 0, len(partialMatches))
		for i, chat := range partialMatches {
			if i >= 5 {
				break
			}
			suggestions = append(suggestions, describeDesktopChatForLabel(chat, accounts[strings.TrimSpace(chat.AccountID)]))
		}
		return "", "", fmt.Errorf("%w: no exact session found for label '%s'. Top matches: %s. Use sessionKey from sessions_list for deterministic targeting.", errDesktopLabelNotFound, trimmed, strings.Join(suggestions, ", "))
	}
	if strings.TrimSpace(opts.AccountID) != "" || strings.TrimSpace(opts.Network) != "" {
		filterParts := []string{}
		if v := strings.TrimSpace(opts.AccountID); v != "" {
			filterParts = append(filterParts, "accountId="+v)
		}
		if v := strings.TrimSpace(opts.Network); v != "" {
			filterParts = append(filterParts, "network="+v)
		}
		return "", "", fmt.Errorf("%w: no session found for label '%s' with filters (%s). Use sessionKey from sessions_list.", errDesktopLabelNotFound, trimmed, strings.Join(filterParts, ", "))
	}
	return "", "", fmt.Errorf("%w: no session found for label '%s'. Use sessionKey from sessions_list.", errDesktopLabelNotFound, trimmed)
}

func (oc *AIClient) resolveDesktopSessionByLabel(ctx context.Context, instance, label string) (string, string, error) {
	return oc.resolveDesktopSessionByLabelWithOptions(ctx, instance, label, desktopLabelResolveOptions{})
}

func (oc *AIClient) resolveDesktopSessionByLabelAnyInstanceWithOptions(ctx context.Context, label string, opts desktopLabelResolveOptions) (string, string, string, error) {
	instances := oc.desktopAPIInstanceNames()
	if len(instances) == 0 {
		return "", "", "", fmt.Errorf("desktop API token is not set")
	}
	var (
		lastErr       error
		matched       bool
		matchInstance string
		matchChatID   string
		matchKey      string
	)
	for _, instance := range instances {
		chatID, key, err := oc.resolveDesktopSessionByLabelWithOptions(ctx, instance, label, opts)
		if err == nil {
			if matched {
				return "", "", "", fmt.Errorf("%w: label '%s' matched chats in multiple instances (%s, %s)", errDesktopLabelAmbiguous, strings.TrimSpace(label), matchInstance, instance)
			}
			matched = true
			matchInstance = instance
			matchChatID = chatID
			matchKey = key
			continue
		}
		if errors.Is(err, errDesktopLabelAmbiguous) {
			return "", "", "", err
		}
		if errors.Is(err, errDesktopLabelNotFound) {
			lastErr = err
			continue
		}
		return "", "", "", err
	}
	if matched {
		return matchInstance, matchChatID, matchKey, nil
	}
	if lastErr != nil {
		return "", "", "", lastErr
	}
	return "", "", "", fmt.Errorf("no session found for label '%s'", strings.TrimSpace(label))
}

func (oc *AIClient) resolveDesktopSessionByLabelAnyInstance(ctx context.Context, label string) (string, string, string, error) {
	return oc.resolveDesktopSessionByLabelAnyInstanceWithOptions(ctx, label, desktopLabelResolveOptions{})
}

func (oc *AIClient) sendDesktopMessage(ctx context.Context, instance, chatID string, req desktopSendMessageRequest) (string, error) {
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

	body := beeperdesktopapi.MessageSendParams{}
	text := strings.TrimSpace(req.Text)
	if text != "" {
		body.Text = beeperdesktopapi.String(text)
	}
	if replyTo := strings.TrimSpace(req.ReplyToMessageID); replyTo != "" {
		body.ReplyToMessageID = beeperdesktopapi.String(replyTo)
	}
	if req.Attachment != nil {
		attachment := beeperdesktopapi.MessageSendParamsAttachment{
			UploadID: strings.TrimSpace(req.Attachment.UploadID),
		}
		if attachment.UploadID == "" {
			return "", errors.New("attachment upload ID is required")
		}
		if kind := strings.TrimSpace(req.Attachment.Type); kind != "" {
			attachment.Type = kind
		}
		body.Attachment = attachment
	}
	if text == "" && req.Attachment == nil {
		return "", errors.New("message text or attachment is required")
	}

	resp, err := client.Messages.Send(ctx, trimmed, body)
	if err != nil {
		return "", err
	}
	if resp == nil {
		return "", nil
	}
	return strings.TrimSpace(resp.PendingMessageID), nil
}

func (oc *AIClient) listDesktopChats(ctx context.Context, instance string, limit int) ([]beeperdesktopapi.ChatListResponse, error) {
	client, err := oc.desktopAPIClient(instance)
	if err != nil {
		return nil, err
	}
	if client == nil {
		return nil, fmt.Errorf("desktop API token is not set")
	}
	if limit <= 0 {
		limit = 50
	}
	page, err := client.Chats.List(ctx, beeperdesktopapi.ChatListParams{})
	if err != nil || page == nil {
		return nil, err
	}
	out := make([]beeperdesktopapi.ChatListResponse, 0, limit)
	for page != nil {
		out = append(out, page.Items...)
		if len(out) >= limit {
			break
		}
		page, err = page.GetNextPage()
		if err != nil {
			return nil, err
		}
	}
	if len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}

func (oc *AIClient) searchDesktopChats(ctx context.Context, instance, query string, limit int) ([]beeperdesktopapi.Chat, error) {
	client, err := oc.desktopAPIClient(instance)
	if err != nil {
		return nil, err
	}
	if client == nil {
		return nil, fmt.Errorf("desktop API token is not set")
	}
	if limit <= 0 {
		limit = 50
	}
	params := beeperdesktopapi.ChatSearchParams{
		Query:        beeperdesktopapi.String(strings.TrimSpace(query)),
		IncludeMuted: beeperdesktopapi.Bool(true),
		Limit:        beeperdesktopapi.Int(int64(min(limit, 200))),
		Type:         beeperdesktopapi.ChatSearchParamsTypeAny,
	}
	page, err := client.Chats.Search(ctx, params)
	if err != nil || page == nil {
		return nil, err
	}
	out := make([]beeperdesktopapi.Chat, 0, limit)
	for page != nil {
		out = append(out, page.Items...)
		if len(out) >= limit {
			break
		}
		page, err = page.GetNextPage()
		if err != nil {
			return nil, err
		}
	}
	if len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}

func (oc *AIClient) searchDesktopMessages(ctx context.Context, instance, query string, limit int, chatID string) ([]shared.Message, error) {
	client, err := oc.desktopAPIClient(instance)
	if err != nil {
		return nil, err
	}
	if client == nil {
		return nil, fmt.Errorf("desktop API token is not set")
	}
	if limit <= 0 {
		limit = 20
	}
	params := beeperdesktopapi.MessageSearchParams{
		Query:        beeperdesktopapi.String(strings.TrimSpace(query)),
		Limit:        beeperdesktopapi.Int(int64(limit)),
		IncludeMuted: beeperdesktopapi.Bool(true),
	}
	if trimmed := strings.TrimSpace(chatID); trimmed != "" {
		params.ChatIDs = []string{trimmed}
	}
	page, err := client.Messages.Search(ctx, params)
	if err != nil || page == nil {
		return nil, err
	}
	out := make([]shared.Message, 0, limit)
	for page != nil {
		out = append(out, page.Items...)
		if len(out) >= limit {
			break
		}
		page, err = page.GetNextPage()
		if err != nil {
			return nil, err
		}
	}
	if len(out) > limit {
		out = out[:limit]
	}
	sort.Slice(out, func(i, j int) bool {
		return out[i].Timestamp.Before(out[j].Timestamp)
	})
	return out, nil
}

func (oc *AIClient) editDesktopMessage(ctx context.Context, instance, chatID, messageID, text string) error {
	client, err := oc.desktopAPIClient(instance)
	if err != nil {
		return err
	}
	if client == nil {
		return fmt.Errorf("desktop API token is not set")
	}
	if strings.TrimSpace(chatID) == "" || strings.TrimSpace(messageID) == "" {
		return fmt.Errorf("chat ID and message ID are required")
	}
	_, err = client.Messages.Update(ctx, strings.TrimSpace(messageID), beeperdesktopapi.MessageUpdateParams{
		ChatID: strings.TrimSpace(chatID),
		Text:   strings.TrimSpace(text),
	})
	return err
}

func (oc *AIClient) createDesktopChat(ctx context.Context, instance, accountID string, participantIDs []string, chatType, title, firstMessage string) (string, error) {
	client, err := oc.desktopAPIClient(instance)
	if err != nil {
		return "", err
	}
	if client == nil {
		return "", fmt.Errorf("desktop API token is not set")
	}
	trimmedAccount := strings.TrimSpace(accountID)
	if trimmedAccount == "" {
		return "", fmt.Errorf("accountId is required")
	}
	cleanParticipants := make([]string, 0, len(participantIDs))
	for _, id := range participantIDs {
		if trimmed := strings.TrimSpace(id); trimmed != "" {
			cleanParticipants = append(cleanParticipants, trimmed)
		}
	}
	if len(cleanParticipants) == 0 {
		return "", fmt.Errorf("participantIds is required")
	}
	kind := strings.ToLower(strings.TrimSpace(chatType))
	if kind == "" {
		if len(cleanParticipants) == 1 {
			kind = string(beeperdesktopapi.ChatNewParamsTypeSingle)
		} else {
			kind = string(beeperdesktopapi.ChatNewParamsTypeGroup)
		}
	}
	params := beeperdesktopapi.ChatNewParams{
		AccountID:      trimmedAccount,
		ParticipantIDs: cleanParticipants,
		Type:           beeperdesktopapi.ChatNewParamsType(kind),
	}
	if msg := strings.TrimSpace(firstMessage); msg != "" {
		params.MessageText = beeperdesktopapi.String(msg)
	}
	if chatTitle := strings.TrimSpace(title); chatTitle != "" {
		params.Title = beeperdesktopapi.String(chatTitle)
	}
	created, err := client.Chats.New(ctx, params)
	if err != nil {
		return "", err
	}
	if created == nil {
		return "", nil
	}
	return strings.TrimSpace(created.ChatID), nil
}

func (oc *AIClient) archiveDesktopChat(ctx context.Context, instance, chatID string, archived bool) error {
	client, err := oc.desktopAPIClient(instance)
	if err != nil {
		return err
	}
	if client == nil {
		return fmt.Errorf("desktop API token is not set")
	}
	_, err = client.Chats.Archive(ctx, strings.TrimSpace(chatID), beeperdesktopapi.ChatArchiveParams{
		Archived: beeperdesktopapi.Bool(archived),
	})
	return err
}

func (oc *AIClient) setDesktopChatReminder(ctx context.Context, instance, chatID string, remindAtMs int64, dismissOnIncoming bool) error {
	client, err := oc.desktopAPIClient(instance)
	if err != nil {
		return err
	}
	if client == nil {
		return fmt.Errorf("desktop API token is not set")
	}
	_, err = client.Chats.Reminders.New(ctx, strings.TrimSpace(chatID), beeperdesktopapi.ChatReminderNewParams{
		Reminder: beeperdesktopapi.ChatReminderNewParamsReminder{
			RemindAtMs:               float64(remindAtMs),
			DismissOnIncomingMessage: beeperdesktopapi.Bool(dismissOnIncoming),
		},
	})
	return err
}

func (oc *AIClient) clearDesktopChatReminder(ctx context.Context, instance, chatID string) error {
	client, err := oc.desktopAPIClient(instance)
	if err != nil {
		return err
	}
	if client == nil {
		return fmt.Errorf("desktop API token is not set")
	}
	_, err = client.Chats.Reminders.Delete(ctx, strings.TrimSpace(chatID))
	return err
}

func (oc *AIClient) uploadDesktopAssetBase64(ctx context.Context, instance string, data []byte, fileName, mimeType string) (*beeperdesktopapi.AssetUploadBase64Response, error) {
	client, err := oc.desktopAPIClient(instance)
	if err != nil {
		return nil, err
	}
	if client == nil {
		return nil, fmt.Errorf("desktop API token is not set")
	}
	params := beeperdesktopapi.AssetUploadBase64Params{
		Content: base64.StdEncoding.EncodeToString(data),
	}
	if name := strings.TrimSpace(fileName); name != "" {
		params.FileName = beeperdesktopapi.String(name)
	}
	if mt := strings.TrimSpace(mimeType); mt != "" {
		params.MimeType = beeperdesktopapi.String(mt)
	}
	return client.Assets.UploadBase64(ctx, params)
}

func (oc *AIClient) downloadDesktopAsset(ctx context.Context, instance, url string) (*beeperdesktopapi.AssetDownloadResponse, error) {
	client, err := oc.desktopAPIClient(instance)
	if err != nil {
		return nil, err
	}
	if client == nil {
		return nil, fmt.Errorf("desktop API token is not set")
	}
	return client.Assets.Download(ctx, beeperdesktopapi.AssetDownloadParams{URL: strings.TrimSpace(url)})
}

func matchDesktopChatsByLabel(chats []beeperdesktopapi.Chat, label string, accounts map[string]beeperdesktopapi.Account) ([]beeperdesktopapi.Chat, []beeperdesktopapi.Chat) {
	lower := strings.ToLower(strings.TrimSpace(label))
	if lower == "" {
		return nil, nil
	}
	exact := make([]beeperdesktopapi.Chat, 0, len(chats))
	partial := make([]beeperdesktopapi.Chat, 0, len(chats))
	for _, chat := range chats {
		account := accounts[strings.TrimSpace(chat.AccountID)]
		candidates := desktopChatLabelCandidates(chat, account)
		if len(candidates) == 0 {
			continue
		}
		matchedExact := false
		matchedPartial := false
		for _, candidate := range candidates {
			normalized := strings.ToLower(strings.TrimSpace(candidate))
			if normalized == "" {
				continue
			}
			if normalized == lower {
				matchedExact = true
				break
			}
			if strings.Contains(normalized, lower) {
				matchedPartial = true
			}
		}
		if matchedExact {
			exact = append(exact, chat)
			continue
		}
		if matchedPartial {
			partial = append(partial, chat)
		}
	}
	return exact, partial
}

func filterDesktopChatsByResolveOptions(chats []beeperdesktopapi.Chat, accounts map[string]beeperdesktopapi.Account, opts desktopLabelResolveOptions) []beeperdesktopapi.Chat {
	accountID := strings.TrimSpace(opts.AccountID)
	network := strings.TrimSpace(opts.Network)
	if accountID == "" && network == "" {
		return chats
	}
	networkFilter := map[string]struct{}{}
	if network != "" {
		networkFilter[network] = struct{}{}
	}
	filtered := make([]beeperdesktopapi.Chat, 0, len(chats))
	for _, chat := range chats {
		chatAccountID := strings.TrimSpace(chat.AccountID)
		if accountID != "" && chatAccountID != accountID {
			continue
		}
		if network != "" {
			account := accounts[chatAccountID]
			if !desktopNetworkFilterMatches(networkFilter, account.Network) {
				continue
			}
		}
		filtered = append(filtered, chat)
	}
	return filtered
}

func desktopChatLabelCandidates(chat beeperdesktopapi.Chat, account beeperdesktopapi.Account) []string {
	title := strings.TrimSpace(chat.Title)
	if title == "" {
		return nil
	}
	accountID := strings.TrimSpace(chat.AccountID)
	network := canonicalDesktopNetwork(account.Network)
	rawNetwork := normalizeDesktopNetworkToken(account.Network)
	candidates := []string{title}
	if accountID != "" {
		candidates = append(candidates, accountID+":"+title, accountID+"/"+title)
	}
	if network != "" {
		candidates = append(candidates, network+":"+title)
	}
	if rawNetwork != "" && rawNetwork != network {
		candidates = append(candidates, rawNetwork+":"+title)
	}
	if network != "" && accountID != "" {
		candidates = append(candidates, network+"/"+accountID+":"+title)
	}
	if rawNetwork != "" && rawNetwork != network && accountID != "" {
		candidates = append(candidates, rawNetwork+"/"+accountID+":"+title)
	}
	return uniqueNonEmptyStrings(candidates)
}

func describeDesktopChatForLabel(chat beeperdesktopapi.Chat, account beeperdesktopapi.Account) string {
	title := strings.TrimSpace(chat.Title)
	if title == "" {
		title = strings.TrimSpace(chat.ID)
	}
	accountID := strings.TrimSpace(chat.AccountID)
	network := strings.TrimSpace(account.Network)
	if accountID == "" && network == "" {
		return title
	}
	parts := make([]string, 0, 2)
	if network != "" {
		parts = append(parts, network)
	}
	if accountID != "" {
		parts = append(parts, accountID)
	}
	return fmt.Sprintf("%s [%s]", title, strings.Join(parts, "/"))
}

func uniqueNonEmptyStrings(values []string) []string {
	seen := map[string]struct{}{}
	out := make([]string, 0, len(values))
	for _, raw := range values {
		trimmed := strings.TrimSpace(raw)
		if trimmed == "" {
			continue
		}
		key := strings.ToLower(trimmed)
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, trimmed)
	}
	return out
}

func desktopSessionAccountID(areThereMultipleDesktopInstances bool, instance string, account beeperdesktopapi.Account) string {
	rawAccountID := strings.TrimSpace(account.AccountID)
	if rawAccountID == "" {
		return ""
	}
	return formatDesktopAccountID(areThereMultipleDesktopInstances, instance, account.Network, rawAccountID)
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
