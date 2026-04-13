package openclaw

import (
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"mime"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/rs/zerolog"
	"go.mau.fi/util/ptr"
	"maunium.net/go/mautrix/bridgev2"
	"maunium.net/go/mautrix/bridgev2/database"
	"maunium.net/go/mautrix/bridgev2/networkid"
	"maunium.net/go/mautrix/bridgev2/simplevent"
	"maunium.net/go/mautrix/bridgev2/status"
	"maunium.net/go/mautrix/event"
	"maunium.net/go/mautrix/id"

	"github.com/beeper/agentremote/pkg/matrixevents"
	"github.com/beeper/agentremote/pkg/shared/backfillutil"
	"github.com/beeper/agentremote/pkg/shared/bridgeutil"
	"github.com/beeper/agentremote/pkg/shared/jsonutil"
	"github.com/beeper/agentremote/pkg/shared/openclawconv"
	"github.com/beeper/agentremote/pkg/shared/streamui"
	"github.com/beeper/agentremote/pkg/shared/stringutil"
	"github.com/beeper/agentremote/sdk"
)

type openClawManager struct {
	client *OpenClawClient

	mu                 sync.RWMutex
	gateway            *gatewayWSClient
	compat             *openClawGatewayCompatibilityReport
	sessions           map[string]gatewaySessionRow
	approvalFlow       *sdk.ApprovalFlow[*openClawPendingApprovalData]
	waiting            map[string]struct{}
	started            map[string]struct{}
	resyncing          map[string]time.Time
	lastEmittedUserMsg map[string]networkid.MessageID
	approvalHints      map[string]openClawPendingApprovalData
	historyCache       map[openClawHistoryCacheKey]openClawHistoryCacheEntry

	cancel context.CancelFunc
}

type openClawHistoryCacheKey struct {
	SessionKey string
	Cursor     string
	Limit      int
}

type openClawHistoryCacheEntry struct {
	CreatedAt time.Time
	ExpiresAt time.Time
	History   *gatewaySessionHistoryResponse
}

type openClawPendingApprovalData struct {
	SessionKey   string
	AgentID      string
	TurnID       string
	ToolCallID   string
	ToolName     string
	Command      string
	Presentation sdk.ApprovalPromptPresentation
	Recovered    bool
	CreatedAtMs  int64
	ExpiresAtMs  int64
}

func newOpenClawManager(client *OpenClawClient) *openClawManager {
	mgr := &openClawManager{
		client:             client,
		sessions:           make(map[string]gatewaySessionRow),
		waiting:            make(map[string]struct{}),
		started:            make(map[string]struct{}),
		resyncing:          make(map[string]time.Time),
		lastEmittedUserMsg: make(map[string]networkid.MessageID),
		approvalHints:      make(map[string]openClawPendingApprovalData),
		historyCache:       make(map[openClawHistoryCacheKey]openClawHistoryCacheEntry),
	}
	mgr.approvalFlow = sdk.NewApprovalFlow(sdk.ApprovalFlowConfig[*openClawPendingApprovalData]{
		Login:    func() *bridgev2.UserLogin { return client.UserLogin },
		Sender:   func(portal *bridgev2.Portal) bridgev2.EventSender { return mgr.approvalSenderForPortal(portal) },
		IDPrefix: "openclaw",
		LogKey:   "openclaw_msg_id",
		RoomIDFromData: func(data *openClawPendingApprovalData) id.RoomID {
			// OpenClaw validates by session key, not room ID directly.
			return ""
		},
		DeliverDecision: func(ctx context.Context, portal *bridgev2.Portal, pending *sdk.Pending[*openClawPendingApprovalData], decision sdk.ApprovalDecisionPayload) error {
			gateway, err := mgr.requireGateway()
			if err != nil {
				return err
			}
			data := pending.Data
			if data != nil {
				state, err := loadOpenClawPortalState(ctx, portal, client.UserLogin)
				if err != nil {
					return err
				}
				if strings.TrimSpace(data.SessionKey) != strings.TrimSpace(state.OpenClawSessionKey) {
					return sdk.ErrApprovalWrongRoom
				}
			}
			return gateway.ResolveApproval(ctx, decision.ApprovalID,
				sdk.DecisionToString(decision, "allow-once", "allow-always", "deny"))
		},
		SendNotice: func(ctx context.Context, portal *bridgev2.Portal, msg string) {
			client.sendSystemNotice(ctx, portal, mgr.approvalSenderForPortal(portal), msg)
		},
		DBMetadata: func(prompt sdk.ApprovalPromptMessage) any {
			return &MessageMetadata{
				BaseMessageMetadata: sdk.BaseMessageMetadata{
					Role:               "assistant",
					ExcludeFromHistory: true,
				},
			}
		},
	})
	return mgr
}

func openClawSessionLogContext(session gatewaySessionRow) func(zerolog.Context) zerolog.Context {
	return func(c zerolog.Context) zerolog.Context {
		return c.Str("session_key", session.Key).Str("session_id", session.SessionID)
	}
}

func openClawSessionNeedsBackfill(session gatewaySessionRow, latestMessage *database.Message) (bool, error) {
	latestSessionTS := openClawSessionTimestamp(session)
	if latestMessage == nil {
		return !latestSessionTS.IsZero() || strings.TrimSpace(session.LastMessagePreview) != "", nil
	} else if latestSessionTS.IsZero() {
		return false, nil
	}
	return latestSessionTS.After(latestMessage.Timestamp), nil
}

func buildOpenClawSessionResyncEvent(client *OpenClawClient, session gatewaySessionRow) *simplevent.ChatResync {
	return &simplevent.ChatResync{
		EventMeta: simplevent.EventMeta{
			Type:         bridgev2.RemoteEventChatResync,
			PortalKey:    client.portalKeyForSession(session.Key),
			CreatePortal: true,
			Timestamp:    openClawSessionTimestamp(session),
			LogContext:   openClawSessionLogContext(session),
		},
		GetChatInfoFunc: func(ctx context.Context, portal *bridgev2.Portal) (*bridgev2.ChatInfo, error) {
			return getOpenClawSessionChatInfo(ctx, portal, client, session)
		},
		CheckNeedsBackfillFunc: func(_ context.Context, latestMessage *database.Message) (bool, error) {
			return openClawSessionNeedsBackfill(session, latestMessage)
		},
	}
}

func getOpenClawSessionChatInfo(ctx context.Context, portal *bridgev2.Portal, client *OpenClawClient, session gatewaySessionRow) (*bridgev2.ChatInfo, error) {
	if portal == nil {
		return nil, fmt.Errorf("missing portal")
	}
	state, err := loadOpenClawPortalState(ctx, portal, client.UserLogin)
	if err != nil {
		return nil, err
	}
	previous := *state
	state.OpenClawGatewayID = client.gatewayID()
	state.OpenClawSessionID = session.SessionID
	state.OpenClawSessionKey = session.Key
	state.OpenClawSpawnedBy = session.SpawnedBy
	state.OpenClawSessionKind = session.Kind
	state.OpenClawSessionLabel = session.Label
	state.OpenClawDisplayName = session.DisplayName
	state.OpenClawDerivedTitle = session.DerivedTitle
	state.OpenClawLastMessagePreview = session.LastMessagePreview
	state.OpenClawChannel = session.Channel
	state.OpenClawSubject = session.Subject
	state.OpenClawGroupChannel = session.GroupChannel
	state.OpenClawSpace = session.Space
	state.OpenClawChatType = session.ChatType
	state.OpenClawOrigin = session.OriginString()
	state.OpenClawAgentID = stringutil.TrimDefault(state.OpenClawAgentID, openclawconv.AgentIDFromSessionKey(session.Key))
	if isOpenClawSyntheticDMSessionKey(session.Key) {
		state.OpenClawDMTargetAgentID = stringutil.TrimDefault(state.OpenClawDMTargetAgentID, openclawconv.AgentIDFromSessionKey(session.Key))
	}
	state.OpenClawSystemSent = session.SystemSent
	state.OpenClawAbortedLastRun = session.AbortedLastRun
	state.ThinkingLevel = session.ThinkingLevel
	state.FastMode = session.FastMode
	state.VerboseLevel = session.VerboseLevel
	state.ReasoningLevel = session.ReasoningLevel
	state.ElevatedLevel = session.ElevatedLevel
	state.SendPolicy = session.SendPolicy
	state.InputTokens = session.InputTokens
	state.OutputTokens = session.OutputTokens
	state.TotalTokens = session.TotalTokens
	state.TotalTokensFresh = session.TotalTokensFresh
	state.EstimatedCostUSD = session.EstimatedCostUSD
	state.Status = session.Status
	state.StartedAt = session.StartedAt
	state.EndedAt = session.EndedAt
	state.RuntimeMs = session.RuntimeMs
	state.ParentSessionKey = session.ParentSessionKey
	state.ChildSessions = append(state.ChildSessions[:0], session.ChildSessions...)
	state.ResponseUsage = session.ResponseUsage
	state.ModelProvider = session.ModelProvider
	state.Model = session.Model
	state.ContextTokens = session.ContextTokens
	state.DeliveryContext = session.DeliveryContext
	state.LastChannel = session.LastChannel
	state.LastTo = session.LastTo
	state.LastAccountID = session.LastAccountID
	state.SessionUpdatedAt = session.UpdatedAt
	if strings.TrimSpace(state.BackgroundBackfillStatus) == "" {
		state.BackgroundBackfillStatus = "pending"
	}
	if err := saveOpenClawPortalState(ctx, portal, client.UserLogin, state); err != nil {
		return nil, err
	}
	portalMeta(portal).IsOpenClawRoom = true

	agentID := stringutil.TrimDefault(state.OpenClawAgentID, "gateway")
	if strings.TrimSpace(state.OpenClawDMTargetAgentID) != "" {
		agentID = strings.TrimSpace(state.OpenClawDMTargetAgentID)
		state.OpenClawAgentID = agentID
	}
	identity := client.lookupAgentIdentity(ctx, agentID, session.Key)
	if identity != nil && strings.TrimSpace(identity.AgentID) != "" {
		agentID = strings.TrimSpace(identity.AgentID)
		state.OpenClawAgentID = agentID
	}
	configured, err := client.agentCatalogEntryByID(ctx, agentID)
	if err != nil {
		client.Log().Debug().Err(err).Str("agent_id", agentID).Msg("Failed to refresh OpenClaw agent catalog during session resync")
	}
	profile := client.resolveAgentProfile(ctx, agentID, session.Key, nil, configured)
	agentName := client.displayNameFromAgentProfile(profile)
	if strings.TrimSpace(state.OpenClawDMTargetAgentName) == "" && strings.TrimSpace(state.OpenClawDMTargetAgentID) == agentID {
		state.OpenClawDMTargetAgentName = agentName
	}
	presentation := client.deriveRoomPresentation(state, client.displayNameForSession(session), client.roomPresentationSummary(ctx, state))
	client.maybeRefreshPortalCapabilities(ctx, portal, &previous, state)
	if presentation.RoomType == database.RoomTypeDM {
		return bridgeutil.BuildLoginDMChatInfo(bridgeutil.LoginDMChatInfoParams{
			Title:          presentation.Title,
			Topic:          presentation.Topic,
			Login:          client.UserLogin,
			HumanUserID:    humanUserID(client.UserLogin.ID),
			HumanSender:    ptr.Ptr(client.senderForAgent(agentID, true)),
			BotUserID:      openClawGhostUserID(agentID),
			BotDisplayName: agentName,
			BotSender:      ptr.Ptr(client.senderForAgent(agentID, false)),
			BotUserInfo:    client.userInfoForAgentProfile(profile),
			CanBackfill:    true,
		}), nil
	}
	memberMap := bridgev2.ChatMemberMap{
		humanUserID(client.UserLogin.ID): {
			EventSender: client.senderForAgent(agentID, true),
		},
		openClawGhostUserID(agentID): {
			EventSender: client.senderForAgent(agentID, false),
			UserInfo:    client.userInfoForAgentProfile(profile),
		},
	}
	return &bridgev2.ChatInfo{
		Type:        ptr.Ptr(presentation.RoomType),
		Name:        ptr.Ptr(presentation.Title),
		Topic:       ptr.NonZero(presentation.Topic),
		CanBackfill: true,
		Members: &bridgev2.ChatMemberList{
			IsFull:    true,
			MemberMap: memberMap,
		},
	}, nil
}

var (
	openClawRequiredGatewayMethods = []string{
		"sessions.list",
		"chat.send",
	}
	openClawPreferredGatewayMethods = []string{
		"sessions.list",
		"sessions.resolve",
		"chat.send",
		"chat.abort",
		"agents.list",
		"models.list",
		"agent.identity.get",
		"exec.approval.list",
		"exec.approval.resolve",
		"agent.wait",
	}
	openClawRequiredGatewayEvents = []string{
		"chat",
	}
	openClawPreferredGatewayEvents = []string{
		"chat",
		"agent",
		"exec.approval.requested",
		"exec.approval.resolved",
	}
)

