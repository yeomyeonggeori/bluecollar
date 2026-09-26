package loop

import (
	"encoding/json"
	"fmt"
	"sort"

	"github.com/yeomyeonggeori/bluecollar/toolcontract"
)

func (agentTurnRunner *AgentTurnRunner) buildActionSchema(toolRegistry *toolcontract.ToolSet, allowQualityCriteria bool, hasFailureDebt bool) string {
	if toolRegistry != nil {
		return ActionSchemaForToolSet(toolRegistry, allowQualityCriteria, hasFailureDebt)
	}
	return buildActionSchemaFromToolDefinitions(nil, nil, allowQualityCriteria, hasFailureDebt)
}

func ActionSchemaForToolSet(toolSet *toolcontract.ToolSet, allowQualityCriteria bool, hasFailureDebt bool, terminalActionValues ...bool) string {
	return actionSchemaCitingEvidence(toolSet, nil, allowQualityCriteria, hasFailureDebt, terminalActionValues...)
}

func actionSchemaCitingEvidence(toolSet *toolcontract.ToolSet, citableEvidenceIDs []string, allowQualityCriteria bool, hasFailureDebt bool, terminalActionValues ...bool) string {
	if toolSet == nil {
		return buildActionSchemaFromToolDefinitions(nil, citableEvidenceIDs, allowQualityCriteria, hasFailureDebt, terminalActionValues...)
	}
	return buildActionSchemaFromToolDefinitions(toolSet.ListToolDefinitions(), citableEvidenceIDs, allowQualityCriteria, hasFailureDebt, terminalActionValues...)
}

func buildActionSchemaFromToolDefinitions(toolDefinitions []toolcontract.ToolDefinition, citableEvidenceIDs []string, allowQualityCriteria bool, hasFailureDebt bool, terminalActionValues ...bool) string {
	allowFail := true
	allowReply := true
	if len(terminalActionValues) > 0 {
		allowFail = terminalActionValues[0]
	}
	if len(terminalActionValues) > 1 {
		allowReply = terminalActionValues[1]
	}
	allowDelegate := false
	if len(terminalActionValues) > 2 {
		allowDelegate = terminalActionValues[2]
	}
	var variants []any
	if allowReply {
		variants = append(variants, replyActionSchema(hasFailureDebt, citableEvidenceIDs))
	}
	if allowFail {
		variants = append(variants, failActionSchema(hasFailureDebt))
	}
	if allowQualityCriteria {
		variants = append(variants, setQualityCriteriaActionSchema())
	}
	if allowDelegate {
		variants = append(variants, delegateActionSchema())
	}
	hasContinueVariant := false
	for _, toolDefinition := range toolDefinitions {
		if variant, isValid := continueActionSchema(toolDefinition); isValid {
			variants = append(variants, variant)
			hasContinueVariant = true
		}
	}

	if len(variants) == 0 {
		variants = append(variants, failActionSchema(hasFailureDebt))
	}
	schema := map[string]any{"oneOf": variants}
	if hasContinueVariant {
		schema["$defs"] = actionSchemaSharedDefinitions()
	}
	return mustMarshalStructuredSchema(schema)
}

func actionSchemaSharedDefinitions() map[string]any {
	return map[string]any{
		"executionStateUpdate": executionStateSchema(),
	}
}

func executionStateUpdateRefSchema() map[string]any {
	return map[string]any{"$ref": "#/$defs/executionStateUpdate"}
}

func replyFailureResolutionValues(hasFailureDebt bool) []string {
	if hasFailureDebt {
		return []string{failureResolutionRecoveredWithSuccess, failureResolutionNoToolFallback}
	}
	return []string{"none", failureResolutionRecoveredWithSuccess, failureResolutionNoToolFallback}
}

