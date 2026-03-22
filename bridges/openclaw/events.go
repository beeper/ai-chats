package openclaw

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/rs/zerolog"
	"go.mau.fi/util/ptr"
	"maunium.net/go/mautrix/bridgev2"
	"maunium.net/go/mautrix/bridgev2/database"
	"maunium.net/go/mautrix/bridgev2/networkid"
	"maunium.net/go/mautrix/bridgev2/simplevent"
	"maunium.net/go/mautrix/event"

	"github.com/beeper/agentremote"
	"github.com/beeper/agentremote/pkg/shared/openclawconv"
	"github.com/beeper/agentremote/pkg/shared/stringutil"
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
	meta := portalMeta(portal)
	previous := *meta
	meta.IsOpenClawRoom = true
	meta.OpenClawGatewayID = client.gatewayID()
	meta.OpenClawSessionID = session.SessionID
	meta.OpenClawSessionKey = session.Key
	meta.OpenClawSpawnedBy = session.SpawnedBy
	meta.OpenClawSessionKind = session.Kind
	meta.OpenClawSessionLabel = session.Label
	meta.OpenClawDisplayName = session.DisplayName
	meta.OpenClawDerivedTitle = session.DerivedTitle
	meta.OpenClawLastMessagePreview = session.LastMessagePreview
	meta.OpenClawChannel = session.Channel
	meta.OpenClawSubject = session.Subject
	meta.OpenClawGroupChannel = session.GroupChannel
	meta.OpenClawSpace = session.Space
	meta.OpenClawChatType = session.ChatType
	meta.OpenClawOrigin = session.OriginString()
	meta.OpenClawAgentID = stringutil.TrimDefault(meta.OpenClawAgentID, openclawconv.AgentIDFromSessionKey(session.Key))
	if isOpenClawSyntheticDMSessionKey(session.Key) {
		meta.OpenClawDMTargetAgentID = stringutil.TrimDefault(meta.OpenClawDMTargetAgentID, openclawconv.AgentIDFromSessionKey(session.Key))
	}
	meta.OpenClawSystemSent = session.SystemSent
	meta.OpenClawAbortedLastRun = session.AbortedLastRun
	meta.ThinkingLevel = session.ThinkingLevel
	meta.FastMode = session.FastMode
	meta.VerboseLevel = session.VerboseLevel
	meta.ReasoningLevel = session.ReasoningLevel
	meta.ElevatedLevel = session.ElevatedLevel
	meta.SendPolicy = session.SendPolicy
	meta.InputTokens = session.InputTokens
	meta.OutputTokens = session.OutputTokens
	meta.TotalTokens = session.TotalTokens
	meta.TotalTokensFresh = session.TotalTokensFresh
	meta.EstimatedCostUSD = session.EstimatedCostUSD
	meta.Status = session.Status
	meta.StartedAt = session.StartedAt
	meta.EndedAt = session.EndedAt
	meta.RuntimeMs = session.RuntimeMs
	meta.ParentSessionKey = session.ParentSessionKey
	meta.ChildSessions = append(meta.ChildSessions[:0], session.ChildSessions...)
	meta.ResponseUsage = session.ResponseUsage
	meta.ModelProvider = session.ModelProvider
	meta.Model = session.Model
	meta.ContextTokens = session.ContextTokens
	meta.DeliveryContext = session.DeliveryContext
	meta.LastChannel = session.LastChannel
	meta.LastTo = session.LastTo
	meta.LastAccountID = session.LastAccountID
	meta.SessionUpdatedAt = session.UpdatedAt
	meta.OpenClawPreviewSnippet = stringutil.TrimDefault(meta.OpenClawPreviewSnippet, session.LastMessagePreview)
	if meta.OpenClawPreviewSnippet != "" && meta.OpenClawLastPreviewAt == 0 {
		meta.OpenClawLastPreviewAt = time.Now().UnixMilli()
	}
	meta.HistoryMode = "paginated"
	meta.RecentHistoryLimit = 0
	client.enrichPortalMetadata(ctx, meta)
	portal.Metadata = meta

	title := client.displayNameForSession(session)
	agentID := stringutil.TrimDefault(meta.OpenClawAgentID, "gateway")
	if strings.TrimSpace(meta.OpenClawDMTargetAgentID) != "" {
		agentID = strings.TrimSpace(meta.OpenClawDMTargetAgentID)
		meta.OpenClawAgentID = agentID
	}
	identity := client.lookupAgentIdentity(ctx, agentID, session.Key)
	if identity != nil && strings.TrimSpace(identity.AgentID) != "" {
		agentID = strings.TrimSpace(identity.AgentID)
		meta.OpenClawAgentID = agentID
	}
	configured, err := client.agentCatalogEntryByID(ctx, agentID)
	if err != nil {
		client.Log().Debug().Err(err).Str("agent_id", agentID).Msg("Failed to refresh OpenClaw agent catalog during session resync")
	}
	profile := client.resolveAgentProfile(ctx, agentID, session.Key, nil, configured)
	agentName := client.displayNameFromAgentProfile(profile)
	if strings.TrimSpace(meta.OpenClawDMTargetAgentName) == "" && strings.TrimSpace(meta.OpenClawDMTargetAgentID) == agentID {
		meta.OpenClawDMTargetAgentName = agentName
	}
	if isOpenClawSyntheticDMSessionKey(session.Key) && strings.TrimSpace(meta.OpenClawDMTargetAgentName) != "" {
		title = strings.TrimSpace(meta.OpenClawDMTargetAgentName)
	}
	roomType := openClawRoomType(meta)
	client.maybeRefreshPortalCapabilities(ctx, portal, &previous)
	if roomType == database.RoomTypeDM {
		chatInfo := agentremote.BuildLoginDMChatInfo(agentremote.LoginDMChatInfoParams{
			Title:             title,
			Login:             client.UserLogin,
			HumanUserIDPrefix: "openclaw-user",
			BotUserID:         openClawGhostUserID(agentID),
			BotDisplayName:    agentName,
			CanBackfill:       true,
		})
		if chatInfo != nil {
			chatInfo.Topic = ptr.NonZero(client.topicForPortal(meta))
			if chatInfo.Members != nil && chatInfo.Members.MemberMap != nil {
				chatInfo.Members.MemberMap[humanUserID(client.UserLogin.ID)] = bridgev2.ChatMember{
					EventSender: client.senderForAgent(agentID, true),
					Membership:  event.MembershipJoin,
				}
				chatInfo.Members.MemberMap[openClawGhostUserID(agentID)] = bridgev2.ChatMember{
					EventSender: client.senderForAgent(agentID, false),
					Membership:  event.MembershipJoin,
					UserInfo:    client.userInfoForAgentProfile(profile),
				}
			}
		}
		return chatInfo, nil
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
		Topic:       ptr.NonZero(client.topicForPortal(meta)),
		CanBackfill: true,
		Members: &bridgev2.ChatMemberList{
			IsFull:    true,
			MemberMap: memberMap,
		},
	}, nil
}

func buildOpenClawRemoteMessage(
	portal networkid.PortalKey,
	messageID networkid.MessageID,
	sender bridgev2.EventSender,
	timestamp time.Time,
	streamOrder int64,
	preBuilt *bridgev2.ConvertedMessage,
) *simplevent.PreConvertedMessage {
	if timestamp.IsZero() {
		timestamp = time.Now()
	}
	if streamOrder == 0 {
		streamOrder = timestamp.UnixMilli()
	}
	return &simplevent.PreConvertedMessage{
		EventMeta: simplevent.EventMeta{
			Type:        bridgev2.RemoteEventMessage,
			PortalKey:   portal,
			Sender:      sender,
			Timestamp:   timestamp,
			StreamOrder: streamOrder,
			LogContext: func(c zerolog.Context) zerolog.Context {
				return c.Str("openclaw_msg_id", string(messageID))
			},
		},
		ID:   messageID,
		Data: preBuilt,
	}
}

func newOpenClawMessageID() networkid.MessageID {
	return networkid.MessageID("openclaw:" + uuid.NewString())
}
