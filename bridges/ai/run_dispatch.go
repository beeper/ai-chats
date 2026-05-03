package ai

import (
	"context"

	"maunium.net/go/mautrix/bridgev2"
	"maunium.net/go/mautrix/event"
)

func (oc *AIClient) launchAgentLoopRun(
	ctx context.Context,
	evt *event.Event,
	portal *bridgev2.Portal,
	meta *PortalMetadata,
	prompt PromptContext,
	done func(),
) {
	go func() {
		defer done()
		run, _ := oc.selectAgentLoopRunFunc(meta, prompt)
		if _, _, err := run(ctx, evt, portal, meta, prompt); err != nil {
			oc.loggerForContext(ctx).Warn().Err(err).Msg("AI response run failed")
		}
	}()
}
