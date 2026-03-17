package openclaw

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/rs/zerolog"
	"go.mau.fi/util/ptr"
	"maunium.net/go/mautrix/bridgev2"
	"maunium.net/go/mautrix/bridgev2/database"
	"maunium.net/go/mautrix/bridgev2/networkid"
	"maunium.net/go/mautrix/bridgev2/status"
	"maunium.net/go/mautrix/event"

	"github.com/beeper/agentremote"
	"github.com/beeper/agentremote/pkg/shared/cachedvalue"
	"github.com/beeper/agentremote/pkg/shared/stringutil"
	bridgesdk "github.com/beeper/agentremote/sdk"
)

var (
	_ bridgev2.NetworkAPI                   = (*OpenClawClient)(nil)
	_ bridgev2.BackfillingNetworkAPI        = (*OpenClawClient)(nil)
	_ bridgev2.DeleteChatHandlingNetworkAPI = (*OpenClawClient)(nil)
	_ bridgev2.ReactionHandlingNetworkAPI   = (*OpenClawClient)(nil)
)

const openClawCapabilityBaseID = "com.beeper.ai.capabilities.2026_03_09+openclaw"

var openClawBaseCaps = agentremote.BuildRoomFeatures(agentremote.RoomFeaturesParams{
	ID:                  openClawCapabilityBaseID,
	File:                agentremote.BuildMediaFileFeatureMap(openClawRejectedFileFeatures),
	MaxTextLength:       100000,
	Reply:               event.CapLevelFullySupported,
	Thread:              event.CapLevelRejected,
	Edit:                event.CapLevelRejected,
	Delete:              event.CapLevelRejected,
	Reaction:            event.CapLevelFullySupported,
	ReadReceipts:        true,
	TypingNotifications: true,
	DeleteChat:          true,
})

type openClawCapabilityProfile struct {
	SupportsVision    bool
	SupportsAudio     bool
	SupportsVideo     bool
	SupportsReasoning bool
	MediaKnown        bool
}

type OpenClawClient struct {
	agentremote.ClientBase
	UserLogin *bridgev2.UserLogin
	connector *OpenClawConnector

	manager *openClawManager

	connectMu     sync.Mutex
	connectCancel context.CancelFunc
	connectSeq    uint64

	agentCache *cachedvalue.CachedValue[agentCatalogEntry]
	modelCache *cachedvalue.CachedValue[[]gatewayModelChoice]

	toolCacheMu sync.Mutex
	toolCaches  map[string]*cachedvalue.CachedValue[gatewayToolsCatalogResponse]

	streamHost *agentremote.StreamTurnHost[openClawStreamState]
}

type openClawStreamState struct {
	portal           *bridgev2.Portal
	turnID           string
	agentID          string
	turn             *bridgesdk.Turn
	sessionKey       string
	messageTS        time.Time
	stream           bridgesdk.StreamPartState
	role             string
	runID            string
	sessionID        string
	promptTokens     int64
	completionTokens int64
	reasoningTokens  int64
	totalTokens      int64
}

func newOpenClawClient(login *bridgev2.UserLogin, connector *OpenClawConnector) (*OpenClawClient, error) {
	if login == nil {
		return nil, errors.New("missing login")
	}
	client := &OpenClawClient{
		UserLogin:  login,
		connector:  connector,
		agentCache: cachedvalue.New[agentCatalogEntry](openClawAgentCatalogTTL),
		modelCache: cachedvalue.New[[]gatewayModelChoice](openClawMetadataCatalogTTL),
		toolCaches: make(map[string]*cachedvalue.CachedValue[gatewayToolsCatalogResponse]),
	}
	client.streamHost = agentremote.NewStreamTurnHost(agentremote.StreamTurnHostCallbacks[openClawStreamState]{
		GetAborter: func(s *openClawStreamState) agentremote.Aborter {
			if s.turn == nil {
				return nil
			}
			return s.turn
		},
	})
	client.InitClientBase(login, client)
	client.HumanUserIDPrefix = "openclaw-user"
	client.MessageIDPrefix = "openclaw"
	client.MessageLogKey = "openclaw_msg_id"
	client.manager = newOpenClawManager(client)
	return client, nil
}

