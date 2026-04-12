package openclaw

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/rs/zerolog"
	"go.mau.fi/util/ptr"
	"maunium.net/go/mautrix/bridgev2"
	"maunium.net/go/mautrix/bridgev2/database"
	"maunium.net/go/mautrix/bridgev2/simplevent"

	"github.com/beeper/agentremote/pkg/shared/openclawconv"
	"github.com/beeper/agentremote/pkg/shared/stringutil"
	"github.com/beeper/agentremote/sdk"
)

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
	state.OpenClawPreviewSnippet = stringutil.TrimDefault(state.OpenClawPreviewSnippet, session.LastMessagePreview)
	if state.OpenClawPreviewSnippet != "" && state.OpenClawLastPreviewAt == 0 {
		state.OpenClawLastPreviewAt = time.Now().UnixMilli()
	}
	state.HistoryMode = "paginated"
	state.RecentHistoryLimit = 0
	if strings.TrimSpace(state.BackgroundBackfillStatus) == "" {
		state.BackgroundBackfillStatus = "pending"
	}
	client.enrichPortalState(ctx, state)
	if err := saveOpenClawPortalState(ctx, portal, client.UserLogin, state); err != nil {
		return nil, err
	}
	portalMeta(portal).IsOpenClawRoom = true

	title := client.displayNameForSession(session)
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
	if isOpenClawSyntheticDMSessionKey(session.Key) && strings.TrimSpace(state.OpenClawDMTargetAgentName) != "" {
		title = strings.TrimSpace(state.OpenClawDMTargetAgentName)
	}
	roomType := openClawRoomType(state)
	client.maybeRefreshPortalCapabilities(ctx, portal, &previous, state)
	if roomType == database.RoomTypeDM {
		return sdk.BuildLoginDMChatInfo(sdk.LoginDMChatInfoParams{
			Title:             title,
			Topic:             client.topicForPortal(state),
			Login:             client.UserLogin,
			HumanUserIDPrefix: "openclaw-user",
			HumanSender:       ptr.Ptr(client.senderForAgent(agentID, true)),
			BotUserID:         openClawGhostUserID(agentID),
			BotDisplayName:    agentName,
			BotSender:         ptr.Ptr(client.senderForAgent(agentID, false)),
			BotUserInfo:       client.userInfoForAgentProfile(profile),
			CanBackfill:       true,
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
		Type:        ptr.Ptr(roomType),
		Name:        ptr.Ptr(title),
		Topic:       ptr.NonZero(client.topicForPortal(state)),
		CanBackfill: true,
		Members: &bridgev2.ChatMemberList{
			IsFull:    true,
			MemberMap: memberMap,
		},
	}, nil
}
