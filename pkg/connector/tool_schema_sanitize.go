package connector

import (
	"maps"
	"slices"

	"github.com/rs/zerolog"
)

// sanitizeToolSchemaWithReport keeps tool schemas within provider-supported JSON Schema.
var unsupportedSchemaKeywords = map[string]struct{}{
	"patternProperties":    {},
	"additionalProperties": {},
	"$schema":              {},
	"$id":                  {},
	"$ref":                 {},
	"$defs":                {},
	"definitions":          {},
	"examples":             {},
	"minLength":            {},
	"maxLength":            {},
	"minimum":              {},
	"maximum":              {},
	"multipleOf":           {},
	"pattern":              {},
	"format":               {},
	"minItems":             {},
	"maxItems":             {},
	"uniqueItems":          {},
	"minProperties":        {},
	"maxProperties":        {},
}

type schemaDefs map[string]any

type ToolStrictMode int

const (
	ToolStrictOff ToolStrictMode = iota
	ToolStrictAuto
	ToolStrictOn
)

type schemaSanitizeReport struct {
	stripped map[string]struct{}
}

func (r *schemaSanitizeReport) add(key string) {
	if r == nil {
		return
	}
	if r.stripped == nil {
		r.stripped = make(map[string]struct{})
	}
	r.stripped[key] = struct{}{}
}

func (r *schemaSanitizeReport) list() []string {
	if r == nil || len(r.stripped) == 0 {
		return nil
	}
	return slices.Sorted(maps.Keys(r.stripped))
}

func logSchemaSanitization(log *zerolog.Logger, toolName string, stripped []string) {
	if log == nil || len(stripped) == 0 {
		return
	}
	log.Debug().
		Str("tool_name", toolName).
		Strs("stripped_keywords", stripped).
		Msg("Sanitized tool schema for provider compatibility")
}

func resolveToolStrictMode(isOpenRouter bool) ToolStrictMode {
	if isOpenRouter {
		return ToolStrictOff
	}
	return ToolStrictAuto
}

func shouldUseStrictMode(mode ToolStrictMode, schema map[string]any) bool {
	switch mode {
	case ToolStrictOn:
		return true
	case ToolStrictOff:
		return false
	default:
		return isStrictSchemaCompatible(schema)
	}
}

func sanitizeToolSchemaWithReport(schema map[string]any) (map[string]any, []string) {
	if schema == nil {
		return nil, nil
	}

	normalized := normalizeToolSchema(schema)
	report := &schemaSanitizeReport{}
	cleaned := cleanSchemaForProviderWithReport(normalized, report)
	cleanedMap, ok := cleaned.(map[string]any)
	if !ok || cleanedMap == nil {
		return normalized, report.list()
	}

	// Ensure top-level object type when properties/required are present.
	if _, hasType := cleanedMap["type"]; !hasType {
		if _, hasProps := cleanedMap["properties"]; hasProps || cleanedMap["required"] != nil {
			cleanedMap["type"] = "object"
		}
	}

	return cleanedMap, report.list()
}

func normalizeToolSchema(schema map[string]any) map[string]any {
	if schema == nil {
		return schema
	}
	if hasObjectProperties(schema) && !hasUnion(schema) {
		if _, hasType := schema["type"]; hasType {
			return schema
		}
		next := maps.Clone(schema)
		next["type"] = "object"
		return next
	}
	if hasUnion(schema) {
		if merged := mergeObjectUnionSchema(schema); merged != nil {
			return merged
		}
	}
	return schema
}

func hasUnion(schema map[string]any) bool {
	if schema == nil {
		return false
	}
	if _, ok := schema["anyOf"].([]any); ok {
		return true
	}
	if _, ok := schema["oneOf"].([]any); ok {
		return true
	}
	return false
}

func hasObjectProperties(schema map[string]any) bool {
	if schema == nil {
		return false
	}
	if _, ok := schema["properties"].(map[string]any); ok {
		return true
	}
	if _, ok := schema["required"].([]any); ok {
		return true
	}
	return false
}