func (oc *OpenClawClient) SetUserLogin(login *bridgev2.UserLogin) {
	oc.UserLogin = login
	oc.ClientBase.SetUserLogin(login)
}

func (oc *OpenClawClient) Connect(ctx context.Context) {
	oc.ResetStreamShutdown()
	oc.connectMu.Lock()
	if oc.connectCancel != nil {
		oc.connectMu.Unlock()
		return
	}
	runCtx, cancel := context.WithCancel(oc.BackgroundContext(ctx))
	oc.connectSeq++
	seq := oc.connectSeq
	oc.connectCancel = cancel
	oc.connectMu.Unlock()

	oc.UserLogin.BridgeState.Send(status.BridgeState{StateEvent: status.StateConnecting, Message: "Connecting"})
	go func() {
		defer func() {
			oc.connectMu.Lock()
			if seq == oc.connectSeq {
				oc.connectCancel = nil
			}
			oc.connectMu.Unlock()
		}()
		oc.connectLoop(runCtx)
	}()
}

func (oc *OpenClawClient) Disconnect() {
	oc.BeginStreamShutdown()
	cancel := oc.detachConnectCancel()
	if cancel != nil {
		cancel()
	}
	if oc.manager != nil {
		oc.manager.Stop()
		if oc.manager.approvalFlow != nil {
			oc.manager.approvalFlow.Close()
		}
	}
	oc.SetLoggedIn(false)
	oc.streamHost.DrainAndAbort("disconnect")
	oc.CloseAllSessions()
	if oc.UserLogin != nil {
		oc.UserLogin.BridgeState.Send(status.BridgeState{StateEvent: status.StateTransientDisconnect, Message: "Disconnected"})
	}
}

func (oc *OpenClawClient) detachConnectCancel() context.CancelFunc {
	oc.connectMu.Lock()
	defer oc.connectMu.Unlock()
	cancel := oc.connectCancel
	oc.connectCancel = nil
	oc.connectSeq++
	return cancel
}

func (oc *OpenClawClient) connectLoop(ctx context.Context) {
	attempt := 0
	for {
		if ctx.Err() != nil {
			return
		}
		connected, err := oc.manager.Start(ctx)
		if ctx.Err() != nil {
			return
		}
		if err == nil {
			if connected {
				oc.SetLoggedIn(false)
			}
			return
		}
		if connected {
			attempt = 0
		}
		retryDelay := openClawReconnectDelay(attempt)
		attempt++
		state, retry := classifyOpenClawConnectionError(err, retryDelay)
		oc.SetLoggedIn(false)
		if oc.UserLogin != nil {
			oc.UserLogin.BridgeState.Send(state)
		}
		if !retry {
			return
		}
		timer := time.NewTimer(retryDelay)
		select {
		case <-ctx.Done():
			timer.Stop()
			return
		case <-timer.C:
		}
	}
}

func (oc *OpenClawClient) GetUserLogin() *bridgev2.UserLogin { return oc.UserLogin }

func (oc *OpenClawClient) GetApprovalHandler() agentremote.ApprovalReactionHandler {
	if oc.manager == nil {
		return nil
	}
	return oc.manager.approvalFlow
}

func (oc *OpenClawClient) LogoutRemote(_ context.Context) {}

func (oc *OpenClawClient) HandleMatrixMessage(ctx context.Context, msg *bridgev2.MatrixMessage) (*bridgev2.MatrixMessageResponse, error) {
	if msg == nil || msg.Portal == nil {
		return nil, errors.New("missing portal context")
	}
	meta := portalMeta(msg.Portal)
	if !meta.IsOpenClawRoom {
		return &bridgev2.MatrixMessageResponse{Pending: false}, nil
	}
	return oc.manager.HandleMatrixMessage(ctx, msg)
}

