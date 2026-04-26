package ai

import (
	"testing"
	"time"

	beeperdesktopapi "github.com/beeper/desktop-api-go"
	"github.com/beeper/desktop-api-go/shared"
)

func TestMatchDesktopChatsByLabelAliases(t *testing.T) {
	chats := []beeperdesktopapi.Chat{
		{ID: "c1", Title: "Family", AccountID: "local-whatsapp_ba_wa"},
		{ID: "c2", Title: "Family", AccountID: "local-instagram_ba_ig"},
	}
	accounts := map[string]beeperdesktopapi.Account{
		"local-whatsapp_ba_wa":  {AccountID: "local-whatsapp_ba_wa"},
		"local-instagram_ba_ig": {AccountID: "local-instagram_ba_ig"},
	}

	exact, _ := matchDesktopChatsByLabel(chats, "family", accounts)
	if len(exact) != 2 {
		t.Fatalf("expected 2 exact matches for plain title, got %d", len(exact))
	}

	exact, _ = matchDesktopChatsByLabel(chats, "whatsapp:family", accounts)
	if len(exact) != 1 || exact[0].ID != "c1" {
		t.Fatalf("expected whatsapp-qualified label to resolve c1, got %+v", exact)
	}

	exact, _ = matchDesktopChatsByLabel(chats, "local-instagram_ba_ig/family", accounts)
	if len(exact) != 1 || exact[0].ID != "c2" {
		t.Fatalf("expected account-qualified label to resolve c2, got %+v", exact)
	}
}

func TestFilterDesktopChatsByResolveOptions(t *testing.T) {
	chats := []beeperdesktopapi.Chat{
		{ID: "c1", Title: "Family", AccountID: "local-whatsapp_ba_wa"},
		{ID: "c2", Title: "Family", AccountID: "local-instagram_ba_ig"},
	}
	accounts := map[string]beeperdesktopapi.Account{
		"local-whatsapp_ba_wa":  {AccountID: "local-whatsapp_ba_wa"},
		"local-instagram_ba_ig": {AccountID: "local-instagram_ba_ig"},
	}

	filtered := filterDesktopChatsByResolveOptions(chats, accounts, "main_desktop", desktopLabelResolveOptions{AccountID: "local-whatsapp_ba_wa"})
	if len(filtered) != 1 || filtered[0].ID != "c1" {
		t.Fatalf("account filter failed: %+v", filtered)
	}

	filtered = filterDesktopChatsByResolveOptions(chats, accounts, "main_desktop", desktopLabelResolveOptions{Network: "instagram"})
	if len(filtered) != 1 || filtered[0].ID != "c2" {
		t.Fatalf("network filter failed: %+v", filtered)
	}
}

func TestFilterDesktopChatsByResolveOptionsCanonicalAccountID(t *testing.T) {
	chats := []beeperdesktopapi.Chat{
		{ID: "c1", Title: "Family", AccountID: "local-whatsapp_ba_wa"},
	}
	accounts := map[string]beeperdesktopapi.Account{
		"local-whatsapp_ba_wa": {AccountID: "local-whatsapp_ba_wa"},
	}

	filtered := filterDesktopChatsByResolveOptions(chats, accounts, "Main Desktop", desktopLabelResolveOptions{
		AccountID: "whatsapp_local-whatsapp_ba_wa",
	})
	if len(filtered) != 1 || filtered[0].ID != "c1" {
		t.Fatalf("single-instance canonical account filter failed: %+v", filtered)
	}

	filtered = filterDesktopChatsByResolveOptions(chats, accounts, "Main Desktop", desktopLabelResolveOptions{
		AccountID: "main_desktop_whatsapp_local-whatsapp_ba_wa",
	})
	if len(filtered) != 1 || filtered[0].ID != "c1" {
		t.Fatalf("multi-instance canonical account filter failed: %+v", filtered)
	}
}

func TestBuildOpenClawDesktopSessionMessages(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	messages := []shared.Message{
		{
			ID:         "m1",
			Text:       "hello",
			SenderName: "Alice",
			Timestamp:  now,
			IsSender:   false,
		},
		{
			ID:        "m2",
			Text:      "",
			Timestamp: now.Add(time.Second),
			IsSender:  true,
			Attachments: []shared.Attachment{
				{Type: shared.AttachmentTypeImg},
			},
		},
		{
			ID:        "m3",
			Text:      "",
			Timestamp: now.Add(2 * time.Second),
			IsSender:  false,
		},
	}

	projected := buildOpenClawDesktopSessionMessages(messages, desktopMessageBuildOptions{IsGroup: true})
	if len(projected) != 2 {
		t.Fatalf("expected 2 projected messages, got %d", len(projected))
	}

	if projected[0]["role"] != "user" {
		t.Fatalf("expected first role=user, got %v", projected[0]["role"])
	}
	firstContent, ok := projected[0]["content"].([]map[string]any)
	if !ok || len(firstContent) != 1 {
		t.Fatalf("expected first content block, got %#v", projected[0]["content"])
	}
	if firstContent[0]["text"] != "Alice: hello" {
		t.Fatalf("unexpected first text: %v", firstContent[0]["text"])
	}

	if projected[1]["role"] != "assistant" {
		t.Fatalf("expected second role=assistant, got %v", projected[1]["role"])
	}
	secondContent, ok := projected[1]["content"].([]map[string]any)
	if !ok || len(secondContent) != 1 {
		t.Fatalf("expected second content block, got %#v", projected[1]["content"])
	}
	if secondContent[0]["text"] != "[attachment: img]" {
		t.Fatalf("unexpected second text: %v", secondContent[0]["text"])
	}
}

