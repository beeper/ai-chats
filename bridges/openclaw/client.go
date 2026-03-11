package openclaw

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/rs/zerolog"
	"go.mau.fi/util/ptr"
	"maunium.net/go/mautrix/bridgev2"
	"maunium.net/go/mautrix/bridgev2/database"
	"maunium.net/go/mautrix/bridgev2/networkid"
	"maunium.net/go/mautrix/bridgev2/status"
	"maunium.net/go/mautrix/event"
	"maunium.net/go/mautrix/id"

	"github.com/beeper/agentremote/pkg/bridgeadapter"
	"github.com/beeper/agentremote/pkg/shared/streamtransport"
	"github.com/beeper/agentremote/pkg/shared/streamui"
)

var _ bridgev2.NetworkAPI = (*OpenClawClient)(nil)
var _ bridgev2.BackfillingNetworkAPI = (*OpenClawClient)(nil)
var _ bridgev2.DeleteChatHandlingNetworkAPI = (*OpenClawClient)(nil)
var _ bridgev2.ReactionHandlingNetworkAPI = (*OpenClawClient)(nil)

const openClawCapabilityBaseID = "com.beeper.ai.capabilities.2026_03_09+openclaw"

var openClawBaseCaps = &event.RoomFeatures{
	ID: openClawCapabilityBaseID,
	File: event.FileFeatureMap{
		event.MsgImage:      openClawRejectedFileFeatures(),
		event.MsgVideo:      openClawRejectedFileFeatures(),
		event.MsgAudio:      openClawRejectedFileFeatures(),
		event.MsgFile:       openClawRejectedFileFeatures(),
		event.CapMsgVoice:   openClawRejectedFileFeatures(),
		event.CapMsgGIF:     openClawRejectedFileFeatures(),
		event.CapMsgSticker: openClawRejectedFileFeatures(),
	},
	MaxTextLength:       100000,
	Reply:               event.CapLevelFullySupported,
	Thread:              event.CapLevelRejected,
	Edit:                event.CapLevelRejected,
	Delete:              event.CapLevelRejected,
	Reaction:            event.CapLevelFullySupported,
	ReadReceipts:        true,
	TypingNotifications: true,
	DeleteChat:          true,
}

type openClawCapabilityProfile struct {
	SupportsVision    bool
	SupportsAudio     bool
	SupportsVideo     bool
	SupportsReasoning bool
	MediaKnown        bool
}

type OpenClawClient struct {
	bridgeadapter.BaseReactionHandler
	UserLogin *bridgev2.UserLogin
	connector *OpenClawConnector

	manager *openClawManager

	connectMu     sync.Mutex
	connectCancel context.CancelFunc
	connectSeq    uint64

	loggedIn atomic.Bool

	agentCatalogMu        sync.RWMutex
	agentCatalog          []gatewayAgentSummary
	agentCatalogDefaultID string
	agentCatalogMainKey   string
	agentCatalogScope     string
	agentCatalogFetchedAt time.Time

	modelCatalogMu        sync.RWMutex
	modelCatalog          []gatewayModelChoice
	modelCatalogFetchedAt time.Time

	toolCatalogMu        sync.RWMutex
	toolCatalog          map[string]gatewayToolsCatalogResponse
	toolCatalogFetchedAt map[string]time.Time

	streamMu                  sync.Mutex
	streamSessions            map[string]*streamtransport.StreamSession
	streamStates              map[string]*openClawStreamState
	streamFallbackToDebounced atomic.Bool
}

type openClawStreamState struct {
	portal                    *bridgev2.Portal
	turnID                    string
	agentID                   string
	sessionKey                string
	messageTS                 time.Time
	placeholderPending        bool
	targetEventID             string
	initialEventID            id.EventID
	networkMessageID          networkid.MessageID
	sequenceNum               int
	accumulated               strings.Builder
	visible                   strings.Builder
	ui                        streamui.UIState
	lastVisibleText           string
	role                      string
	runID                     string
	sessionID                 string
	finishReason              string
	errorText                 string
	promptTokens              int64
	completionTokens          int64
	reasoningTokens           int64
	totalTokens               int64
	startedAtMs               int64
	firstTokenAtMs            int64
	completedAtMs             int64
	streamFallbackToDebounced atomic.Bool
}