func (oc *OpenClawClient) FetchMessages(ctx context.Context, params bridgev2.FetchMessagesParams) (*bridgev2.FetchMessagesResponse, error) {
	if params.Portal == nil {
		return nil, nil
	}
	if !portalMeta(params.Portal).IsOpenClawRoom {
		return nil, nil
	}
	return oc.manager.FetchMessages(ctx, params)
}

func (oc *OpenClawClient) HandleMatrixDeleteChat(ctx context.Context, msg *bridgev2.MatrixDeleteChat) error {
	if oc == nil || msg == nil || msg.Portal == nil || oc.manager == nil {
		return nil
	}
	meta := portalMeta(msg.Portal)
	if !meta.IsOpenClawRoom {
		return nil
	}
	sessionKey := strings.TrimSpace(meta.OpenClawSessionKey)
	if sessionKey == "" {
		return nil
	}
	gateway := oc.manager.gatewayClient()
	if gateway == nil {
		return nil
	}
	// Best-effort cleanup. Local room deletion is handled by the core bridge.
	_ = gateway.AbortRun(ctx, sessionKey, "")
	if err := gateway.DeleteSession(ctx, sessionKey, true); err != nil {
		return nil
	}
	oc.manager.forgetSession(sessionKey)
	meta.OpenClawSessionID = ""
	meta.OpenClawSessionKey = ""
	meta.OpenClawSessionLabel = ""
	meta.OpenClawLastMessagePreview = ""
	meta.OpenClawPreviewSnippet = ""
	_ = msg.Portal.Save(ctx)
	return nil
}

func (oc *OpenClawClient) GetCapabilities(ctx context.Context, portal *bridgev2.Portal) *event.RoomFeatures {
	caps := openClawBaseCaps.Clone()
	profile := oc.openClawCapabilityProfile(ctx, portalMeta(portal))
	caps.ID = openClawCapabilityID(profile)
	if !profile.MediaKnown {
		for _, msgType := range agentremote.MediaMessageTypes {
			caps.File[msgType] = openClawFileFeatures.Clone()
		}
		return caps
	}
	caps.File[event.MsgFile] = openClawFileFeatures.Clone()
	if profile.SupportsVision {
		caps.File[event.MsgImage] = openClawFileFeatures.Clone()
		caps.File[event.CapMsgGIF] = openClawFileFeatures.Clone()
		caps.File[event.CapMsgSticker] = openClawFileFeatures.Clone()
	}
	if profile.SupportsAudio {
		caps.File[event.MsgAudio] = openClawFileFeatures.Clone()
		caps.File[event.CapMsgVoice] = openClawFileFeatures.Clone()
	}
	if profile.SupportsVideo {
		caps.File[event.MsgVideo] = openClawFileFeatures.Clone()
	}
	return caps
}

func (oc *OpenClawClient) capabilityIDForPortalMeta(ctx context.Context, meta *PortalMetadata) string {
	return openClawCapabilityID(oc.openClawCapabilityProfile(ctx, meta))
}

func (oc *OpenClawClient) maybeRefreshPortalCapabilities(ctx context.Context, portal *bridgev2.Portal, previous *PortalMetadata) {
	if oc == nil || oc.UserLogin == nil || portal == nil || portal.MXID == "" {
		return
	}
	current := portalMeta(portal)
	if oc.capabilityIDForPortalMeta(ctx, previous) == oc.capabilityIDForPortalMeta(ctx, current) {
		return
	}
	portal.UpdateCapabilities(ctx, oc.UserLogin, true)
}

