package msgconv

import (
	"testing"

	"maunium.net/go/mautrix/id"
)

func TestRelatesToReplaceRequiresInitialEventID(t *testing.T) {
	rel := RelatesToReplace("", id.EventID("$reply"))
	if rel != nil {
		t.Fatalf("expected nil relates_to when initial event id is missing, got %#v", rel)
	}
}