func replyVariantProperties(hasFailureDebt bool, citableEvidenceIDs []string) map[string]any {
	return map[string]any{
		"message":               stringSchema(),
		"final":                 booleanSchema(),
		"failureResolution":     enumValuesStringSchema(replyFailureResolutionValues(hasFailureDebt)),
		"goalSatisfied":         booleanSchema(),
		"hasRemainingWork":      booleanSchema(),
		"completionEvidenceIDs": completionEvidenceIDArraySchema(citableEvidenceIDs),
		"qualityReview":         qualityReviewSchema(),
		"executionStateUpdate":  executionStateSchema(),
	}
}

func replyActionSchema(hasFailureDebt bool, citableEvidenceIDs []string) map[string]any {
	properties := replyVariantProperties(hasFailureDebt, citableEvidenceIDs)
	properties["action"] = enumStringSchema("reply")
	properties["attachments"] = replyAttachmentArraySchema()
	properties["expectsAnswer"] = booleanSchema()
	properties["choices"] = replyChoiceArraySchema()
	properties["goalStatus"] = enumValuesStringSchema([]string{"satisfied", "in_progress"})
	return closedObjectSchema(properties)
}

func replyChoiceArraySchema() map[string]any {
	return map[string]any{"type": "array", "items": stringSchema()}
}

func replyAttachmentArraySchema() map[string]any {
	return map[string]any{
		"type": "array",
		"items": closedObjectSchema(map[string]any{
			"path":     stringSchema(),
			"filename": stringSchema(),
		}),
	}
}

func delegateActionSchema() map[string]any {
	return closedObjectSchema(map[string]any{
		"action":         enumStringSchema("delegate"),
		"instruction":    stringSchema(),
		"expectedResult": stringSchema(),
	})
}

func setQualityCriteriaActionSchema() map[string]any {
	return closedObjectSchema(map[string]any{
		"action":               enumStringSchema("set_quality_criteria"),
		"qualityCriteria":      qualityCriteriaSchema(),
		"reason":               stringSchema(),
		"goalStatus":           enumValuesStringSchema([]string{"in_progress"}),
		"goalSatisfied":        booleanSchema(),
		"executionStateUpdate": executionStateSchema(),
	})
}

func failActionSchema(hasFailureDebt bool) map[string]any {
	properties := map[string]any{
		"action":               enumStringSchema("fail"),
		"message":              stringSchema(),
		"reason":               stringSchema(),
		"goalStatus":           enumValuesStringSchema([]string{"blocked"}),
		"executionStateUpdate": executionStateSchema(),
	}
	if hasFailureDebt {
		properties["failureResolution"] = enumValuesStringSchema([]string{"failure_report"})
		properties["usedFailureFacts"] = failureReportFactsSchema()
	}
	return closedObjectSchema(properties)
}

func continueActionSchema(toolDefinition toolcontract.ToolDefinition) (map[string]any, bool) {
	inputSchema, isValid := toolInputSchema(toolDefinition)
	if !isValid {
		return nil, false
	}
	schema := closedObjectSchema(map[string]any{
		"action":               enumStringSchema("continue"),
		"toolName":             enumStringSchema(toolDefinition.Name),
		"toolInput":            inputSchema,
		"message":              stringSchema(),
		"goalStatus":           enumValuesStringSchema([]string{"in_progress"}),
		"goalSatisfied":        booleanSchema(),
		"hasRemainingWork":     booleanSchema(),
		"executionStateUpdate": executionStateUpdateRefSchema(),
	})
	if description := toolDefinition.ModelFacingDescription(); description != "" {
		schema["description"] = description
	}
	return schema, true
}

func toolInputSchema(toolDefinition toolcontract.ToolDefinition) (any, bool) {
	if len(toolDefinition.InputSchema) == 0 {
		return nil, false
	}
	var schema map[string]any
	if json.Unmarshal(toolDefinition.InputSchema, &schema) != nil {
		return nil, false
	}
	if schema["type"] != "object" {
		return nil, false
	}
	return portableNestedSchema(schema), true
}

