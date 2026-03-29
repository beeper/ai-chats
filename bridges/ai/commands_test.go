package ai

import "testing"

func TestParseAgentsCommandArgs(t *testing.T) {
	tests := []struct {
		name             string
		args             []string
		currentlyEnabled bool
		wantEnabled      bool
		wantChanged      bool
		wantErr          bool
	}{
		{name: "bare shows status", args: nil, currentlyEnabled: true, wantEnabled: true, wantChanged: false},
		{name: "bare shows status disabled", args: nil, currentlyEnabled: false, wantEnabled: false, wantChanged: false},
		{name: "status when enabled", args: []string{"status"}, currentlyEnabled: true, wantEnabled: true, wantChanged: false},
		{name: "status when disabled", args: []string{"status"}, currentlyEnabled: false, wantEnabled: false, wantChanged: false},
		{name: "on enables", args: []string{"on"}, currentlyEnabled: false, wantEnabled: true, wantChanged: true},
		{name: "off disables", args: []string{"off"}, currentlyEnabled: true, wantEnabled: false, wantChanged: true},
		{name: "invalid usage", args: []string{"wat"}, currentlyEnabled: true, wantErr: true},
		{name: "too many args", args: []string{"on", "extra"}, currentlyEnabled: true, wantErr: true},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			gotEnabled, gotChanged, _, err := parseAgentsCommandArgs(tc.args, tc.currentlyEnabled)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("expected error")
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if gotEnabled != tc.wantEnabled {
				t.Fatalf("enabled=%v, want %v", gotEnabled, tc.wantEnabled)
			}
			if gotChanged != tc.wantChanged {
				t.Fatalf("changed=%v, want %v", gotChanged, tc.wantChanged)
			}
		})
	}
}