const (
	openClawHistoryCacheTTL            = 45 * time.Second
	openClawHistoryCacheMaxEntries     = 128
	openClawBackgroundBackfillSettle   = 2 * time.Second
	openClawBackgroundBackfillPasses   = 3
	openClawBackgroundBackfillInterval = 8 * time.Second
)

func (m *openClawManager) Start(ctx context.Context) (bool, error) {
	meta := loginMetadata(m.client.UserLogin)
	cfg := gatewayConnectConfig{
		URL:         meta.GatewayURL,
		Token:       meta.GatewayToken,
		Password:    meta.GatewayPassword,
		DeviceToken: meta.DeviceToken,
	}
	gw := newGatewayWSClient(cfg)
	deviceToken, err := gw.Connect(ctx)
	if err != nil {
		return false, err
	}
	if deviceToken != "" && deviceToken != meta.DeviceToken {
		meta.DeviceToken = deviceToken
		if err := m.client.UserLogin.Save(ctx); err != nil {
			return false, err
		}
	}
	runCtx, cancel := context.WithCancel(ctx)
	started := false
	defer func() {
		cancel()
		if !started || ctx.Err() == nil {
			gw.Close()
		}
		m.mu.Lock()
		if m.gateway == gw {
			m.gateway = nil
		}
		m.cancel = nil
		m.started = make(map[string]struct{})
		m.resyncing = make(map[string]time.Time)
		m.approvalHints = make(map[string]openClawPendingApprovalData)
		m.historyCache = make(map[openClawHistoryCacheKey]openClawHistoryCacheEntry)
		m.mu.Unlock()
	}()
	m.mu.Lock()
	m.gateway = gw
	m.compat = nil
	m.cancel = cancel
	m.mu.Unlock()
	report, compatErr := m.validateGatewayCompatibility(ctx, gw)
	m.mu.Lock()
	m.compat = report
	m.mu.Unlock()
	if compatErr != nil {
		return false, compatErr
	}
	if report != nil && (!report.HistoryEndpointOK || len(report.MissingMethods) > 0 || len(report.MissingEvents) > 0) {
		m.client.Log().Warn().
			Str("server_version", report.ServerVersion).
			Strs("missing_methods", report.MissingMethods).
			Strs("missing_events", report.MissingEvents).
			Bool("history_endpoint_ok", report.HistoryEndpointOK).
			Int("history_endpoint_code", report.HistoryEndpointCode).
			Str("history_endpoint_error", report.HistoryEndpointError).
			Msg("OpenClaw gateway connected with compatibility fallbacks")
	}
	if err = m.syncSessions(ctx); err != nil {
		return false, err
	}
	if err = m.rehydratePendingApprovals(ctx); err != nil {
		return false, err
	}
	m.seedBackgroundBackfill(ctx)
	if _, err := m.client.loadAgentCatalog(m.client.BackgroundContext(ctx), true); err != nil {
		m.client.Log().Debug().Err(err).Msg("Failed to refresh OpenClaw agent catalog on connect")
	}
	if _, err := m.client.loadModelCatalog(m.client.BackgroundContext(ctx), true); err != nil {
		m.client.Log().Debug().Err(err).Msg("Failed to refresh OpenClaw model catalog on connect")
	}
	m.client.SetLoggedIn(true)
	m.client.UserLogin.BridgeState.Send(status.BridgeState{StateEvent: status.StateConnected, Message: "Connected"})
	started = true
	m.eventLoop(runCtx, gw.Events())
	if ctx.Err() != nil {
		return true, nil
	}
	if err := gw.LastError(); err != nil {
		return true, err
	}
	return true, errors.New("gateway connection closed")
}

func (m *openClawManager) Stop() {
	m.mu.Lock()
	cancel := m.cancel
	gateway := m.gateway
	m.cancel = nil
	m.gateway = nil
	m.started = make(map[string]struct{})
	m.resyncing = make(map[string]time.Time)
	m.approvalHints = make(map[string]openClawPendingApprovalData)
	m.historyCache = make(map[openClawHistoryCacheKey]openClawHistoryCacheEntry)
	m.compat = nil
	m.mu.Unlock()
	if cancel != nil {
		cancel()
	}
	if gateway != nil {
		gateway.Close()
	}
}

func (m *openClawManager) syncSessions(ctx context.Context) error {
	gateway := m.gatewayClient()
	if gateway == nil {
		return errors.New("gateway client is unavailable")
	}
	sessions, err := gateway.ListSessions(ctx, 0)
	if err != nil {
		return err
	}
	m.mu.Lock()
	refreshed := make(map[string]gatewaySessionRow, len(sessions))
	for _, session := range sessions {
		refreshed[session.Key] = session
		delete(m.resyncing, session.Key)
	}
	m.sessions = refreshed
	m.mu.Unlock()
	for _, session := range sessions {
		m.client.UserLogin.QueueRemoteEvent(buildOpenClawSessionResyncEvent(m.client, session))
	}
	meta := loginMetadata(m.client.UserLogin)
	meta.SessionsSynced = true
	meta.LastSyncAt = time.Now().UnixMilli()
	return m.client.UserLogin.Save(ctx)
}

func (m *openClawManager) validateGatewayCompatibility(ctx context.Context, gateway *gatewayWSClient) (*openClawGatewayCompatibilityReport, error) {
	report := &openClawGatewayCompatibilityReport{}
	if gateway == nil {
		return report, &openClawCompatibilityError{Report: *report}
	}
	hello := gateway.Hello()
	if hello == nil {
		report.HistoryEndpointError = "missing gateway hello payload"
		return report, &openClawCompatibilityError{Report: *report}
	}
	if version := strings.TrimSpace(stringValue(hello.Server["version"])); version != "" {
		report.ServerVersion = version
	}
	report.RequiredMissingMethods = findMissingGatewayFeatures(hello.Features.Methods, openClawRequiredGatewayMethods)
	report.RequiredMissingEvents = findMissingGatewayFeatures(hello.Features.Events, openClawRequiredGatewayEvents)
	report.MissingMethods = findMissingGatewayFeatures(hello.Features.Methods, openClawPreferredGatewayMethods)
	report.MissingEvents = findMissingGatewayFeatures(hello.Features.Events, openClawPreferredGatewayEvents)
	historyProbe := gateway.ProbeSessionHistory(ctx)
	report.HistoryEndpointOK = historyProbe.HistoryEndpointOK
	report.HistoryEndpointCode = historyProbe.HistoryEndpointCode
	report.HistoryEndpointError = historyProbe.HistoryEndpointError
	if report.Compatible() {
		return report, nil
	}
	return report, &openClawCompatibilityError{Report: *report}
}

func findMissingGatewayFeatures(have, required []string) []string {
	seen := make(map[string]struct{}, len(have))
	for _, item := range have {
		if trimmed := strings.TrimSpace(item); trimmed != "" {
			seen[strings.ToLower(trimmed)] = struct{}{}
		}
	}
	var missing []string
	for _, item := range required {
		if _, ok := seen[strings.ToLower(strings.TrimSpace(item))]; !ok {
			missing = append(missing, item)
		}
	}
	return missing
}

func (m *openClawManager) rehydratePendingApprovals(ctx context.Context) error {
	gateway, err := m.requireGateway()
	if err != nil {
		return err
	}
	approvals, err := gateway.ListPendingApprovals(ctx)
	if err != nil {
		return err
	}
	upstream := make(map[string]gatewayApprovalRequestEvent, len(approvals))
	for _, approval := range approvals {
		approval.ID = strings.TrimSpace(approval.ID)
		if approval.ID == "" {
			continue
		}
		upstream[approval.ID] = approval
		m.handleApprovalRequest(ctx, approval)
	}
	for _, approvalID := range m.approvalFlow.PendingIDs() {
		if _, ok := upstream[approvalID]; ok {
			continue
		}
		m.expireLocalApproval(ctx, approvalID)
	}
	return nil
}

func (m *openClawManager) expireLocalApproval(ctx context.Context, approvalID string) {
	approvalID = strings.TrimSpace(approvalID)
	if approvalID == "" {
		return
	}
	pending := m.approvalFlow.Get(approvalID)
	sessionKey := ""
	if pending != nil && pending.Data != nil {
		sessionKey = strings.TrimSpace(pending.Data.SessionKey)
	}
	if sessionKey == "" {
		sessionKey = strings.TrimSpace(m.approvalHint(approvalID).SessionKey)
	}
	if sessionKey != "" {
		if portal := m.resolvePortal(ctx, sessionKey); portal != nil && portal.MXID != "" {
			m.client.sendSystemNotice(ctx, portal, m.approvalSenderForPortal(portal), "OpenClaw approval expired")
		}
	}
	m.approvalFlow.ResolveExternal(ctx, approvalID, sdk.ApprovalDecisionPayload{
		ApprovalID: approvalID,
		Approved:   false,
		Reason:     "expired",
		ResolvedBy: sdk.ApprovalResolutionOriginAgent,
	})
	m.clearApprovalHint(approvalID)
}

func (m *openClawManager) seedBackgroundBackfill(ctx context.Context) {
	if m == nil || m.client == nil || m.client.UserLogin == nil {
		return
	}
	if !m.client.UserLogin.Bridge.Config.Backfill.Enabled || !m.client.UserLogin.Bridge.Config.Backfill.Queue.Enabled {
		return
	}
	sessions := m.sortedSessionsByActivity()
	if len(sessions) == 0 {
		return
	}
	go func() {
		timer := time.NewTimer(openClawBackgroundBackfillSettle)
		defer timer.Stop()
		select {
		case <-ctx.Done():
			return
		case <-timer.C:
		}
		for pass := 0; pass < openClawBackgroundBackfillPasses; pass++ {
			if ctx.Err() != nil {
				return
			}
			for _, session := range sessions {
				if ctx.Err() != nil {
					return
				}
				m.ensureBackgroundBackfillTask(ctx, session.Key)
			}
			m.client.UserLogin.Bridge.WakeupBackfillQueue()
			if pass == openClawBackgroundBackfillPasses-1 {
				return
			}
			timer.Reset(openClawBackgroundBackfillInterval)
			select {
			case <-ctx.Done():
				return
			case <-timer.C:
			}
		}
	}()
}

func (m *openClawManager) sortedSessionsByActivity() []gatewaySessionRow {
	m.mu.RLock()
	defer m.mu.RUnlock()
	sessions := make([]gatewaySessionRow, 0, len(m.sessions))
	for _, session := range m.sessions {
		sessions = append(sessions, session)
	}
	sort.SliceStable(sessions, func(i, j int) bool {
		if sessions[i].UpdatedAt != sessions[j].UpdatedAt {
			return sessions[i].UpdatedAt > sessions[j].UpdatedAt
		}
		return strings.TrimSpace(sessions[i].Key) < strings.TrimSpace(sessions[j].Key)
	})
	return sessions
}

func (m *openClawManager) ensureBackgroundBackfillTask(ctx context.Context, sessionKey string) {
	sessionKey = strings.TrimSpace(sessionKey)
	if sessionKey == "" || m.client == nil || m.client.UserLogin == nil {
		return
	}
	key := m.client.portalKeyForSession(sessionKey)
	portal, err := m.client.UserLogin.Bridge.GetExistingPortalByKey(ctx, key)
	if err != nil || portal == nil || portal.MXID == "" {
		return
	}
	state, err := loadOpenClawPortalState(ctx, portal, m.client.UserLogin)
	if err != nil {
		return
	}
	if strings.TrimSpace(state.BackgroundBackfillStatus) == "" || state.BackgroundBackfillStatus == "failed" {
		state.BackgroundBackfillStatus = "pending"
		state.BackgroundBackfillError = ""
		_ = saveOpenClawPortalState(ctx, portal, m.client.UserLogin, state)
	}
	if err = m.client.UserLogin.Bridge.DB.BackfillTask.EnsureExists(ctx, portal.PortalKey, m.client.UserLogin.ID); err != nil {
		return
	}
	_ = m.client.UserLogin.Bridge.DB.BackfillTask.MarkNotDone(ctx, portal.PortalKey, m.client.UserLogin.ID)
}

func (m *openClawManager) gatewayClient() *gatewayWSClient {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.gateway
}