func portableNestedSchema(value any) any {
	document, isDocument := value.(map[string]any)
	if isDocument {
		clone := map[string]any{}
		for fieldName, fieldValue := range document {
			if fieldName == "type" && fieldValue == "integer" {
				clone[fieldName] = "number"
				continue
			}
			clone[fieldName] = portableNestedSchema(fieldValue)
		}
		if clone["type"] == "object" {
			if _, isFound := clone["properties"]; !isFound {
				clone["properties"] = map[string]any{}
			}
		}
		return clone
	}
	values, isValues := value.([]any)
	if isValues {
		clone := make([]any, 0, len(values))
		for _, item := range values {
			clone = append(clone, portableNestedSchema(item))
		}
		return clone
	}
	return value
}

func enumStringSchema(value string) map[string]any {
	return map[string]any{"type": "string", "enum": []string{value}}
}

func enumValuesStringSchema(values []string) map[string]any {
	return map[string]any{"type": "string", "enum": values}
}

func stringSchema() map[string]any {
	return map[string]any{"type": "string"}
}

func booleanSchema() map[string]any {
	return map[string]any{"type": "boolean"}
}

func closedObjectSchema(properties map[string]any) map[string]any {
	return map[string]any{
		"type":                 "object",
		"properties":           properties,
		"additionalProperties": false,
		"required":             sortedPropertyNames(properties),
	}
}

func nullableObjectSchema(properties map[string]any) map[string]any {
	schema := closedObjectSchema(properties)
	schema["type"] = []string{"object", "null"}
	return schema
}

func sortedPropertyNames(properties map[string]any) []string {
	names := make([]string, 0, len(properties))
	for name := range properties {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

func completionEvidenceSchema() map[string]any {
	return stringArraySchema(0)
}

func qualityCriteriaSchema() map[string]any {
	return stringArraySchema(0)
}

func qualityReviewSchema() map[string]any {
	return map[string]any{
		"type": "array",
		"items": closedObjectSchema(map[string]any{
			"id":          stringSchema(),
			"passed":      booleanSchema(),
			"evidenceIDs": completionEvidenceSchema(),
			"notes":       stringSchema(),
		}),
	}
}

func failureReportFactsSchema() map[string]any {
	return closedObjectSchema(map[string]any{
		"attempts": map[string]any{
			"type": "array",
			"items": closedObjectSchema(map[string]any{
				"toolName":     stringSchema(),
				"inputSummary": stringSchema(),
				"errorCode":    stringSchema(),
				"failureStage": stringSchema(),
				"message":      stringSchema(),
			}),
		},
		"budgetState": stringSchema(),
	})
}

func terminalActionUnifiedSchema(hasFailureDebt bool) map[string]any {
	properties := replyVariantProperties(hasFailureDebt, nil)
	properties["action"] = enumValuesStringSchema([]string{"reply", "fail"})
	properties["goalStatus"] = enumValuesStringSchema([]string{"satisfied", "blocked"})
	properties["reason"] = stringSchema()
	if hasFailureDebt {
		properties["failureResolution"] = enumValuesStringSchema(append(replyFailureResolutionValues(hasFailureDebt), failureResolutionFailureReport))
		properties["usedFailureFacts"] = failureReportFactsSchema()
	}
	return closedObjectSchema(properties)
}

func terminalNoToolsActionSchema() string {
	return mustMarshalStructuredSchema(terminalActionUnifiedSchema(true))
}

func mustMarshalStructuredSchema(schema any) string {
	document, errorValue := json.Marshal(schema)
	if errorValue != nil {
		panic(fmt.Errorf("marshal structured schema: %w", errorValue))
	}
	return string(document)
}

func recoveryDecisionSchema() string {
	return mustMarshalStructuredSchema(closedObjectSchema(map[string]any{
		"nextAction":      stringSchema(),
		"userReplyIntent": stringSchema(),
	}))
}

func completionEvidenceIDArraySchema(citableEvidenceIDs []string) map[string]any {
	if len(citableEvidenceIDs) == 0 {
		return stringArraySchema(0)
	}
	schema := stringArraySchema(0)
	schema["items"] = enumValuesStringSchema(citableEvidenceIDs)
	return schema
}
