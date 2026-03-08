package connector

import (
	"time"

	"maunium.net/go/mautrix/bridgev2/commands"

	"github.com/beeper/ai-bridge/pkg/connector/commandregistry"
	airuntime "github.com/beeper/ai-bridge/pkg/runtime"
)

var _ = registerAICommand(commandregistry.Definition{
	Name:           "status",
	Description:    "Show current session status",
	Section:        HelpSectionAI,
	RequiresPortal: true,
	RequiresLogin:  true,
	Handler:        fnStatus,
})

func fnStatus(ce *commands.Event) {
	client, meta, ok := requireClientMeta(ce)
	if !ok {
		return
	}
	isGroup := client.isGroupChat(ce.Ctx, ce.Portal)
	queueSettings, _, _, _ := client.resolveQueueSettingsForPortal(ce.Ctx, ce.Portal, meta, "", airuntime.QueueInlineOptions{})
	ce.Reply("%s", client.buildStatusText(ce.Ctx, ce.Portal, meta, isGroup, queueSettings))
}

var _ = registerAICommand(commandregistry.Definition{
	Name:           "reset",
	Description:    "Start a new session/thread in this room",
	Section:        HelpSectionAI,
	RequiresPortal: true,
	RequiresLogin:  true,
	Handler:        fnReset,
})

func fnReset(ce *commands.Event) {
	client, meta, ok := requireClientMeta(ce)
	if !ok {
		return
	}

	meta.SessionResetAt = time.Now().UnixMilli()
	client.savePortalQuiet(ce.Ctx, ce.Portal, "session reset")
	client.clearPendingQueue(ce.Portal.MXID)
	client.cancelRoomRun(ce.Portal.MXID)

	ce.Reply("%s", formatSystemAck("Session reset."))
}

var _ = registerAICommand(commandregistry.Definition{
	Name:           "stop",
	Description:    "Abort the current run and clear the pending queue",
	Section:        HelpSectionAI,
	RequiresPortal: true,
	RequiresLogin:  true,
	Handler:        fnStop,
})

func fnStop(ce *commands.Event) {
	client, meta, ok := requireClientMeta(ce)
	if !ok {
		return
	}
	stopped := client.abortRoom(ce.Ctx, ce.Portal, meta)
	ce.Reply("%s", formatAbortNotice(stopped))
}
