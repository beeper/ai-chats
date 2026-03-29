package codex

import (
	"strings"
	"testing"

	"maunium.net/go/mautrix/bridgev2"
	"maunium.net/go/mautrix/bridgev2/database"
	"maunium.net/go/mautrix/bridgev2/networkid"
	"maunium.net/go/mautrix/event"
	"maunium.net/go/mautrix/id"

	"github.com/beeper/agentremote"
)

func TestFillPortalBridgeInfoSetsAIRoomType(t *testing.T) {
	conn := NewConnector()
	portal := &bridgev2.Portal{Portal: &database.Portal{RoomType: database.RoomTypeDM}}
	content := &event.BridgeEventContent{}

	conn.FillPortalBridgeInfo(portal, content)
	if content.BeeperRoomTypeV2 != "dm" {
		t.Fatalf("expected dm room type, got %q", content.BeeperRoomTypeV2)
	}
	if content.Protocol.ID != "ai-codex" {
		t.Fatalf("expected ai-codex protocol, got %q", content.Protocol.ID)
	}
}

func TestGetCapabilitiesEnablesContactListProvisioning(t *testing.T) {
	conn := NewConnector()
	caps := conn.GetCapabilities()
	if caps == nil {
		t.Fatal("expected capabilities")
	}
	if !caps.Provisioning.ResolveIdentifier.ContactList {
		t.Fatal("expected contact list provisioning to be enabled")
	}
}

func TestGetNameUsesDefaultCommandPrefixBeforeStartup(t *testing.T) {
	conn := NewConnector()
	if got := conn.GetName().DefaultCommandPrefix; got != "!ai" {
		t.Fatalf("expected default command prefix !ai, got %q", got)
	}
}

func TestHostAuthLoginIDUsesDedicatedPrefix(t *testing.T) {
	conn := NewConnector()
	mxid := id.UserID("@alice:example.com")

	got := conn.hostAuthLoginID(mxid)
	manual := agentremote.MakeUserLoginID("codex", mxid, 1)

	if got == manual {
		t.Fatalf("expected host-auth login id to differ from manual login id, got %q", got)
	}
	if !strings.HasPrefix(string(got), hostAuthLoginPrefix+":") {
		t.Fatalf("expected host-auth login id to use %q prefix, got %q", hostAuthLoginPrefix, got)
	}
}

func TestHasManagedCodexLoginIgnoresHostAuthLogin(t *testing.T) {
	logins := []*bridgev2.UserLogin{
		{
			UserLogin: &database.UserLogin{
				ID: hostAuthLoginIDForTest("@alice:example.com"),
				Metadata: &UserLoginMetadata{
					Provider:        ProviderCodex,
					CodexAuthSource: CodexAuthSourceHost,
				},
			},
		},
		{
			UserLogin: &database.UserLogin{
				ID: "codex:alice:1",
				Metadata: &UserLoginMetadata{
					Provider:        ProviderCodex,
					CodexAuthSource: CodexAuthSourceManaged,
				},
			},
		},
	}

	if !hasManagedCodexLogin(logins, hostAuthLoginIDForTest("@alice:example.com")) {
		t.Fatal("expected managed Codex login to be detected")
	}
}

func TestHasManagedCodexLoginSkipsExceptID(t *testing.T) {
	exceptID := networkid.UserLoginID("codex:alice:1")
	logins := []*bridgev2.UserLogin{
		{
			UserLogin: &database.UserLogin{
				ID: exceptID,
				Metadata: &UserLoginMetadata{
					Provider:        ProviderCodex,
					CodexAuthSource: CodexAuthSourceManaged,
				},
			},
		},
		{
			UserLogin: &database.UserLogin{
				ID: "codex_host:alice:1",
				Metadata: &UserLoginMetadata{
					Provider:        ProviderCodex,
					CodexAuthSource: CodexAuthSourceHost,
				},
			},
		},
	}

	if hasManagedCodexLogin(logins, exceptID) {
		t.Fatal("expected exceptID login to be ignored")
	}
}

func TestHasManagedCodexLoginOnlyMatchesCodexManagedLogins(t *testing.T) {
	logins := []*bridgev2.UserLogin{
		{
			UserLogin: &database.UserLogin{
				ID: "other:1",
				Metadata: &UserLoginMetadata{
					Provider:        "other",
					CodexAuthSource: CodexAuthSourceManaged,
				},
			},
		},
	}

	if hasManagedCodexLogin(logins, "") {
		t.Fatal("expected non-Codex login to be ignored")
	}
}

func hostAuthLoginIDForTest(mxid string) networkid.UserLoginID {
	conn := NewConnector()
	return conn.hostAuthLoginID(id.UserID(mxid))
}
