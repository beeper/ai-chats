package connector

import (
	"context"
	"strings"

	"maunium.net/go/mautrix/bridgev2"
	"maunium.net/go/mautrix/event"
)

type mediaUnderstandingResult struct {
	Outputs    []MediaUnderstandingOutput
	Decisions  []MediaUnderstandingDecision
	Body       string
	Transcript string
	FileBlocks []string
}

func mediaCapabilityForMessage(msgType event.MessageType) (MediaUnderstandingCapability, bool) {
	switch msgType {
	case event.MsgImage:
		return MediaCapabilityImage, true
	case event.MsgAudio:
		return MediaCapabilityAudio, true
	case event.MsgVideo:
		return MediaCapabilityVideo, true
	default:
		return "", false
	}
}

func (oc *AIClient) applyMediaUnderstandingForAttachments(
	ctx context.Context,
	portal *bridgev2.Portal,
	meta *PortalMetadata,
	capability MediaUnderstandingCapability,
	attachments []mediaAttachment,
	rawCaption string,
	hasUserCaption bool,
) (*mediaUnderstandingResult, error) {
	result := &mediaUnderstandingResult{}
	toolsCfg := oc.connector.Config.Tools.Media
	capCfg := toolsCfg.ConfigForCapability(capability)

	if capCfg != nil && capCfg.Enabled != nil && !*capCfg.Enabled {
		result.Decisions = []MediaUnderstandingDecision{{
			Capability: capability,
			Outcome:    MediaOutcomeDisabled,
		}}
		return result, nil
	}

	selected := selectMediaAttachments(attachments, capCfg.Attachments)
	if len(selected) == 0 {
		result.Decisions = []MediaUnderstandingDecision{{
			Capability: capability,
			Outcome:    MediaOutcomeNoAttachment,
		}}
		return result, nil
	}

	// Skip image understanding when the primary model supports vision.
	if capability == MediaCapabilityImage {
		responder := oc.responderForMeta(ctx, meta)
		if responder != nil && responder.SupportsVision {
			attachmentDecisions := make([]MediaUnderstandingAttachmentDecision, 0, len(selected))
			modelID := responder.ModelID
			provider := normalizeMediaProviderID(oc.responderProvider(responder))
			for _, attachment := range selected {
				attempt := MediaUnderstandingModelDecision{
					Type:     MediaEntryTypeProvider,
					Provider: provider,
					Model:    modelID,
					Outcome:  MediaOutcomeSkipped,
					Reason:   "primary model supports vision",
				}
				attachmentDecisions = append(attachmentDecisions, MediaUnderstandingAttachmentDecision{
					AttachmentIndex: attachment.Index,
					Attempts:        []MediaUnderstandingModelDecision{attempt},
					Chosen:          &attempt,
				})
			}
			result.Decisions = []MediaUnderstandingDecision{{
				Capability:  capability,
				Outcome:     MediaOutcomeSkipped,
				Attachments: attachmentDecisions,
			}}
			return result, nil
		}
	}

	entries := resolveMediaEntries(toolsCfg, capCfg, capability)
	if len(entries) == 0 {
		if auto := oc.resolveAutoMediaEntries(capability, capCfg, meta); len(auto) > 0 {
			entries = append(entries, auto...)
		}
	}
	if len(entries) == 0 {
		attachmentDecisions := make([]MediaUnderstandingAttachmentDecision, 0, len(selected))
		for _, attachment := range selected {
			attachmentDecisions = append(attachmentDecisions, MediaUnderstandingAttachmentDecision{
				AttachmentIndex: attachment.Index,
				Attempts:        nil,
			})
		}
		result.Decisions = []MediaUnderstandingDecision{{
			Capability:  capability,
			Outcome:     MediaOutcomeSkipped,
			Attachments: attachmentDecisions,
		}}
		return result, nil
	}

	var outputs []MediaUnderstandingOutput
	var lastErr error
	attachmentDecisions := make([]MediaUnderstandingAttachmentDecision, 0, len(selected))
	for _, attachment := range selected {
		output, attempts, err := oc.runMediaUnderstandingEntries(ctx, capability, attachment, entries, capCfg)
		if err != nil {
			lastErr = err
		}
		decision := MediaUnderstandingAttachmentDecision{
			AttachmentIndex: attachment.Index,
			Attempts:        attempts,
		}
		for i := range attempts {
			if attempts[i].Outcome == MediaOutcomeSuccess {
				decision.Chosen = &attempts[i]
				break
			}
		}
		if output != nil {
			outputs = append(outputs, *output)
		}
		attachmentDecisions = append(attachmentDecisions, decision)
	}

	result.Outputs = outputs
	decisionOutcome := MediaOutcomeSkipped
	if len(outputs) > 0 {
		decisionOutcome = MediaOutcomeSuccess
	}
	result.Decisions = []MediaUnderstandingDecision{{
		Capability:  capability,
		Outcome:     decisionOutcome,
		Attachments: attachmentDecisions,
	}}
	oc.loggerForContext(ctx).Debug().
		Str("capability", string(capability)).
		Str("outcome", string(decisionOutcome)).
		Int("attachments", len(selected)).
		Int("outputs", len(outputs)).
		Msg("Media understanding decision")

	bodyBase := ""
	if hasUserCaption {
		bodyBase = rawCaption
	}
	combined := formatMediaUnderstandingBody(bodyBase, outputs)
	if len(outputs) > 0 {
		audioOutputs := filterMediaOutputs(outputs, MediaKindAudioTranscription)
		if len(audioOutputs) > 0 {
			result.Transcript = formatAudioTranscripts(audioOutputs)
		}
	}

	fileBlocks := oc.extractMediaFileBlocks(ctx, selected, outputs)
	if len(fileBlocks) > 0 {
		result.FileBlocks = fileBlocks
		if combined == "" {
			combined = strings.Join(fileBlocks, "\n\n")
		} else {
			combined = strings.TrimSpace(combined + "\n\n" + strings.Join(fileBlocks, "\n\n"))
		}
	}
	result.Body = combined
	if len(outputs) == 0 && lastErr != nil {
		return result, lastErr
	}
	return result, nil
}
