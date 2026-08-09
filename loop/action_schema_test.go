package loop

import (
	"encoding/json"
	"github.com/yeomyeonggeori/bluecollar/toolcontract"
	"os"
	"sort"
	"strconv"
	"testing"

	"github.com/google/jsonschema-go/jsonschema"
)

func TestActionSchemasRecursivelyCloseEveryObject(t *testing.T) {
	toolDefinitions := []toolcontract.ToolDefinition{{
		Name: "test_create",
		InputSchema: json.RawMessage(`{
			"type":"object",
			"properties":{
				"content":{
					"type":"object",
					"properties":{"title":{"type":"string"}},
					"additionalProperties":false
				}
			},
			"additionalProperties":false
		}`),
	}}
	schemaDocuments := map[string]string{
		"agent action":      buildActionSchemaFromToolDefinitions(toolDefinitions, nil, true, nil, true),
		"finalizer":         finalizerActionSchema(),
		"terminal no tools": terminalNoToolsActionSchema(),
		"recovery decision": recoveryDecisionSchema(),
	}

	for schemaName, schemaDocument := range schemaDocuments {
		t.Run(schemaName, func(t *testing.T) {
			var schemaValue any
			if errorValue := json.Unmarshal([]byte(schemaDocument), &schemaValue); errorValue != nil {
				t.Fatal(errorValue)
			}
			assertEveryObjectSchemaIsClosed(t, schemaValue)
		})
	}
}

const eightToolActionSchemaByteCeiling = 19500

func TestActionSchemaSharedEnvelopeByteBudget(t *testing.T) {
	toolDefinitions := eightToolCapabilityCatalogFixture(t)

	schemaDocument := buildActionSchemaFromToolDefinitions(toolDefinitions, nil, true, nil, false)

	t.Logf("action schema byte length for an 8-tool catalog: %d", len(schemaDocument))
	if len(schemaDocument) >= eightToolActionSchemaByteCeiling {
		t.Fatalf("expected the deduplicated action schema to stay under %d bytes, got %d", eightToolActionSchemaByteCeiling, len(schemaDocument))
	}
	var compiledSchema jsonschema.Schema
	if errorValue := json.Unmarshal([]byte(schemaDocument), &compiledSchema); errorValue != nil {
		t.Fatalf("expected the action schema to parse with the santhosh jsonschema library, got %v", errorValue)
	}
	if _, errorValue := compiledSchema.Resolve(nil); errorValue != nil {
		t.Fatalf("expected the action schema to resolve with the santhosh jsonschema library, got %v", errorValue)
	}
}

func legacyRootOneOfFinalizerSchema(hasFailureDebt bool) string {
	return mustMarshalStructuredSchema(map[string]any{"oneOf": []any{finishActionSchema(hasFailureDebt, nil), failActionSchema(hasFailureDebt)}})
}

func TestTerminalActionSchemasAreFlatAndSmallerThanTheLegacyRootOneOf(t *testing.T) {
	cases := []struct {
		name           string
		hasFailureDebt bool
		flatSchema     string
		legacySchema   string
	}{
		{name: "finalizer", hasFailureDebt: false, flatSchema: finalizerActionSchema(), legacySchema: legacyRootOneOfFinalizerSchema(false)},
		{name: "terminal no tools", hasFailureDebt: true, flatSchema: terminalNoToolsActionSchema(), legacySchema: legacyRootOneOfFinalizerSchema(true)},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			var flatDocument map[string]any
			if errorValue := json.Unmarshal([]byte(testCase.flatSchema), &flatDocument); errorValue != nil {
				t.Fatalf("expected flat schema json: %v", errorValue)
			}
			if _, hasOneOf := flatDocument["oneOf"]; hasOneOf {
				t.Fatalf("expected a flat schema without a root oneOf, got %s", testCase.flatSchema)
			}
			if flatDocument["type"] != "object" {
				t.Fatalf("expected a flat closed object schema, got %s", testCase.flatSchema)
			}
			if flatDocument["additionalProperties"] != false {
				t.Fatalf("expected the flat schema to stay closed, got %s", testCase.flatSchema)
			}
			actionProperty := mapFromAny(mapFromAny(flatDocument["properties"])["action"])
			for _, actionName := range []string{"finish", "fail"} {
				if !containsString(stringSliceFromAny(actionProperty["enum"]), actionName) {
					t.Fatalf("expected flat schema action enum to allow %q, got %+v", actionName, actionProperty)
				}
			}
			t.Logf("%s schema bytes: legacy root oneOf=%d, flat=%d", testCase.name, len(testCase.legacySchema), len(testCase.flatSchema))
			if len(testCase.flatSchema) >= len(testCase.legacySchema) {
				t.Fatalf("expected the flat %s schema (%d bytes) to be smaller than the legacy root oneOf schema (%d bytes)", testCase.name, len(testCase.flatSchema), len(testCase.legacySchema))
			}
		})
	}
}

