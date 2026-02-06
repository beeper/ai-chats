package connector

import (
	"strings"
	"testing"
)

func TestFormatDesktopAccountID(t *testing.T) {
	single := formatDesktopAccountID(false, "Main Desktop", "WhatsApp", "acc_123")
	if single != "whatsapp_acc_123" {
		t.Fatalf("unexpected single-instance account id: %q", single)
	}

	multi := formatDesktopAccountID(true, "Main Desktop", "WhatsApp", "acc_123")
	if multi != "main_desktop_whatsapp_acc_123" {
		t.Fatalf("unexpected multi-instance account id: %q", multi)
	}
}

func TestSanitizeDesktopInstanceKey(t *testing.T) {
	if got := sanitizeDesktopInstanceKey("  Work/API #1  "); got != "work_api_1" {
		t.Fatalf("unexpected sanitized key: %q", got)
	}
	if got := sanitizeDesktopInstanceKey("!!!"); got != desktopDefaultInstance {
		t.Fatalf("expected default instance fallback, got %q", got)
	}
}

func TestBuildDesktopAccountDisplayFromView(t *testing.T) {
	display := buildDesktopAccountDisplayFromView(desktopAccountView{
		accountID:   "acc-1",
		network:     "WhatsApp",
		userID:      "user-42",
		fullName:    "Alice Example",
		username:    "alice",
		phoneNumber: "+14155552671",
		email:       "alice@example.com",
	})
	if !strings.HasPrefix(display, "Alice Example") {
		t.Fatalf("expected full name baseline, got %q", display)
	}
	expectedFragments := []string{
		"username: alice",
		"phone: +14155552671",
		"email: alice@example.com",
		"user id: user-42",
		"account id: acc-1",
		"network: WhatsApp",
	}
	for _, fragment := range expectedFragments {
		if !strings.Contains(display, fragment) {
			t.Fatalf("missing display fragment %q in %q", fragment, display)
		}
	}
}

func TestBuildDesktopAccountDisplayDeduplicatesBaseline(t *testing.T) {
	display := buildDesktopAccountDisplayFromView(desktopAccountView{
		accountID: "acc-1",
		fullName:  "alice",
		username:  "alice",
	})
	if strings.Contains(display, "username: alice") {
		t.Fatalf("username fragment should be deduplicated when equal to baseline: %q", display)
	}
}

func TestRenderDesktopAccountHintPromptGate(t *testing.T) {
	none := renderDesktopAccountHintPrompt(desktopAccountHintsSnapshot{})
	if none != "" {
		t.Fatalf("expected empty prompt for zero accounts, got %q", none)
	}

	single := renderDesktopAccountHintPrompt(desktopAccountHintsSnapshot{
		BaseURL: "https://desktop.example.test",
		Items: []desktopAccountHint{
			{AccountID: "whatsapp_acc", Display: "Alice"},
		},
	})
	if single != "" {
		t.Fatalf("expected empty prompt for single account, got %q", single)
	}

	multi := renderDesktopAccountHintPrompt(desktopAccountHintsSnapshot{
		BaseURL: "https://desktop.example.test",
		Items: []desktopAccountHint{
			{AccountID: "whatsapp_acc", Display: "Alice"},
			{AccountID: "telegram_acc", Display: "Bob"},
		},
	})
	if !strings.Contains(multi, `Accounts connected on Beeper Desktop API via connection "desktop" (https://desktop.example.test)`) {
		t.Fatalf("missing required header: %q", multi)
	}
	if !strings.Contains(multi, "$whatsapp_acc - Alice") || !strings.Contains(multi, "$telegram_acc - Bob") {
		t.Fatalf("missing account lines: %q", multi)
	}
}

func TestNormalizeDesktopBridgeType(t *testing.T) {
	tests := []struct {
		name    string
		network string
		want    string
	}{
		{name: "whatsapp business", network: "WhatsApp Business", want: "whatsapp"},
		{name: "telegram bot", network: "telegram_bot", want: "telegram"},
		{name: "unknown token fallback", network: "Custom Network V2", want: "custom_network_v2"},
		{name: "empty unknown", network: "", want: "unknown"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := normalizeDesktopBridgeType(tt.network)
			if got != tt.want {
				t.Fatalf("normalizeDesktopBridgeType(%q) = %q, want %q", tt.network, got, tt.want)
			}
		})
	}
}