func (m *openClawManager) approvalSenderForPortal(portal *bridgev2.Portal) bridgev2.EventSender {
	if portal == nil {
		return m.client.senderForAgent("gateway", false)
	}
	state, err := loadOpenClawPortalState(m.client.BackgroundContext(context.Background()), portal, m.client.UserLogin)
	if err != nil {
		return m.client.senderForAgent("gateway", false)
	}
	agentID := strings.TrimSpace(state.OpenClawDMTargetAgentID)
	if agentID == "" {
		agentID = resolveOpenClawAgentID(state, state.OpenClawSessionKey, nil)
	}
	if agentID == "" {
		agentID = "gateway"
	}
	return m.client.senderForAgent(agentID, false)
}

func (m *openClawManager) discoveredAgentIDs() []string {
	if m == nil {
		return nil
	}
	m.mu.RLock()
	defer m.mu.RUnlock()
	if len(m.sessions) == 0 {
		return nil
	}
	seen := make(map[string]struct{}, len(m.sessions))
	agentIDs := make([]string, 0, len(m.sessions))
	for _, session := range m.sessions {
		agentID := strings.TrimSpace(openclawconv.AgentIDFromSessionKey(session.Key))
		if agentID == "" {
			continue
		}
		key := strings.ToLower(agentID)
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		agentIDs = append(agentIDs, agentID)
	}
	sort.Strings(agentIDs)
	return agentIDs
}

func (m *openClawManager) requireGateway() (*gatewayWSClient, error) {
	gateway := m.gatewayClient()
	if gateway == nil {
		return nil, errors.New("gateway client is unavailable")
	}
	return gateway, nil
}

