package connector

import (
	"context"
	"strings"
	"testing"
)

func TestNexusContactsDisablesArchiveRestoreMerge(t *testing.T) {
	ctx := context.Background()
	disabled := []string{"archive", "restore", "merge", "archive_contact", "restore_contact", "merge_contacts", "mergecontacts"}
	for _, action := range disabled {
		_, err := executeNexusContacts(ctx, map[string]any{"action": action})
		if err == nil {
			t.Fatalf("expected error for disabled action %q", action)
		}
		if !strings.Contains(strings.ToLower(err.Error()), "disabled") {
			t.Fatalf("expected disabled error for %q, got %v", action, err)
		}
	}
}
