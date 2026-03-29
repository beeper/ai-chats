package turns

import (
	"maunium.net/go/mautrix/bridgev2"
	"maunium.net/go/mautrix/event"
)

// BuildConvertedEdit wraps a message content payload into a single-part Matrix edit.
func BuildConvertedEdit(content *event.MessageEventContent, topLevelExtra map[string]any) *bridgev2.ConvertedEdit {
	return BuildConvertedEditWithExtra(content, nil, topLevelExtra)
}

// BuildConvertedEditWithExtra wraps a message content payload plus m.new_content
// extras into a single-part Matrix edit.
func BuildConvertedEditWithExtra(content *event.MessageEventContent, extra, topLevelExtra map[string]any) *bridgev2.ConvertedEdit {
	if content == nil {
		return nil
	}
	return &bridgev2.ConvertedEdit{
		ModifiedParts: []*bridgev2.ConvertedEditPart{{
			Type:          event.EventMessage,
			Content:       content,
			Extra:         extra,
			TopLevelExtra: topLevelExtra,
		}},
	}
}
