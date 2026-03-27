package main

import "testing"

func TestBridgeNameRoundTrip(t *testing.T) {
	const deviceID = "abc123def0"

	remote, ok := remoteBridgeNameForLocalInstance(deviceID, "opencode-test-run")
	if !ok {
		t.Fatal("expected local instance to resolve to a remote bridge name")
	}
	if remote != "sh-abc123def0-opencode-test-run" {
		t.Fatalf("unexpected remote name: %q", remote)
	}

	local, ok := localInstanceNameForRemoteBridge(deviceID, remote)
	if !ok {
		t.Fatal("expected remote bridge name to resolve to a local instance")
	}
	if local != "opencode-test-run" {
		t.Fatalf("unexpected local instance name: %q", local)
	}
}

func TestRemoteBridgeNameForUnknownLocalInstance(t *testing.T) {
	if _, ok := remoteBridgeNameForLocalInstance("abc123def0", "unknown-instance"); ok {
		t.Fatal("expected unknown instance to fail resolution")
	}
}