func (oc *OpenClawClient) GetChatInfo(ctx context.Context, portal *bridgev2.Portal) (*bridgev2.ChatInfo, error) {
	meta := portalMeta(portal)
	oc.enrichPortalMetadata(ctx, meta)
	title := oc.displayNameForPortal(meta)
	roomType := openClawRoomType(meta)
	agentID := stringutil.TrimDefault(meta.OpenClawDMTargetAgentID, meta.OpenClawAgentID)
	if roomType == database.RoomTypeDM && agentID != "" {
		info := oc.syntheticDMPortalInfo(agentID, title)
		info.Topic = ptr.NonZero(oc.topicForPortal(meta))
		info.Type = ptr.Ptr(roomType)
		info.CanBackfill = true
		return info, nil
	}
	return &bridgev2.ChatInfo{
		Name:        ptr.Ptr(title),
		Topic:       ptr.NonZero(oc.topicForPortal(meta)),
		Type:        ptr.Ptr(roomType),
		CanBackfill: true,
	}, nil
}

func openClawRejectedFileFeatures() *event.FileFeatures {
	return &event.FileFeatures{
		MimeTypes: map[string]event.CapabilitySupportLevel{
			"*/*": event.CapLevelRejected,
		},
		Caption: event.CapLevelRejected,
	}
}

func (oc *OpenClawClient) openClawCapabilityProfile(ctx context.Context, meta *PortalMetadata) openClawCapabilityProfile {
	model := oc.effectiveModelChoice(ctx, meta)
	if model == nil {
		return openClawCapabilityProfile{}
	}
	profile := openClawCapabilityProfile{
		SupportsReasoning: model.Reasoning,
		MediaKnown:        len(model.Input) > 0,
	}
	for _, modality := range model.Input {
		switch strings.ToLower(strings.TrimSpace(modality)) {
		case "image":
			profile.SupportsVision = true
		case "audio":
			profile.SupportsAudio = true
		case "video":
			profile.SupportsVideo = true
		}
	}
	return profile
}

func openClawCapabilityID(profile openClawCapabilityProfile) string {
	// Suffixes are appended in alphabetical order so no sorting is needed.
	var suffixes []string
	if profile.SupportsAudio {
		suffixes = append(suffixes, "audio")
	}
	if !profile.MediaKnown {
		suffixes = append(suffixes, "fallbackmedia")
	}
	if profile.SupportsReasoning {
		suffixes = append(suffixes, "reasoning")
	}
	if profile.SupportsVideo {
		suffixes = append(suffixes, "video")
	}
	if profile.SupportsVision {
		suffixes = append(suffixes, "vision")
	}
	if len(suffixes) == 0 {
		return openClawCapabilityBaseID
	}
	return openClawCapabilityBaseID + "+" + strings.Join(suffixes, "+")
}

func (oc *OpenClawClient) GetUserInfo(ctx context.Context, ghost *bridgev2.Ghost) (*bridgev2.UserInfo, error) {
	if ghost == nil {
		return agentremote.BuildBotUserInfo("OpenClaw"), nil
	}
	agentID, ok := parseOpenClawGhostID(string(ghost.ID))
	if !ok {
		return agentremote.BuildBotUserInfo("OpenClaw"), nil
	}
	current := ghostMeta(ghost)
	configured, err := oc.agentCatalogEntryByID(ctx, agentID)
	if err != nil {
		oc.Log().Debug().Err(err).Str("agent_id", agentID).Msg("Failed to refresh OpenClaw agent catalog for ghost info")
	}
	profile := oc.resolveAgentProfile(ctx, agentID, "", current, configured)
	return oc.userInfoForAgentProfile(profile), nil
}

func (oc *OpenClawClient) Log() *zerolog.Logger {
	if oc == nil || oc.UserLogin == nil {
		l := zerolog.Nop()
		return &l
	}
	l := oc.UserLogin.Log.With().Str("component", "openclaw").Logger()
	return &l
}

func (oc *OpenClawClient) gatewayID() string {
	meta := loginMetadata(oc.UserLogin)
	return openClawGatewayID(meta.GatewayURL, meta.GatewayLabel)
}

func (oc *OpenClawClient) portalKeyForSession(sessionKey string) networkid.PortalKey {
	return openClawPortalKey(oc.UserLogin.ID, oc.gatewayID(), sessionKey)
}

