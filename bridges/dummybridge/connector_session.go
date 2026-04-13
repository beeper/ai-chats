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
	"maunium.net/go/mautrix/bridgev2/networkid"

	"github.com/beeper/agentremote/sdk"
)

const dummyPortalTopic = "DummyBridge demo room for turns, streaming, tools, approvals, and artifacts."

type dummySession struct {
	login *bridgev2.UserLogin
	log   zerolog.Logger
}

func (dc *DummyBridgeConnector) loggerForLogin(login *bridgev2.UserLogin) zerolog.Logger {
	if login == nil {
		return zerolog.Nop()
	}
	return login.Log.With().Str("component", "dummybridge").Logger()
}

func (dc *DummyBridgeConnector) onConnect(ctx context.Context, info *sdk.LoginInfo) (*dummySession, error) {
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
		login: login,
		log:   log,
	}, nil
}

func (dc *DummyBridgeConnector) onDisconnect(_ *dummySession) {}

func (dc *DummyBridgeConnector) getChatInfo(conv *sdk.Conversation) (*bridgev2.ChatInfo, error) {
	if conv == nil || conv.Portal() == nil {
		return sdk.BuildChatInfoWithFallback("", "", dummyAgentName, dummyPortalTopic), nil
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
	info := sdk.BuildChatInfoWithFallback(title, portal.Name, dummyAgentName, portal.Topic)
	if strings.TrimSpace(meta.Topic) != "" {
		info.Topic = ptr.Ptr(meta.Topic)
	}
	return info, nil
}

func (dc *DummyBridgeConnector) getUserInfo(_ *bridgev2.Ghost) (*bridgev2.UserInfo, error) {
	return dummySDKAgent().UserInfo(), nil
}

func (dc *DummyBridgeConnector) ensureInitialRoom(ctx context.Context, login *bridgev2.UserLogin) error {
	dc.chatMu.Lock()
	defer dc.chatMu.Unlock()

	meta := loginMetadata(login)
	if strings.TrimSpace(meta.Provider) == "" {
		meta.Provider = ProviderDummyBridge
		if err := login.Save(ctx); err != nil {
			return fmt.Errorf("save login metadata: %w", err)
		}
	}
	if _, err := dc.ensureChatForIndexLocked(ctx, login, 1); err != nil {
		return err
	}
	return nil
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

	if err := sdk.ConfigureDMPortal(ctx, sdk.ConfigureDMPortalParams{
		Portal:      portal,
		Title:       title,
		Topic:       dummyPortalTopic,
		OtherUserID: dummyAgentUserID,
		Save:        false,
	}); err != nil {
		return nil, fmt.Errorf("save portal: %w", err)
	}

	chatInfo := dc.composeChatInfo(login, title)
	if err := portal.Save(ctx); err != nil {
		return nil, fmt.Errorf("save portal: %w", err)
	}
	if portal.MXID == "" {
		if err := portal.CreateMatrixRoom(ctx, login, chatInfo); err != nil {
			return nil, fmt.Errorf("create Matrix room: %w", err)
		}
	} else {
		portal.UpdateInfo(ctx, chatInfo, login, nil, time.Time{})
	}
	portal.UpdateBridgeInfo(ctx)
	portal.UpdateCapabilities(ctx, login, true)
	return &bridgev2.CreateChatResponse{
		PortalKey:  portal.PortalKey,
		Portal:     portal,
		PortalInfo: chatInfo,
	}, nil
}

func (dc *DummyBridgeConnector) composeChatInfo(login *bridgev2.UserLogin, title string) *bridgev2.ChatInfo {
	return sdk.BuildLoginDMChatInfo(sdk.LoginDMChatInfoParams{
		Title:             title,
		Topic:             dummyPortalTopic,
		Login:             login,
		HumanUserIDPrefix: "dummybridge-user",
		BotUserID:         dummyAgentUserID,
		BotDisplayName:    dummyAgentName,
		BotUserInfo:       dummySDKAgent().UserInfo(),
		CanBackfill:       false,
	})
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