func TestTerminalActionSchemasAcceptFinishAndFailDocuments(t *testing.T) {
	finishDocument := `{"completionSummary":"","executionStateUpdate":null,"failureResolution":"none","remainingWork":"","replyParts":[],"reason":"","completionEvidenceIDs":[],"qualityReview":[],"hasRemainingWork":false,"message":"done","goalStatus":"satisfied","goalSatisfied":true,"action":"finish"}`
	failDocument := `{"completionSummary":"","executionStateUpdate":null,"failureResolution":"none","remainingWork":"","replyParts":[],"reason":"blocked by captcha","completionEvidenceIDs":[],"qualityReview":[],"hasRemainingWork":false,"message":"","goalStatus":"blocked","goalSatisfied":false,"action":"fail"}`
	failWithDebtDocument := `{"completionSummary":"","executionStateUpdate":null,"failureResolution":"failure_report","remainingWork":"","replyParts":[],"reason":"blocked by captcha","completionEvidenceIDs":[],"qualityReview":[],"hasRemainingWork":false,"message":"","goalStatus":"blocked","goalSatisfied":false,"action":"fail","usedFailureFacts":{"attempts":[{"toolName":"terminal_run","errorCode":"operation_failed","failureStage":"terminal_run","message":"blocked","inputSummary":""}],"budgetState":"failure_report_required"}}`
	finishWithDebtDocument := `{"completionSummary":"","executionStateUpdate":null,"failureResolution":"no_tool_fallback","remainingWork":"","replyParts":[],"reason":"","completionEvidenceIDs":[],"qualityReview":[],"hasRemainingWork":false,"message":"done from context","goalStatus":"satisfied","goalSatisfied":true,"action":"finish","usedFailureFacts":{"attempts":[],"budgetState":""}}`

	assertDocumentValidatesAgainstSchema(t, finalizerActionSchema(), finishDocument)
	assertDocumentValidatesAgainstSchema(t, finalizerActionSchema(), failDocument)
	assertDocumentValidatesAgainstSchema(t, terminalNoToolsActionSchema(), finishWithDebtDocument)
	assertDocumentValidatesAgainstSchema(t, terminalNoToolsActionSchema(), failWithDebtDocument)
}

func assertDocumentValidatesAgainstSchema(t *testing.T, schemaDocument string, instanceDocument string) {
	t.Helper()
	var compiledSchema jsonschema.Schema
	if errorValue := json.Unmarshal([]byte(schemaDocument), &compiledSchema); errorValue != nil {
		t.Fatalf("expected schema json: %v", errorValue)
	}
	resolvedSchema, errorValue := compiledSchema.Resolve(nil)
	if errorValue != nil {
		t.Fatalf("expected schema to resolve: %v", errorValue)
	}
	var instanceValue any
	if errorValue := json.Unmarshal([]byte(instanceDocument), &instanceValue); errorValue != nil {
		t.Fatalf("expected instance json: %v", errorValue)
	}
	if errorValue := resolvedSchema.Validate(instanceValue); errorValue != nil {
		t.Fatalf("expected instance to validate against schema: %v\ninstance: %s\nschema: %s", errorValue, instanceDocument, schemaDocument)
	}
}

func eightToolCapabilityCatalogFixture(t *testing.T) []toolcontract.ToolDefinition {
	t.Helper()
	document, errorValue := os.ReadFile("testdata/capability-tools.json")
	if errorValue != nil {
		t.Fatal(errorValue)
	}
	var catalog struct {
		Tools []struct {
			ModelName   string          `json:"modelName"`
			Description string          `json:"description"`
			InputSchema json.RawMessage `json:"inputSchema"`
		} `json:"tools"`
	}
	if errorValue := json.Unmarshal(document, &catalog); errorValue != nil {
		t.Fatal(errorValue)
	}
	selectedToolNames := map[string]bool{
		"task_add": true, "task_update": true, "message_send": true, "message_search": true,
		"document_read": true, "image_read": true, "web_search": true, "site_serve": true,
	}
	toolDefinitions := make([]toolcontract.ToolDefinition, 0, len(selectedToolNames))
	for _, tool := range catalog.Tools {
		if !selectedToolNames[tool.ModelName] {
			continue
		}
		toolDefinitions = append(toolDefinitions, toolcontract.ToolDefinition{
			Name:        tool.ModelName,
			Description: tool.Description,
			InputSchema: tool.InputSchema,
		})
	}
	if len(toolDefinitions) != len(selectedToolNames) {
		t.Fatalf("expected %d fixture tools, got %d", len(selectedToolNames), len(toolDefinitions))
	}
	return toolDefinitions
}

