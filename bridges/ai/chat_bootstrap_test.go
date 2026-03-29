package ai

import "testing"

func TestShouldEnsureDefaultChat(t *testing.T) {
	enabled := true
	disabled := false

	tests := []struct {
		name string
		meta *UserLoginMetadata
		want bool
	}{
		{
			name: "nil metadata",
			meta: nil,
			want: false,
		},
		{
			name: "new login with nil agents",
			meta: &UserLoginMetadata{},
			want: true,
		},
		{
			name: "agents enabled",
			meta: &UserLoginMetadata{Agents: &enabled},
			want: true,
		},
		{
			name: "agents disabled",
			meta: &UserLoginMetadata{Agents: &disabled},
			want: false,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := shouldEnsureDefaultChat(tc.meta); got != tc.want {
				t.Fatalf("shouldEnsureDefaultChat() = %v, want %v", got, tc.want)
			}
		})
	}
}
