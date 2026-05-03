package sdk

import (
	"encoding/json"
	"fmt"
	"maps"
	"reflect"
	"strings"

	"maunium.net/go/mautrix/event"
	"maunium.net/go/mautrix/id"

	"github.com/beeper/agentremote/pkg/matrixevents"
	"github.com/beeper/agentremote/pkg/shared/jsonutil"
	"github.com/beeper/agentremote/turns"
)

const MaxMatrixEventContentBytes = 60000

// BuildCompactFinalUIMessage removes duplicate streaming-only parts from a UI
// message so the payload is suitable for attachment to the final Matrix edit.
// Visible assistant text already lives in the Matrix message body, but
// reasoning/tool/artifact parts are preserved when the size budget allows.
func BuildCompactFinalUIMessage(uiMessage map[string]any) map[string]any {
	if len(uiMessage) == 0 {
		return nil
	}
	out := map[string]any{}
	for key, value := range uiMessage {
		if key != "parts" {
			out[key] = value
		}
	}

	rawParts := extractUIMessageParts(uiMessage)
	if len(rawParts) == 0 {
		return out
	}

	parts := make([]any, 0, len(rawParts))
	for _, raw := range rawParts {
		part, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		switch strings.TrimSpace(stringValue(part["type"])) {
		case "text", "step-start":
			continue
		default:
			parts = append(parts, part)
		}
	}
	if len(parts) > 0 {
		out["parts"] = append([]any(nil), parts...)
	}
	return out
}