func TestCanonicalDesktopNetwork(t *testing.T) {
	tests := []struct {
		name    string
		network string
		want    string
	}{
		{name: "whatsapp business", network: "WhatsApp Business", want: "whatsapp"},
		{name: "telegram bot", network: "telegram_bot", want: "telegram"},
		{name: "google messages", network: "Google Messages", want: "sms"},
		{name: "nextcloud talk", network: "nextcloud-talk", want: "nextcloudtalk"},
		{name: "mattermost", network: "MatterMost", want: "mattermost"},
		{name: "unknown token fallback", network: "Custom Network V2", want: "custom_network_v2"},
		{name: "empty", network: "", want: ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := canonicalDesktopNetwork(tt.network)
			if got != tt.want {
				t.Fatalf("canonicalDesktopNetwork(%q) = %q, want %q", tt.network, got, tt.want)
			}
		})
	}
}

func TestDesktopSessionChannelForNetwork(t *testing.T) {
	tests := []struct {
		name    string
		network string
		want    string
	}{
		{name: "canonical known", network: "whatsapp_business", want: "whatsapp"},
		{name: "canonical unknown", network: "custom_network_v2", want: "custom_network_v2"},
		{name: "empty fallback", network: "", want: channelDesktopAPI},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := desktopSessionChannelForNetwork(tt.network)
			if got != tt.want {
				t.Fatalf("desktopSessionChannelForNetwork(%q) = %q, want %q", tt.network, got, tt.want)
			}
		})
	}
}

func TestDesktopNetworkFilterMatches(t *testing.T) {
	tests := []struct {
		name    string
		filters map[string]struct{}
		network string
		want    bool
	}{
		{
			name:    "empty filters allow all",
			filters: map[string]struct{}{},
			network: "whatsapp",
			want:    true,
		},
		{
			name:    "canonical match",
			filters: map[string]struct{}{"whatsapp": {}},
			network: "WhatsApp Business",
			want:    true,
		},
		{
			name:    "variant filter canonicalizes",
			filters: map[string]struct{}{"telegram_bot": {}},
			network: "telegram",
			want:    true,
		},
		{
			name:    "sms alias canonicalizes",
			filters: map[string]struct{}{"google_messages": {}},
			network: "sms",
			want:    true,
		},
		{
			name:    "raw token fallback",
			filters: map[string]struct{}{"custom_net_v2": {}},
			network: "custom net v2",
			want:    true,
		},
		{
			name:    "mismatch",
			filters: map[string]struct{}{"signal": {}},
			network: "telegram",
			want:    false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := desktopNetworkFilterMatches(tt.filters, tt.network)
			if got != tt.want {
				t.Fatalf("desktopNetworkFilterMatches(%v, %q) = %v, want %v", tt.filters, tt.network, got, tt.want)
			}
		})
	}
}

func TestDesktopChatLabelCandidatesIncludeDerivedNetworkAlias(t *testing.T) {
	chat := beeperdesktopapi.Chat{
		ID:        "c1",
		Title:     "Family",
		AccountID: "local-whatsapp_ba_wa",
	}
	account := beeperdesktopapi.Account{
		AccountID: "local-whatsapp_ba_wa",
	}

	candidates := desktopChatLabelCandidates(chat, account)
	for _, candidate := range candidates {
		if candidate == "whatsapp:Family" {
			return
		}
	}
	t.Fatalf("expected derived network alias, got %v", candidates)
}

func TestDesktopSessionAccountID(t *testing.T) {
	account := beeperdesktopapi.Account{
		AccountID: "local-whatsapp_ba_123",
	}

	single := desktopSessionAccountID(false, "Main Desktop", account)
	if single != "whatsapp_local-whatsapp_ba_123" {
		t.Fatalf("unexpected single-instance account id: %q", single)
	}

	multi := desktopSessionAccountID(true, "Main Desktop", account)
	if multi != "main_desktop_whatsapp_local-whatsapp_ba_123" {
		t.Fatalf("unexpected multi-instance account id: %q", multi)
	}
}
