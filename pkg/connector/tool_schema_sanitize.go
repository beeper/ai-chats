package connector

import (
	"reflect"
	"slices"
	"strings"

	"github.com/rs/zerolog"
)

// Based on OpenClaw's tool schema cleaning to keep providers happy.
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
	out := make([]string, 0, len(r.stripped))
	for key := range r.stripped {
		out = append(out, key)
	}
	slices.Sort(out)
	return out
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

func sanitizeToolSchema(schema map[string]any) map[string]any {
	cleaned, _ := sanitizeToolSchemaWithReport(schema)
	return cleaned
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
		next := cloneSchemaMap(schema)
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

func cloneSchemaMap(schema map[string]any) map[string]any {
	clone := make(map[string]any, len(schema))
	for k, v := range schema {
		clone[k] = v
	}
	return clone
}

func mergeObjectUnionSchema(schema map[string]any) map[string]any {
	var variants []any
	if anyOf, ok := schema["anyOf"].([]any); ok {
		variants = anyOf
	} else if oneOf, ok := schema["oneOf"].([]any); ok {
		variants = oneOf
	}
	if len(variants) == 0 {
		return nil
	}

	mergedProperties := make(map[string]any)
	requiredCounts := make(map[string]int)
	objectVariants := 0

	for _, entry := range variants {
		variant, ok := entry.(map[string]any)
		if !ok {
			continue
		}
		props, ok := variant["properties"].(map[string]any)
		if !ok || len(props) == 0 {
			continue
		}
		objectVariants++
		for key, value := range props {
			if existing, ok := mergedProperties[key]; ok {
				mergedProperties[key] = mergePropertySchemas(existing, value)
			} else {
				mergedProperties[key] = value
			}
		}
		if required, ok := variant["required"].([]any); ok {
			for _, raw := range required {
				if name, ok := raw.(string); ok {
					requiredCounts[name] = requiredCounts[name] + 1
				}
			}
		}
	}

	baseRequired := extractRequired(schema)
	mergedRequired := baseRequired
	if len(mergedRequired) == 0 && objectVariants > 0 {
		for name, count := range requiredCounts {
			if count == objectVariants {
				mergedRequired = append(mergedRequired, name)
			}
		}
	}

	next := map[string]any{
		"type":       "object",
		"properties": mergedProperties,
	}
	if title, ok := schema["title"].(string); ok && title != "" {
		next["title"] = title
	}
	if desc, ok := schema["description"].(string); ok && desc != "" {
		next["description"] = desc
	}
	if len(mergedRequired) > 0 {
		next["required"] = mergedRequired
	}
	if additional, ok := schema["additionalProperties"]; ok {
		next["additionalProperties"] = additional
	} else {
		next["additionalProperties"] = true
	}

	return next
}

func extractRequired(schema map[string]any) []string {
	raw, ok := schema["required"].([]any)
	if !ok {
		return nil
	}
	required := make([]string, 0, len(raw))
	for _, entry := range raw {
		if name, ok := entry.(string); ok {
			required = append(required, name)
		}
	}
	return required
}

func extractEnumValues(schema any) []any {
	obj, ok := schema.(map[string]any)
	if !ok {
		return nil
	}
	if enumVals, ok := obj["enum"].([]any); ok {
		return enumVals
	}
	if value, ok := obj["const"]; ok {
		return []any{value}
	}
	var variants []any
	if anyOf, ok := obj["anyOf"].([]any); ok {
		variants = anyOf
	} else if oneOf, ok := obj["oneOf"].([]any); ok {
		variants = oneOf
	}
	if len(variants) == 0 {
		return nil
	}
	var values []any
	for _, variant := range variants {
		extracted := extractEnumValues(variant)
		if len(extracted) > 0 {
			values = append(values, extracted...)
		}
	}
	if len(values) == 0 {
		return nil
	}
	return values
}

func mergePropertySchemas(existing any, incoming any) any {
	if existing == nil {
		return incoming
	}
	if incoming == nil {
		return existing
	}

	existingEnum := extractEnumValues(existing)
	incomingEnum := extractEnumValues(incoming)
	if existingEnum != nil || incomingEnum != nil {
		values := append([]any{}, existingEnum...)
		values = append(values, incomingEnum...)
		unique := make([]any, 0, len(values))
		seen := make(map[any]struct{}, len(values))
		for _, value := range values {
			if value != nil {
				if reflect.TypeOf(value).Comparable() {
					if _, ok := seen[value]; ok {
						continue
					}
					seen[value] = struct{}{}
				}
			}
			unique = append(unique, value)
		}

		merged := map[string]any{}
		for _, source := range []any{existing, incoming} {
			obj, ok := source.(map[string]any)
			if !ok {
				continue
			}
			for _, key := range []string{"title", "description", "default"} {
				if _, present := merged[key]; !present {
					if value, ok := obj[key]; ok {
						merged[key] = value
					}
				}
			}
		}

		merged["enum"] = unique
		if typ := enumType(unique); typ != "" {
			merged["type"] = typ
		}
		return merged
	}

	return existing
}

func enumType(values []any) string {
	if len(values) == 0 {
		return ""
	}
	var typ string
	for _, value := range values {
		valueType := jsonTypeOf(value)
		if valueType == "" {
			return ""
		}
		if typ == "" {
			typ = valueType
		} else if typ != valueType {
			return ""
		}
	}
	return typ
}

func jsonTypeOf(value any) string {
	if value == nil {
		return "null"
	}
	switch value.(type) {
	case string:
		return "string"
	case bool:
		return "boolean"
	case float64, float32, int, int64, int32, uint, uint64, uint32:
		return "number"
	case []any:
		return "array"
	case map[string]any:
		return "object"
	}
	if reflect.TypeOf(value) != nil && reflect.TypeOf(value).Kind() == reflect.Slice {
		return "array"
	}
	return ""
}

func isStrictSchemaCompatible(schema map[string]any) bool {
	if schema == nil {
		return false
	}
	if typ, ok := schema["type"].(string); !ok || typ != "object" {
		return false
	}
	if hasUnsupportedKeywords(schema) {
		return false
	}
	return true
}

func hasUnsupportedKeywords(schema any) bool {
	switch value := schema.(type) {
	case map[string]any:
		for key, entry := range value {
			if _, blocked := unsupportedSchemaKeywords[key]; blocked {
				return true
			}
			if key == "anyOf" || key == "oneOf" || key == "allOf" {
				return true
			}
			if hasUnsupportedKeywords(entry) {
				return true
			}
		}
		return false
	case []any:
		for _, entry := range value {
			if hasUnsupportedKeywords(entry) {
				return true
			}
		}
		return false
	default:
		return false
	}
}

func cleanSchemaForProviderWithReport(schema any, report *schemaSanitizeReport) any {
	if schema == nil {
		return schema
	}
	if arr, ok := schema.([]any); ok {
		out := make([]any, 0, len(arr))
		for _, item := range arr {
			out = append(out, cleanSchemaForProviderWithReport(item, report))
		}
		return out
	}
	obj, ok := schema.(map[string]any)
	if !ok {
		return schema
	}
	defs := extendSchemaDefs(nil, obj)
	return cleanSchemaWithDefs(obj, defs, nil, report)
}

func extendSchemaDefs(defs schemaDefs, schema map[string]any) schemaDefs {
	next := defs
	if rawDefs, ok := schema["$defs"].(map[string]any); ok {
		if next == nil {
			next = make(schemaDefs)
		}
		for k, v := range rawDefs {
			next[k] = v
		}
	}
	if rawDefs, ok := schema["definitions"].(map[string]any); ok {
		if next == nil {
			next = make(schemaDefs)
		}
		for k, v := range rawDefs {
			next[k] = v
		}
	}
	return next
}

func decodeJsonPointerSegment(segment string) string {
	return strings.ReplaceAll(strings.ReplaceAll(segment, "~1", "/"), "~0", "~")
}

func tryResolveLocalRef(ref string, defs schemaDefs) any {
	if defs == nil {
		return nil
	}
	switch {
	case strings.HasPrefix(ref, "#/$defs/"):
		name := decodeJsonPointerSegment(strings.TrimPrefix(ref, "#/$defs/"))
		return defs[name]
	case strings.HasPrefix(ref, "#/definitions/"):
		name := decodeJsonPointerSegment(strings.TrimPrefix(ref, "#/definitions/"))
		return defs[name]
	default:
		return nil
	}
}

func tryFlattenLiteralAnyOf(variants []any) map[string]any {
	if len(variants) == 0 {
		return nil
	}
	var commonType string
	values := make([]any, 0, len(variants))
	for _, variant := range variants {
		obj, ok := variant.(map[string]any)
		if !ok {
			return nil
		}
		var literal any
		if v, ok := obj["const"]; ok {
			literal = v
		} else if enumVals, ok := obj["enum"].([]any); ok && len(enumVals) == 1 {
			literal = enumVals[0]
		} else {
			return nil
		}
		typ, ok := obj["type"].(string)
		if !ok || typ == "" {
			return nil
		}
		if commonType == "" {
			commonType = typ
		} else if commonType != typ {
			return nil
		}
		values = append(values, literal)
	}
	if commonType == "" {
		return nil
	}
	return map[string]any{
		"type": commonType,
		"enum": values,
	}
}

func isNullSchema(variant any) bool {
	obj, ok := variant.(map[string]any)
	if !ok {
		return false
	}
	if v, ok := obj["const"]; ok && v == nil {
		return true
	}
	if enumVals, ok := obj["enum"].([]any); ok && len(enumVals) == 1 && enumVals[0] == nil {
		return true
	}
	switch typ := obj["type"].(type) {
	case string:
		return typ == "null"
	case []any:
		if len(typ) == 1 {
			if s, ok := typ[0].(string); ok && s == "null" {
				return true
			}
		}
	case []string:
		return len(typ) == 1 && typ[0] == "null"
	}
	return false
}

func stripNullVariants(variants []any) ([]any, bool) {
	if len(variants) == 0 {
		return variants, false
	}
	nonNull := make([]any, 0, len(variants))
	for _, variant := range variants {
		if !isNullSchema(variant) {
			nonNull = append(nonNull, variant)
		}
	}
	return nonNull, len(nonNull) != len(variants)
}

func copySchemaMeta(src map[string]any, dst map[string]any) {
	for _, key := range []string{"description", "title", "default"} {
		if value, ok := src[key]; ok {
			dst[key] = value
		}
	}
}

func cleanSchemaWithDefs(schema map[string]any, defs schemaDefs, refStack map[string]struct{}, report *schemaSanitizeReport) any {
	nextDefs := extendSchemaDefs(defs, schema)

	if ref, ok := schema["$ref"].(string); ok && ref != "" {
		report.add("$ref")
		if refStack != nil {
			if _, seen := refStack[ref]; seen {
				return map[string]any{}
			}
		}
		if resolved := tryResolveLocalRef(ref, nextDefs); resolved != nil {
			nextStack := make(map[string]struct{}, len(refStack)+1)
			for k := range refStack {
				nextStack[k] = struct{}{}
			}
			nextStack[ref] = struct{}{}
			cleaned := cleanSchemaForProviderWithDefs(resolved, nextDefs, nextStack, report)
			if obj, ok := cleaned.(map[string]any); ok {
				result := make(map[string]any, len(obj)+3)
				for k, v := range obj {
					result[k] = v
				}
				copySchemaMeta(schema, result)
				return result
			}
			return cleaned
		}
		result := map[string]any{}
		copySchemaMeta(schema, result)
		return result
	}

	hasAnyOf := false
	hasOneOf := false
	if _, ok := schema["anyOf"].([]any); ok {
		hasAnyOf = true
	}
	if _, ok := schema["oneOf"].([]any); ok {
		hasOneOf = true
	}

	var cleanedAnyOf []any
	var cleanedOneOf []any
	if hasAnyOf {
		raw := schema["anyOf"].([]any)
		cleanedAnyOf = make([]any, 0, len(raw))
		for _, variant := range raw {
			cleanedAnyOf = append(cleanedAnyOf, cleanSchemaForProviderWithDefs(variant, nextDefs, refStack, report))
		}
	}
	if hasOneOf {
		raw := schema["oneOf"].([]any)
		cleanedOneOf = make([]any, 0, len(raw))
		for _, variant := range raw {
			cleanedOneOf = append(cleanedOneOf, cleanSchemaForProviderWithDefs(variant, nextDefs, refStack, report))
		}
	}

	if hasAnyOf {
		nonNull, stripped := stripNullVariants(cleanedAnyOf)
		if stripped {
			cleanedAnyOf = nonNull
		}
		if flattened := tryFlattenLiteralAnyOf(nonNull); flattened != nil {
			copySchemaMeta(schema, flattened)
			return flattened
		}
		if stripped && len(nonNull) == 1 {
			if lone, ok := nonNull[0].(map[string]any); ok {
				result := make(map[string]any, len(lone)+3)
				for k, v := range lone {
					result[k] = v
				}
				copySchemaMeta(schema, result)
				return result
			}
			return nonNull[0]
		}
	}

	if hasOneOf {
		nonNull, stripped := stripNullVariants(cleanedOneOf)
		if stripped {
			cleanedOneOf = nonNull
		}
		if flattened := tryFlattenLiteralAnyOf(nonNull); flattened != nil {
			copySchemaMeta(schema, flattened)
			return flattened
		}
		if stripped && len(nonNull) == 1 {
			if lone, ok := nonNull[0].(map[string]any); ok {
				result := make(map[string]any, len(lone)+3)
				for k, v := range lone {
					result[k] = v
				}
				copySchemaMeta(schema, result)
				return result
			}
			return nonNull[0]
		}
	}

	cleaned := make(map[string]any, len(schema))
	for key, value := range schema {
		if _, blocked := unsupportedSchemaKeywords[key]; blocked {
			report.add(key)
			continue
		}

		if key == "const" {
			cleaned["enum"] = []any{value}
			continue
		}

		if key == "type" && (hasAnyOf || hasOneOf) {
			continue
		}
		if key == "type" {
			if arr, ok := value.([]any); ok {
				types := make([]string, 0, len(arr))
				for _, entry := range arr {
					s, ok := entry.(string)
					if !ok {
						types = nil
						break
					}
					if s != "null" {
						types = append(types, s)
					}
				}
				if types != nil {
					if len(types) == 1 {
						cleaned["type"] = types[0]
					} else if len(types) > 1 {
						cleaned["type"] = types
					}
					continue
				}
			}
		}

		switch key {
		case "properties":
			if props, ok := value.(map[string]any); ok {
				nextProps := make(map[string]any, len(props))
				for k, v := range props {
					nextProps[k] = cleanSchemaForProviderWithDefs(v, nextDefs, refStack, report)
				}
				cleaned[key] = nextProps
			} else {
				cleaned[key] = value
			}
		case "items":
			switch items := value.(type) {
			case []any:
				nextItems := make([]any, 0, len(items))
				for _, entry := range items {
					nextItems = append(nextItems, cleanSchemaForProviderWithDefs(entry, nextDefs, refStack, report))
				}
				cleaned[key] = nextItems
			case map[string]any:
				cleaned[key] = cleanSchemaForProviderWithDefs(items, nextDefs, refStack, report)
			default:
				cleaned[key] = value
			}
		case "anyOf":
			if arr, ok := value.([]any); ok {
				if cleanedAnyOf != nil {
					cleaned[key] = cleanedAnyOf
				} else {
					nextItems := make([]any, 0, len(arr))
					for _, entry := range arr {
						nextItems = append(nextItems, cleanSchemaForProviderWithDefs(entry, nextDefs, refStack, report))
					}
					cleaned[key] = nextItems
				}
			}
		case "oneOf":
			if arr, ok := value.([]any); ok {
				if cleanedOneOf != nil {
					cleaned[key] = cleanedOneOf
				} else {
					nextItems := make([]any, 0, len(arr))
					for _, entry := range arr {
						nextItems = append(nextItems, cleanSchemaForProviderWithDefs(entry, nextDefs, refStack, report))
					}
					cleaned[key] = nextItems
				}
			}
		case "allOf":
			if arr, ok := value.([]any); ok {
				nextItems := make([]any, 0, len(arr))
				for _, entry := range arr {
					nextItems = append(nextItems, cleanSchemaForProviderWithDefs(entry, nextDefs, refStack, report))
				}
				cleaned[key] = nextItems
			}
		default:
			cleaned[key] = value
		}
	}

	return cleaned
}

func cleanSchemaForProviderWithDefs(schema any, defs schemaDefs, refStack map[string]struct{}, report *schemaSanitizeReport) any {
	if schema == nil {
		return schema
	}
	if arr, ok := schema.([]any); ok {
		out := make([]any, 0, len(arr))
		for _, item := range arr {
			out = append(out, cleanSchemaForProviderWithDefs(item, defs, refStack, report))
		}
		return out
	}
	if obj, ok := schema.(map[string]any); ok {
		return cleanSchemaWithDefs(obj, defs, refStack, report)
	}
	return schema
}
