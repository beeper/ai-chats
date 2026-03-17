package dummybridge

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/rs/zerolog"
	"go.mau.fi/util/ptr"
	"maunium.net/go/mautrix/bridgev2"
	"maunium.net/go/mautrix/bridgev2/database"
	"maunium.net/go/mautrix/bridgev2/networkid"

	"github.com/beeper/agentremote"
	bridgesdk "github.com/beeper/agentremote/sdk"
)

const dummyPortalTopic = "DummyBridge demo room for turns, streaming, tools, approvals, and artifacts."

type dummySession struct {
	login         *bridgev2.UserLogin
	acceptedValue string
	log           zerolog.Logger
}

func (dc *DummyBridgeConnector) loggerForLogin(login *bridgev2.UserLogin) zerolog.Logger {
	if login == nil {
		return zerolog.Nop()
	}
	return login.Log.With().Str("component", "dummybridge").Logger()
}

func sessionFromAny(session any) (*dummySession, error) {
	dummy, ok := session.(*dummySession)
	if !ok || dummy == nil || dummy.login == nil {
		return nil, errors.New("dummybridge session is unavailable")
	}
	return dummy, nil
}

func (dc *DummyBridgeConnector) onConnect(ctx context.Context, info *bridgesdk.LoginInfo) (any, error) {
	if info == nil || info.Login == nil {
		return nil, errors.New("missing login info")
	}
	login := info.Login
	log := dc.loggerForLogin(login).With().Str("login_id", string(login.ID)).Logger()
	if err := dummySDKAgent().EnsureGhost(ctx, login); err != nil {
		return nil, fmt.Errorf("ensure ghost: %w", err)
	}
	if err := dc.ensureInitialRoom(ctx, login); err != nil {
		return nil, err
	}
	return &dummySession{
		login:         login,
		acceptedValue: loginMetadata(login).AcceptedString,
		log:           log,
	}, nil
}

func (dc *DummyBridgeConnector) onDisconnect(session any) {
	_, _ = sessionFromAny(session)
}

func (dc *DummyBridgeConnector) getContactList(ctx context.Context, session any) ([]*bridgev2.ResolveIdentifierResponse, error) {
	dummy, err := sessionFromAny(session)
	if err != nil {
		return nil, err
	}
	resp, err := dc.contactResponse(ctx, dummy.login, false)
	if err != nil {
		return nil, err
	}
	return []*bridgev2.ResolveIdentifierResponse{resp}, nil
}

func (dc *DummyBridgeConnector) searchUsers(ctx context.Context, session any, query string) ([]*bridgev2.ResolveIdentifierResponse, error) {
	dummy, err := sessionFromAny(session)
	if err != nil {
		return nil, err
	}
	resp, err := dc.contactResponse(ctx, dummy.login, false)
	if err != nil {
		return nil, err
	}
	query = strings.TrimSpace(strings.ToLower(query))
	if query == "" {
		return []*bridgev2.ResolveIdentifierResponse{resp}, nil
	}
	text := strings.Join([]string{
		strings.ToLower(dummyAgentName),
		strings.ToLower(string(dummyAgentUserID)),
		dummyAgentIdentifierPrimary,
		dummyAgentIdentifierShort,
	}, " ")
	if strings.Contains(text, query) {
		return []*bridgev2.ResolveIdentifierResponse{resp}, nil
	}
	return nil, nil
}

func (dc *DummyBridgeConnector) resolveIdentifier(ctx context.Context, session any, identifier string, createChat bool) (*bridgev2.ResolveIdentifierResponse, error) {
	dummy, err := sessionFromAny(session)
	if err != nil {
		return nil, err
	}
	if !matchesDummyIdentifier(identifier) {
		return nil, fmt.Errorf("unknown identifier: %s", identifier)
	}
	return dc.contactResponse(ctx, dummy.login, createChat)
}

func (dc *DummyBridgeConnector) getChatInfo(conv *bridgesdk.Conversation) (*bridgev2.ChatInfo, error) {
	if conv == nil || conv.Portal() == nil {
		return agentremote.BuildChatInfoWithFallback("", "", dummyAgentName, dummyPortalTopic), nil
	}
	portal := conv.Portal()
	meta := portalMeta(portal)
	title := strings.TrimSpace(meta.Title)
	if title == "" {
		title = strings.TrimSpace(portal.Name)
	}
	if title == "" {
		title = dummyAgentName
	}
	info := agentremote.BuildChatInfoWithFallback(title, portal.Name, dummyAgentName, portal.Topic)
	if strings.TrimSpace(meta.Topic) != "" {
		info.Topic = ptr.Ptr(meta.Topic)
	}
	return info, nil
}

func (dc *DummyBridgeConnector) getUserInfo(_ *bridgev2.Ghost) (*bridgev2.UserInfo, error) {
	return dummySDKAgent().UserInfo(), nil
}

func matchesDummyIdentifier(identifier string) bool {
	id := strings.TrimSpace(strings.ToLower(identifier))
	switch id {
	case "", dummyAgentIdentifierPrimary, dummyAgentIdentifierShort, strings.ToLower(string(dummyAgentUserID)), strings.ToLower(dummyAgentName):
		return id != ""
	default:
		return strings.Contains(id, dummyAgentIdentifierPrimary) || strings.Contains(id, dummyAgentIdentifierShort)
	}
}

