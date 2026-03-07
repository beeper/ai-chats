package connector

import "testing"

func TestParseModelPrefixWithoutPrefix(t *testing.T) {
	backend, actual := ParseModelPrefix("gpt-5.2")
	if backend != "" {
		t.Fatalf("expected no backend, got %q", backend)
	}
	if actual != "gpt-5.2" {
		t.Fatalf("expected model to remain unchanged, got %q", actual)
	}
}

func TestParseDesktopSessionKeyWithoutInstance(t *testing.T) {
	instance, chatID, ok := parseDesktopSessionKey("desktop-api:chat-123")
	if !ok {
		t.Fatal("expected session key to parse")
	}
	if instance != desktopDefaultInstance {
		t.Fatalf("expected default instance, got %q", instance)
	}
	if chatID != "chat-123" {
		t.Fatalf("expected chat id chat-123, got %q", chatID)
	}
}

func TestParseQueueDirectiveArgsParsesOptions(t *testing.T) {
	_, result := parseQueueDirectiveArgs("debounce:250 cap:5 drop:old")
	if result.RawDebounce != "250" || result.DebounceMs == nil || *result.DebounceMs != 250 {
		t.Fatalf("unexpected debounce parse: %+v", result)
	}
	if result.RawCap != "5" || result.Cap == nil || *result.Cap != 5 {
		t.Fatalf("unexpected cap parse: %+v", result)
	}
	if result.RawDrop != "old" || result.DropPolicy == nil || *result.DropPolicy != "old" {
		t.Fatalf("unexpected drop parse: %+v", result)
	}
}

func TestParseGeoURIWithoutAccuracy(t *testing.T) {
	parsed, ok := parseGeoURI("geo:1.25,2.5")
	if !ok {
		t.Fatal("expected geo URI to parse")
	}
	if parsed.Latitude != 1.25 || parsed.Longitude != 2.5 {
		t.Fatalf("unexpected coordinates: %+v", parsed)
	}
	if parsed.Accuracy != nil {
		t.Fatalf("expected nil accuracy, got %+v", parsed.Accuracy)
	}
}