func (oc *OpenClawClient) displayNameForSession(session gatewaySessionRow) string {
	sourceLabel := openClawSourceLabel(session.Space, session.GroupChannel, session.Subject)
	for _, value := range []string{
		session.DerivedTitle,
		session.DisplayName,
		session.Label,
		sourceLabel,
		session.Subject,
		session.LastTo,
		session.Channel,
		session.Key,
	} {
		if trimmed := strings.TrimSpace(value); trimmed != "" {
			return trimmed
		}
	}
	return "OpenClaw"
}

func (oc *OpenClawClient) displayNameForPortal(meta *PortalMetadata) string {
	if meta == nil {
		return "OpenClaw"
	}
	if trimmed := strings.TrimSpace(meta.OpenClawDMTargetAgentName); trimmed != "" {
		return trimmed
	}
	sourceLabel := openClawSourceLabel(meta.OpenClawSpace, meta.OpenClawGroupChannel, meta.OpenClawSubject)
	candidates := []string{
		meta.OpenClawDerivedTitle,
		meta.OpenClawDisplayName,
		meta.OpenClawSessionLabel,
		sourceLabel,
		meta.OpenClawSubject,
		meta.LastTo,
		meta.OpenClawChannel,
		meta.OpenClawSessionKey,
	}
	for _, value := range candidates {
		if trimmed := strings.TrimSpace(value); trimmed != "" {
			return trimmed
		}
	}
	return "OpenClaw"
}

func appendDedupedPart(parts []string, value string) []string {
	value = strings.TrimSpace(value)
	if value == "" {
		return parts
	}
	for _, existing := range parts {
		if strings.EqualFold(existing, value) {
			return parts
		}
	}
	return append(parts, value)
}

func (oc *OpenClawClient) topicForPortal(meta *PortalMetadata) string {
	if meta == nil {
		return ""
	}
	if strings.TrimSpace(meta.OpenClawDMTargetAgentID) != "" || isOpenClawSyntheticDMSessionKey(meta.OpenClawSessionKey) {
		return "OpenClaw agent DM"
	}
	parts := make([]string, 0, 8)
	parts = appendDedupedPart(parts, normalizeOpenClawChatType(meta.OpenClawChatType))
	parts = appendDedupedPart(parts, meta.OpenClawChannel)
	parts = appendDedupedPart(parts, openClawSourceLabel(meta.OpenClawSpace, meta.OpenClawGroupChannel, meta.OpenClawSubject))
	parts = appendDedupedPart(parts, summarizeOpenClawOrigin(meta.OpenClawOrigin, meta.OpenClawChannel))
	parts = appendDedupedPart(parts, meta.ModelProvider)
	parts = appendDedupedPart(parts, meta.Model)
	if preview := stringutil.TrimDefault(meta.OpenClawPreviewSnippet, meta.OpenClawLastMessagePreview); preview != "" {
		parts = appendDedupedPart(parts, "Recent: "+preview)
	}
	if meta.HistoryMode != "" {
		parts = appendDedupedPart(parts, "History: "+meta.HistoryMode)
	}
	if meta.OpenClawToolCount > 0 {
		toolSummary := fmt.Sprintf("Tools: %d", meta.OpenClawToolCount)
		if profile := strings.TrimSpace(meta.OpenClawToolProfile); profile != "" {
			toolSummary += " (" + profile + ")"
		}
		parts = appendDedupedPart(parts, toolSummary)
	}
	if meta.OpenClawKnownModelCount > 0 {
		parts = appendDedupedPart(parts, fmt.Sprintf("Models: %d", meta.OpenClawKnownModelCount))
	}
	return strings.Join(parts, " | ")
}

func normalizeOpenClawChatType(raw string) string {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "dm", "direct", "private", "one_to_one", "one-to-one":
		return "direct"
	case "group", "room":
		return "group"
	case "channel", "thread":
		return "channel"
	default:
		return ""
	}
}

