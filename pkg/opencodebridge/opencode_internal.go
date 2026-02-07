package opencodebridge

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/google/uuid"
	"maunium.net/go/mautrix/bridgev2"
	"maunium.net/go/mautrix/id"

	"github.com/beeper/ai-bridge/pkg/opencode"
)

func (b *Bridge) DispatchInternalMessage(
	ctx context.Context,
	portal *bridgev2.Portal,
	meta *PortalMeta,
	body string,
) (id.EventID, bool, error) {
	if b == nil || b.manager == nil {
		return "", false, errors.New("OpenCode integration is not available")
	}
	trimmed := strings.TrimSpace(body)
	if trimmed == "" {
		return "", false, errors.New("message body is required")
	}
	if meta == nil || meta.ReadOnly || !b.manager.IsConnected(meta.InstanceID) {
		return "", false, errors.New("OpenCode is disconnected")
	}

	eventID := id.EventID(fmt.Sprintf("$opencode-internal-%s", uuid.NewString()))

	runCtx := b.host.BackgroundContext(ctx)
	go func() {
		parts := []opencode.PartInput{{Type: "text", Text: trimmed}}
		response, err := b.manager.SendMessage(runCtx, meta.InstanceID, meta.SessionID, parts, eventID)
		if err != nil {
			b.host.SendSystemNotice(runCtx, portal, "OpenCode send failed: "+err.Error())
			return
		}
		if response != nil {
			if inst := b.manager.getInstance(meta.InstanceID); inst != nil {
				b.manager.handleMessageParts(runCtx, inst, portal, response.Info.Role, response)
			}
		}
	}()

	return eventID, false, nil
}
