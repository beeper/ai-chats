package streamtransport

import (
	"maunium.net/go/mautrix/bridgev2"
)

// EnsureDontRenderEdited marks every edit part so clients can suppress "edited" UI chrome.
func EnsureDontRenderEdited(edit *bridgev2.ConvertedEdit) {
	if edit == nil {
		return
	}
	for _, part := range edit.ModifiedParts {
		if part == nil {
			continue
		}
		if part.TopLevelExtra == nil {
			part.TopLevelExtra = map[string]any{}
		}
		part.TopLevelExtra["com.beeper.dont_render_edited"] = true
	}
}