func openClawRoomType(meta *PortalMetadata) database.RoomType {
	if meta == nil {
		return database.RoomTypeDM
	}
	switch normalizeOpenClawChatType(meta.OpenClawChatType) {
	case "group", "channel":
		return database.RoomTypeDefault
	}
	if strings.TrimSpace(meta.OpenClawSpace) != "" || strings.TrimSpace(meta.OpenClawGroupChannel) != "" {
		return database.RoomTypeDefault
	}
	return database.RoomTypeDM
}

func openClawSourceLabel(space, groupChannel, subject string) string {
	space = strings.TrimSpace(space)
	groupChannel = strings.TrimSpace(groupChannel)
	subject = strings.TrimSpace(subject)
	if groupChannel != "" {
		if !strings.HasPrefix(groupChannel, "#") {
			groupChannel = "#" + groupChannel
		}
		if space != "" {
			return space + groupChannel
		}
		return groupChannel
	}
	if space != "" {
		return space
	}
	return subject
}

func compactOpenClawOrigin(origin string) string {
	origin = strings.TrimSpace(strings.Join(strings.Fields(origin), " "))
	if origin == "" {
		return ""
	}
	const maxLen = 80
	if len(origin) > maxLen {
		return "Origin: " + origin[:maxLen-1] + "…"
	}
	return "Origin: " + origin
}

func summarizeOpenClawOrigin(origin, channel string) string {
	origin = strings.TrimSpace(origin)
	if origin == "" {
		return ""
	}
	var structured map[string]any
	if err := json.Unmarshal([]byte(origin), &structured); err != nil || len(structured) == 0 {
		return compactOpenClawOrigin(origin)
	}
	parts := make([]string, 0, 5)
	provider := stringutil.TrimDefault(stringValue(structured["provider"]), stringValue(structured["source"]))
	if provider != "" && !strings.EqualFold(provider, strings.TrimSpace(channel)) {
		parts = appendDedupedPart(parts, provider)
	}
	parts = appendDedupedPart(parts, stringutil.TrimDefault(stringValue(structured["label"]), stringValue(structured["name"])))
	parts = appendDedupedPart(parts, stringutil.TrimDefault(
		stringutil.TrimDefault(stringValue(structured["workspace"]), stringValue(structured["space"])),
		stringValue(structured["team"]),
	))
	if value := stringutil.TrimDefault(
		stringutil.TrimDefault(stringValue(structured["channel"]), stringValue(structured["channelId"])),
		stringValue(structured["groupChannel"]),
	); value != "" {
		parts = appendDedupedPart(parts, "Channel "+value)
	}
	if value := stringutil.TrimDefault(stringValue(structured["threadId"]), stringValue(structured["threadID"])); value != "" {
		parts = appendDedupedPart(parts, "Thread "+value)
	}
	if value := stringutil.TrimDefault(stringValue(structured["account"]), stringValue(structured["accountId"])); value != "" {
		parts = appendDedupedPart(parts, "Account "+value)
	}
	if len(parts) == 0 {
		return compactOpenClawOrigin(origin)
	}
	return "Origin: " + strings.Join(parts, " • ")
}

func (oc *OpenClawClient) displayNameForAgent(agentID string) string {
	agentID = strings.TrimSpace(agentID)
	if agentID == "" || strings.EqualFold(agentID, "gateway") {
		if label := strings.TrimSpace(loginMetadata(oc.UserLogin).GatewayLabel); label != "" {
			return label
		}
		return "OpenClaw"
	}
	return agentID
}

func (oc *OpenClawClient) lookupAgentIdentity(ctx context.Context, agentID, sessionKey string) *gatewayAgentIdentity {
	if oc == nil || oc.manager == nil {
		return nil
	}
	gateway := oc.manager.gatewayClient()
	if gateway == nil {
		return nil
	}
	identity, err := gateway.GetAgentIdentity(ctx, agentID, sessionKey)
	if err != nil {
		oc.Log().Debug().Err(err).Str("agent_id", agentID).Str("session_key", sessionKey).Msg("Failed to fetch OpenClaw agent identity")
		return nil
	}
	return identity
}

