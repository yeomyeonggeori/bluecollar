package loop

import (
	"encoding/json"
	"errors"
	"sort"
	"strings"

	"github.com/yeomyeonggeori/blueprotocol/model"
)

func ParseAgentActionResponse(response model.StructuredResponse) (agentAction, error) {
	content, errorValue := normalizeAgentActionResponseContent([]byte(response.Content))
	if errorValue != nil {
		return turnActionDocument{}, errorValue
	}
	var actionDocument turnActionDocument
	if decodeError := json.Unmarshal(content, &actionDocument); decodeError != nil {
		return turnActionDocument{}, actionDecodeError(content, decodeError)
	}
	return normalizeParsedAction(actionDocument), nil
}

func actionDecodeError(content []byte, decodeError error) error {
	fieldNames := wrongTypedActionFieldNames(content)
	if len(fieldNames) == 0 {
		return decodeError
	}
	return wrongTypedActionFieldError{fieldNames: fieldNames}
}

func wrongTypedActionFieldNames(content []byte) []string {
	var fields map[string]json.RawMessage
	if json.Unmarshal(content, &fields) != nil {
		return nil
	}
	fieldNames := []string{}
	for fieldName, fieldValue := range fields {
		if fieldReadsIntoActionDocument(fieldName, fieldValue) {
			continue
		}
		fieldNames = append(fieldNames, fieldName)
	}
	sort.Strings(fieldNames)
	return fieldNames
}

func fieldReadsIntoActionDocument(fieldName string, fieldValue json.RawMessage) bool {
	document, errorValue := json.Marshal(map[string]json.RawMessage{fieldName: fieldValue})
	if errorValue != nil {
		return false
	}
	return json.Unmarshal(document, &turnActionDocument{}) == nil
}

type wrongTypedActionFieldError struct {
	fieldNames []string
}

func (errorValue wrongTypedActionFieldError) Error() string {
	return "action fields carry a type the schema does not declare: " + strings.Join(errorValue.fieldNames, ", ")
}

func (errorValue wrongTypedActionFieldError) Unwrap() error {
	return unreadableModelActionError{reason: errorValue.Error()}
}

func normalizeAgentActionResponseContent(content []byte) ([]byte, error) {
	var document map[string]json.RawMessage
	if errorValue := json.Unmarshal(content, &document); errorValue != nil {
		return nil, errorValue
	}
	if _, hasAction := document["action"]; hasAction {
		return normalizeAgentActionResponseScalarContent(content)
	}
	candidateAction, candidateCount := agentActionResponseCandidate(document)
	if candidateCount == 0 {
		return normalizeAgentActionResponseScalarContent(content)
	}
	if candidateCount > 1 {
		return nil, errors.New("action response contains multiple candidate action blocks")
	}
	injectedContent, errorValue := injectAgentActionResponseCandidate(document, candidateAction)
	if errorValue != nil {
		return nil, errorValue
	}
	return normalizeAgentActionResponseScalarContent(injectedContent)
}

func agentActionResponseCandidate(document map[string]json.RawMessage) (string, int) {
	actionNames := []string{"reply", "continue", "fail", "set_quality_criteria"}
	candidateAction := ""
	candidateCount := 0
	for _, actionName := range actionNames {
		if _, isPresent := document[actionName]; !isPresent {
			continue
		}
		candidateAction = actionName
		candidateCount++
	}
	return candidateAction, candidateCount
}

func injectAgentActionResponseCandidate(document map[string]json.RawMessage, actionName string) ([]byte, error) {
	normalizedDocument := map[string]json.RawMessage{}
	for fieldName, fieldValue := range document {
		normalizedDocument[fieldName] = fieldValue
	}
	var nestedDocument map[string]json.RawMessage
	if json.Unmarshal(document[actionName], &nestedDocument) == nil {
		for fieldName, fieldValue := range nestedDocument {
			if _, isPresent := normalizedDocument[fieldName]; isPresent {
				continue
			}
			normalizedDocument[fieldName] = fieldValue
		}
	}
	actionValue, errorValue := json.Marshal(actionName)
	if errorValue != nil {
		return nil, errorValue
	}
	normalizedDocument["action"] = actionValue
	return json.Marshal(normalizedDocument)
}

