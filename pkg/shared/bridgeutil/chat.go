package bridgeutil

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
	return &bridgev2.ChatInfo{
		Name:        ptr.Ptr(p.Title),
		Topic:       ptr.NonZero(p.Topic),
		Type:        ptr.Ptr(database.RoomTypeDM),
		CanBackfill: p.CanBackfill,
		Members: &bridgev2.ChatMemberList{
			IsFull:      true,
			OtherUserID: p.BotUserID,
			MemberMap: bridgev2.ChatMemberMap{
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
			},
		},
	}
}

type LoginDMChatInfoParams struct {
	Title               string
	Topic               string
	Login               *bridgev2.UserLogin
	HumanUserID         networkid.UserID
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
		HumanUserID:         p.HumanUserID,
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
	return &bridgev2.ChatInfo{
		Name:  ptr.Ptr(firstNonEmpty(metaTitle, portalName, fallbackTitle)),
		Topic: ptr.NonZero(strings.TrimSpace(portalTopic)),
	}
}

func BuildPortalFallbackChatInfo(portal *bridgev2.Portal, fallbackTitle string) *bridgev2.ChatInfo {
	if portal == nil {
		return nil
	}
	return BuildChatInfoWithFallback("", portal.Name, fallbackTitle, portal.Topic)
}

func MessageStatusEventInfo(portal *bridgev2.Portal, evt *event.Event) *bridgev2.MessageStatusEventInfo {
	if portal == nil || evt == nil {
		return nil
	}
	info := bridgev2.StatusEventInfoFromEvent(evt)
	if info == nil {
		return nil
	}
	if info.RoomID == "" && portal.MXID != "" {
		info.RoomID = portal.MXID
	}
	return info
}

func SendMessageStatus(ctx context.Context, portal *bridgev2.Portal, evt *event.Event, status bridgev2.MessageStatus) {
	if portal == nil || portal.Bridge == nil {
		return
	}
	info := MessageStatusEventInfo(portal, evt)
	if info == nil {
		return
	}
	portal.Bridge.Matrix.SendMessageStatus(ctx, &status, info)
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if strings.TrimSpace(v) != "" {
			return strings.TrimSpace(v)
		}
	}
	return ""
}
