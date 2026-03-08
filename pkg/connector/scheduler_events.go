package connector

import (
	"context"
	"encoding/json"
	"reflect"

	"maunium.net/go/mautrix/bridgev2"
	"maunium.net/go/mautrix/event"

	"github.com/beeper/ai-bridge/pkg/bridgeadapter"
)

func init() {
	event.TypeMap[ScheduleTickEventType] = reflect.TypeOf(ScheduleTickContent{})
}

var ScheduleTickEventType = event.Type{
	Type:  "com.beeper.ai.schedule.tick",
	Class: event.MessageEventType,
}

type ScheduleTickContent struct {
	Kind           string `json:"kind"`
	EntityID       string `json:"entityId"`
	Revision       int    `json:"revision"`
	ScheduledForMs int64  `json:"scheduledForMs"`
	RunKey         string `json:"runKey"`
	Reason         string `json:"reason,omitempty"`
}

func (oc *OpenAIConnector) handleScheduleTickEvent(ctx context.Context, evt *event.Event) {
	if oc == nil || oc.br == nil || evt == nil {
		return
	}
	portal, err := oc.br.GetPortalByMXID(ctx, evt.RoomID)
	if err != nil || portal == nil {
		oc.br.Log.Warn().Err(err).Stringer("room_id", evt.RoomID).Msg("Failed to resolve portal for schedule tick")
		return
	}
	if kind := moduleRoomKind(portalMeta(portal)); kind != "cron" && kind != "heartbeat" {
		oc.br.Log.Warn().Stringer("portal", portal.PortalKey).Stringer("room_id", evt.RoomID).Msg("Ignoring schedule tick for non-scheduler room")
		return
	}
	if !bridgeadapter.IsMatrixBotUser(ctx, oc.br, evt.Sender) || oc.br.Bot == nil || evt.Sender != oc.br.Bot.GetMXID() {
		oc.br.Log.Warn().Stringer("portal", portal.PortalKey).Stringer("sender", evt.Sender).Msg("Ignoring schedule tick from non-bot sender")
		return
	}
	login := resolvePortalLogin(oc.br, portal)
	if login == nil {
		oc.br.Log.Warn().Stringer("portal", portal.PortalKey).Msg("No login found for schedule tick portal")
		return
	}
	client, ok := login.Client.(*AIClient)
	if !ok || client == nil || client.scheduler == nil {
		oc.br.Log.Warn().Stringer("portal", portal.PortalKey).Msg("No scheduler client available for schedule tick")
		return
	}

	// Parse eagerly so malformed content does not get retried through deeper layers.
	var content ScheduleTickContent
	if err := json.Unmarshal(evt.Content.VeryRaw, &content); err != nil {
		oc.br.Log.Warn().Err(err).Stringer("event_id", evt.ID).Msg("Failed to parse schedule tick")
		return
	}
	client.scheduler.HandleScheduleTickContent(ctx, content, evt, portal)
}

func resolvePortalLogin(br *bridgev2.Bridge, portal *bridgev2.Portal) *bridgev2.UserLogin {
	if br == nil || portal == nil || portal.Receiver == "" {
		return nil
	}
	return br.GetCachedUserLoginByID(portal.Receiver)
}
