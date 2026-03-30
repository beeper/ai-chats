package ai

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"gopkg.in/yaml.v3"
)

func TestMainConfigExampleNetworkBlockMatchesEmbeddedExample(t *testing.T) {
	mainConfigPath := filepath.Join("..", "..", "config.example.yaml")
	mainData, err := os.ReadFile(mainConfigPath)
	if err != nil {
		t.Fatalf("read %s: %v", mainConfigPath, err)
	}

	var mainDoc map[string]any
	if err := yaml.Unmarshal(mainData, &mainDoc); err != nil {
		t.Fatalf("unmarshal %s: %v", mainConfigPath, err)
	}

	networkRaw, ok := mainDoc["network"]
	if !ok {
		t.Fatalf("%s is missing top-level network block", mainConfigPath)
	}
	network, ok := networkRaw.(map[string]any)
	if !ok {
		t.Fatalf("%s network block has unexpected type %T", mainConfigPath, networkRaw)
	}

	embeddedPath := "integrations_example-config.yaml"
	embeddedData, err := os.ReadFile(embeddedPath)
	if err != nil {
		t.Fatalf("read %s: %v", embeddedPath, err)
	}

	var embeddedDoc map[string]any
	if err := yaml.Unmarshal(embeddedData, &embeddedDoc); err != nil {
		t.Fatalf("unmarshal %s: %v", embeddedPath, err)
	}

	if reflect.DeepEqual(network, embeddedDoc) {
		return
	}

	gotJSON, _ := json.MarshalIndent(network, "", "  ")
	wantJSON, _ := json.MarshalIndent(embeddedDoc, "", "  ")
	t.Fatalf("config.example.yaml network block drifted from %s\n--- got ---\n%s\n--- want ---\n%s", embeddedPath, gotJSON, wantJSON)
}
