package codex

import "testing"

func TestParseCodexCommand(t *testing.T) {
	command, args, ok := parseCodexCommand("!codex import ~/repo")
	if !ok {
		t.Fatal("expected !codex command to be detected")
	}
	if command != "import" {
		t.Fatalf("expected import command, got %q", command)
	}
	if args != "~/repo" {
		t.Fatalf("expected args ~/repo, got %q", args)
	}
}

func TestParseCodexCommandIgnoresNormalText(t *testing.T) {
	if _, _, ok := parseCodexCommand("/status"); ok {
		t.Fatal("expected slash command text to be ignored")
	}
	if _, _, ok := parseCodexCommand("hello codex"); ok {
		t.Fatal("expected normal text to be ignored")
	}
}

func TestResolveManagedPathArgumentDefaultsToCurrentRoomPath(t *testing.T) {
	cc := newTestCodexClient("@owner:example.com")
	got, err := cc.resolveManagedPathArgument("", &codexPortalState{CodexCwd: "/tmp/repo"})
	if err != nil {
		t.Fatalf("expected current room path fallback, got error: %v", err)
	}
	if got != "/tmp/repo" {
		t.Fatalf("expected /tmp/repo, got %q", got)
	}
}