func (oc *OpenClawClient) agentAvatar(meta *GhostMetadata, agentID string) *bridgev2.Avatar {
	if meta == nil {
		return nil
	}
	avatarURL, err := oc.resolveAllowedAvatarURL(strings.TrimSpace(meta.OpenClawAgentAvatarURL))
	if err != nil || avatarURL == "" {
		return nil
	}
	return &bridgev2.Avatar{
		ID: networkid.AvatarID("openclaw:" + stringutil.TrimDefault(meta.OpenClawAgentID, agentID) + ":" + avatarURL),
		Get: func(ctx context.Context) ([]byte, error) {
			req, err := http.NewRequestWithContext(ctx, http.MethodGet, avatarURL, nil)
			if err != nil {
				return nil, err
			}
			resp, err := (&http.Client{Timeout: 15 * time.Second}).Do(req)
			if err != nil {
				return nil, err
			}
			defer resp.Body.Close()
			if resp.StatusCode < 200 || resp.StatusCode >= 300 {
				return nil, errors.New("avatar download failed")
			}
			return io.ReadAll(io.LimitReader(resp.Body, 10*1024*1024))
		},
	}
}

func (oc *OpenClawClient) resolveAllowedAvatarURL(raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", errors.New("missing avatar URL")
	}
	if strings.HasPrefix(raw, "data:image/") {
		return raw, nil
	}
	parsed, err := url.Parse(raw)
	if err != nil {
		return "", err
	}
	loginURL := strings.TrimSpace(loginMetadata(oc.UserLogin).GatewayURL)
	if loginURL == "" {
		return "", errors.New("gateway URL is unavailable")
	}
	base, err := url.Parse(loginURL)
	if err != nil {
		return "", err
	}
	switch base.Scheme {
	case "ws":
		base.Scheme = "http"
	case "wss":
		base.Scheme = "https"
	}
	switch parsed.Scheme {
	case "":
		parsed = base.ResolveReference(parsed)
	case "http", "https":
	default:
		return "", errors.New("avatar URL scheme is not permitted")
	}
	if !strings.EqualFold(parsed.Host, base.Host) {
		return "", errors.New("avatar URL host is not permitted")
	}
	return parsed.String(), nil
}

func (oc *OpenClawClient) senderForAgent(agentID string, fromMe bool) bridgev2.EventSender {
	if fromMe {
		return bridgev2.EventSender{
			Sender:      humanUserID(oc.UserLogin.ID),
			SenderLogin: oc.UserLogin.ID,
			IsFromMe:    true,
		}
	}
	return bridgev2.EventSender{
		Sender:      openClawGhostUserID(agentID),
		SenderLogin: oc.UserLogin.ID,
		ForceDMUser: true,
	}
}

func (oc *OpenClawClient) sendSystemNoticeViaPortal(ctx context.Context, portal *bridgev2.Portal, msg string) {
	if portal == nil || strings.TrimSpace(msg) == "" {
		return
	}
	converted := &bridgev2.ConvertedMessage{
		Parts: []*bridgev2.ConvertedMessagePart{{
			ID:      networkid.PartID("0"),
			Type:    event.EventMessage,
			Content: &event.MessageEventContent{MsgType: event.MsgNotice, Body: msg},
			Extra:   map[string]any{"msgtype": event.MsgNotice, "body": msg, "m.mentions": map[string]any{}},
		}},
	}
	oc.UserLogin.QueueRemoteEvent(buildOpenClawRemoteMessage(
		portal.PortalKey,
		newOpenClawMessageID(),
		oc.senderForAgent("gateway", false),
		time.Now(),
		0,
		converted,
	))
}

func (oc *OpenClawClient) DownloadAndEncodeMedia(ctx context.Context, mediaURL string, file *event.EncryptedFileInfo, maxMB int) (string, string, error) {
	return agentremote.DownloadAndEncodeMedia(ctx, oc.UserLogin, mediaURL, file, maxMB)
}
