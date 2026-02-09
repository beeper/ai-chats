package connector

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestCallOpenRouterImageGen_ParsesMessageImagesSnakeCase(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/chat/completions" {
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer tok" {
			t.Fatalf("unexpected authorization header: %q", got)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
  "choices": [
    {
      "message": {
        "role": "assistant",
        "images": [
          { "type": "image_url", "image_url": { "url": "data:image/png;base64,aGVsbG8=" } }
        ]
      }
    }
  ]
}`))
	}))
	defer srv.Close()

	images, err := callOpenRouterImageGen(context.Background(), "tok", srv.URL, map[string]any{
		"model":      "google/gemini-2.5-flash-image-preview",
		"messages":   []map[string]any{{"role": "user", "content": "cat"}},
		"modalities": []string{"image", "text"},
		"stream":     false,
	})
	if err != nil {
		t.Fatalf("callOpenRouterImageGen returned error: %v", err)
	}
	if len(images) != 1 || images[0] != "aGVsbG8=" {
		t.Fatalf("unexpected images: %#v", images)
	}
}

func TestCallOpenRouterImageGen_ParsesMessageImagesCamelCase(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
  "choices": [
    {
      "message": {
        "role": "assistant",
        "images": [
          { "type": "image_url", "imageUrl": { "url": "data:image/png;base64,aGVsbG8=" } }
        ]
      }
    }
  ]
}`))
	}))
	defer srv.Close()

	images, err := callOpenRouterImageGen(context.Background(), "tok", srv.URL, map[string]any{
		"model":      "google/gemini-2.5-flash-image-preview",
		"messages":   []map[string]any{{"role": "user", "content": "cat"}},
		"modalities": []string{"image", "text"},
		"stream":     false,
	})
	if err != nil {
		t.Fatalf("callOpenRouterImageGen returned error: %v", err)
	}
	if len(images) != 1 || images[0] != "aGVsbG8=" {
		t.Fatalf("unexpected images: %#v", images)
	}
}

func TestCallOpenRouterImageGen_ParsesContentPartsArray(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
  "choices": [
    {
      "message": {
        "role": "assistant",
        "content": [
          { "type": "text", "text": "ok" },
          { "type": "image_url", "image_url": { "url": "data:image/png;base64,aGVsbG8=" } }
        ]
      }
    }
  ]
}`))
	}))
	defer srv.Close()

	images, err := callOpenRouterImageGen(context.Background(), "tok", srv.URL, map[string]any{
		"model":      "google/gemini-2.5-flash-image-preview",
		"messages":   []map[string]any{{"role": "user", "content": "cat"}},
		"modalities": []string{"image", "text"},
		"stream":     false,
	})
	if err != nil {
		t.Fatalf("callOpenRouterImageGen returned error: %v", err)
	}
	if len(images) != 1 || images[0] != "aGVsbG8=" {
		t.Fatalf("unexpected images: %#v", images)
	}
}

