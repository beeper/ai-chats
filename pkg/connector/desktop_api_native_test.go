package connector

import (
	"testing"
	"time"

	beeperdesktopapi "github.com/beeper/desktop-api-go"
	"github.com/beeper/desktop-api-go/shared"
)

func TestParseDesktopAPIAddArgs(t *testing.T) {
	tests := []struct {
		name    string
		args    []string
		wantN   string
		wantT   string
		wantURL string
		wantErr bool
	}{
		{name: "token only", args: []string{"tok"}, wantN: desktopDefaultInstance, wantT: "tok"},
		{name: "token and base url", args: []string{"tok", "https://example.test"}, wantN: desktopDefaultInstance, wantT: "tok", wantURL: "https://example.test"},
		{name: "name and token", args: []string{"work", "tok"}, wantN: "work", wantT: "tok"},
		{name: "name token and base url", args: []string{"work", "tok", "https://example.test"}, wantN: "work", wantT: "tok", wantURL: "https://example.test"},
		{name: "empty", args: nil, wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gotN, gotT, gotURL, err := parseDesktopAPIAddArgs(tt.args)
			if (err != nil) != tt.wantErr {
				t.Fatalf("error mismatch: got=%v wantErr=%v", err, tt.wantErr)
			}
			if tt.wantErr {
				return
			}
			if gotN != tt.wantN || gotT != tt.wantT || gotURL != tt.wantURL {
				t.Fatalf("unexpected parse: got (%q,%q,%q) want (%q,%q,%q)", gotN, gotT, gotURL, tt.wantN, tt.wantT, tt.wantURL)
			}
		})
	}
}

func TestMatchDesktopChatsByLabelAliases(t *testing.T) {
	chats := []beeperdesktopapi.Chat{
		{ID: "c1", Title: "Family", AccountID: "acc-wa"},
		{ID: "c2", Title: "Family", AccountID: "acc-ig"},
	}
	accounts := map[string]beeperdesktopapi.Account{
		"acc-wa": {AccountID: "acc-wa", Network: "whatsapp"},
		"acc-ig": {AccountID: "acc-ig", Network: "instagram"},
	}

	exact, _ := matchDesktopChatsByLabel(chats, "family", accounts)
	if len(exact) != 2 {
		t.Fatalf("expected 2 exact matches for plain title, got %d", len(exact))
	}

	exact, _ = matchDesktopChatsByLabel(chats, "whatsapp:family", accounts)
	if len(exact) != 1 || exact[0].ID != "c1" {
		t.Fatalf("expected whatsapp-qualified label to resolve c1, got %+v", exact)
	}

	exact, _ = matchDesktopChatsByLabel(chats, "acc-ig/family", accounts)
	if len(exact) != 1 || exact[0].ID != "c2" {
		t.Fatalf("expected account-qualified label to resolve c2, got %+v", exact)
	}
}

func TestFilterDesktopChatsByResolveOptions(t *testing.T) {
	chats := []beeperdesktopapi.Chat{
		{ID: "c1", Title: "Family", AccountID: "acc-wa"},
		{ID: "c2", Title: "Family", AccountID: "acc-ig"},
	}
	accounts := map[string]beeperdesktopapi.Account{
		"acc-wa": {AccountID: "acc-wa", Network: "whatsapp"},
		"acc-ig": {AccountID: "acc-ig", Network: "instagram"},
	}

	filtered := filterDesktopChatsByResolveOptions(chats, accounts, desktopLabelResolveOptions{AccountID: "acc-wa"})
	if len(filtered) != 1 || filtered[0].ID != "c1" {
		t.Fatalf("account filter failed: %+v", filtered)
	}

	filtered = filterDesktopChatsByResolveOptions(chats, accounts, desktopLabelResolveOptions{Network: "instagram"})
	if len(filtered) != 1 || filtered[0].ID != "c2" {
		t.Fatalf("network filter failed: %+v", filtered)
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

func TestDesktopChatLabelCandidatesIncludeCanonicalAndRawNetworkAliases(t *testing.T) {
	chat := beeperdesktopapi.Chat{
		ID:        "c1",
		Title:     "Family",
		AccountID: "acc-wa",
	}
	account := beeperdesktopapi.Account{
		AccountID: "acc-wa",
		Network:   "whatsapp_business",
	}

	candidates := desktopChatLabelCandidates(chat, account)
	hasCanonical := false
	hasRaw := false
	for _, candidate := range candidates {
		switch candidate {
		case "whatsapp:Family":
			hasCanonical = true
		case "whatsapp_business:Family":
			hasRaw = true
		}
	}
	if !hasCanonical || !hasRaw {
		t.Fatalf("expected canonical and raw aliases, got %v", candidates)
	}
}

func TestDesktopSessionAccountID(t *testing.T) {
	account := beeperdesktopapi.Account{
		AccountID: "acc_123",
		Network:   "whatsapp_business",
	}

	single := desktopSessionAccountID(false, "Main Desktop", account)
	if single != "whatsapp_acc_123" {
		t.Fatalf("unexpected single-instance account id: %q", single)
	}

	multi := desktopSessionAccountID(true, "Main Desktop", account)
	if multi != "main_desktop_whatsapp_acc_123" {
		t.Fatalf("unexpected multi-instance account id: %q", multi)
	}
}
