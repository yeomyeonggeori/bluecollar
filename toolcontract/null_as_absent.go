package toolcontract

import (
	"encoding/json"
	"slices"
)

func schemaDocumentWithNullAsAbsent(schemaDocument json.RawMessage) json.RawMessage {
	var schema any
	if json.Unmarshal(schemaDocument, &schema) != nil {
		return schemaDocument
	}
	normalized, errorValue := json.Marshal(schemaWithNullAsAbsent(schema))
	if errorValue != nil {
		return schemaDocument
	}
	return normalized
}

func schemaWithNullAsAbsent(schema any) any {
	object, isObject := schema.(map[string]any)
	if !isObject {
		return schema
	}
	normalized := make(map[string]any, len(object))
	for key, value := range object {
		normalized[key] = value
	}
	for _, key := range []string{"items", "additionalProperties", "not"} {
		if value, isPresent := object[key]; isPresent {
			normalized[key] = schemaWithNullAsAbsent(value)
		}
	}
	for _, key := range []string{"anyOf", "oneOf", "allOf"} {
		if alternatives, isList := object[key].([]any); isList {
			normalized[key] = schemasWithNullAsAbsent(alternatives)
		}
	}
	for _, key := range []string{"$defs", "definitions"} {
		if definitions, isMap := object[key].(map[string]any); isMap {
			normalized[key] = propertiesWithNullAsAbsent(definitions)
		}
	}
	properties, hasProperties := object["properties"].(map[string]any)
	if !hasProperties {
		return normalized
	}
	normalized["properties"] = propertiesWithNullAsAbsent(properties)
	if required, isList := object["required"].([]any); isList {
		normalized["required"] = slices.DeleteFunc(slices.Clone(required), func(name any) bool {
			fieldName, isName := name.(string)
			return isName && acceptsNull(properties[fieldName])
		})
	}
	return normalized
}

func schemasWithNullAsAbsent(schemas []any) []any {
	normalized := make([]any, 0, len(schemas))
	for _, schema := range schemas {
		normalized = append(normalized, schemaWithNullAsAbsent(schema))
	}
	return normalized
}

func propertiesWithNullAsAbsent(properties map[string]any) map[string]any {
	normalized := make(map[string]any, len(properties))
	for name, property := range properties {
		normalized[name] = withoutNullAlternative(schemaWithNullAsAbsent(property))
	}
	return normalized
}

func acceptsNull(schema any) bool {
	object, isObject := schema.(map[string]any)
	if !isObject {
		return false
	}
	switch declared := object["type"].(type) {
	case string:
		if declared == "null" {
			return true
		}
	case []any:
		if slices.Contains(declared, any("null")) {
			return true
		}
	}
	for _, key := range []string{"anyOf", "oneOf"} {
		alternatives, _ := object[key].([]any)
		if slices.ContainsFunc(alternatives, isNullSchema) {
			return true
		}
	}
	return false
}

func isNullSchema(schema any) bool {
	object, isObject := schema.(map[string]any)
	return isObject && object["type"] == "null" && len(object) == 1
}

func withoutNullAlternative(schema any) any {
	object, isObject := schema.(map[string]any)
	if !isObject {
		return schema
	}
	if declared, isList := object["type"].([]any); isList && slices.Contains(declared, any("null")) {
		remaining := slices.DeleteFunc(slices.Clone(declared), func(name any) bool { return name == "null" })
		normalized := clonedSchema(object)
		normalized["type"] = remaining
		if len(remaining) == 1 {
			normalized["type"] = remaining[0]
		}
		return normalized
	}
	for _, key := range []string{"anyOf", "oneOf"} {
		alternatives, isList := object[key].([]any)
		if !isList || !slices.ContainsFunc(alternatives, isNullSchema) {
			continue
		}
		remaining := slices.DeleteFunc(slices.Clone(alternatives), isNullSchema)
		normalized := clonedSchema(object)
		if len(remaining) != 1 {
			normalized[key] = remaining
			return normalized
		}
		delete(normalized, key)
		only, _ := remaining[0].(map[string]any)
		for name, value := range only {
			normalized[name] = value
		}
		return normalized
	}
	return object
}

func clonedSchema(object map[string]any) map[string]any {
	cloned := make(map[string]any, len(object))
	for key, value := range object {
		cloned[key] = value
	}
	return cloned
}

func documentWithNullAsAbsent(document any) any {
	switch value := document.(type) {
	case map[string]any:
		normalized := make(map[string]any, len(value))
		for key, field := range value {
			if field != nil {
				normalized[key] = documentWithNullAsAbsent(field)
			}
		}
		return normalized
	case []any:
		normalized := make([]any, 0, len(value))
		for _, item := range value {
			normalized = append(normalized, documentWithNullAsAbsent(item))
		}
		return normalized
	}
	return document
}
