package ai

import (
	"context"

	"github.com/rs/zerolog"
	"maunium.net/go/mautrix/bridgev2"
)

func (oc *AIClient) finalizeResponsesStream(
	ctx context.Context,
	log zerolog.Logger,
	portal *bridgev2.Portal,
	state *streamingState,
	meta *PortalMetadata,
) {
	if state.finishReason == "" {
		state.finishReason = "stop"
	}

	// Send any generated images as separate messages
	for _, img := range state.pendingImages {
		imageData, mimeType, err := decodeBase64Image(img.imageB64)
		if err != nil {
			log.Warn().Err(err).Str("item_id", img.itemID).Msg("Failed to decode generated image")
			continue
		}
		// Native API image generation; no user-provided prompt is available for captioning.
		eventID, mediaURL, err := oc.sendGeneratedImage(ctx, portal, imageData, mimeType, img.turnID, "")
		if err != nil {
			log.Warn().Err(err).Str("item_id", img.itemID).Msg("Failed to send generated image to Matrix")
			continue
		}
		recordGeneratedFile(state, mediaURL, mimeType)
		state.writer().File(ctx, mediaURL, mimeType)
		log.Info().Stringer("event_id", eventID).Str("item_id", img.itemID).Msg("Sent generated image to Matrix")
	}
	oc.completeStreamingSuccess(ctx, log, portal, state, meta)

	log.Info().
		Str("turn_id", state.turn.ID()).
		Str("finish_reason", state.finishReason).
		Int("content_length", state.accumulated.Len()).
		Int("reasoning_length", state.reasoning.Len()).
		Int("tool_calls", len(state.toolCalls)).
		Str("response_id", state.responseID).
		Int("images_sent", len(state.pendingImages)).
		Msg("Responses API streaming finished")
}
