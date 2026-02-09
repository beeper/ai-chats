package textfs

import "testing"

func TestIsAllowedTextNotePath(t *testing.T) {
	cases := []struct {
		path string
		ok   bool
	}{
		{"MEMORY.md", true},
		{"memory/2026-01-01.md", true},
		{"workspace/notes.txt", true},
		{"notes/plan.ORG", true},
		{"docs/spec.RST", true},
		{"docs/readme.markdown", true},
		{"a/b/c.adoc", true},
		{"a/b/c.asciidoc", true},
		{"a/b/c.text", true},
		{"a/b/c.log", true},
		{"a/b/c.csv", true},
		{"README", false},
		{"image.png", false},
		{"archive.tar.gz", false},
	}
	for _, tc := range cases {
		ok, _, _ := IsAllowedTextNotePath(tc.path)
		if ok != tc.ok {
			t.Fatalf("IsAllowedTextNotePath(%q)=%v, want %v", tc.path, ok, tc.ok)
		}
	}
}