func (dc *DummyBridgeConnector) contactResponse(ctx context.Context, login *bridgev2.UserLogin, createChat bool) (*bridgev2.ResolveIdentifierResponse, error) {
	if login == nil || login.Bridge == nil {
		return nil, errors.New("login unavailable")
	}
	if err := dummySDKAgent().EnsureGhost(ctx, login); err != nil {
		return nil, fmt.Errorf("ensure ghost: %w", err)
	}
	ghost, err := login.Bridge.GetGhostByID(ctx, dummyAgentUserID)
	if err != nil {
		return nil, fmt.Errorf("get ghost: %w", err)
	}
	var chat *bridgev2.CreateChatResponse
	if createChat {
		chat, err = dc.createChat(ctx, login)
		if err != nil {
			return nil, err
		}
	}
	return &bridgev2.ResolveIdentifierResponse{
		UserID:   dummyAgentUserID,
		UserInfo: dummySDKAgent().UserInfo(),
		Ghost:    ghost,
		Chat:     chat,
	}, nil
}

func (dc *DummyBridgeConnector) ensureInitialRoom(ctx context.Context, login *bridgev2.UserLogin) error {
	dc.chatMu.Lock()
	defer dc.chatMu.Unlock()

	meta := loginMetadata(login)
	updated := false
	if strings.TrimSpace(meta.Provider) == "" {
		meta.Provider = ProviderDummyBridge
		updated = true
	}
	if meta.NextChatIndex < 1 {
		meta.NextChatIndex = 1
		updated = true
	}
	if updated {
		if err := login.Save(ctx); err != nil {
			return fmt.Errorf("save login metadata: %w", err)
		}
	}
	if _, err := dc.ensureChatForIndexLocked(ctx, login, 1); err != nil {
		return err
	}
	return nil
}

func (dc *DummyBridgeConnector) createChat(ctx context.Context, login *bridgev2.UserLogin) (*bridgev2.CreateChatResponse, error) {
	dc.chatMu.Lock()
	defer dc.chatMu.Unlock()

	meta := loginMetadata(login)
	if meta.NextChatIndex < 1 {
		meta.NextChatIndex = 1
	}
	meta.NextChatIndex++
	if err := login.Save(ctx); err != nil {
		return nil, fmt.Errorf("save login chat index: %w", err)
	}
	return dc.ensureChatForIndexLocked(ctx, login, meta.NextChatIndex)
}

func (dc *DummyBridgeConnector) ensureChatForIndexLocked(ctx context.Context, login *bridgev2.UserLogin, idx int) (*bridgev2.CreateChatResponse, error) {
	if login == nil || login.Bridge == nil {
		return nil, errors.New("login unavailable")
	}
	title := dummyChatTitle(idx)
	portal, err := login.Bridge.GetPortalByKey(ctx, networkid.PortalKey{
		ID:       networkid.PortalID(dummyPortalID(idx)),
		Receiver: login.ID,
	})
	if err != nil {
		return nil, fmt.Errorf("get portal: %w", err)
	}
	meta := portalMeta(portal)
	meta.IsDummyBridgeRoom = true
	meta.Title = title
	meta.Topic = dummyPortalTopic
	meta.ChatIndex = idx

	portal.RoomType = database.RoomTypeDM
	portal.OtherUserID = dummyAgentUserID
	portal.Name = title
	portal.Topic = dummyPortalTopic
	portal.NameSet = true
	portal.TopicSet = true
	if err := portal.Save(ctx); err != nil {
		return nil, fmt.Errorf("save portal: %w", err)
	}

	chatInfo := dc.composeChatInfo(login, title)
	if _, err := bridgesdk.EnsurePortalLifecycle(ctx, bridgesdk.PortalLifecycleOptions{
		Login:             login,
		Portal:            portal,
		ChatInfo:          chatInfo,
		SaveBeforeCreate:  true,
		AIRoomKind:        agentremote.AIRoomKindAgent,
		ForceCapabilities: true,
	}); err != nil {
		return nil, fmt.Errorf("ensure portal lifecycle: %w", err)
	}
	return &bridgev2.CreateChatResponse{
		PortalKey:  portal.PortalKey,
		Portal:     portal,
		PortalInfo: chatInfo,
	}, nil
}

func (dc *DummyBridgeConnector) composeChatInfo(login *bridgev2.UserLogin, title string) *bridgev2.ChatInfo {
	info := agentremote.BuildLoginDMChatInfo(agentremote.LoginDMChatInfoParams{
		Title:             title,
		Login:             login,
		HumanUserIDPrefix: "dummybridge-user",
		BotUserID:         dummyAgentUserID,
		BotDisplayName:    dummyAgentName,
		CanBackfill:       false,
	})
	if info == nil {
		return nil
	}
	info.Topic = ptr.Ptr(dummyPortalTopic)
	if info.Members != nil && info.Members.MemberMap != nil {
		member := info.Members.MemberMap[dummyAgentUserID]
		member.UserInfo = dummySDKAgent().UserInfo()
		info.Members.MemberMap[dummyAgentUserID] = member
	}
	return info
}

func dummyPortalID(idx int) string {
	return fmt.Sprintf("chat-%d", idx)
}

func dummyChatTitle(idx int) string {
	if idx <= 1 {
		return dummyAgentName
	}
	return fmt.Sprintf("%s %d", dummyAgentName, idx)
}

func futureDuration(seconds int) time.Duration {
	if seconds <= 0 {
		return 0
	}
	return time.Duration(seconds) * time.Second
}
