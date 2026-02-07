package connector

import (
	"strings"
	"testing"
)

func TestResolveDesktopInstanceName(t *testing.T) {
	t.Run("no instances", func(t *testing.T) {
		if _, err := resolveDesktopInstanceName(map[string]DesktopAPIInstance{}, ""); err == nil {
			t.Fatalf("expected error for empty instance map")
		}
	})

	t.Run("single non-default instance makes instance optional", func(t *testing.T) {
		instances := map[string]DesktopAPIInstance{
			"work": {Token: "tok"},
		}
		for _, requested := range []string{"", "default", "DEFAULT"} {
			got, err := resolveDesktopInstanceName(instances, requested)
			if err != nil {
				t.Fatalf("unexpected error for requested=%q: %v", requested, err)
			}
			if got != "work" {
				t.Fatalf("unexpected instance for requested=%q: got=%q want=%q", requested, got, "work")
			}
		}
	})

	t.Run("single default instance", func(t *testing.T) {
		instances := map[string]DesktopAPIInstance{
			"default": {Token: "tok"},
		}
		got, err := resolveDesktopInstanceName(instances, "")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if got != "default" {
			t.Fatalf("unexpected instance: got=%q want=%q", got, "default")
		}
	})

	t.Run("multi-instance without default is ambiguous", func(t *testing.T) {
		instances := map[string]DesktopAPIInstance{
			"work": {Token: "tok"},
			"home": {Token: "tok"},
		}
		_, err := resolveDesktopInstanceName(instances, "")
		if err == nil {
			t.Fatalf("expected error")
		}
		if !strings.Contains(strings.ToLower(err.Error()), "multiple desktop api instances") {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	t.Run("multi-instance with default picks default", func(t *testing.T) {
		instances := map[string]DesktopAPIInstance{
			"default": {Token: "tok"},
			"work":    {Token: "tok"},
		}
		got, err := resolveDesktopInstanceName(instances, "")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if got != "default" {
			t.Fatalf("unexpected instance: got=%q want=%q", got, "default")
		}
	})

	t.Run("explicit instance lookup", func(t *testing.T) {
		instances := map[string]DesktopAPIInstance{
			"work": {Token: "tok"},
		}
		got, err := resolveDesktopInstanceName(instances, "Work")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if got != "work" {
			t.Fatalf("unexpected instance: got=%q want=%q", got, "work")
		}
	})

	t.Run("unknown instance errors", func(t *testing.T) {
		instances := map[string]DesktopAPIInstance{
			"work": {Token: "tok"},
		}
		_, err := resolveDesktopInstanceName(instances, "nope")
		if err == nil {
			t.Fatalf("expected error")
		}
		if !strings.Contains(strings.ToLower(err.Error()), "not found") {
			t.Fatalf("unexpected error: %v", err)
		}
	})
}
