package connector

import (
	"reflect"
)

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
	typ, ok := schema["type"].(string)
	return ok && typ == "object" && !hasUnsupportedKeywords(schema)
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