// BuildMinimalFinalUIMessage removes optional detail from a UI message while
// preserving stable identifiers and metadata.
func BuildMinimalFinalUIMessage(uiMessage map[string]any) map[string]any {
	if len(uiMessage) == 0 {
		return nil
	}
	out := map[string]any{}
	for _, key := range []string{"id", "role", "metadata"} {
		if value, ok := uiMessage[key]; ok && value != nil {
			out[key] = value
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// BuildFinalEditPayload constructs the canonical final Matrix edit payload.
// The visible replacement body lives in Content; AI UI payload and link previews
// live in m.new_content Extra; edit-only metadata stays top-level.
func BuildFinalEditPayload(content event.MessageEventContent, uiMessage map[string]any, linkPreviews []map[string]any, finishReason string) *FinalEditPayload {
	content.RelatesTo = nil
	content.BeeperLinkPreviews = nil

	var extra map[string]any
	if len(uiMessage) > 0 {
		uiMessage = jsonutil.DeepCloneMap(uiMessage)
		if strings.TrimSpace(finishReason) != "" {
			metadata := jsonutil.DeepCloneMap(jsonutil.ToMap(uiMessage["metadata"]))
			if strings.TrimSpace(stringValue(metadata["finish_reason"])) == "" {
				if metadata == nil {
					metadata = map[string]any{}
				}
				metadata["finish_reason"] = strings.TrimSpace(finishReason)
				uiMessage["metadata"] = metadata
			}
		}
		if extra == nil {
			extra = map[string]any{}
		}
		extra[matrixevents.BeeperAIKey] = uiMessage
	}
	if len(linkPreviews) > 0 {
		if extra == nil {
			extra = map[string]any{}
		}
		extra["com.beeper.linkpreviews"] = jsonutil.DeepCloneAny(linkPreviews)
	}

	return &FinalEditPayload{
		Content: &content,
		Extra:   extra,
		TopLevelExtra: map[string]any{
			"com.beeper.dont_render_edited": true,
		},
	}
}

func hasMeaningfulFinalUIMessage(uiMessage map[string]any) bool {
	if len(uiMessage) == 0 {
		return false
	}
	for key, value := range uiMessage {
		switch key {
		case "id", "role", "metadata":
			continue
		case "parts":
			switch typed := value.(type) {
			case []any:
				if len(typed) > 0 {
					return true
				}
			case []map[string]any:
				if len(typed) > 0 {
					return true
				}
			}
		default:
			if value != nil {
				return true
			}
		}
	}
	return false
}

type FinalEditFitDetails struct {
	OriginalSize         int
	FinalSize            int
	ClearedFormattedBody bool
	DroppedLinkPreviews  bool
	CompactedUIMessage   bool
	DroppedUIMessage     bool
	DroppedExtra         bool
	TrimmedBody          bool
}

func (d FinalEditFitDetails) Changed() bool {
	return d.Summary() != ""
}

func (d FinalEditFitDetails) Summary() string {
	steps := make([]string, 0, 6)
	if d.ClearedFormattedBody {
		steps = append(steps, "cleared_formatted_body")
	}
	if d.DroppedLinkPreviews {
		steps = append(steps, "dropped_link_previews")
	}
	if d.CompactedUIMessage {
		steps = append(steps, "compacted_ui_message")
	}
	if d.DroppedUIMessage {
		steps = append(steps, "dropped_ui_message")
	}
	if d.DroppedExtra {
		steps = append(steps, "dropped_extra")
	}
	if d.TrimmedBody {
		steps = append(steps, "trimmed_body")
	}
	if len(steps) == 0 {
		return ""
	}
	return strings.Join(steps, ",")
}

func cloneFinalEditPayload(payload *FinalEditPayload) *FinalEditPayload {
	if payload == nil {
		return nil
	}
	cloned := &FinalEditPayload{
		Extra:         jsonutil.DeepCloneMap(payload.Extra),
		TopLevelExtra: jsonutil.DeepCloneMap(payload.TopLevelExtra),
	}
	if payload.Content != nil {
		content := *payload.Content
		if payload.Content.Mentions != nil {
			mentions := *payload.Content.Mentions
			if len(mentions.UserIDs) > 0 {
				mentions.UserIDs = append([]id.UserID(nil), mentions.UserIDs...)
			}
			content.Mentions = &mentions
		}
		cloned.Content = &content
	}
	return cloned
}

func estimateFinalEditContentSize(payload *FinalEditPayload, target id.EventID) int {
	if payload == nil || payload.Content == nil {
		return 0
	}
	content := *payload.Content
	if content.Mentions == nil {
		content.Mentions = &event.Mentions{}
	}
	content.SetEdit(target)
	raw := maps.Clone(payload.TopLevelExtra)
	if raw == nil {
		raw = map[string]any{}
	}
	if payload.Extra != nil {
		// Shallow-clone Extra to avoid mutation by mautrix's Content.MarshalJSON
		// which deep-merges Parsed into Raw in-place via mergeMaps.
		raw["m.new_content"] = maps.Clone(payload.Extra)
	}
	data, err := json.Marshal(&event.Content{
		Parsed: &content,
		Raw:    raw,
	})
	if err != nil {
		return MaxMatrixEventContentBytes + 1
	}
	return len(data)
}

func FitFinalEditPayload(payload *FinalEditPayload, target id.EventID) (*FinalEditPayload, FinalEditFitDetails, error) {
	if payload == nil || payload.Content == nil {
		return payload, FinalEditFitDetails{}, nil
	}
	initialSize := estimateFinalEditContentSize(payload, target)
	if initialSize <= MaxMatrixEventContentBytes {
		return payload, FinalEditFitDetails{OriginalSize: initialSize, FinalSize: initialSize}, nil
	}
	fitted := cloneFinalEditPayload(payload)
	details := FinalEditFitDetails{OriginalSize: initialSize}
	size := initialSize

	if fitted.Content.Format != "" || fitted.Content.FormattedBody != "" {
		fitted.Content.Format = ""
		fitted.Content.FormattedBody = ""
		details.ClearedFormattedBody = true
		size = estimateFinalEditContentSize(fitted, target)
	}
	if size > MaxMatrixEventContentBytes && fitted.Extra != nil {
		if _, ok := fitted.Extra["com.beeper.linkpreviews"]; ok {
			delete(fitted.Extra, "com.beeper.linkpreviews")
			details.DroppedLinkPreviews = true
			size = estimateFinalEditContentSize(fitted, target)
		}
	}
	if size > MaxMatrixEventContentBytes && fitted.Extra != nil {
		if rawUI, ok := fitted.Extra[matrixevents.BeeperAIKey].(map[string]any); ok {
			minimalUI := BuildMinimalFinalUIMessage(rawUI)
			switch {
			case minimalUI == nil:
				delete(fitted.Extra, matrixevents.BeeperAIKey)
				details.DroppedUIMessage = true
			case !reflect.DeepEqual(minimalUI, rawUI):
				fitted.Extra[matrixevents.BeeperAIKey] = minimalUI
				details.CompactedUIMessage = true
			}
			size = estimateFinalEditContentSize(fitted, target)
		}
	}
	if size > MaxMatrixEventContentBytes && fitted.Extra != nil {
		if _, ok := fitted.Extra[matrixevents.BeeperAIKey]; ok {
			delete(fitted.Extra, matrixevents.BeeperAIKey)
			details.DroppedUIMessage = true
			size = estimateFinalEditContentSize(fitted, target)
		}
	}
	if size > MaxMatrixEventContentBytes && len(fitted.Extra) > 0 {
		fitted.Extra = nil
		details.DroppedExtra = true
		size = estimateFinalEditContentSize(fitted, target)
	}
	if size > MaxMatrixEventContentBytes && fitted.Content != nil && fitted.Content.Body != "" {
		originalBody := fitted.Content.Body
		best := strings.TrimSpace(originalBody)
		bestSize := size
		low, high := 1, len(originalBody)
		for low <= high {
			mid := (low + high) / 2
			candidate, _ := turns.SplitAtMarkdownBoundary(originalBody, mid)
			candidate = strings.TrimSpace(candidate)
			if candidate == "" {
				high = mid - 1
				continue
			}
			fitted.Content.Body = candidate
			candidateSize := estimateFinalEditContentSize(fitted, target)
			if candidateSize <= MaxMatrixEventContentBytes {
				best = candidate
				bestSize = candidateSize
				low = mid + 1
			} else {
				high = mid - 1
			}
		}
		fitted.Content.Body = best
		details.TrimmedBody = best != strings.TrimSpace(originalBody)
		size = bestSize
	}
	details.FinalSize = size
	if size > MaxMatrixEventContentBytes {
		return nil, details, fmt.Errorf("final edit payload exceeds Matrix content limit after fitting: %d > %d", size, MaxMatrixEventContentBytes)
	}
	return fitted, details, nil
}
