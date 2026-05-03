package bridgeentry

import "testing"

func TestRegistryKeepsSupportedBridges(t *testing.T) {
	wantNames := []string{"ai", "codex", "dummybridge"}
	gotNames := Names()
	if len(gotNames) != len(wantNames) {
		t.Fatalf("Names() length = %d, want %d (%v)", len(gotNames), len(wantNames), gotNames)
	}
	for i, want := range wantNames {
		if gotNames[i] != want {
			t.Fatalf("Names()[%d] = %q, want %q (all names: %v)", i, gotNames[i], want, gotNames)
		}
	}
}
