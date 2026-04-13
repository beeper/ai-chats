package sdk

import (
	"context"
	"fmt"
	"strings"

	"go.mau.fi/util/ptr"
	"maunium.net/go/mautrix/bridgev2"
	"maunium.net/go/mautrix/bridgev2/database"
	"maunium.net/go/mautrix/bridgev2/networkid"
	"maunium.net/go/mautrix/event"
)

const AIRoomKindAgent = "agent"

// DMChatInfoParams holds the parameters for BuildDMChatInfo.
type DMChatInfoParams struct {
	Title               string
	Topic               string
	HumanUserID         networkid.UserID
	LoginID             networkid.UserLoginID
	HumanSender         *bridgev2.EventSender
	BotUserID           networkid.UserID
	BotDisplayName      string
	BotSender           *bridgev2.EventSender
	BotUserInfo         *bridgev2.UserInfo
	BotMemberEventExtra map[string]any
	CanBackfill         bool
}

// BuildDMChatInfo creates a ChatInfo for a DM room between a human user and a bot ghost.
func BuildDMChatInfo(p DMChatInfoParams) *bridgev2.ChatInfo {
	humanSender := bridgev2.EventSender{
		Sender:      p.HumanUserID,
		IsFromMe:    true,
		SenderLogin: p.LoginID,
	}
	if p.HumanSender != nil {
		humanSender = *p.HumanSender
	}
	botSender := bridgev2.EventSender{
		Sender:      p.BotUserID,
		SenderLogin: p.LoginID,
	}
	if p.BotSender != nil {
		botSender = *p.BotSender
	}
	botInfo := p.BotUserInfo
	if botInfo == nil {
		botInfo = &bridgev2.UserInfo{
			Name:  ptr.Ptr(p.BotDisplayName),
			IsBot: ptr.Ptr(true),
		}
	}
	memberEventExtra := p.BotMemberEventExtra
	if memberEventExtra == nil && p.BotDisplayName != "" {
		memberEventExtra = map[string]any{
			"displayname": p.BotDisplayName,
		}
	}
	members := bridgev2.ChatMemberMap{
		p.HumanUserID: {
			EventSender: humanSender,
			Membership:  event.MembershipJoin,
		},
		p.BotUserID: {
			EventSender:      botSender,
			Membership:       event.MembershipJoin,
			UserInfo:         botInfo,
			MemberEventExtra: memberEventExtra,
		},
	}
	return &bridgev2.ChatInfo{
		Name:        ptr.Ptr(p.Title),
		Topic:       ptr.NonZero(p.Topic),
		Type:        ptr.Ptr(database.RoomTypeDM),
		CanBackfill: p.CanBackfill,
		Members: &bridgev2.ChatMemberList{
			IsFull:      true,
			OtherUserID: p.BotUserID,
			MemberMap:   members,
		},
	}
}

type LoginDMChatInfoParams struct {
	Title               string
	Topic               string
	Login               *bridgev2.UserLogin
	HumanUserIDPrefix   string
	HumanSender         *bridgev2.EventSender
	BotUserID           networkid.UserID
	BotDisplayName      string
	BotSender           *bridgev2.EventSender
	BotUserInfo         *bridgev2.UserInfo
	BotMemberEventExtra map[string]any
	CanBackfill         bool
}

func BuildLoginDMChatInfo(p LoginDMChatInfoParams) *bridgev2.ChatInfo {
	if p.Login == nil {
		return nil
	}
	return BuildDMChatInfo(DMChatInfoParams{
		Title:               p.Title,
		Topic:               p.Topic,
		HumanUserID:         HumanUserID(p.HumanUserIDPrefix, p.Login.ID),
		LoginID:             p.Login.ID,
		HumanSender:         p.HumanSender,
		BotUserID:           p.BotUserID,
		BotDisplayName:      p.BotDisplayName,
		BotSender:           p.BotSender,
		BotUserInfo:         p.BotUserInfo,
		BotMemberEventExtra: p.BotMemberEventExtra,
		CanBackfill:         p.CanBackfill,
	})
}

type ConfigureDMPortalParams struct {
	Portal       *bridgev2.Portal
	Title        string
	Topic        string
	OtherUserID  networkid.UserID
	Save         bool
	MutatePortal func(*bridgev2.Portal)
}

func ConfigureDMPortal(ctx context.Context, p ConfigureDMPortalParams) error {
	if p.Portal == nil {
		return fmt.Errorf("missing portal")
	}
	p.Portal.RoomType = database.RoomTypeDM
	p.Portal.OtherUserID = p.OtherUserID
	p.Portal.Name = strings.TrimSpace(p.Title)
	p.Portal.NameSet = p.Portal.Name != ""
	p.Portal.Topic = strings.TrimSpace(p.Topic)
	p.Portal.TopicSet = p.Portal.Topic != ""
	if p.MutatePortal != nil {
		p.MutatePortal(p.Portal)
	}
	if !p.Save {
		return nil
	}
	return p.Portal.Save(ctx)
}

func BuildChatInfoWithFallback(metaTitle, portalName, fallbackTitle, portalTopic string) *bridgev2.ChatInfo {
	title := coalesceStrings(metaTitle, portalName, fallbackTitle)
	return &bridgev2.ChatInfo{
		Name:  ptr.Ptr(title),
		Topic: ptr.NonZero(portalTopic),
	}
}

// BuildBotUserInfo returns a UserInfo for an AI bot ghost with the given name and identifiers.
func BuildBotUserInfo(name string, identifiers ...string) *bridgev2.UserInfo {
	return &bridgev2.UserInfo{
		Name:        ptr.Ptr(name),
		IsBot:       ptr.Ptr(true),
		Identifiers: identifiers,
	}
}

func NormalizeAIRoomTypeV2(roomType database.RoomType, aiKind string) string {
	if aiKind != "" && aiKind != AIRoomKindAgent {
		return "group"
	}
	switch roomType {
	case database.RoomTypeDM:
		return "dm"
	case database.RoomTypeSpace:
		return "space"
	default:
		return "group"
	}
}

func ApplyAgentRemoteBridgeInfo(content *event.BridgeEventContent, protocolID string, roomType database.RoomType, aiKind string) {
	if content == nil {
		return
	}
	if protocolID != "" {
		content.Protocol.ID = protocolID
	}
	content.BeeperRoomTypeV2 = NormalizeAIRoomTypeV2(roomType, aiKind)
}

// coalesceStrings returns the first non-empty string from the arguments.
func coalesceStrings(values ...string) string {
	for _, v := range values {
		if v != "" {
			return v
		}
	}
	return ""
}
