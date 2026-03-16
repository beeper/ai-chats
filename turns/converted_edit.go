package turns

import (
	"maunium.net/go/mautrix/bridgev2"
	"maunium.net/go/mautrix/event"
)

// BuildConvertedEdit wraps a message content payload into a single-part Matrix edit.
func BuildConvertedEdit(content *event.MessageEventContent, topLevelExtra map[string]any) *bridgev2.ConvertedEdit {
	if content == nil {
		return nil
	}
	return &bridgev2.ConvertedEdit{
		ModifiedParts: []*bridgev2.ConvertedEditPart{{
			Type:          event.EventMessage,
			Content:       content,
			TopLevelExtra: topLevelExtra,
		}},
	}
}
