package ai

import (
	"context"
	"testing"

	"maunium.net/go/mautrix/bridgev2"
	"maunium.net/go/mautrix/bridgev2/database"
	"maunium.net/go/mautrix/event"
)

func TestHandleMatrixEdit_ModelRoomRejectsEdits(t *testing.T) {
	oc := &AIClient{}
	edit := &bridgev2.MatrixEdit{
		MatrixEventBase: bridgev2.MatrixEventBase[*event.MessageEventContent]{
			Portal: &bridgev2.Portal{
				Portal: &database.Portal{
					OtherUserID: modelUserID("openai/gpt-5"),
					Metadata:    modelModeTestMeta("openai/gpt-5"),
				},
			},
			Content: &event.MessageEventContent{Body: "updated"},
		},
		EditTarget: &database.Message{},
	}

	err := oc.HandleMatrixEdit(context.Background(), edit)
	if err == nil {
		t.Fatal("expected model room edit to be rejected")
	}
	if err.Error() != bridgev2.ErrEditsNotSupportedInPortal.Error() {
		t.Fatalf("expected ErrEditsNotSupportedInPortal, got %v", err)
	}
}