func newOpenClawClient(login *bridgev2.UserLogin, connector *OpenClawConnector) (*OpenClawClient, error) {
	if login == nil {
		return nil, errors.New("missing login")
	}
	client := &OpenClawClient{
		UserLogin:            login,
		connector:            connector,
		streamSessions:       make(map[string]*streamtransport.StreamSession),
		streamStates:         make(map[string]*openClawStreamState),
		toolCatalog:          make(map[string]gatewayToolsCatalogResponse),
		toolCatalogFetchedAt: make(map[string]time.Time),
	}
	client.BaseReactionHandler.Target = client
	client.manager = newOpenClawManager(client)
	return client, nil
}

func (oc *OpenClawClient) Connect(ctx context.Context) {
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
	oc.connectMu.Lock()
	cancel := oc.connectCancel
	oc.connectCancel = nil
	oc.connectSeq++
	oc.connectMu.Unlock()
	if cancel != nil {
		cancel()
	}
	if oc.manager != nil {
		oc.manager.Stop()
	}
	oc.loggedIn.Store(false)
	oc.streamMu.Lock()
	sessions := make([]*streamtransport.StreamSession, 0, len(oc.streamSessions))
	for _, s := range oc.streamSessions {
		if s != nil {
			sessions = append(sessions, s)
		}
	}
	oc.streamSessions = make(map[string]*streamtransport.StreamSession)
	oc.streamStates = make(map[string]*openClawStreamState)
	oc.streamMu.Unlock()
	for _, s := range sessions {
		s.End(context.Background(), streamtransport.EndReasonDisconnect)
	}
	if oc.UserLogin != nil {
		oc.UserLogin.BridgeState.Send(status.BridgeState{StateEvent: status.StateTransientDisconnect, Message: "Disconnected"})
	}
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
				oc.loggedIn.Store(false)
			}
			return
		}
		if connected {
			attempt = 0
		}
		retryDelay := openClawReconnectDelay(attempt)
		attempt++
		state, retry := classifyOpenClawConnectionError(err, retryDelay)
		oc.loggedIn.Store(false)
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

func (oc *OpenClawClient) IsLoggedIn() bool { return oc.loggedIn.Load() }

func (oc *OpenClawClient) GetUserLogin() *bridgev2.UserLogin { return oc.UserLogin }

func (oc *OpenClawClient) GetApprovalHandler() bridgeadapter.ApprovalReactionHandler {
	if oc.manager == nil {
		return nil
	}
	return oc.manager.approvalFlow
}

func (oc *OpenClawClient) LogoutRemote(_ context.Context) {}

func (oc *OpenClawClient) IsThisUser(_ context.Context, userID networkid.UserID) bool {
	return userID == humanUserID(oc.UserLogin.ID)
}

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
		for _, msgType := range []event.MessageType{
			event.MsgImage,
			event.MsgVideo,
			event.MsgAudio,
			event.MsgFile,
			event.CapMsgVoice,
			event.CapMsgGIF,
			event.CapMsgSticker,
		} {
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
	agentID := stringsTrimDefault(meta.OpenClawDMTargetAgentID, meta.OpenClawAgentID)
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
	sort.Strings(suffixes)
	return openClawCapabilityBaseID + "+" + strings.Join(suffixes, "+")
}

