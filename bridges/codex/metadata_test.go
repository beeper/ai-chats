package codex

import (
	"testing"

	"maunium.net/go/mautrix/bridgev2"
	"maunium.net/go/mautrix/bridgev2/database"
	"maunium.net/go/mautrix/bridgev2/networkid"
	"maunium.net/go/mautrix/id"
)

func TestIsHostAuthLogin_WithExplicitHostSource(t *testing.T) {
	meta := &UserLoginMetadata{CodexAuthSource: CodexAuthSourceHost}
	if !isHostAuthLogin(meta) {
		t.Fatal("expected host source to be treated as host-auth login")
	}
}

func TestIsManagedAuthLogin_SourceManaged(t *testing.T) {
	meta := &UserLoginMetadata{CodexAuthSource: CodexAuthSourceManaged}
	if !isManagedAuthLogin(meta) {
		t.Fatal("expected managed source to be treated as managed login")
	}
}

func TestIsHostAuthLogin_DistinguishesManagedFromHost(t *testing.T) {
	hostMeta := &UserLoginMetadata{CodexAuthSource: CodexAuthSourceHost}
	if !isHostAuthLogin(hostMeta) {
		t.Fatal("expected host-auth login to be recognized")
	}

	managedMeta := &UserLoginMetadata{CodexAuthSource: CodexAuthSourceManaged}
	if isHostAuthLogin(managedMeta) {
		t.Fatal("expected managed login to not be host-auth")
	}
}

func TestManagedCodexPathsNormalizeAndSort(t *testing.T) {
	meta := &UserLoginMetadata{
		ManagedPaths: []string{" /tmp/b ", "/tmp/a", "/tmp/b", "", "/tmp/a "},
	}
	got := managedCodexPaths(meta)
	if len(got) != 2 {
		t.Fatalf("expected 2 normalized paths, got %#v", got)
	}
	if got[0] != "/tmp/a" || got[1] != "/tmp/b" {
		t.Fatalf("unexpected normalized order: %#v", got)
	}
}

func TestManagedCodexPathAddRemove(t *testing.T) {
	meta := &UserLoginMetadata{}
	if !addManagedCodexPath(meta, "/tmp/repo") {
		t.Fatal("expected path add to succeed")
	}
	if addManagedCodexPath(meta, "/tmp/repo") {
		t.Fatal("expected duplicate path add to be ignored")
	}
	if !hasManagedCodexPath(meta, "/tmp/repo") {
		t.Fatal("expected managed path lookup to succeed")
	}
	if !removeManagedCodexPath(meta, "/tmp/repo") {
		t.Fatal("expected path removal to succeed")
	}
	if hasManagedCodexPath(meta, "/tmp/repo") {
		t.Fatal("expected managed path to be removed")
	}
}

func TestCodexTopicHelpers(t *testing.T) {
	cc := newTestCodexClient(id.UserID("@owner:example.com"))
	cc.UserLogin.ID = "login-1"

	welcomePortal := &bridgev2.Portal{Portal: &database.Portal{PortalKey: networkid.PortalKey{ID: "codex:login-1:welcome:1"}}}
	importedPortal := &bridgev2.Portal{Portal: &database.Portal{PortalKey: networkid.PortalKey{ID: "codex:login-1:thread:thr_1"}}}

	if got := codexTopicForPath("/tmp/repo"); got != "Working directory: /tmp/repo" {
		t.Fatalf("unexpected topic string: %q", got)
	}
	if got := cc.codexTopicForPortal(welcomePortal, &PortalMetadata{IsCodexRoom: true, CodexCwd: "/tmp/repo", AwaitingCwdSetup: true}); got != "" {
		t.Fatalf("expected welcome room topic to be empty, got %q", got)
	}
	if got := cc.codexTopicForPortal(importedPortal, &PortalMetadata{IsCodexRoom: true, CodexCwd: "/tmp/repo"}); got != "Working directory: /tmp/repo" {
		t.Fatalf("expected imported room topic, got %q", got)
	}
}
