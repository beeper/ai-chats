package connector

import "testing"

func TestSanitizeToolSchema_StripsUnsupportedKeywords(t *testing.T) {
	schema := map[string]any{
		"type":          "object",
		"minProperties": 1,
		"properties": map[string]any{
			"title": map[string]any{
				"type":      "string",
				"minLength": 1,
			},
		},
	}

	cleaned, _ := sanitizeToolSchemaWithReport(schema)
	if _, ok := cleaned["minProperties"]; ok {
		t.Fatalf("expected minProperties to be stripped")
	}
	props, ok := cleaned["properties"].(map[string]any)
	if !ok {
		t.Fatalf("expected properties to remain")
	}
	title, ok := props["title"].(map[string]any)
	if !ok {
		t.Fatalf("expected title property to remain")
	}
	if _, ok := title["minLength"]; ok {
		t.Fatalf("expected minLength to be stripped from nested property")
	}
}

func TestSanitizeToolSchema_ConvertsConstToEnum(t *testing.T) {
	schema := map[string]any{
		"const": "send",
	}
	cleaned, _ := sanitizeToolSchemaWithReport(schema)
	if _, ok := cleaned["const"]; ok {
		t.Fatalf("expected const to be removed")
	}
	enumVals, ok := cleaned["enum"].([]any)
	if !ok || len(enumVals) != 1 || enumVals[0] != "send" {
		t.Fatalf("expected enum to contain const value, got %+v", cleaned["enum"])
	}
}

func TestSanitizeToolSchema_MergesUnionProperties(t *testing.T) {
	schema := map[string]any{
		"anyOf": []any{
			map[string]any{
				"properties": map[string]any{
					"foo": map[string]any{"type": "string"},
				},
				"required": []any{"foo"},
			},
			map[string]any{
				"properties": map[string]any{
					"foo": map[string]any{"type": "string"},
					"bar": map[string]any{"type": "number"},
				},
				"required": []any{"foo", "bar"},
			},
		},
	}

	cleaned, _ := sanitizeToolSchemaWithReport(schema)
	if _, ok := cleaned["anyOf"]; ok {
		t.Fatalf("expected anyOf to be merged away")
	}
	if typ, _ := cleaned["type"].(string); typ != "object" {
		t.Fatalf("expected type=object, got %v", cleaned["type"])
	}
	props, ok := cleaned["properties"].(map[string]any)
	if !ok {
		t.Fatalf("expected properties to remain")
	}
	if _, ok := props["foo"]; !ok {
		t.Fatalf("expected foo property")
	}
	if _, ok := props["bar"]; !ok {
		t.Fatalf("expected bar property")
	}
	switch required := cleaned["required"].(type) {
	case []string:
		if len(required) != 1 || required[0] != "foo" {
			t.Fatalf("expected required=[foo], got %+v", required)
		}
	case []any:
		if len(required) != 1 || required[0] != "foo" {
			t.Fatalf("expected required=[foo], got %+v", required)
		}
	default:
		t.Fatalf("expected required to be set, got %T", cleaned["required"])
	}
}

func TestIsStrictSchemaCompatible(t *testing.T) {
	okSchema := map[string]any{
		"type": "object",
		"properties": map[string]any{
			"foo": map[string]any{"type": "string"},
		},
	}
	if !isStrictSchemaCompatible(okSchema) {
		t.Fatalf("expected schema to be strict-compatible")
	}

	badSchema := map[string]any{
		"type":          "object",
		"minProperties": 1,
	}
	if isStrictSchemaCompatible(badSchema) {
		t.Fatalf("expected schema with unsupported keyword to be incompatible")
	}
}