func assertEveryObjectSchemaIsClosed(t *testing.T, schemaValue any) {
	t.Helper()
	switch typedValue := schemaValue.(type) {
	case []any:
		for _, item := range typedValue {
			assertEveryObjectSchemaIsClosed(t, item)
		}
	case map[string]any:
		if toolcontract.SchemaTypeIncludesObject(typedValue["type"]) && typedValue["additionalProperties"] != false {
			t.Fatalf("expected object schema to be explicitly closed: %+v", typedValue)
		}
		for _, child := range typedValue {
			assertEveryObjectSchemaIsClosed(t, child)
		}
	}
}

func TestAStrictActionSchemaRequiresEveryPropertyItDeclares(t *testing.T) {
	toolSet := newTestToolSet([]string{toolcontract.TerminalRunToolName})

	for _, allowQualityCriteria := range []bool{false, true} {
		for _, hasFailureDebt := range []bool{false, true} {
			document := actionSchemaForToolSet(toolSet, nil, allowQualityCriteria, nil, hasFailureDebt, true, true)
			missing := propertiesMissingFromRequired(t, document)
			if len(missing) > 0 {
				t.Fatalf("a strict schema whose required list omits %v is rejected before the model ever sees it (quality=%v debt=%v)", missing, allowQualityCriteria, hasFailureDebt)
			}
		}
	}
}

func propertiesMissingFromRequired(t *testing.T, document string) []string {
	t.Helper()
	var schema any
	if errorValue := json.Unmarshal([]byte(document), &schema); errorValue != nil {
		t.Fatalf("expected a decodable schema: %v", errorValue)
	}
	missing := []string{}
	collectPropertiesMissingFromRequired(schema, "", &missing)
	sort.Strings(missing)
	return missing
}

func collectPropertiesMissingFromRequired(node any, path string, missing *[]string) {
	object, isObject := node.(map[string]any)
	if !isObject {
		if list, isList := node.([]any); isList {
			for index, item := range list {
				collectPropertiesMissingFromRequired(item, path+"["+strconv.Itoa(index)+"]", missing)
			}
		}
		return
	}
	properties, hasProperties := object["properties"].(map[string]any)
	if hasProperties {
		required := map[string]bool{}
		for _, name := range asStrings(object["required"]) {
			required[name] = true
		}
		for name := range properties {
			if !required[name] {
				*missing = append(*missing, path+"."+name)
			}
		}
	}
	for key, value := range object {
		collectPropertiesMissingFromRequired(value, path+"."+key, missing)
	}
}

func asStrings(value any) []string {
	list, isList := value.([]any)
	if !isList {
		return nil
	}
	names := make([]string, 0, len(list))
	for _, item := range list {
		if name, isString := item.(string); isString {
			names = append(names, name)
		}
	}
	return names
}

func TestFinishCanOnlyCiteEvidenceThatExists(t *testing.T) {
	toolSet := newTestToolSet([]string{toolcontract.TerminalRunToolName})

	document := actionSchemaForToolSet(toolSet, []string{"obs-001", "obs-003"}, false, nil, false, true, true)

	var schema any
	if errorValue := json.Unmarshal([]byte(document), &schema); errorValue != nil {
		t.Fatal(errorValue)
	}
	allowed := completionEvidenceEnumInSchema(t, schema)
	if len(allowed) != 2 || allowed[0] != "obs-001" || allowed[1] != "obs-003" {
		t.Fatalf("a model that can name an observation the run never made will be refused every turn until the run dies, got %v", allowed)
	}
}

func TestFinishCitesFreelyWhenThereIsNoEvidenceToName(t *testing.T) {
	toolSet := newTestToolSet([]string{toolcontract.TerminalRunToolName})

	document := actionSchemaForToolSet(toolSet, nil, false, nil, false, true, true)

	var schema any
	if errorValue := json.Unmarshal([]byte(document), &schema); errorValue != nil {
		t.Fatal(errorValue)
	}
	if allowed := completionEvidenceEnumInSchema(t, schema); allowed != nil {
		t.Fatalf("an empty enum is not a schema any model can satisfy, got %v", allowed)
	}
}

func completionEvidenceEnumInSchema(t *testing.T, node any) []string {
	t.Helper()
	object, isObject := node.(map[string]any)
	if !isObject {
		list, isList := node.([]any)
		if !isList {
			return nil
		}
		for _, item := range list {
			if found := completionEvidenceEnumInSchema(t, item); found != nil {
				return found
			}
		}
		return nil
	}
	properties, hasProperties := object["properties"].(map[string]any)
	if hasProperties {
		if evidence, hasEvidence := properties["completionEvidenceIDs"].(map[string]any); hasEvidence {
			items, hasItems := evidence["items"].(map[string]any)
			if hasItems {
				return stringSliceFromAny(items["enum"])
			}
		}
	}
	for _, child := range object {
		if found := completionEvidenceEnumInSchema(t, child); found != nil {
			return found
		}
	}
	return nil
}
