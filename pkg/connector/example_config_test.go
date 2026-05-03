package connector

import (
	"fmt"
	"os"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

func TestExampleConfigFilesExcludeLegacyConfigSections(t *testing.T) {
	legacyKeys := map[string]struct{}{
		"chunking":     {},
		"sync":         {},
		"query":        {},
		"cache":        {},
		"experimental": {},
		"pruning":      {},
		"recall":       {},
	}

	t.Run("bridge example", func(t *testing.T) {
		rel := "example-config.yaml"
		data, err := os.ReadFile(rel)
		if err != nil {
			t.Fatalf("read %s: %v", rel, err)
		}

		var doc map[string]any
		if err := yaml.Unmarshal(data, &doc); err != nil {
			t.Fatalf("unmarshal %s: %v", rel, err)
		}

		if path := findLegacyConfigKey(doc, legacyKeys, nil); path != "" {
			t.Fatalf("found legacy config key %q in %s", path, rel)
		}
	})

}

func findLegacyConfigKey(node any, legacyKeys map[string]struct{}, path []string) string {
	switch value := node.(type) {
	case map[string]any:
		for key, child := range value {
			if _, ok := legacyKeys[key]; ok {
				return strings.Join(append(path, key), ".")
			}
			if found := findLegacyConfigKey(child, legacyKeys, append(path, key)); found != "" {
				return found
			}
		}
	case []any:
		for idx, child := range value {
			if found := findLegacyConfigKey(child, legacyKeys, append(path, fmt.Sprintf("[%d]", idx))); found != "" {
				return found
			}
		}
	}
	return ""
}