func normalizeAgentActionResponseScalarContent(content []byte) ([]byte, error) {
	var document map[string]json.RawMessage
	if errorValue := json.Unmarshal(content, &document); errorValue != nil {
		return nil, errorValue
	}
	didChange := normalizeJSONStringBooleanField(document, "goalSatisfied")
	for _, fieldName := range []string{"completionEvidenceIDs", "qualityCriteria"} {
		if normalizeJSONStringToArrayField(document, fieldName) {
			didChange = true
		}
	}
	if !didChange {
		return content, nil
	}
	return json.Marshal(document)
}

func normalizeJSONStringToArrayField(document map[string]json.RawMessage, fieldName string) bool {
	fieldValue, isPresent := document[fieldName]
	if !isPresent {
		return false
	}
	var stringValue string
	if json.Unmarshal(fieldValue, &stringValue) != nil {
		return false
	}
	arrayValue := []string{}
	for _, item := range strings.Split(stringValue, ",") {
		if trimmedItem := strings.TrimSpace(item); trimmedItem != "" {
			arrayValue = append(arrayValue, trimmedItem)
		}
	}
	marshaledValue, errorValue := json.Marshal(arrayValue)
	if errorValue != nil {
		return false
	}
	document[fieldName] = marshaledValue
	return true
}

func normalizeJSONStringBooleanField(document map[string]json.RawMessage, fieldName string) bool {
	fieldValue, isPresent := document[fieldName]
	if !isPresent {
		return false
	}
	var stringValue string
	if errorValue := json.Unmarshal(fieldValue, &stringValue); errorValue != nil {
		return false
	}
	switch strings.ToLower(strings.TrimSpace(stringValue)) {
	case "true":
		document[fieldName] = json.RawMessage("true")
		return true
	case "false":
		document[fieldName] = json.RawMessage("false")
		return true
	default:
		return false
	}
}

func normalizeParsedAction(actionDocument turnActionDocument) turnActionDocument {
	actionDocument = normalizeParsedEvidence(actionDocument)
	action := strings.TrimSpace(actionDocument.Action)
	switch action {
	case "continue":
		actionDocument.Action = "continue"
		actionDocument.ToolName = strings.TrimSpace(actionDocument.ToolName)
	case "reply":
		actionDocument.Action = "reply"
		if actionDocument.Final {
			actionDocument.Action = "finish"
		}
	default:
		actionDocument.Action = action
	}
	return actionDocument
}

func normalizeParsedEvidence(actionDocument turnActionDocument) turnActionDocument {
	actionDocument.CompletionEvidence = evidenceReferencesFromIDs(actionDocument.CompletionEvidenceIDs)
	for index, item := range actionDocument.QualityReview {
		item.Evidence = evidenceReferencesFromIDs(item.EvidenceIDs)
		actionDocument.QualityReview[index] = item
	}
	return actionDocument
}

func evidenceReferencesFromIDs(values []string) []completionEvidenceReference {
	references := []completionEvidenceReference{}
	seenReferences := map[string]bool{}
	for _, value := range values {
		observationID := strings.TrimSpace(value)
		if observationID == "" || seenReferences[observationID] {
			continue
		}
		seenReferences[observationID] = true
		references = append(references, completionEvidenceReference{ObservationID: observationID})
	}
	return references
}

// The model producing something the runtime cannot read is the model's mistake, which every
// other layer hands back for one more try. Only a transport failure ends the turn.
type unreadableModelActionError struct {
	reason string
}

func (errorValue unreadableModelActionError) Error() string {
	return errorValue.reason
}

func isUnreadableModelActionError(errorValue error) bool {
	var typedError unreadableModelActionError
	return errors.As(errorValue, &typedError)
}
