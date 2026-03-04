package providers

import (
	"testing"
)

func TestResolveGoogleVertexProjectAndLocation(t *testing.T) {
	t.Setenv("GOOGLE_CLOUD_PROJECT", "")
	t.Setenv("GCLOUD_PROJECT", "")
	t.Setenv("GOOGLE_CLOUD_LOCATION", "")

	if _, err := ResolveGoogleVertexProject(nil); err == nil {
		t.Fatalf("expected missing project error")
	}
	if _, err := ResolveGoogleVertexLocation(nil); err == nil {
		t.Fatalf("expected missing location error")
	}

	t.Setenv("GOOGLE_CLOUD_PROJECT", "env-project")
	t.Setenv("GOOGLE_CLOUD_LOCATION", "us-central1")
	project, err := ResolveGoogleVertexProject(nil)
	if err != nil || project != "env-project" {
		t.Fatalf("expected env project, got project=%q err=%v", project, err)
	}
	location, err := ResolveGoogleVertexLocation(nil)
	if err != nil || location != "us-central1" {
		t.Fatalf("expected env location, got location=%q err=%v", location, err)
	}

	project, err = ResolveGoogleVertexProject(&GoogleVertexOptions{Project: "opt-project"})
	if err != nil || project != "opt-project" {
		t.Fatalf("expected option project override, got project=%q err=%v", project, err)
	}
	location, err = ResolveGoogleVertexLocation(&GoogleVertexOptions{Location: "europe-west4"})
	if err != nil || location != "europe-west4" {
		t.Fatalf("expected option location override, got location=%q err=%v", location, err)
	}
}
