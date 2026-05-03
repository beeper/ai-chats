package connector

import (
	"strings"
)

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
	cloned := false
	for _, key := range []string{"$defs", "definitions"} {
		rawDefs, ok := schema[key].(map[string]any)
		if !ok {
			continue
		}
		if defs != nil && !cloned {
			next = make(schemaDefs, len(defs))
			for k, v := range defs {
				next[k] = v
			}
			cloned = true
		} else if next == nil {
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

func tryCollapseUnionVariants(schema map[string]any, variants []any) (any, bool) {
	nonNull, stripped := stripNullVariants(variants)
	if flattened := tryFlattenLiteralAnyOf(nonNull); flattened != nil {
		copySchemaMeta(schema, flattened)
		return flattened, true
	}
	if stripped && len(nonNull) == 1 {
		if lone, ok := nonNull[0].(map[string]any); ok {
			result := make(map[string]any, len(lone)+3)
			for k, v := range lone {
				result[k] = v
			}
			copySchemaMeta(schema, result)
			return result, true
		}
		return nonNull[0], true
	}
	return nil, false
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

	// Pre-clean and try to collapse anyOf/oneOf union variants
	cleanUnionVariants := func(key string) ([]any, bool) {
		raw, ok := schema[key].([]any)
		if !ok {
			return nil, false
		}
		cleaned := make([]any, 0, len(raw))
		for _, variant := range raw {
			cleaned = append(cleaned, cleanSchemaForProviderWithDefs(variant, nextDefs, refStack, report))
		}
		return cleaned, true
	}

	cleanedAnyOf, hasAnyOf := cleanUnionVariants("anyOf")
	cleanedOneOf, hasOneOf := cleanUnionVariants("oneOf")

	if hasAnyOf && !hasOneOf {
		if collapsed, ok := tryCollapseUnionVariants(schema, cleanedAnyOf); ok {
			return collapsed
		}
	}
	if hasOneOf && !hasAnyOf {
		if collapsed, ok := tryCollapseUnionVariants(schema, cleanedOneOf); ok {
			return collapsed
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
			if _, ok := value.([]any); ok {
				if cleanedAnyOf != nil {
					cleaned[key] = cleanedAnyOf
				}
			}
		case "oneOf":
			if _, ok := value.([]any); ok {
				if cleanedOneOf != nil {
					cleaned[key] = cleanedOneOf
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
