package bridgeutil

import (
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
