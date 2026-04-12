package ai

import "testing"

func TestShouldEnsureDefaultChat(t *testing.T) {
	enabled := true
	disabled := false

	tests := []struct {
		name string
		cfg  *aiLoginConfig
		want bool
	}{
		{
			name: "nil metadata",
			cfg:  nil,
			want: false,
		},
		{
			name: "new login with nil agents",
			cfg:  &aiLoginConfig{},
			want: true,
		},
		{
			name: "agents enabled",
			cfg:  &aiLoginConfig{Agents: &enabled},
			want: true,
		},
		{
			name: "agents disabled",
			cfg:  &aiLoginConfig{Agents: &disabled},
			want: false,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := shouldEnsureDefaultChat(tc.cfg); got != tc.want {
				t.Fatalf("shouldEnsureDefaultChat() = %v, want %v", got, tc.want)
			}
		})
	}
}
