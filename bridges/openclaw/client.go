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

	"github.com/coder/websocket"
	"github.com/rs/zerolog"
	"go.mau.fi/util/ptr"
	"maunium.net/go/mautrix/bridgev2"
	"maunium.net/go/mautrix/bridgev2/database"
	"maunium.net/go/mautrix/bridgev2/networkid"
	"maunium.net/go/mautrix/bridgev2/status"
	"maunium.net/go/mautrix/event"

	"github.com/beeper/agentremote/pkg/shared/cachedvalue"
	"github.com/beeper/agentremote/pkg/shared/stringutil"
	"github.com/beeper/agentremote/sdk"
)

var (
	_ bridgev2.NetworkAPI                      = (*OpenClawClient)(nil)
	_ bridgev2.BackfillingNetworkAPI           = (*OpenClawClient)(nil)
	_ bridgev2.BackfillingNetworkAPIWithLimits = (*OpenClawClient)(nil)
	_ bridgev2.DeleteChatHandlingNetworkAPI    = (*OpenClawClient)(nil)
	_ bridgev2.ReactionHandlingNetworkAPI      = (*OpenClawClient)(nil)
)

const openClawCapabilityBaseID = "com.beeper.ai.capabilities.2026_03_09+openclaw"

var openClawBaseCaps = sdk.BuildRoomFeatures(sdk.RoomFeaturesParams{
	ID:                  openClawCapabilityBaseID,
	File:                sdk.BuildMediaFileFeatureMap(openClawRejectedFileFeatures),
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
	sdk.ClientBase
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

	streamHost *sdk.StreamTurnHost[openClawStreamState]
}

type openClawStreamState struct {
	portal           *bridgev2.Portal
	turnID           string
	agentID          string
	turn             *sdk.Turn
	sessionKey       string
	messageTS        time.Time
	stream           sdk.StreamPartState
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
	client.streamHost = sdk.NewStreamTurnHost(sdk.StreamTurnHostCallbacks[openClawStreamState]{
		GetAborter: func(s *openClawStreamState) sdk.Aborter {
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

func (oc *OpenClawClient) GetApprovalHandler() sdk.ApprovalReactionHandler {
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

func (oc *OpenClawClient) GetBackfillMaxBatchCount(_ context.Context, _ *bridgev2.Portal, _ *database.BackfillTask) int {
	return -1
}

func (oc *OpenClawClient) HandleMatrixDeleteChat(ctx context.Context, msg *bridgev2.MatrixDeleteChat) error {
	if oc == nil || msg == nil || msg.Portal == nil || oc.manager == nil {
		return nil
	}
	meta := portalMeta(msg.Portal)
	if !meta.IsOpenClawRoom {
		return nil
	}
	state, err := loadOpenClawPortalState(ctx, msg.Portal, oc.UserLogin)
	if err != nil {
		return err
	}
	sessionKey := strings.TrimSpace(state.OpenClawSessionKey)
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
	state.OpenClawSessionID = ""
	state.OpenClawSessionKey = ""
	state.OpenClawSessionLabel = ""
	state.OpenClawLastMessagePreview = ""
	state.OpenClawPreviewSnippet = ""
	state.OpenClawLastPreviewAt = 0
	state.BackgroundBackfillStatus = ""
	state.BackgroundBackfillError = ""
	state.BackgroundBackfillCursor = ""
	state.BackgroundBackfillStartedAt = 0
	state.BackgroundBackfillCompletedAt = 0
	if err := saveOpenClawPortalState(ctx, msg.Portal, oc.UserLogin, state); err != nil {
		return err
	}
	return nil
}

func (oc *OpenClawClient) GetCapabilities(ctx context.Context, portal *bridgev2.Portal) *event.RoomFeatures {
	state, err := loadOpenClawPortalState(ctx, portal, oc.UserLogin)
	if err != nil {
		return openClawCapabilitiesFromProfile(openClawCapabilityProfile{})
	}
	oc.enrichPortalState(ctx, state)
	profile := oc.openClawCapabilityProfile(ctx, state)
	return openClawCapabilitiesFromProfile(profile)
}

func (oc *OpenClawClient) capabilityIDForPortalState(ctx context.Context, state *openClawPortalState) string {
	return openClawCapabilityID(oc.openClawCapabilityProfile(ctx, state))
}

func (oc *OpenClawClient) maybeRefreshPortalCapabilities(ctx context.Context, portal *bridgev2.Portal, previous, current *openClawPortalState) {
	if oc == nil || oc.UserLogin == nil || portal == nil || portal.MXID == "" || previous == nil || current == nil {
		return
	}
	if oc.capabilityIDForPortalState(ctx, previous) == oc.capabilityIDForPortalState(ctx, current) {
		return
	}
	portal.UpdateCapabilities(ctx, oc.UserLogin, true)
}

func openClawCapabilitiesFromProfile(profile openClawCapabilityProfile) *event.RoomFeatures {
	caps := openClawBaseCaps.Clone()
	caps.ID = openClawCapabilityID(profile)
	if !profile.MediaKnown {
		for _, msgType := range sdk.MediaMessageTypes {
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

func (oc *OpenClawClient) GetChatInfo(ctx context.Context, portal *bridgev2.Portal) (*bridgev2.ChatInfo, error) {
	state, err := loadOpenClawPortalState(ctx, portal, oc.UserLogin)
	if err != nil {
		return nil, err
	}
	oc.enrichPortalState(ctx, state)
	title := oc.displayNameForPortal(state)
	roomType := openClawRoomType(state)
	agentID := stringutil.TrimDefault(state.OpenClawDMTargetAgentID, state.OpenClawAgentID)
	if roomType == database.RoomTypeDM && agentID != "" {
		info := oc.buildOpenClawDMChatInfo(agentID, title, nil)
		info.Topic = ptr.NonZero(oc.topicForPortal(state))
		info.Type = ptr.Ptr(roomType)
		info.CanBackfill = true
		return info, nil
	}
	return &bridgev2.ChatInfo{
		Name:        ptr.Ptr(title),
		Topic:       ptr.NonZero(oc.topicForPortal(state)),
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

func (oc *OpenClawClient) openClawCapabilityProfile(ctx context.Context, state *openClawPortalState) openClawCapabilityProfile {
	model := oc.effectiveModelChoice(ctx, state)
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
		return sdk.BuildBotUserInfo("OpenClaw"), nil
	}
	loginID, agentID, ok := parseOpenClawGhostID(string(ghost.ID))
	if !ok || (loginID != "" && loginID != oc.UserLogin.ID) {
		return sdk.BuildBotUserInfo("OpenClaw"), nil
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

func (oc *OpenClawClient) displayNameForPortal(state *openClawPortalState) string {
	if state == nil {
		return "OpenClaw"
	}
	if trimmed := strings.TrimSpace(state.OpenClawDMTargetAgentName); trimmed != "" {
		return trimmed
	}
	sourceLabel := openClawSourceLabel(state.OpenClawSpace, state.OpenClawGroupChannel, state.OpenClawSubject)
	candidates := []string{
		state.OpenClawDerivedTitle,
		state.OpenClawDisplayName,
		state.OpenClawSessionLabel,
		sourceLabel,
		state.OpenClawSubject,
		state.LastTo,
		state.OpenClawChannel,
		state.OpenClawSessionKey,
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

func (oc *OpenClawClient) topicForPortal(state *openClawPortalState) string {
	if state == nil {
		return ""
	}
	if strings.TrimSpace(state.OpenClawDMTargetAgentID) != "" || isOpenClawSyntheticDMSessionKey(state.OpenClawSessionKey) {
		return "OpenClaw agent DM"
	}
	parts := make([]string, 0, 8)
	parts = appendDedupedPart(parts, normalizeOpenClawChatType(state.OpenClawChatType))
	parts = appendDedupedPart(parts, state.OpenClawChannel)
	parts = appendDedupedPart(parts, openClawSourceLabel(state.OpenClawSpace, state.OpenClawGroupChannel, state.OpenClawSubject))
	parts = appendDedupedPart(parts, summarizeOpenClawOrigin(state.OpenClawOrigin, state.OpenClawChannel))
	parts = appendDedupedPart(parts, state.ModelProvider)
	parts = appendDedupedPart(parts, state.Model)
	if preview := stringutil.TrimDefault(state.OpenClawPreviewSnippet, state.OpenClawLastMessagePreview); preview != "" {
		parts = appendDedupedPart(parts, "Recent: "+preview)
	}
	if state.HistoryMode != "" {
		parts = appendDedupedPart(parts, "History: "+state.HistoryMode)
	}
	if state.OpenClawToolCount > 0 {
		toolSummary := fmt.Sprintf("Tools: %d", state.OpenClawToolCount)
		if profile := strings.TrimSpace(state.OpenClawToolProfile); profile != "" {
			toolSummary += " (" + profile + ")"
		}
		parts = appendDedupedPart(parts, toolSummary)
	}
	if state.OpenClawKnownModelCount > 0 {
		parts = appendDedupedPart(parts, fmt.Sprintf("Models: %d", state.OpenClawKnownModelCount))
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

func openClawRoomType(state *openClawPortalState) database.RoomType {
	if state == nil {
		return database.RoomTypeDM
	}
	switch normalizeOpenClawChatType(state.OpenClawChatType) {
	case "group", "channel":
		return database.RoomTypeDefault
	}
	if strings.TrimSpace(state.OpenClawSpace) != "" || strings.TrimSpace(state.OpenClawGroupChannel) != "" {
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
		ID: networkid.AvatarID("openclaw:" + string(oc.UserLogin.ID) + ":" + stringutil.TrimDefault(meta.OpenClawAgentID, agentID) + ":" + avatarURL),
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
		Sender:      openClawScopedGhostUserID(oc.UserLogin.ID, agentID),
		SenderLogin: oc.UserLogin.ID,
		ForceDMUser: true,
	}
}

func (oc *OpenClawClient) sendSystemNotice(ctx context.Context, portal *bridgev2.Portal, sender bridgev2.EventSender, msg string) {
	if oc == nil || portal == nil || strings.TrimSpace(msg) == "" {
		return
	}
	if err := sdk.SendSystemMessage(ctx, oc.UserLogin, portal, sender, msg); err != nil {
		if oc.UserLogin != nil {
			oc.UserLogin.Log.Warn().Err(err).Msg("Failed to send system notice")
		}
	}
}

func (oc *OpenClawClient) DownloadAndEncodeMedia(ctx context.Context, mediaURL string, file *event.EncryptedFileInfo, maxMB int) (string, string, error) {
	return sdk.DownloadAndEncodeMedia(ctx, oc.UserLogin, mediaURL, file, maxMB)
}

func (oc *OpenClawClient) sdkAgentForProfile(profile openClawAgentProfile) *sdk.Agent {
	displayName := oc.displayNameFromAgentProfile(profile)
	agentID := strings.TrimSpace(profile.AgentID)
	return &sdk.Agent{
		ID:           string(openClawGhostUserID(agentID)),
		Name:         displayName,
		Description:  "OpenClaw agent",
		AvatarURL:    profile.AvatarURL,
		Identifiers:  oc.configuredAgentIdentifiers(agentID),
		ModelKey:     agentID,
		Capabilities: sdk.BaseAgentCapabilities(),
	}
}

const (
	openClawPairingRequiredError status.BridgeStateErrorCode = "openclaw-pairing-required"
	openClawAuthFailedError      status.BridgeStateErrorCode = "openclaw-auth-failed"
	openClawIncompatibleError    status.BridgeStateErrorCode = "openclaw-incompatible-gateway"
	openClawConnectError         status.BridgeStateErrorCode = "openclaw-connect-error"
	openClawTransientDisconnect  status.BridgeStateErrorCode = "openclaw-transient-disconnect"
	openClawGatewayClosedError   status.BridgeStateErrorCode = "openclaw-gateway-closed"
	openClawMaxReconnectDelay                                = time.Minute
)

func init() {
	status.BridgeStateHumanErrors.Update(status.BridgeStateErrorMap{
		openClawPairingRequiredError: "OpenClaw device pairing is required.",
		openClawAuthFailedError:      "OpenClaw authentication failed. Please relogin.",
		openClawIncompatibleError:    "OpenClaw gateway is incompatible with this bridge version.",
		openClawConnectError:         "Failed to connect to OpenClaw gateway. Retrying.",
		openClawTransientDisconnect:  "Disconnected from OpenClaw gateway. Retrying.",
		openClawGatewayClosedError:   "OpenClaw gateway closed the connection. Retrying.",
	})
}

type openClawCompatibilityError struct {
	Report openClawGatewayCompatibilityReport
}

func (e *openClawCompatibilityError) Error() string {
	if e == nil {
		return "OpenClaw gateway is incompatible"
	}
	parts := make([]string, 0, 3)
	if len(e.Report.MissingMethods) > 0 {
		parts = append(parts, "missing methods: "+strings.Join(e.Report.MissingMethods, ", "))
	}
	if len(e.Report.MissingEvents) > 0 {
		parts = append(parts, "missing events: "+strings.Join(e.Report.MissingEvents, ", "))
	}
	if !e.Report.HistoryEndpointOK {
		if e.Report.HistoryEndpointError != "" {
			parts = append(parts, "history endpoint: "+e.Report.HistoryEndpointError)
		} else if e.Report.HistoryEndpointCode != 0 {
			parts = append(parts, fmt.Sprintf("history endpoint: http %d", e.Report.HistoryEndpointCode))
		}
	}
	if len(parts) == 0 {
		return "OpenClaw gateway is incompatible"
	}
	return "OpenClaw gateway is incompatible: " + strings.Join(parts, "; ")
}

func openClawReconnectDelay(attempt int) time.Duration {
	attempt = max(attempt, 0)
	attempt = min(attempt, 6)
	return min(time.Second*time.Duration(1<<attempt), openClawMaxReconnectDelay)
}

func classifyOpenClawConnectionError(err error, retryDelay time.Duration) (status.BridgeState, bool) {
	state := status.BridgeState{
		StateEvent: status.StateTransientDisconnect,
		Error:      openClawTransientDisconnect,
		Message:    "Disconnected from OpenClaw gateway",
	}
	var rpcErr *gatewayRPCError
	var compatErr *openClawCompatibilityError
	switch {
	case errors.As(err, &compatErr):
		state.StateEvent = status.StateBadCredentials
		state.Error = openClawIncompatibleError
		state.Message = strings.TrimSpace(err.Error())
		state.UserAction = status.UserActionRestart
		if compatErr != nil {
			state.Info = map[string]any{
				"server_version":           compatErr.Report.ServerVersion,
				"missing_methods":          compatErr.Report.MissingMethods,
				"missing_events":           compatErr.Report.MissingEvents,
				"required_missing_methods": compatErr.Report.RequiredMissingMethods,
				"required_missing_events":  compatErr.Report.RequiredMissingEvents,
				"history_endpoint_ok":      compatErr.Report.HistoryEndpointOK,
				"history_endpoint_code":    compatErr.Report.HistoryEndpointCode,
				"history_endpoint_err":     compatErr.Report.HistoryEndpointError,
			}
		}
		return state, false
	case errors.As(err, &rpcErr) && rpcErr.IsPairingRequired():
		state.StateEvent = status.StateBadCredentials
		state.Error = openClawPairingRequiredError
		state.Message = strings.TrimSpace(rpcErr.Error())
		state.UserAction = status.UserActionRestart
		if strings.TrimSpace(rpcErr.RequestID) != "" {
			state.Info = map[string]any{"request_id": strings.TrimSpace(rpcErr.RequestID)}
		}
		return state, false
	case errors.As(err, &rpcErr) && strings.HasPrefix(strings.ToUpper(strings.TrimSpace(rpcErr.DetailCode)), "AUTH_"):
		state.StateEvent = status.StateBadCredentials
		state.Error = openClawAuthFailedError
		state.Message = strings.TrimSpace(rpcErr.Error())
		return state, false
	}

	state.Info = map[string]any{
		"go_error": err.Error(),
	}
	if retryDelay > 0 {
		state.Info["retry_in_ms"] = retryDelay.Milliseconds()
	}
	if closeStatus := websocket.CloseStatus(err); closeStatus != -1 {
		state.Info["websocket_close_status"] = int(closeStatus)
		switch closeStatus {
		case websocket.StatusNormalClosure:
			state.Error = openClawGatewayClosedError
			state.Message = "OpenClaw gateway closed the connection"
		case websocket.StatusPolicyViolation:
			state.Error = openClawConnectError
			state.Message = "OpenClaw gateway rejected the connection"
		}
	}
	if strings.Contains(strings.ToLower(err.Error()), "dial gateway websocket") {
		state.Error = openClawConnectError
		state.Message = "Failed to connect to OpenClaw gateway"
	}
	if retryDelay > 0 {
		state.Message = fmt.Sprintf("%s, retrying in %s", state.Message, retryDelay)
	} else {
		state.Message += ", retrying"
	}
	return state, true
}