func (m *openClawManager) trackWaitingRun(runID string) bool {
	runID = strings.TrimSpace(runID)
	if runID == "" {
		return false
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, exists := m.waiting[runID]; exists {
		return false
	}
	m.waiting[runID] = struct{}{}
	return true
}

func (m *openClawManager) untrackWaitingRun(runID string) {
	runID = strings.TrimSpace(runID)
	if runID == "" {
		return
	}
	m.mu.Lock()
	delete(m.waiting, runID)
	m.mu.Unlock()
}

func (m *openClawManager) forgetSession(sessionKey string) {
	sessionKey = strings.TrimSpace(sessionKey)
	if sessionKey == "" {
		return
	}
	m.mu.Lock()
	delete(m.sessions, sessionKey)
	delete(m.resyncing, sessionKey)
	m.mu.Unlock()
}

func (m *openClawManager) HandleMatrixMessage(ctx context.Context, msg *bridgev2.MatrixMessage) (*bridgev2.MatrixMessageResponse, error) {
	gateway, err := m.requireGateway()
	if err != nil {
		return nil, err
	}
	state, err := loadOpenClawPortalState(ctx, msg.Portal, m.client.UserLogin)
	if err != nil {
		return nil, err
	}
	attachments, text, err := m.buildOutboundPayload(ctx, msg)
	if err != nil {
		return nil, err
	}
	if text == "" && len(attachments) == 0 {
		return &bridgev2.MatrixMessageResponse{Pending: false}, nil
	}
	sessionKey := strings.TrimSpace(state.OpenClawSessionKey)
	if state.OpenClawDMCreatedFromContact && state.OpenClawSessionID == "" && isOpenClawSyntheticDMSessionKey(state.OpenClawSessionKey) {
		if resolvedKey, err := gateway.ResolveSessionKey(ctx, state.OpenClawSessionKey); err == nil {
			resolvedKey = strings.TrimSpace(resolvedKey)
			if resolvedKey != "" {
				updated := *state
				updated.OpenClawSessionKey = resolvedKey
				if err := saveOpenClawPortalState(ctx, msg.Portal, m.client.UserLogin, &updated); err != nil {
					return nil, err
				}
				state.OpenClawSessionKey = resolvedKey
				sessionKey = resolvedKey
			}
		}
	}
	_, err = gateway.SendMessage(
		ctx,
		sessionKey,
		text,
		attachments,
		state.ThinkingLevel,
		state.VerboseLevel,
		string(msg.Event.ID),
	)
	if err != nil {
		return nil, err
	}
	if state.OpenClawDMCreatedFromContact && state.OpenClawSessionID == "" && isOpenClawSyntheticDMSessionKey(state.OpenClawSessionKey) {
		go func() {
			if err := m.syncSessions(m.client.BackgroundContext(ctx)); err != nil {
				m.client.Log().Debug().Err(err).Str("session_key", sessionKey).Msg("Failed to refresh OpenClaw sessions after synthetic DM message")
			}
		}()
	}
	return &bridgev2.MatrixMessageResponse{Pending: true}, nil
}

func (m *openClawManager) buildOutboundPayload(ctx context.Context, msg *bridgev2.MatrixMessage) ([]map[string]any, string, error) {
	content := msg.Content
	msgType := content.MsgType
	if msg.Event.Type == event.EventSticker {
		msgType = event.MsgImage
	}
	switch msgType {
	case event.MsgText, event.MsgNotice, event.MsgEmote:
		return nil, strings.TrimSpace(content.Body), nil
	case event.MsgImage, event.MsgVideo, event.MsgAudio, event.MsgFile:
		mediaURL := string(content.URL)
		if mediaURL == "" && content.File != nil {
			mediaURL = string(content.File.URL)
		}
		if mediaURL == "" {
			return nil, "", errors.New("missing media URL")
		}
		encoded, mimeType, err := m.client.DownloadAndEncodeMedia(ctx, mediaURL, content.File, 50)
		if err != nil {
			return nil, "", err
		}
		if content.Info != nil && strings.TrimSpace(content.Info.MimeType) != "" {
			mimeType = strings.TrimSpace(content.Info.MimeType)
		}
		if mimeType == "" {
			mimeType = "application/octet-stream"
		}
		fileName := strings.TrimSpace(content.FileName)
		if fileName == "" {
			exts, _ := mime.ExtensionsByType(mimeType)
			if len(exts) > 0 {
				fileName = "file" + exts[0]
			} else {
				fileName = "file"
			}
		}
		text := strings.TrimSpace(content.Body)
		if text == fileName {
			text = ""
		}
		return []map[string]any{{
			"type":     "file",
			"mimeType": mimeType,
			"fileName": fileName,
			"content":  encoded,
		}}, text, nil
	default:
		return nil, "", fmt.Errorf("unsupported message type %s", msgType)
	}
}

func (m *openClawManager) FetchMessages(ctx context.Context, params bridgev2.FetchMessagesParams) (*bridgev2.FetchMessagesResponse, error) {
	gateway, err := m.requireGateway()
	if err != nil {
		return nil, err
	}
	state, err := loadOpenClawPortalState(ctx, params.Portal, m.client.UserLogin)
	if err != nil {
		return nil, err
	}
	m.markBackgroundBackfillFetch(params.Portal, state, params.Task)
	var (
		entries          []openClawBackfillEntry
		cursor           networkid.PaginationCursor
		hasMore          bool
		approxTotalCount int
	)
	cursorMode, cursorSeq := parseOpenClawHistoryCursor(params.Cursor)
	if params.Forward || params.AnchorMessage != nil || cursorMode == openClawForwardHistoryCursorPrefix {
		allMessages, loadErr := m.loadAllHistoryMessages(ctx, gateway, state.OpenClawSessionKey)
		if loadErr != nil {
			m.markBackgroundBackfillError(params.Portal, state, params.Task, loadErr)
			m.saveHistoryPortalState(ctx, params.Portal, state, "after history fetch error")
			return nil, loadErr
		}
		allEntries := prepareOpenClawBackfillEntries(state, allMessages)
		entries, cursor, hasMore = paginateOpenClawBackfillEntries(allEntries, params, cursorMode, cursorSeq)
		approxTotalCount = len(allEntries)
	} else {
		history, historyErr := m.loadBackwardHistoryPage(ctx, gateway, state.OpenClawSessionKey, normalizeHistoryLimit(params.Count), formatOpenClawBackwardCursor(cursorSeq), params.Task == nil)
		if historyErr != nil {
			m.markBackgroundBackfillError(params.Portal, state, params.Task, historyErr)
			m.saveHistoryPortalState(ctx, params.Portal, state, "after history fetch error")
			return nil, historyErr
		}
		entries = prepareOpenClawBackfillEntries(state, history.Messages)
		hasMore = history.HasMore
		cursor = networkid.PaginationCursor(openClawBackwardCursor(parseOpenClawCursorSeq(history.NextCursor)))
		if len(entries) > 0 && cursor == "" && hasMore {
			cursor = networkid.PaginationCursor(openClawBackwardCursor(entries[0].sequence))
		}
		if len(entries) > 0 {
			if newestSeq := entries[len(entries)-1].sequence; newestSeq > 0 {
				approxTotalCount = int(newestSeq)
			}
		}
	}
	backfill := make([]*bridgev2.BackfillMessage, 0, len(entries))
	for _, entry := range entries {
		converted, sender, messageID := m.convertHistoryMessage(ctx, params.Portal, state, entry.message)
		if converted == nil || messageID == "" {
			continue
		}
		backfill = append(backfill, &bridgev2.BackfillMessage{
			ConvertedMessage: converted,
			Sender:           sender,
			ID:               messageID,
			TxnID:            networkid.TransactionID(messageID),
			Timestamp:        entry.timestamp,
			StreamOrder:      entry.streamOrder,
		})
	}
	state.LastHistorySyncAt = time.Now().UnixMilli()
	m.completeBackgroundBackfillFetch(params.Portal, state, params.Task, cursor, hasMore)
	m.saveHistoryPortalState(ctx, params.Portal, state, "after history fetch")
	if params.Task == nil && !params.Forward && params.AnchorMessage == nil && hasMore && strings.TrimSpace(string(cursor)) != "" {
		go m.prefetchBackwardHistoryPage(m.client.BackgroundContext(ctx), state.OpenClawSessionKey, normalizeHistoryLimit(params.Count), formatOpenClawBackwardCursor(parseOpenClawCursorSeq(string(cursor))))
	}
	return &bridgev2.FetchMessagesResponse{
		Messages:                backfill,
		Cursor:                  cursor,
		HasMore:                 hasMore,
		Forward:                 params.Forward,
		AggressiveDeduplication: true,
		ApproxTotalCount:        approxTotalCount,
	}, nil
}

const (
	openClawBackwardHistoryCursorPrefix = "seq:"
	openClawForwardHistoryCursorPrefix  = "after:"
)

type openClawBackfillEntry struct {
	message     map[string]any
	messageID   networkid.MessageID
	timestamp   time.Time
	streamOrder int64
	sequence    int64
}

func paginateOpenClawBackfillEntries(entries []openClawBackfillEntry, params bridgev2.FetchMessagesParams, cursorMode string, cursorSeq int64) ([]openClawBackfillEntry, networkid.PaginationCursor, bool) {
	if len(entries) == 0 {
		return nil, "", false
	}
	if params.Forward {
		start := 0
		switch {
		case cursorMode == openClawForwardHistoryCursorPrefix && cursorSeq > 0:
			start = sort.Search(len(entries), func(i int) bool {
				return entries[i].sequence > cursorSeq
			})
		case params.AnchorMessage != nil:
			if idx, ok := findOpenClawAnchorIndex(entries, params.AnchorMessage); ok {
				start = idx + 1
			} else {
				start = backfillutil.IndexAtOrAfter(len(entries), func(i int) time.Time {
					return entries[i].timestamp
				}, params.AnchorMessage.Timestamp)
			}
		}
		if start >= len(entries) {
			return nil, "", false
		}
		end := min(len(entries), start+normalizeHistoryLimit(params.Count))
		hasMore := end < len(entries)
		cursor := networkid.PaginationCursor("")
		if hasMore && entries[end-1].sequence > 0 {
			cursor = networkid.PaginationCursor(openClawForwardCursor(entries[end-1].sequence))
		}
		return entries[start:end], cursor, hasMore
	}
	if params.AnchorMessage != nil || (cursorMode == openClawBackwardHistoryCursorPrefix && cursorSeq > 0) {
		var end int
		if cursorMode == openClawBackwardHistoryCursorPrefix && cursorSeq > 0 {
			end = sort.Search(len(entries), func(i int) bool {
				return entries[i].sequence >= cursorSeq
			})
		} else if idx, ok := findOpenClawAnchorIndex(entries, params.AnchorMessage); ok {
			end = idx
		} else {
			end = backfillutil.IndexAtOrAfter(len(entries), func(i int) time.Time {
				return entries[i].timestamp
			}, params.AnchorMessage.Timestamp)
		}
		if end <= 0 {
			return nil, "", false
		}
		start := max(0, end-normalizeHistoryLimit(params.Count))
		hasMore := start > 0
		cursor := networkid.PaginationCursor("")
		if hasMore && entries[start].sequence > 0 {
			cursor = networkid.PaginationCursor(openClawBackwardCursor(entries[start].sequence))
		}
		return entries[start:end], cursor, hasMore
	}
	result := backfillutil.Paginate(
		len(entries),
		backfillutil.PaginateParams{
			Count:              normalizeHistoryLimit(params.Count),
			Forward:            params.Forward,
			Cursor:             params.Cursor,
			AnchorMessage:      params.AnchorMessage,
			ForwardAnchorShift: 1,
		},
		func(anchor *database.Message) (int, bool) {
			return findOpenClawAnchorIndex(entries, anchor)
		},
		func(anchor *database.Message) int {
			return backfillutil.IndexAtOrAfter(len(entries), func(i int) time.Time {
				return entries[i].timestamp
			}, anchor.Timestamp)
		},
	)
	return entries[result.Start:result.End], result.Cursor, result.HasMore
}

func parseOpenClawHistoryCursor(cursor networkid.PaginationCursor) (string, int64) {
	trimmed := strings.TrimSpace(string(cursor))
	switch {
	case strings.HasPrefix(trimmed, openClawForwardHistoryCursorPrefix):
		return openClawForwardHistoryCursorPrefix, parseOpenClawCursorSeq(strings.TrimPrefix(trimmed, openClawForwardHistoryCursorPrefix))
	case trimmed != "":
		return openClawBackwardHistoryCursorPrefix, parseOpenClawCursorSeq(trimmed)
	default:
		return "", 0
	}
}

func parseOpenClawCursorSeq(raw string) int64 {
	raw = strings.TrimSpace(strings.TrimPrefix(raw, openClawBackwardHistoryCursorPrefix))
	if raw == "" {
		return 0
	}
	value, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || value <= 0 {
		return 0
	}
	return value
}

func formatOpenClawBackwardCursor(seq int64) string {
	if seq <= 0 {
		return ""
	}
	return strconv.FormatInt(seq, 10)
}

func openClawBackwardCursor(seq int64) string {
	if seq <= 0 {
		return ""
	}
	return openClawBackwardHistoryCursorPrefix + strconv.FormatInt(seq, 10)
}

func openClawForwardCursor(seq int64) string {
	if seq <= 0 {
		return ""
	}
	return openClawForwardHistoryCursorPrefix + strconv.FormatInt(seq, 10)
}

func prepareOpenClawBackfillEntries(state *openClawPortalState, history []map[string]any) []openClawBackfillEntry {
	entries := make([]openClawBackfillEntry, 0, len(history))
	for _, message := range history {
		if message == nil {
			continue
		}
		normalized := normalizeOpenClawLiveMessage(0, message)
		if len(normalized) == 0 {
			continue
		}
		timestamp := extractMessageTimestamp(normalized)
		role := openClawMessageRole(normalized)
		text := openclawconv.ExtractMessageText(normalized)
		if role == "toolresult" && strings.TrimSpace(text) == "" {
			if details, ok := normalized["details"]; ok && details != nil {
				if data, err := json.Marshal(details); err == nil {
					text = string(data)
				}
			}
		}
		messageID := historyFingerprintMessageID(state.OpenClawSessionKey, role, timestamp, text, normalized)
		sequence := openClawHistoryMessageSeq(normalized)
		entries = append(entries, openClawBackfillEntry{
			message:   normalized,
			messageID: messageID,
			timestamp: timestamp,
			sequence:  sequence,
		})
	}
	sort.SliceStable(entries, func(i, j int) bool {
		if entries[i].sequence > 0 && entries[j].sequence > 0 && entries[i].sequence != entries[j].sequence {
			return entries[i].sequence < entries[j].sequence
		}
		if c := entries[i].timestamp.Compare(entries[j].timestamp); c != 0 {
			return c < 0
		}
		return cmp.Compare(entries[i].messageID, entries[j].messageID) < 0
	})
	var lastStreamOrder int64
	for i := range entries {
		if entries[i].sequence > 0 {
			entries[i].streamOrder = entries[i].sequence
			lastStreamOrder = entries[i].streamOrder
			continue
		}
		lastStreamOrder = backfillutil.NextStreamOrder(lastStreamOrder, entries[i].timestamp)
		entries[i].streamOrder = lastStreamOrder
	}
	return entries
}

func findOpenClawAnchorIndex(entries []openClawBackfillEntry, anchor *database.Message) (int, bool) {
	if anchor == nil || anchor.ID == "" {
		return 0, false
	}
	for idx, entry := range entries {
		if entry.messageID == anchor.ID {
			return idx, true
		}
	}
	return 0, false
}

func normalizeHistoryLimit(count int) int {
	if count <= 0 {
		return openClawMaxHistoryPageLimit
	}
	return min(count, openClawMaxHistoryPageLimit)
}

func (m *openClawManager) loadBackwardHistoryPage(ctx context.Context, gateway *gatewayWSClient, sessionKey string, limit int, cursor string, allowCache bool) (*gatewaySessionHistoryResponse, error) {
	limit = normalizeHistoryLimit(limit)
	cacheKey := openClawHistoryCacheKey{
		SessionKey: strings.TrimSpace(sessionKey),
		Cursor:     strings.TrimSpace(cursor),
		Limit:      limit,
	}
	if allowCache {
		if history := m.cachedBackwardHistoryPage(cacheKey); history != nil {
			return history, nil
		}
	}
	history, err := gateway.SessionHistory(ctx, sessionKey, limit, cursor)
	if err != nil {
		return nil, err
	}
	if allowCache {
		m.storeBackwardHistoryPage(cacheKey, history)
	}
	return history, nil
}

func (m *openClawManager) cachedBackwardHistoryPage(key openClawHistoryCacheKey) *gatewaySessionHistoryResponse {
	now := time.Now()
	m.mu.Lock()
	defer m.mu.Unlock()
	entry, ok := m.historyCache[key]
	if !ok {
		return nil
	}
	if now.After(entry.ExpiresAt) {
		delete(m.historyCache, key)
		return nil
	}
	return cloneGatewaySessionHistory(entry.History)
}

func (m *openClawManager) storeBackwardHistoryPage(key openClawHistoryCacheKey, history *gatewaySessionHistoryResponse) {
	if history == nil {
		return
	}
	now := time.Now()
	m.mu.Lock()
	defer m.mu.Unlock()
	m.historyCache[key] = openClawHistoryCacheEntry{
		CreatedAt: now,
		ExpiresAt: now.Add(openClawHistoryCacheTTL),
		History:   cloneGatewaySessionHistory(history),
	}
	if len(m.historyCache) <= openClawHistoryCacheMaxEntries {
		return
	}
	var (
		oldestKey   openClawHistoryCacheKey
		oldestEntry openClawHistoryCacheEntry
		found       bool
	)
	for candidateKey, candidateEntry := range m.historyCache {
		if !found || candidateEntry.CreatedAt.Before(oldestEntry.CreatedAt) {
			oldestKey = candidateKey
			oldestEntry = candidateEntry
			found = true
		}
	}
	if found {
		delete(m.historyCache, oldestKey)
	}
}

func cloneGatewaySessionHistory(history *gatewaySessionHistoryResponse) *gatewaySessionHistoryResponse {
	if history == nil {
		return nil
	}
	clone := *history
	if len(history.Messages) > 0 {
		clone.Messages = make([]map[string]any, len(history.Messages))
		for i, message := range history.Messages {
			clone.Messages[i] = jsonutil.DeepCloneMap(message)
		}
	}
	if len(history.Items) > 0 {
		clone.Items = make([]map[string]any, len(history.Items))
		for i, item := range history.Items {
			clone.Items[i] = jsonutil.DeepCloneMap(item)
		}
	}
	return &clone
}

func (m *openClawManager) prefetchBackwardHistoryPage(ctx context.Context, sessionKey string, limit int, cursor string) {
	gateway := m.gatewayClient()
	if gateway == nil {
		return
	}
	cacheKey := openClawHistoryCacheKey{
		SessionKey: strings.TrimSpace(sessionKey),
		Cursor:     strings.TrimSpace(cursor),
		Limit:      normalizeHistoryLimit(limit),
	}
	if m.cachedBackwardHistoryPage(cacheKey) != nil {
		return
	}
	history, err := gateway.SessionHistory(ctx, sessionKey, cacheKey.Limit, cursor)
	if err != nil {
		return
	}
	m.storeBackwardHistoryPage(cacheKey, history)
}

func (m *openClawManager) invalidateHistoryCache(sessionKey string) {
	sessionKey = strings.TrimSpace(sessionKey)
	if sessionKey == "" {
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	for key := range m.historyCache {
		if key.SessionKey == sessionKey {
			delete(m.historyCache, key)
		}
	}
}

func (m *openClawManager) saveHistoryPortalState(ctx context.Context, portal *bridgev2.Portal, state *openClawPortalState, action string) {
	if portal == nil || state == nil {
		return
	}
	if err := saveOpenClawPortalState(ctx, portal, m.client.UserLogin, state); err != nil {
		m.client.Log().Warn().Err(err).Str("session_key", strings.TrimSpace(state.OpenClawSessionKey)).Msg("Failed saving OpenClaw portal state " + action)
	}
}

func (m *openClawManager) loadAllHistoryMessages(ctx context.Context, gateway *gatewayWSClient, sessionKey string) ([]map[string]any, error) {
	cursor := ""
	prevCursor := ""
	pages := make([][]map[string]any, 0, 4)
	for {
		history, err := gateway.SessionHistory(ctx, sessionKey, openClawMaxHistoryPageLimit, cursor)
		if err != nil {
			return nil, err
		}
		if history == nil || len(history.Messages) == 0 {
			break
		}
		pages = append(pages, history.Messages)
		nextCursor := strings.TrimSpace(history.NextCursor)
		if !history.HasMore || nextCursor == "" {
			break
		}
		currentCursor := strings.TrimSpace(cursor)
		if nextCursor == currentCursor || (prevCursor != "" && nextCursor == prevCursor) {
			break
		}
		prevCursor = currentCursor
		cursor = nextCursor
	}
	total := 0
	for _, page := range pages {
		total += len(page)
	}
	all := make([]map[string]any, 0, total)
	for i := len(pages) - 1; i >= 0; i-- {
		all = append(all, pages[i]...)
	}
	return all, nil
}

func (m *openClawManager) markBackgroundBackfillFetch(portal *bridgev2.Portal, state *openClawPortalState, task *database.BackfillTask) {
	if portal == nil || state == nil || task == nil {
		return
	}
	now := time.Now().UnixMilli()
	if state.BackgroundBackfillStartedAt == 0 {
		state.BackgroundBackfillStartedAt = now
	}
	state.BackgroundBackfillStatus = "running"
	state.BackgroundBackfillError = ""
	state.BackgroundBackfillCursor = strings.TrimSpace(string(task.Cursor))
}

func (m *openClawManager) completeBackgroundBackfillFetch(portal *bridgev2.Portal, state *openClawPortalState, task *database.BackfillTask, cursor networkid.PaginationCursor, hasMore bool) {
	if portal == nil || state == nil || task == nil {
		return
	}
	state.BackgroundBackfillCursor = strings.TrimSpace(string(cursor))
	state.BackgroundBackfillError = ""
	if hasMore {
		state.BackgroundBackfillStatus = "running"
		return
	}
	state.BackgroundBackfillStatus = "complete"
	state.BackgroundBackfillCompletedAt = time.Now().UnixMilli()
	state.BackgroundBackfillCursor = ""
}

func (m *openClawManager) markBackgroundBackfillError(portal *bridgev2.Portal, state *openClawPortalState, task *database.BackfillTask, err error) {
	if portal == nil || state == nil || task == nil || err == nil {
		return
	}
	state.BackgroundBackfillStatus = "failed"
	state.BackgroundBackfillError = strings.TrimSpace(err.Error())
}

func openClawHistoryMessageSeq(message map[string]any) int64 {
	meta := jsonutil.ToMap(message["__openclaw"])
	switch seq := meta["seq"].(type) {
	case int:
		return int64(seq)
	case int64:
		return seq
	case float64:
		if seq > 0 {
			return int64(seq)
		}
	case string:
		value, err := strconv.ParseInt(strings.TrimSpace(seq), 10, 64)
		if err == nil && value > 0 {
			return value
		}
	}
	return 0
}

func (m *openClawManager) convertHistoryMessage(ctx context.Context, portal *bridgev2.Portal, state *openClawPortalState, message map[string]any) (*bridgev2.ConvertedMessage, bridgev2.EventSender, networkid.MessageID) {
	message = normalizeOpenClawLiveMessage(0, message)
	if len(message) == 0 {
		return nil, bridgev2.EventSender{}, ""
	}
	role := openClawMessageRole(message)
	text := openclawconv.ExtractMessageText(message)
	attachmentBlocks := openclawconv.ExtractAttachmentBlocks(message)
	if role == "toolresult" && strings.TrimSpace(text) == "" {
		if details, ok := message["details"]; ok && details != nil {
			if data, err := json.Marshal(details); err == nil {
				text = string(data)
			}
		}
	}
	agentID := resolveOpenClawAgentID(state, state.OpenClawSessionKey, message)
	sender := m.client.senderForAgent(agentID, false)
	if role == "user" {
		sender = m.client.senderForAgent("", true)
	}
	ts := extractMessageTimestamp(message)
	messageID := historyFingerprintMessageID(state.OpenClawSessionKey, role, ts, text, message)
	uiParts, uiMetadata := convertHistoryToCanonicalUI(message, role, state)
	if len(uiParts) == 0 && strings.TrimSpace(text) == "" && len(attachmentBlocks) == 0 {
		return nil, bridgev2.EventSender{}, ""
	}
	parts := make([]*bridgev2.ConvertedMessagePart, 0, 1+len(attachmentBlocks))
	if strings.TrimSpace(text) != "" {
		parts = append(parts, &bridgev2.ConvertedMessagePart{
			ID:      networkid.PartID("0"),
			Type:    event.EventMessage,
			Content: &event.MessageEventContent{MsgType: event.MsgText, Body: text, Mentions: &event.Mentions{}},
		})
	} else if len(uiParts) > 0 {
		fallbackText := openClawHistoryFallbackText(uiParts)
		if fallbackText != "" {
			parts = append(parts, &bridgev2.ConvertedMessagePart{
				ID:      networkid.PartID("0"),
				Type:    event.EventMessage,
				Content: &event.MessageEventContent{MsgType: event.MsgNotice, Body: fallbackText, Mentions: &event.Mentions{}},
			})
		}
	}
	for idx, block := range attachmentBlocks {
		uploaded, err := m.client.buildOpenClawAttachmentContent(ctx, portal, block)
		if err != nil {
			fallbackText := openClawAttachmentFallbackText(block, err)
			parts = append(parts, &bridgev2.ConvertedMessagePart{
				ID:      networkid.PartID(fmt.Sprintf("attachment-fallback-%d", idx)),
				Type:    event.EventMessage,
				Content: &event.MessageEventContent{MsgType: event.MsgNotice, Body: fallbackText, Mentions: &event.Mentions{}},
			})
			uiParts = append(uiParts, map[string]any{"type": "text", "text": fallbackText, "state": "done"})
			continue
		}
		parts = append(parts, &bridgev2.ConvertedMessagePart{
			ID:      networkid.PartID(fmt.Sprintf("attachment-%d", idx)),
			Type:    event.EventMessage,
			Content: uploaded.Content,
			Extra:   uploaded.Metadata,
		})
		uiPart := map[string]any{
			"type":      "file",
			"mediaType": uploaded.Content.Info.MimeType,
			"filename":  uploaded.Content.FileName,
		}
		if uploaded.MatrixURL != "" {
			uiPart["url"] = uploaded.MatrixURL
		}
		uiParts = append(uiParts, uiPart)
	}
	if len(parts) == 0 {
		return nil, bridgev2.EventSender{}, ""
	}
	uiRole := "assistant"
	if role == "user" {
		uiRole = "user"
	}
	uiTurnID := strings.TrimSpace(stringValue(uiMetadata["turn_id"]))
	uiMessage := sdk.BuildUIMessage(sdk.UIMessageParams{
		TurnID:   uiTurnID,
		Role:     uiRole,
		Metadata: uiMetadata,
		Parts:    uiParts,
	})
	parts[0].DBMetadata = buildOpenClawHistoryMessageMetadata(message, state, role, agentID, text, attachmentBlocks, uiMetadata, uiMessage)
	parts[0].Extra[matrixevents.BeeperAIKey] = uiMessage
	return &bridgev2.ConvertedMessage{Parts: parts}, sender, messageID
}

func buildOpenClawHistoryMessageMetadata(message map[string]any, state *openClawPortalState, role, agentID, text string, attachmentBlocks []map[string]any, uiMetadata, uiMessage map[string]any) *MessageMetadata {
	snapshot := sdk.BuildTurnSnapshot(uiMessage, sdk.TurnDataBuildOptions{
		ID:       strings.TrimSpace(stringValue(uiMetadata["turn_id"])),
		Role:     strings.TrimSpace(role),
		Text:     strings.TrimSpace(text),
		Metadata: jsonutil.DeepCloneMap(uiMetadata),
	}, "openclaw")
	metadata := buildOpenClawMessageMetadata(openClawMessageMetadataParams{
		Base: sdk.BuildBaseMetadataFromSnapshot(sdk.BaseSnapshotMetadataParams{
			Snapshot: snapshot,
			Role:     role,
			AgentID:  agentID,
		}),
		SessionID:   state.OpenClawSessionID,
		SessionKey:  state.OpenClawSessionKey,
		Attachments: attachmentBlocks,
	})
	if value := strings.TrimSpace(stringValue(uiMetadata["completion_id"])); value != "" {
		metadata.RunID = value
	}
	if value := strings.TrimSpace(stringValue(uiMetadata["turn_id"])); value != "" {
		metadata.TurnID = value
	}
	if value := strings.TrimSpace(stringValue(uiMetadata["finish_reason"])); value != "" {
		metadata.FinishReason = value
	}
	if value := strings.TrimSpace(stringValue(uiMetadata["error_text"])); value != "" {
		metadata.ErrorText = value
	}
	applyUsageToMessageMetadata(jsonutil.ToMap(uiMetadata["usage"]), metadata)
	return metadata
}

func historyFingerprintMessageID(sessionKey, role string, ts time.Time, text string, raw map[string]any) networkid.MessageID {
	hashSource := map[string]any{
		"sessionKey":   sessionKey,
		"role":         role,
		"timestamp":    ts.UnixMilli(),
		"text":         text,
		"attachments":  openclawconv.ExtractAttachmentBlocks(raw),
		"turnId":       historyMessageTurnID(raw),
		"messageId":    openClawMessageStringField(raw, "id"),
		"messageRunId": openClawMessageStringField(raw, "runId", "run_id"),
	}
	data, _ := json.Marshal(hashSource)
	return networkid.MessageID("openclaw:" + stringutil.ShortHash(string(data), 12))
}

func openClawStreamMessageMetadata(state *openClawPortalState, payload gatewayChatEvent, agentID, turnID string) map[string]any {
	params := sdk.UIMessageMetadataParams{
		TurnID:       turnID,
		AgentID:      agentID,
		CompletionID: payload.RunID,
		FinishReason: stringutil.TrimDefault(strings.TrimSpace(payload.StopReason), strings.TrimSpace(payload.State)),
		IncludeUsage: true,
	}
	applyNormalizedUsageToParams(normalizeOpenClawUsage(payload.Usage), &params)
	return buildOpenClawUIMessageMetadata(params,
		stringutil.TrimDefault(stringValue(payload.Message["sessionId"]), state.OpenClawSessionID),
		stringutil.TrimDefault(payload.SessionKey, state.OpenClawSessionKey),
		openClawErrorText(payload),
	)
}

func normalizeOpenClawUsage(raw map[string]any) map[string]any {
	if len(raw) == 0 {
		return nil
	}
	normalized := make(map[string]any, 3)
	if value, ok := openClawUsageNumber(raw, "prompt_tokens", "promptTokens", "inputTokens", "input_tokens", "input"); ok {
		normalized["prompt_tokens"] = int64(value)
	}
	if value, ok := openClawUsageNumber(raw, "completion_tokens", "completionTokens", "outputTokens", "output_tokens", "output"); ok {
		normalized["completion_tokens"] = int64(value)
	}
	if value, ok := openClawUsageNumber(raw, "reasoning_tokens", "reasoningTokens", "reasoning_tokens"); ok {
		normalized["reasoning_tokens"] = int64(value)
	}
	if value, ok := openClawUsageNumber(raw, "total_tokens", "totalTokens", "total"); ok {
		normalized["total_tokens"] = int64(value)
	}
	if len(normalized) == 0 {
		return nil
	}
	return normalized
}

func openClawUsageNumber(raw map[string]any, keys ...string) (float64, bool) {
	for _, key := range keys {
		switch typed := raw[key].(type) {
		case int:
			return float64(typed), true
		case int64:
			return float64(typed), true
		case float64:
			return typed, true
		case json.Number:
			if value, err := typed.Float64(); err == nil {
				return value, true
			}
		}
	}
	return 0, false
}

func openClawUsageInt64(raw map[string]any, key string) (int64, bool) {
	value, ok := openClawUsageNumber(raw, key)
	return int64(value), ok
}

type parsedTokenUsage struct {
	PromptTokens     int64
	CompletionTokens int64
	ReasoningTokens  int64
	TotalTokens      int64
}

func parseTokenUsage(usage map[string]any) parsedTokenUsage {
	var out parsedTokenUsage
	if len(usage) == 0 {
		return out
	}
	if value, ok := openClawUsageInt64(usage, "prompt_tokens"); ok {
		out.PromptTokens = value
	}
	if value, ok := openClawUsageInt64(usage, "completion_tokens"); ok {
		out.CompletionTokens = value
	}
	if value, ok := openClawUsageInt64(usage, "reasoning_tokens"); ok {
		out.ReasoningTokens = value
	}
	if value, ok := openClawUsageInt64(usage, "total_tokens"); ok {
		out.TotalTokens = value
	}
	return out
}

func applyUsageToMessageMetadata(usage map[string]any, metadata *MessageMetadata) {
	if len(usage) == 0 || metadata == nil {
		return
	}
	parsed := parseTokenUsage(usage)
	metadata.PromptTokens = parsed.PromptTokens
	metadata.CompletionTokens = parsed.CompletionTokens
	metadata.ReasoningTokens = parsed.ReasoningTokens
	metadata.TotalTokens = parsed.TotalTokens
}

func maybeUpdatePreviewSnippet(state *openClawPortalState, text string, eventTS time.Time) bool {
	trimmed := strings.TrimSpace(text)
	if trimmed == "" {
		return false
	}
	state.OpenClawLastMessagePreview = trimmed
	return true
}

func applyNormalizedUsageToParams(usage map[string]any, params *sdk.UIMessageMetadataParams) {
	if len(usage) == 0 {
		return
	}
	parsed := parseTokenUsage(usage)
	params.PromptTokens = parsed.PromptTokens
	params.CompletionTokens = parsed.CompletionTokens
	params.ReasoningTokens = parsed.ReasoningTokens
	params.TotalTokens = parsed.TotalTokens
}

func openClawErrorText(payload gatewayChatEvent) string {
	return stringutil.TrimDefault(payload.ErrorMessage, strings.TrimSpace(payload.StopReason))
}

func extractOpenClawEventTimestamp(eventTS int64, message map[string]any) time.Time {
	if ts := extractMessageTimestamp(message); !ts.IsZero() && !ts.Equal(openClawMissingMessageTimestamp) {
		return ts
	}
	if eventTS > 0 {
		return time.UnixMilli(eventTS)
	}
	return time.Time{}
}

func normalizeOpenClawLiveMessage(eventTS int64, message map[string]any) map[string]any {
	if len(message) == 0 {
		return nil
	}
	normalized := make(map[string]any, len(message)+1)
	for key, value := range message {
		normalized[key] = value
	}
	if nested := jsonutil.ToMap(normalized["message"]); len(nested) > 0 {
		for _, key := range []string{
			"role",
			"text",
			"content",
			"timestamp",
			"turnId",
			"turn_id",
			"runId",
			"run_id",
			"id",
			"sessionKey",
			"session_key",
			"sessionId",
			"session_id",
			"agentId",
			"agent_id",
			"agent",
			"usage",
			"model",
			"stopReason",
			"stop_reason",
			"error",
			"errorMessage",
		} {
			if _, has := normalized[key]; has {
				continue
			}
			if value, ok := nested[key]; ok {
				normalized[key] = value
			}
		}
	}
	if _, ok := normalized["timestamp"]; !ok && eventTS > 0 {
		normalized["timestamp"] = eventTS
	}
	return normalized
}

func isOpenClawDirectChatEvent(message map[string]any) bool {
	if len(message) == 0 {
		return false
	}
	return openClawMessageRole(message) == "user"
}

func (m *openClawManager) eventLoop(ctx context.Context, events <-chan gatewayEvent) {
	for {
		select {
		case <-ctx.Done():
			return
		case evt, ok := <-events:
			if !ok {
				return
			}
			m.handleEvent(ctx, evt)
		}
	}
}

func (m *openClawManager) handleEvent(ctx context.Context, evt gatewayEvent) {
	switch evt.Name {
	case "chat":
		var payload gatewayChatEvent
		if err := json.Unmarshal(evt.Payload, &payload); err == nil {
			m.handleChatEvent(ctx, payload)
		}
	case "agent":
		var payload gatewayAgentEvent
		if err := json.Unmarshal(evt.Payload, &payload); err == nil {
			m.handleAgentEvent(ctx, payload)
		}
	case "exec.approval.requested":
		var payload gatewayApprovalRequestEvent
		if err := json.Unmarshal(evt.Payload, &payload); err == nil {
			m.handleApprovalRequest(ctx, payload)
		}
	case "exec.approval.resolved":
		var payload gatewayApprovalResolvedEvent
		if err := json.Unmarshal(evt.Payload, &payload); err == nil {
			m.handleApprovalResolved(ctx, payload)
		}
	}
}

func (m *openClawManager) handleChatEvent(ctx context.Context, payload gatewayChatEvent) {
	if strings.TrimSpace(payload.SessionKey) == "" {
		return
	}
	portal := m.resolvePortal(ctx, payload.SessionKey)
	if portal == nil || portal.MXID == "" {
		return
	}
	state, err := loadOpenClawPortalState(ctx, portal, m.client.UserLogin)
	if err != nil {
		m.client.Log().Debug().Err(err).Str("session_key", payload.SessionKey).Msg("Failed to load OpenClaw portal state for chat event")
		return
	}
	payload.Message = normalizeOpenClawLiveMessage(payload.TS, payload.Message)
	eventTS := extractOpenClawEventTimestamp(payload.TS, payload.Message)
	if isOpenClawDirectChatEvent(payload.Message) {
		m.handleDirectChatEvent(ctx, portal, state, payload, eventTS)
		return
	}
	isTerminal := openClawIsTerminalChatState(payload.State)
	agentID := resolveOpenClawAgentID(state, payload.SessionKey, payload.Message)
	turnID := strings.TrimSpace(payload.RunID)
	if turnID == "" {
		return
	}
	messageMetadata := openClawStreamMessageMetadata(state, payload, agentID, turnID)
	if payload.State == "delta" {
		m.ensureStreamStart(ctx, portal, state, turnID, payload.RunID, agentID, eventTS, messageMetadata, &payload)
		m.startRunRecovery(ctx, portal, turnID, payload.RunID, agentID)
		text := openclawconv.ExtractMessageText(payload.Message)
		delta := m.client.computeVisibleDelta(turnID, text)
		if delta != "" {
			m.client.EmitStreamPart(ctx, portal, turnID, agentID, payload.SessionKey, map[string]any{
				"timestamp": eventTS.UnixMilli(),
				"type":      "text-delta",
				"id":        "text-" + turnID,
				"delta":     delta,
			})
		}
		return
	}
	if isTerminal {
		m.invalidateHistoryCache(payload.SessionKey)
		m.ensureStreamStart(ctx, portal, state, turnID, payload.RunID, agentID, eventTS, messageMetadata, &payload)
		if usage := normalizeOpenClawUsage(payload.Usage); len(usage) > 0 {
			reasoningTokens := int64(0)
			if value, ok := openClawUsageInt64(usage, "prompt_tokens"); ok {
				state.InputTokens = value
			}
			if value, ok := openClawUsageInt64(usage, "completion_tokens"); ok {
				state.OutputTokens = value
			}
			if value, ok := openClawUsageInt64(usage, "reasoning_tokens"); ok {
				reasoningTokens = value
			}
			if value, ok := openClawUsageInt64(usage, "total_tokens"); ok {
				state.TotalTokens = value
			} else {
				state.TotalTokens = state.InputTokens + state.OutputTokens + reasoningTokens
			}
			state.TotalTokensFresh = true
		}
		text := openclawconv.ExtractMessageText(payload.Message)
		maybeUpdatePreviewSnippet(state, text, eventTS)
		if delta := m.client.computeVisibleDelta(turnID, text); delta != "" {
			m.client.EmitStreamPart(ctx, portal, turnID, agentID, payload.SessionKey, map[string]any{
				"timestamp": eventTS.UnixMilli(),
				"type":      "text-delta",
				"id":        "text-" + turnID,
				"delta":     delta,
			})
		}
		if payload.State == "error" {
			m.client.EmitStreamPart(ctx, portal, turnID, agentID, payload.SessionKey, map[string]any{
				"timestamp": eventTS.UnixMilli(),
				"type":      "error",
				"errorText": openClawErrorText(payload),
			})
		} else if payload.State == "aborted" {
			m.client.EmitStreamPart(ctx, portal, turnID, agentID, payload.SessionKey, map[string]any{
				"timestamp": eventTS.UnixMilli(),
				"type":      "abort",
				"reason":    stringutil.TrimDefault(payload.StopReason, "aborted"),
			})
		}
		m.client.EmitStreamPart(ctx, portal, turnID, agentID, payload.SessionKey, map[string]any{
			"timestamp":       eventTS.UnixMilli(),
			"type":            "finish",
			"finishReason":    payload.State,
			"errorText":       openClawErrorText(payload),
			"messageMetadata": messageMetadata,
		})
		m.clearStartedTurn(turnID)
		m.untrackWaitingRun(payload.RunID)
		state.LastLiveSeq = payload.Seq
		_ = saveOpenClawPortalState(ctx, portal, m.client.UserLogin, state)
	}
}

func (m *openClawManager) handleDirectChatEvent(ctx context.Context, portal *bridgev2.Portal, state *openClawPortalState, payload gatewayChatEvent, eventTS time.Time) {
	converted, sender, messageID := m.convertHistoryMessage(ctx, portal, state, payload.Message)
	if converted == nil || messageID == "" {
		return
	}
	m.invalidateHistoryCache(payload.SessionKey)
	m.client.UserLogin.QueueRemoteEvent(sdk.BuildPreConvertedRemoteMessage(sdk.PreConvertedRemoteMessageParams{
		PortalKey:   portal.PortalKey,
		Sender:      sender,
		MsgID:       messageID,
		LogKey:      "openclaw_msg_id",
		Timestamp:   eventTS,
		StreamOrder: payload.Seq * 2,
		Converted:   converted,
	}))
	if maybeUpdatePreviewSnippet(state, openclawconv.ExtractMessageText(payload.Message), eventTS) {
		_ = saveOpenClawPortalState(ctx, portal, m.client.UserLogin, state)
	}
}

func (m *openClawManager) emitLatestUserMessageFromHistory(ctx context.Context, portal *bridgev2.Portal, state *openClawPortalState, payload gatewayChatEvent) {
	gateway := m.gatewayClient()
	if gateway == nil || portal == nil {
		return
	}
	history, err := gateway.SessionHistory(ctx, payload.SessionKey, 8, "")
	if err != nil || history == nil || len(history.Messages) == 0 {
		return
	}
	for idx := len(history.Messages) - 1; idx >= 0; idx-- {
		message := normalizeOpenClawLiveMessage(payload.TS, history.Messages[idx])
		if !shouldMirrorLatestUserMessageFromHistory(payload, message) {
			continue
		}
		converted, sender, messageID := m.convertHistoryMessage(ctx, portal, state, message)
		if converted == nil || messageID == "" {
			continue
		}
		m.mu.Lock()
		if m.lastEmittedUserMsg[payload.SessionKey] == messageID {
			m.mu.Unlock()
			return
		}
		m.lastEmittedUserMsg[payload.SessionKey] = messageID
		m.mu.Unlock()
		eventTS := extractOpenClawEventTimestamp(payload.TS, message)
		m.client.UserLogin.QueueRemoteEvent(sdk.BuildPreConvertedRemoteMessage(sdk.PreConvertedRemoteMessageParams{
			PortalKey:   portal.PortalKey,
			Sender:      sender,
			MsgID:       messageID,
			LogKey:      "openclaw_msg_id",
			Timestamp:   eventTS,
			StreamOrder: payload.Seq*2 - 1,
			Converted:   converted,
		}))
		if maybeUpdatePreviewSnippet(state, openclawconv.ExtractMessageText(message), eventTS) {
			_ = saveOpenClawPortalState(ctx, portal, m.client.UserLogin, state)
		}
		return
	}
}

const openClawHistoryMirrorFallbackWindow = 15 * time.Minute

func shouldMirrorLatestUserMessageFromHistory(payload gatewayChatEvent, message map[string]any) bool {
	if openClawMessageRole(message) != "user" {
		return false
	}
	idempotencyKey := openClawMessageIdempotencyKey(message)
	if isLikelyMatrixEventID(idempotencyKey) {
		return false
	}
	runID := strings.TrimSpace(payload.RunID)
	for _, candidate := range []string{
		openClawMessageTurnMarker(message),
		openClawMessageRunMarker(message),
		idempotencyKey,
	} {
		if candidate != "" && strings.EqualFold(candidate, runID) {
			return true
		}
	}
	if openClawMessageTurnMarker(message) != "" || openClawMessageRunMarker(message) != "" || idempotencyKey != "" {
		return false
	}

	messageTS := extractMessageTimestamp(message)
	if messageTS.IsZero() || messageTS.Equal(openClawMissingMessageTimestamp) {
		return false
	}
	eventTS := extractOpenClawEventTimestamp(payload.TS, payload.Message)
	if eventTS.IsZero() || messageTS.After(eventTS.Add(5*time.Second)) {
		return false
	}
	return eventTS.Sub(messageTS) <= openClawHistoryMirrorFallbackWindow
}

func (m *openClawManager) ensureStreamStart(ctx context.Context, portal *bridgev2.Portal, state *openClawPortalState, turnID, runID, agentID string, eventTS time.Time, messageMetadata map[string]any, payload *gatewayChatEvent) {
	if strings.TrimSpace(turnID) == "" {
		return
	}
	m.mu.Lock()
	if _, exists := m.started[turnID]; exists {
		m.mu.Unlock()
		return
	}
	m.started[turnID] = struct{}{}
	m.mu.Unlock()
	if payload != nil {
		m.emitLatestUserMessageFromHistory(ctx, portal, state, *payload)
	}
	if agentID == "" {
		agentID = resolveOpenClawAgentID(state, state.OpenClawSessionKey, nil)
	}
	if len(messageMetadata) == 0 {
		messageMetadata = buildOpenClawUIMessageMetadata(sdk.UIMessageMetadataParams{
			TurnID:       turnID,
			AgentID:      agentID,
			CompletionID: runID,
		}, state.OpenClawSessionID, state.OpenClawSessionKey, "")
	}
	m.client.EmitStreamPart(ctx, portal, turnID, agentID, state.OpenClawSessionKey, map[string]any{
		"timestamp":       eventTS.UnixMilli(),
		"type":            "start",
		"messageId":       turnID,
		"messageMetadata": messageMetadata,
	})
}

func (m *openClawManager) handleAgentEvent(ctx context.Context, payload gatewayAgentEvent) {
	if strings.TrimSpace(payload.SessionKey) == "" {
		return
	}
	portal := m.resolvePortal(ctx, payload.SessionKey)
	if portal == nil || portal.MXID == "" {
		return
	}
	state, err := loadOpenClawPortalState(ctx, portal, m.client.UserLogin)
	if err != nil {
		return
	}
	agentID := resolveOpenClawAgentID(state, payload.SessionKey, payload.Data)
	turnID := strings.TrimSpace(payload.RunID)
	if turnID == "" {
		turnID = strings.TrimSpace(payload.SourceRunID)
	}
	if turnID == "" {
		return
	}
	agentMetadata := buildOpenClawUIMessageMetadata(sdk.UIMessageMetadataParams{
		TurnID:       turnID,
		AgentID:      agentID,
		CompletionID: payload.RunID,
	}, state.OpenClawSessionID, payload.SessionKey, "")
	eventTS := extractOpenClawEventTimestamp(payload.TS, nil)
	m.ensureStreamStart(ctx, portal, state, turnID, payload.RunID, agentID, eventTS, agentMetadata, nil)
	m.startRunRecovery(ctx, portal, turnID, payload.RunID, agentID)
	stream := strings.ToLower(strings.TrimSpace(payload.Stream))
	switch stream {
	case "assistant":
		if !shouldEmitOpenClawRawAgentData(stream, payload.Data) {
			return
		}
		m.client.EmitStreamPart(ctx, portal, turnID, agentID, payload.SessionKey, map[string]any{
			"timestamp": eventTS.UnixMilli(),
			"type":      "data-openclaw-" + stream,
			"id":        fmt.Sprintf("openclaw-%s-%d", stream, payload.Seq),
			"data":      map[string]any{"stream": payload.Stream, "data": payload.Data},
		})
		return
	case "reasoning":
		if text := stringutil.TrimDefault(stringValue(payload.Data["text"]), stringValue(payload.Data["delta"])); text != "" {
			m.client.EmitStreamPart(ctx, portal, turnID, agentID, payload.SessionKey, map[string]any{
				"timestamp": eventTS.UnixMilli(),
				"type":      "reasoning-delta",
				"id":        "reasoning-" + turnID,
				"delta":     text,
			})
		}
	case "tool":
		toolCallID := stringutil.TrimDefault(stringValue(payload.Data["toolCallId"]), stringutil.TrimDefault(stringValue(payload.Data["toolUseId"]), stringValue(payload.Data["id"])))
		toolName := stringutil.TrimDefault(stringValue(payload.Data["toolName"]), stringutil.TrimDefault(stringValue(payload.Data["name"]), "tool"))
		if toolCallID != "" {
			update := openClawBuildToolStreamUpdate(eventTS, payload.Data)
			emitted := false
			for _, part := range update.Parts {
				m.client.EmitStreamPart(ctx, portal, turnID, agentID, payload.SessionKey, part)
				emitted = true
			}
			if approvalID := strings.TrimSpace(stringutil.TrimDefault(stringValue(payload.Data["approvalId"]), stringValue(jsonutil.ToMap(payload.Data["approval"])["id"]))); approvalID != "" {
				m.attachApprovalContext(approvalID, payload.SessionKey, agentID, turnID, toolCallID, toolName)
				m.client.EmitStreamPart(ctx, portal, turnID, agentID, payload.SessionKey, map[string]any{
					"timestamp":  eventTS.UnixMilli(),
					"type":       "tool-approval-request",
					"approvalId": approvalID,
					"toolCallId": toolCallID,
				})
				emitted = true
			}
			if update.HasFinalOutput {
				m.ensureSpawnedSessionPortal(ctx, openClawSpawnedSessionKeyFromToolResult(toolName, update.FinalOutput))
			}
			if emitted {
				return
			}
		}
		fallthrough
	default:
		m.client.EmitStreamPart(ctx, portal, turnID, agentID, payload.SessionKey, map[string]any{
			"timestamp": eventTS.UnixMilli(),
			"type":      "data-openclaw-" + stream,
			"id":        fmt.Sprintf("openclaw-%s-%d", stream, payload.Seq),
			"data":      map[string]any{"stream": payload.Stream, "data": payload.Data},
		})
	}
}

func shouldEmitOpenClawRawAgentData(stream string, data map[string]any) bool {
	stream = strings.ToLower(strings.TrimSpace(stream))
	if stream != "assistant" {
		return true
	}
	return strings.TrimSpace(stringutil.TrimDefault(stringValue(data["text"]), stringValue(data["delta"]))) == ""
}

func (m *openClawManager) ensureSpawnedSessionPortal(ctx context.Context, sessionKey string) {
	sessionKey = strings.TrimSpace(sessionKey)
	if sessionKey == "" {
		return
	}

	// Queue a portal resync immediately so persistent child sessions materialize
	// as their own rooms instead of waiting for later child traffic.
	m.resolvePortal(m.client.BackgroundContext(ctx), sessionKey)

	go func() {
		if err := m.syncSessions(m.client.BackgroundContext(ctx)); err != nil {
			m.client.Log().Debug().Err(err).Str("session_key", sessionKey).Msg("Failed to refresh OpenClaw sessions after spawned session detection")
		}
	}()
}

func openClawSpawnedSessionKeyFromToolResult(toolName string, value any) string {
	if !strings.EqualFold(strings.TrimSpace(toolName), "sessions_spawn") {
		return ""
	}
	return openClawExtractSpawnedSessionKey(value)
}

func openClawExtractSpawnedSessionKey(value any) string {
	switch typed := value.(type) {
	case map[string]any:
		if childSessionKey := strings.TrimSpace(stringValue(typed["childSessionKey"])); isOpenClawSpawnedSessionKey(childSessionKey) {
			return childSessionKey
		}
		for _, nestedKey := range []string{"result", "output", "payload", "data"} {
			if nested := openClawExtractSpawnedSessionKey(typed[nestedKey]); nested != "" {
				return nested
			}
		}
	case string:
		trimmed := strings.TrimSpace(typed)
		if trimmed == "" {
			return ""
		}
		if strings.HasPrefix(trimmed, "{") || strings.HasPrefix(trimmed, "[") {
			var parsed map[string]any
			if err := json.Unmarshal([]byte(trimmed), &parsed); err == nil {
				return openClawExtractSpawnedSessionKey(parsed)
			}
		}
		if isOpenClawSpawnedSessionKey(trimmed) {
			return trimmed
		}
	}
	return ""
}

type openClawToolStreamUpdate struct {
	Parts          []map[string]any
	FinalOutput    any
	HasFinalOutput bool
}

func openClawBuildToolStreamUpdate(eventTS time.Time, data map[string]any) openClawToolStreamUpdate {
	toolCallID := strings.TrimSpace(stringutil.TrimDefault(stringValue(data["toolCallId"]), stringutil.TrimDefault(stringValue(data["toolUseId"]), stringValue(data["id"]))))
	if toolCallID == "" {
		return openClawToolStreamUpdate{}
	}
	toolName := strings.TrimSpace(stringutil.TrimDefault(stringValue(data["toolName"]), stringutil.TrimDefault(stringValue(data["name"]), "tool")))
	if toolName == "" {
		toolName = "tool"
	}
	base := map[string]any{
		"timestamp":        eventTS.UnixMilli(),
		"toolCallId":       toolCallID,
		"toolName":         toolName,
		"providerExecuted": true,
	}
	partWithBase := func(partType string) map[string]any {
		part := jsonutil.DeepCloneMap(base)
		part["type"] = partType
		return part
	}

	update := openClawToolStreamUpdate{}
	switch strings.ToLower(strings.TrimSpace(stringValue(data["phase"]))) {
	case "start":
		part := partWithBase("tool-input-start")
		if input, ok := openClawToolEventInput(data); ok {
			part["type"] = "tool-input-available"
			part["input"] = input
		}
		update.Parts = append(update.Parts, part)
	case "update":
		if output, ok := openClawToolEventPartialOutput(data); ok {
			part := partWithBase("tool-output-available")
			part["output"] = output
			part["preliminary"] = true
			update.Parts = append(update.Parts, part)
		}
	case "result":
		if errText := openClawToolEventErrorText(data); errText != "" {
			part := partWithBase("tool-output-error")
			part["errorText"] = errText
			update.Parts = append(update.Parts, part)
			return update
		}
		if output, ok := openClawToolEventFinalOutput(data); ok {
			part := partWithBase("tool-output-available")
			part["output"] = output
			update.Parts = append(update.Parts, part)
			update.FinalOutput = output
			update.HasFinalOutput = true
		}
	}
	return update
}

func openClawToolEventInput(data map[string]any) (any, bool) {
	input, ok := data["args"]
	if !ok || input == nil {
		return nil, false
	}
	return jsonutil.DeepCloneAny(input), true
}

func openClawToolEventPartialOutput(data map[string]any) (any, bool) {
	output, ok := data["partialResult"]
	if !ok || output == nil {
		return nil, false
	}
	return jsonutil.DeepCloneAny(output), true
}

func openClawToolEventFinalOutput(data map[string]any) (any, bool) {
	output, ok := data["result"]
	if !ok || output == nil {
		return nil, false
	}
	return jsonutil.DeepCloneAny(output), true
}

func openClawToolEventErrorText(data map[string]any) string {
	isError, _ := data["isError"].(bool)
	if !isError {
		return ""
	}
	if text := openClawToolResultErrorText(data["result"]); text != "" {
		return text
	}
	if text := strings.TrimSpace(stringValue(data["error"])); text != "" {
		return text
	}
	return "OpenClaw tool failed"
}

func openClawToolResultErrorText(result any) string {
	switch typed := result.(type) {
	case map[string]any:
		if text := strings.TrimSpace(openclawconv.ExtractMessageText(typed)); text != "" {
			return text
		}
		for _, key := range []string{"error", "message"} {
			if text := strings.TrimSpace(stringValue(typed[key])); text != "" {
				return text
			}
		}
		for _, key := range []string{"details", "result", "output"} {
			if nested := openClawToolResultErrorText(typed[key]); nested != "" {
				return nested
			}
		}
	case string:
		return strings.TrimSpace(typed)
	}
	return ""
}

func isOpenClawSpawnedSessionKey(sessionKey string) bool {
	sessionKey = strings.TrimSpace(sessionKey)
	if sessionKey == "" {
		return false
	}
	return strings.Contains(sessionKey, ":subagent:") || strings.Contains(sessionKey, ":acp:")
}

func (m *openClawManager) startRunRecovery(ctx context.Context, portal *bridgev2.Portal, turnID, runID, agentID string) {
	runID = strings.TrimSpace(runID)
	if runID == "" || strings.TrimSpace(turnID) == "" || portal == nil || portal.MXID == "" {
		return
	}
	if !m.trackWaitingRun(runID) {
		return
	}
	go m.waitForRunCompletion(m.client.BackgroundContext(ctx), portal, turnID, runID, agentID)
}

func (m *openClawManager) waitForRunCompletion(ctx context.Context, portal *bridgev2.Portal, turnID, runID, agentID string) {
	defer m.untrackWaitingRun(runID)

	timer := time.NewTimer(20 * time.Second)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return
	case <-timer.C:
	}

	if !m.client.isStreamActive(turnID) {
		return
	}
	gateway := m.gatewayClient()
	if gateway == nil {
		return
	}
	waitResp, err := gateway.WaitForRun(ctx, runID, 30*time.Second)
	if err != nil || waitResp == nil || !m.client.isStreamActive(turnID) {
		return
	}
	status := strings.ToLower(strings.TrimSpace(waitResp.Status))
	if status == "" || status == "timeout" {
		return
	}

	state, err := loadOpenClawPortalState(ctx, portal, m.client.UserLogin)
	if err != nil {
		return
	}
	recoveredText := m.recoverRunText(ctx, state.OpenClawSessionKey, turnID)
	if recoveredText == "" {
		recoveredText = m.recoverRunPreview(ctx, portal, state)
	}
	if recoveredText != "" {
		if delta := m.client.computeVisibleDelta(turnID, recoveredText); delta != "" {
			m.client.EmitStreamPart(ctx, portal, turnID, agentID, state.OpenClawSessionKey, map[string]any{
				"type":  "text-delta",
				"id":    "text-" + turnID,
				"delta": delta,
			})
		}
	}

	metadata := buildOpenClawUIMessageMetadata(sdk.UIMessageMetadataParams{
		TurnID:        turnID,
		AgentID:       agentID,
		CompletionID:  runID,
		FinishReason:  status,
		StartedAtMs:   waitResp.StartedAt,
		CompletedAtMs: waitResp.EndedAt,
		IncludeUsage:  true,
	}, state.OpenClawSessionID, state.OpenClawSessionKey, strings.TrimSpace(waitResp.Error))
	if status == "error" {
		m.client.EmitStreamPart(ctx, portal, turnID, agentID, state.OpenClawSessionKey, map[string]any{
			"type":      "error",
			"errorText": stringutil.TrimDefault(waitResp.Error, "OpenClaw run failed"),
		})
	}
	m.client.EmitStreamPart(ctx, portal, turnID, agentID, state.OpenClawSessionKey, map[string]any{
		"type":            "finish",
		"finishReason":    status,
		"errorText":       strings.TrimSpace(waitResp.Error),
		"messageMetadata": metadata,
	})
	m.clearStartedTurn(turnID)
}

func (m *openClawManager) recoverRunText(ctx context.Context, sessionKey, turnID string) string {
	gateway := m.gatewayClient()
	if gateway == nil || strings.TrimSpace(sessionKey) == "" {
		return ""
	}
	history, err := gateway.SessionHistory(ctx, sessionKey, 25, "")
	if err != nil || history == nil {
		return ""
	}
	filtered := history.Messages
	if trimmedTurnID := strings.TrimSpace(turnID); trimmedTurnID != "" {
		filtered = make([]map[string]any, 0, len(history.Messages))
		for _, message := range history.Messages {
			if strings.EqualFold(historyMessageTurnID(message), trimmedTurnID) {
				filtered = append(filtered, message)
			}
		}
		if len(filtered) == 0 {
			return ""
		}
	}
	for i := len(filtered) - 1; i >= 0; i-- {
		message := filtered[i]
		role := strings.ToLower(strings.TrimSpace(stringValue(message["role"])))
		if role != "assistant" && role != "toolresult" {
			continue
		}
		text := openclawconv.ExtractMessageText(message)
		if strings.TrimSpace(text) != "" {
			return text
		}
	}
	return ""
}

func (m *openClawManager) recoverRunPreview(ctx context.Context, portal *bridgev2.Portal, state *openClawPortalState) string {
	if m == nil || m.client == nil || portal == nil || state == nil {
		return ""
	}
	snippet := strings.TrimSpace(m.client.previewSessionSnippet(ctx, state.OpenClawSessionKey))
	if snippet == "" {
		return ""
	}
	state.OpenClawLastMessagePreview = snippet
	_ = saveOpenClawPortalState(ctx, portal, m.client.UserLogin, state)
	return snippet
}

func (m *openClawManager) resolvePortal(ctx context.Context, sessionKey string) *bridgev2.Portal {
	if strings.TrimSpace(sessionKey) == "" {
		return nil
	}
	key := m.client.portalKeyForSession(sessionKey)
	portal, err := m.client.UserLogin.Bridge.GetPortalByKey(ctx, key)
	if err == nil && portal != nil {
		m.clearPendingPortalResync(sessionKey)
		return portal
	}
	m.mu.RLock()
	session, ok := m.sessions[sessionKey]
	m.mu.RUnlock()
	if !ok {
		session = gatewaySessionRow{Key: sessionKey, SessionID: sessionKey}
	}
	if m.shouldQueuePortalResync(sessionKey) {
		m.client.UserLogin.QueueRemoteEvent(buildOpenClawSessionResyncEvent(m.client, session))
	}
	portal, _ = m.client.UserLogin.Bridge.GetPortalByKey(ctx, key)
	if portal != nil {
		m.clearPendingPortalResync(sessionKey)
	}
	return portal
}

var openClawMissingMessageTimestamp = time.Unix(0, 0).UTC()

func openClawSessionTimestamp(session gatewaySessionRow) time.Time {
	if session.UpdatedAt > 0 {
		return time.UnixMilli(session.UpdatedAt)
	}
	return time.Time{}
}

func extractMessageTimestamp(message map[string]any) time.Time {
	if ts, ok := message["timestamp"].(float64); ok && ts > 0 {
		return time.UnixMilli(int64(ts))
	}
	if ts, ok := message["timestamp"].(int64); ok && ts > 0 {
		return time.UnixMilli(ts)
	}
	if ts, ok := message["timestamp"].(int); ok && ts > 0 {
		return time.UnixMilli(int64(ts))
	}
	if ts, ok := message["timestamp"].(string); ok {
		ts = strings.TrimSpace(ts)
		if ts != "" {
			if unixMilli, err := strconv.ParseInt(ts, 10, 64); err == nil && unixMilli > 0 {
				return time.UnixMilli(unixMilli)
			}
			if parsed, err := time.Parse(time.RFC3339Nano, ts); err == nil {
				return parsed
			}
			if parsed, err := time.Parse(time.RFC3339, ts); err == nil {
				return parsed
			}
		}
	}
	return openClawMissingMessageTimestamp
}

func openClawMessageStringField(message map[string]any, keys ...string) string {
	for _, key := range keys {
		if value := strings.TrimSpace(stringValue(message[key])); value != "" {
			return value
		}
	}
	nested := jsonutil.ToMap(message["message"])
	for _, key := range keys {
		if value := strings.TrimSpace(stringValue(nested[key])); value != "" {
			return value
		}
	}
	return ""
}

func openClawMessageIdempotencyKey(message map[string]any) string {
	return openClawMessageStringField(message, "idempotencyKey", "idempotency_key")
}

func openClawMessageTurnMarker(message map[string]any) string {
	return openClawMessageStringField(message, "turnId", "turn_id")
}

func openClawMessageRunMarker(message map[string]any) string {
	return openClawMessageStringField(message, "runId", "run_id")
}

func isLikelyMatrixEventID(value string) bool {
	value = strings.TrimSpace(value)
	return strings.HasPrefix(value, "$") && strings.Contains(value, ":")
}

func openClawMessageRole(message map[string]any) string {
	role := strings.ToLower(strings.TrimSpace(openClawMessageStringField(message, "role")))
	if role == "human" {
		return "user"
	}
	return role
}

func openClawIsTerminalChatState(state string) bool {
	switch strings.ToLower(strings.TrimSpace(state)) {
	case "final", "done", "complete", "completed", "aborted", "error":
		return true
	default:
		return false
	}
}

func historyMessageTurnID(message map[string]any) string {
	return strings.TrimSpace(stringutil.TrimDefault(
		openClawMessageStringField(message, "turnId", "turn_id"),
		openClawMessageStringField(message, "runId", "run_id"),
	))
}

func (m *openClawManager) clearStartedTurn(turnID string) {
	turnID = strings.TrimSpace(turnID)
	if turnID == "" {
		return
	}
	m.mu.Lock()
	delete(m.started, turnID)
	m.mu.Unlock()
}

func (m *openClawManager) shouldQueuePortalResync(sessionKey string) bool {
	sessionKey = strings.TrimSpace(sessionKey)
	if sessionKey == "" {
		return false
	}
	now := time.Now()
	m.mu.Lock()
	defer m.mu.Unlock()
	if last, ok := m.resyncing[sessionKey]; ok && now.Sub(last) < 5*time.Second {
		return false
	}
	m.resyncing[sessionKey] = now
	return true
}

func (m *openClawManager) clearPendingPortalResync(sessionKey string) {
	sessionKey = strings.TrimSpace(sessionKey)
	if sessionKey == "" {
		return
	}
	m.mu.Lock()
	delete(m.resyncing, sessionKey)
	m.mu.Unlock()
}

func stringValue(v any) string {
	return stringutil.StringValue(v)
}

func openClawAttachmentFallbackText(block map[string]any, err error) string {
	name := openClawBlockFilename(block)
	if name == "" {
		name = "attachment"
	}
	if err == nil {
		return "[Attachment: " + name + "]"
	}
	return fmt.Sprintf("[Attachment unavailable: %s (%v)]", name, err)
}

func convertHistoryToCanonicalUI(message map[string]any, role string, state *openClawPortalState) ([]map[string]any, map[string]any) {
	agentID := resolveOpenClawAgentID(state, stringutil.TrimDefault(state.OpenClawSessionKey, stringValue(message["sessionKey"])), message)
	turnID := strings.TrimSpace(stringutil.TrimDefault(
		stringValue(message["turnId"]),
		stringValue(message["runId"]),
	))
	params := sdk.UIMessageMetadataParams{
		TurnID:       turnID,
		AgentID:      agentID,
		Model:        stringutil.TrimDefault(stringValue(message["model"]), state.Model),
		FinishReason: stringutil.TrimDefault(stringValue(message["finishReason"]), stringValue(message["stopReason"])),
		CompletionID: stringValue(message["runId"]),
		IncludeUsage: true,
	}
	applyNormalizedUsageToParams(normalizeOpenClawUsage(jsonutil.ToMap(message["usage"])), &params)
	metadata := buildOpenClawUIMessageMetadata(params,
		stringutil.TrimDefault(stringValue(message["sessionId"]), state.OpenClawSessionID),
		stringutil.TrimDefault(stringValue(message["sessionKey"]), state.OpenClawSessionKey),
		stringutil.TrimDefault(stringValue(message["errorMessage"]), stringValue(message["error"])),
	)
	return openClawHistoryUIParts(message, role), metadata
}

func openClawHistoryUIParts(message map[string]any, role string) []map[string]any {
	state := &streamui.UIState{
		TurnID: stringutil.TrimDefault(
			stringValue(message["turnId"]),
			stringValue(message["runId"]),
		),
	}
	openClawApplyHistoryChunks(state, message, role)
	snapshot := streamui.SnapshotUIMessage(state)
	return sdk.NormalizeUIParts(snapshot["parts"])
}

func openClawApplyHistoryChunks(state *streamui.UIState, message map[string]any, role string) {
	if state == nil {
		return
	}
	state.InitMaps()
	replayer := sdk.NewUIStateReplayer(state)
	role = strings.ToLower(strings.TrimSpace(role))
	if role == "toolresult" {
		openClawApplyHistoryToolResult(replayer, message)
		return
	}
	blocks := openclawconv.ContentBlocks(message)
	for idx, block := range blocks {
		blockType := strings.ToLower(strings.TrimSpace(stringValue(block["type"])))
		switch blockType {
		case "text", "input_text", "output_text":
			text := strings.TrimSpace(stringutil.TrimDefault(stringValue(block["text"]), stringValue(block["content"])))
			if text == "" {
				continue
			}
			replayer.Text(fmt.Sprintf("text-%d", idx), text)
		case "reasoning", "thinking":
			text := strings.TrimSpace(stringutil.TrimDefault(stringValue(block["text"]), stringValue(block["content"])))
			if text == "" {
				continue
			}
			replayer.Reasoning(fmt.Sprintf("reasoning-%d", idx), text)
		case "toolcall", "tooluse", "functioncall":
			toolCallID := strings.TrimSpace(stringutil.TrimDefault(stringValue(block["id"]), stringValue(block["call_id"])))
			if toolCallID == "" {
				toolCallID = fmt.Sprintf("tool-call-%d", idx)
			}
			toolName := strings.TrimSpace(stringutil.TrimDefault(stringValue(block["name"]), stringValue(block["toolName"])))
			input := jsonutil.ToMap(block["arguments"])
			if len(input) == 0 {
				input = jsonutil.ToMap(block["input"])
			}
			replayer.ToolInput(toolCallID, stringutil.TrimDefault(toolName, "tool"), input, false)
			if approvalID := strings.TrimSpace(stringutil.TrimDefault(stringValue(block["approvalId"]), stringValue(jsonutil.ToMap(block["approval"])["id"]))); approvalID != "" {
				replayer.ApprovalRequest(approvalID, toolCallID)
			}
		case "toolresult", "tool_result", "tool-output":
			openClawApplyHistoryToolResult(replayer, block)
		}
	}
	if len(blocks) == 0 {
		if text := strings.TrimSpace(openclawconv.ExtractMessageText(message)); text != "" {
			replayer.Text("text-history", text)
		}
	}
}

func openClawApplyHistoryToolResult(replayer sdk.UIStateReplayer, message map[string]any) {
	toolCallID := strings.TrimSpace(stringutil.TrimDefault(stringValue(message["toolCallId"]), stringValue(message["toolUseId"])))
	if toolCallID == "" {
		toolCallID = "tool-result"
	}
	toolName := strings.TrimSpace(stringutil.TrimDefault(stringValue(message["toolName"]), stringValue(message["name"])))
	if toolName != "" {
		replayer.ToolInput(toolCallID, toolName, jsonutil.DeepCloneAny(jsonutil.ToMap(message["input"])), false)
	}
	if approvalID := strings.TrimSpace(stringutil.TrimDefault(stringValue(message["approvalId"]), stringValue(jsonutil.ToMap(message["approval"])["id"]))); approvalID != "" {
		replayer.ApprovalRequest(approvalID, toolCallID)
	}
	if isError, _ := message["isError"].(bool); isError {
		replayer.ToolOutputError(toolCallID, stringutil.TrimDefault(openclawconv.ExtractMessageText(message), stringValue(message["error"])), false)
		return
	}
	output := jsonutil.DeepCloneAny(message["details"])
	if output == nil {
		output = jsonutil.DeepCloneAny(stringutil.TrimDefault(openclawconv.ExtractMessageText(message), stringValue(message["result"])))
	}
	replayer.ToolOutput(toolCallID, output, false)
}

func openClawHistoryFallbackText(uiParts []map[string]any) string {
	for _, part := range uiParts {
		partType := strings.TrimSpace(stringValue(part["type"]))
		switch partType {
		case "text", "reasoning":
			if text := strings.TrimSpace(stringValue(part["text"])); text != "" {
				return text
			}
		case "dynamic-tool", "tool":
			toolName := strings.TrimSpace(stringutil.TrimDefault(stringValue(part["toolName"]), "tool"))
			switch strings.TrimSpace(stringValue(part["state"])) {
			case "approval-requested":
				return "Tool approval required: " + toolName
			case "output-error":
				return "Tool failed: " + toolName
			case "output-available":
				return "Tool completed: " + toolName
			default:
				return "Tool activity: " + toolName
			}
		}
	}
	return ""
}

func resolveOpenClawAgentID(state *openClawPortalState, sessionKey string, payload map[string]any) string {
	for _, key := range []string{"agentId", "agent_id", "agent"} {
		if payload != nil {
			if value := strings.TrimSpace(stringValue(payload[key])); value != "" {
				return value
			}
		}
	}
	if state != nil && strings.TrimSpace(state.OpenClawDMTargetAgentID) != "" {
		return strings.TrimSpace(state.OpenClawDMTargetAgentID)
	}
	if value := openclawconv.AgentIDFromSessionKey(sessionKey); value != "" {
		return value
	}
	return "gateway"
}
