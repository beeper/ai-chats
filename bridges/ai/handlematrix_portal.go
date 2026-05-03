package ai

import (
	"context"
	"errors"
	"fmt"

	"maunium.net/go/mautrix/bridgev2"
)

func (oc *AIClient) savePortal(ctx context.Context, portal *bridgev2.Portal, action string) error {
	if oc == nil || portal == nil {
		return nil
	}
	var err error
	portal, err = resolvePortalForAIDB(ctx, oc, portal)
	if err != nil {
		return fmt.Errorf("resolve portal for %s: %w", action, err)
	}
	if err := portal.Save(ctx); err != nil {
		return fmt.Errorf("save portal for %s: %w", action, err)
	}
	return nil
}

// savePortalQuiet saves portal and logs errors without failing
func (oc *AIClient) savePortalQuiet(ctx context.Context, portal *bridgev2.Portal, action string) {
	if err := oc.savePortal(ctx, portal, action); err != nil && !errors.Is(err, context.Canceled) {
		oc.loggerForContext(ctx).Warn().Err(err).Str("action", action).Msg("Failed to save portal")
	}
}