func (oc *OpenClawClient) GetUserInfo(ctx context.Context, ghost *bridgev2.Ghost) (*bridgev2.UserInfo, error) {
	if ghost == nil {
		return &bridgev2.UserInfo{Name: ptr.Ptr("OpenClaw"), IsBot: ptr.Ptr(true)}, nil
	}
	agentID, ok := parseOpenClawGhostID(string(ghost.ID))
	if !ok {
		return &bridgev2.UserInfo{Name: ptr.Ptr("OpenClaw"), IsBot: ptr.Ptr(true)}, nil
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

func (oc *OpenClawClient) BackgroundContext(ctx context.Context) context.Context {
	if ctx != nil {
		return ctx
	}
	if oc != nil && oc.UserLogin != nil && oc.UserLogin.Bridge != nil && oc.UserLogin.Bridge.BackgroundCtx != nil {
		return oc.UserLogin.Bridge.BackgroundCtx
	}
	return context.Background()
}

func (oc *OpenClawClient) gatewayID() string {
	meta := loginMetadata(oc.UserLogin)
	return openClawGatewayID(meta.GatewayURL, meta.GatewayLabel)
}

func (oc *OpenClawClient) portalKeyForSession(sessionKey string) networkid.PortalKey {
	return openClawPortalKey(oc.UserLogin.ID, oc.gatewayID(), sessionKey)
}

func (oc *OpenClawClient) displayNameForSession(session gatewaySessionRow) string {
	if strings.TrimSpace(session.DerivedTitle) != "" {
		return strings.TrimSpace(session.DerivedTitle)
	}
	if strings.TrimSpace(session.DisplayName) != "" {
		return strings.TrimSpace(session.DisplayName)
	}
	if strings.TrimSpace(session.Label) != "" {
		return strings.TrimSpace(session.Label)
	}
	if sourceLabel := openClawSourceLabel(session.Space, session.GroupChannel, session.Subject); sourceLabel != "" {
		return sourceLabel
	}
	if strings.TrimSpace(session.Subject) != "" {
		return strings.TrimSpace(session.Subject)
	}
	if strings.TrimSpace(session.LastTo) != "" {
		return strings.TrimSpace(session.LastTo)
	}
	if strings.TrimSpace(session.Channel) != "" {
		return strings.TrimSpace(session.Channel)
	}
	if strings.TrimSpace(session.Key) != "" {
		return strings.TrimSpace(session.Key)
	}
	return "OpenClaw"
}

func (oc *OpenClawClient) displayNameForPortal(meta *PortalMetadata) string {
	if meta == nil {
		return "OpenClaw"
	}
	if strings.TrimSpace(meta.OpenClawDMTargetAgentName) != "" {
		return strings.TrimSpace(meta.OpenClawDMTargetAgentName)
	}
	if sourceLabel := openClawSourceLabel(meta.OpenClawSpace, meta.OpenClawGroupChannel, meta.OpenClawSubject); sourceLabel != "" {
		for _, value := range []string{meta.OpenClawDerivedTitle, meta.OpenClawDisplayName, meta.OpenClawSessionLabel, sourceLabel, meta.OpenClawSubject, meta.LastTo, meta.OpenClawChannel, meta.OpenClawSessionKey} {
			if strings.TrimSpace(value) != "" {
				return strings.TrimSpace(value)
			}
		}
		return "OpenClaw"
	}
	for _, value := range []string{meta.OpenClawDerivedTitle, meta.OpenClawDisplayName, meta.OpenClawSessionLabel, meta.OpenClawSubject, meta.LastTo, meta.OpenClawChannel, meta.OpenClawSessionKey} {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return "OpenClaw"
}

func (oc *OpenClawClient) topicForPortal(meta *PortalMetadata) string {
	if meta == nil {
		return ""
	}
	if strings.TrimSpace(meta.OpenClawDMTargetAgentID) != "" || isOpenClawSyntheticDMSessionKey(meta.OpenClawSessionKey) {
		return "OpenClaw agent DM"
	}
	parts := make([]string, 0, 8)
	appendPart := func(value string) {
		value = strings.TrimSpace(value)
		if value == "" {
			return
		}
		for _, existing := range parts {
			if strings.EqualFold(existing, value) {
				return
			}
		}
		parts = append(parts, value)
	}
	appendPart(normalizeOpenClawChatType(meta.OpenClawChatType))
	appendPart(meta.OpenClawChannel)
	appendPart(openClawSourceLabel(meta.OpenClawSpace, meta.OpenClawGroupChannel, meta.OpenClawSubject))
	appendPart(summarizeOpenClawOrigin(meta.OpenClawOrigin, meta.OpenClawChannel))
	appendPart(meta.ModelProvider)
	appendPart(meta.Model)
	if preview := stringsTrimDefault(meta.OpenClawPreviewSnippet, meta.OpenClawLastMessagePreview); strings.TrimSpace(preview) != "" {
		appendPart("Recent: " + strings.TrimSpace(preview))
	}
	if meta.HistoryMode != "" {
		appendPart("History: " + meta.HistoryMode)
	}
	if meta.OpenClawToolCount > 0 {
		toolSummary := "Tools: " + fmt.Sprintf("%d", meta.OpenClawToolCount)
		if profile := strings.TrimSpace(meta.OpenClawToolProfile); profile != "" {
			toolSummary += " (" + profile + ")"
		}
		appendPart(toolSummary)
	}
	if meta.OpenClawKnownModelCount > 0 {
		appendPart(fmt.Sprintf("Models: %d", meta.OpenClawKnownModelCount))
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
	case "direct":
		return database.RoomTypeDM
	case "group", "channel":
		return database.RoomTypeDefault
	}
	if strings.TrimSpace(meta.OpenClawSpace) != "" || strings.TrimSpace(meta.OpenClawGroupChannel) != "" {
		return database.RoomTypeDefault
	}
	if strings.TrimSpace(meta.OpenClawDMTargetAgentID) != "" || isOpenClawSyntheticDMSessionKey(meta.OpenClawSessionKey) {
		return database.RoomTypeDM
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
	if subject != "" {
		return subject
	}
	return ""
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
	var legacy string
	if err := json.Unmarshal([]byte(origin), &legacy); err == nil {
		legacy = strings.TrimSpace(legacy)
		if legacy == "" || strings.EqualFold(legacy, strings.TrimSpace(channel)) {
			return ""
		}
		return compactOpenClawOrigin(legacy)
	}
	var structured map[string]any
	if err := json.Unmarshal([]byte(origin), &structured); err != nil || len(structured) == 0 {
		return compactOpenClawOrigin(origin)
	}
	parts := make([]string, 0, 5)
	appendPart := func(value string) {
		value = strings.TrimSpace(value)
		if value == "" {
			return
		}
		for _, existing := range parts {
			if strings.EqualFold(existing, value) {
				return
			}
		}
		parts = append(parts, value)
	}
	provider := strings.TrimSpace(stringsTrimDefault(stringValue(structured["provider"]), stringValue(structured["source"])))
	if provider != "" && !strings.EqualFold(provider, strings.TrimSpace(channel)) {
		appendPart(provider)
	}
	appendPart(stringsTrimDefault(stringValue(structured["label"]), stringValue(structured["name"])))
	appendPart(stringsTrimDefault(
		stringsTrimDefault(stringValue(structured["workspace"]), stringValue(structured["space"])),
		stringValue(structured["team"]),
	))
	if value := stringsTrimDefault(
		stringsTrimDefault(stringValue(structured["channel"]), stringValue(structured["channelId"])),
		stringValue(structured["groupChannel"]),
	); value != "" {
		appendPart("Channel " + value)
	}
	if value := stringsTrimDefault(stringValue(structured["threadId"]), stringValue(structured["threadID"])); value != "" {
		appendPart("Thread " + value)
	}
	if value := stringsTrimDefault(stringValue(structured["account"]), stringValue(structured["accountId"])); value != "" {
		appendPart("Account " + value)
	}
	if len(parts) == 0 {
		return compactOpenClawOrigin(origin)
	}
	return "Origin: " + strings.Join(parts, " • ")
}

func (oc *OpenClawClient) displayNameForAgent(agentID string) string {
	if strings.TrimSpace(agentID) == "" || strings.EqualFold(strings.TrimSpace(agentID), "gateway") {
		meta := loginMetadata(oc.UserLogin)
		if label := strings.TrimSpace(meta.GatewayLabel); label != "" {
			return label
		}
		return "OpenClaw"
	}
	return strings.TrimSpace(agentID)
}

func (oc *OpenClawClient) formatAgentDisplayName(meta *GhostMetadata, agentID string) string {
	name := ""
	emoji := ""
	if meta != nil {
		name = strings.TrimSpace(meta.OpenClawAgentName)
		emoji = strings.TrimSpace(meta.OpenClawAgentEmoji)
	}
	if name == "" {
		name = oc.displayNameForAgent(agentID)
	}
	if emoji != "" && !strings.HasPrefix(name, emoji) {
		return emoji + " " + name
	}
	return name
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
		ID: networkid.AvatarID("openclaw:" + stringsTrimDefault(meta.OpenClawAgentID, agentID) + ":" + avatarURL),
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
	oc.UserLogin.QueueRemoteEvent(&OpenClawRemoteMessage{
		portal:    portal.PortalKey,
		id:        newOpenClawMessageID(),
		sender:    oc.senderForAgent("gateway", false),
		timestamp: time.Now(),
		preBuilt:  converted,
	})
}

func (oc *OpenClawClient) sendApprovalRequestFallbackEvent(
	ctx context.Context,
	portal *bridgev2.Portal,
	approvalID, toolCallID, toolName, turnID, body string,
	expiresAt time.Time,
) {
	if oc.manager == nil || oc.manager.approvalFlow == nil {
		return
	}
	oc.manager.approvalFlow.SendPrompt(ctx, portal, bridgeadapter.SendPromptParams{
		ApprovalPromptMessageParams: bridgeadapter.ApprovalPromptMessageParams{
			ApprovalID: approvalID,
			ToolCallID: toolCallID,
			ToolName:   toolName,
			TurnID:     turnID,
			Body:       body,
			ExpiresAt:  expiresAt,
		},
		RoomID:    portal.MXID,
		OwnerMXID: oc.UserLogin.UserMXID,
	})
}

func (oc *OpenClawClient) DownloadAndEncodeMedia(ctx context.Context, mediaURL string, file *event.EncryptedFileInfo, maxMB int) (string, string, error) {
	if strings.TrimSpace(mediaURL) == "" {
		return "", "", errors.New("missing media URL")
	}
	if oc == nil || oc.UserLogin == nil || oc.UserLogin.Bridge == nil || oc.UserLogin.Bridge.Bot == nil {
		return "", "", errors.New("bridge is unavailable")
	}
	maxBytes := int64(0)
	if maxMB > 0 {
		maxBytes = int64(maxMB) * 1024 * 1024
	}
	var encoded string
	err := oc.UserLogin.Bridge.Bot.DownloadMediaToFile(ctx, id.ContentURIString(mediaURL), file, false, func(f *os.File) error {
		var reader io.Reader = f
		if maxBytes > 0 {
			reader = io.LimitReader(f, maxBytes+1)
		}
		data, err := io.ReadAll(reader)
		if err != nil {
			return err
		}
		if maxBytes > 0 && int64(len(data)) > maxBytes {
			return errors.New("media exceeds max size")
		}
		encoded = base64.StdEncoding.EncodeToString(data)
		return nil
	})
	if err != nil {
		return "", "", err
	}
	return encoded, "application/octet-stream", nil
}

func stringsTrimDefault(value, fallback string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return fallback
	}
	return value
}
