package loop

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/yeomyeonggeori/blueprotocol/model"
	"github.com/yeomyeonggeori/blueprotocol/toolcontract"
)

const (
	requestedFieldNone          = "none"
	requestedFieldChoiceMinimum = 0.5
)

func requestedFieldQuestions(toolSet *toolcontract.ToolSet, expected []expectedChange, heldObjectTypes map[string]bool, carriedOut map[string]float64) map[string]model.DecisionQuestion {
	questions := map[string]model.DecisionQuestion{}
	objectTypeByKind := objectTypeByChangeKind(toolSet)
	for index, change := range expected {
		if heldObjectTypes[objectTypeByKind[change.Change]] || carriedOut[changeQuestionKey(index)] < changeCarriedOutThreshold {
			continue
		}
		fieldMeanings := inputFieldMeaningsOfChangeKind(toolSet, change.Change)
		if len(fieldMeanings) == 0 {
			continue
		}
		fieldMeanings[requestedFieldNone] = "asked names no particular field: it asks for the record as a whole, such as making or removing it, or for several fields at once"
		questions[requestedFieldQuestionKey(index)] = model.ChoiceQuestion{
			Instructions:       fmt.Sprintf("asked in expectedChanges[%d] is the user's own words asking for a change of that kind. Which field of the write holds what asked asks to set? Choose by what each field means, not by how its name sounds.", index),
			OptionDescriptions: fieldMeanings,
		}.Question()
	}
	return questions
}

func inputFieldMeaningsOfChangeKind(toolSet *toolcontract.ToolSet, kind string) map[string]string {
	meanings := map[string]string{}
	for _, toolName := range toolNamesMakingChangeKind(toolSet, kind) {
		definition, _ := toolSet.ToolDefinition(toolName)
		for fieldName, description := range inputFieldDescriptions(definition.InputSchema) {
			if fieldName == reasoningSlotName || fieldName == requestedFieldNone {
				continue
			}
			meanings[fieldName] = strings.TrimSpace(meanings[fieldName] + " " + firstSentence(description))
		}
	}
	return meanings
}

func toolNamesMakingChangeKind(toolSet *toolcontract.ToolSet, kind string) []string {
	if toolSet == nil {
		return nil
	}
	toolNames := []string{}
	for _, toolName := range toolNamesThatCanChange(toolSet) {
		definition, isFound := toolSet.ToolDefinition(toolName)
		if !isFound || definition.ResultContract == nil {
			continue
		}
		for _, effect := range definition.ResultContract.Effects {
			if changeKind(effect.ObjectType, effect.Effect) == kind {
				toolNames = append(toolNames, toolName)
				break
			}
		}
	}
	return toolNames
}

func inputFieldDescriptions(inputSchema json.RawMessage) map[string]string {
	var schema struct {
		Properties map[string]struct {
			Description string `json:"description"`
		} `json:"properties"`
	}
	if json.Unmarshal(inputSchema, &schema) != nil {
		return nil
	}
	descriptions := map[string]string{}
	for fieldName, property := range schema.Properties {
		descriptions[fieldName] = property.Description
	}
	return descriptions
}

func requestedField(answers map[string]model.DecisionAnswer, index int) string {
	answer, isAnswered := answers[requestedFieldQuestionKey(index)]
	if !isAnswered || answer.Choice == "" || answer.Choice == requestedFieldNone {
		return ""
	}
	if answer.ChoiceProbability(answer.Choice) < requestedFieldChoiceMinimum {
		return ""
	}
	return answer.Choice
}

func isFieldWrittenToObjectType(observations []turnObservation, objectType string, fieldName string) bool {
	for _, observation := range successfulToolObservations(observations) {
		if !changesObjectType(observation, objectType) {
			continue
		}
		var input map[string]json.RawMessage
		if json.Unmarshal(observation.ToolInput, &input) != nil {
			continue
		}
		if _, isWritten := input[fieldName]; isWritten {
			return true
		}
	}
	return false
}

func changesObjectType(observation turnObservation, objectType string) bool {
	for _, effect := range observation.Effects {
		if strings.TrimSpace(effect.ObjectType) == objectType {
			return true
		}
	}
	return false
}

func alreadySoQuestions(unwrittenFields map[int]string) map[string]model.DecisionQuestion {
	questions := map[string]model.DecisionQuestion{}
	for index, fieldName := range unwrittenFields {
		questions[alreadySoQuestionKey(index)] = model.NoulQuestion{
			Instructions: strings.Join([]string{
				fmt.Sprintf("expectedChanges[%d] asks to set the field %s, and no recorded write set that field. Did the record already hold what asked asks for in that field before this work, as lookups show, or did unrecordedWork set it?", index, fieldName),
				"The same value written into a different field does not count. Do not require anything asked does not state.",
			}, "\n"),
			TrueDescription:  "lookups show the record already held it, or unrecordedWork set it",
			FalseDescription: "nothing shows that field holding what asked asks for",
		}.Question()
	}
	return questions
}

func requestedFieldAnswers(ctx context.Context, decisionModel model.DecisionModel, request AgentTurnRequest, expected []expectedChange, heldObjectTypes map[string]bool, carriedOut map[string]float64) (map[string]model.DecisionAnswer, error) {
	questions := requestedFieldQuestions(request.ToolSet, expected, heldObjectTypes, carriedOut)
	if len(questions) == 0 {
		return nil, nil
	}
	state := map[string]any{"request": strings.Join(requestWordings(request), "\n\nLatest message about it:\n"), "expectedChanges": expected}
	if conversation := conversationBeforeRequest(request); len(conversation) > 0 {
		state["conversationBefore"] = conversation
	}
	response, errorValue := decisionModel.Decide(ctx, model.DecisionRequest{State: state, Questions: questions})
	return response.Answers, errorValue
}

func withUnwrittenFieldsChecked(ctx context.Context, decisionModel model.DecisionModel, request AgentTurnRequest, state map[string]any, expected []expectedChange, heldObjectTypes map[string]bool, observations []turnObservation, check changeCheck) (changeCheck, error) {
	answers, errorValue := requestedFieldAnswers(ctx, decisionModel, request, expected, heldObjectTypes, check.CarriedOut)
	if errorValue != nil {
		return changeCheck{}, errorValue
	}
	objectTypeByKind := objectTypeByChangeKind(request.ToolSet)
	unwrittenFields := map[int]string{}
	for index, change := range expected {
		fieldName := requestedField(answers, index)
		if fieldName == "" {
			continue
		}
		if check.RequestedFields == nil {
			check.RequestedFields = map[string]string{}
		}
		check.RequestedFields[changeQuestionKey(index)] = fieldName
		if !isFieldWrittenToObjectType(observations, objectTypeByKind[change.Change], fieldName) {
			unwrittenFields[index] = fieldName
		}
	}
	if len(unwrittenFields) == 0 {
		return check, nil
	}
	response, errorValue := decisionModel.Decide(ctx, model.DecisionRequest{State: state, Questions: alreadySoQuestions(unwrittenFields)})
	if errorValue != nil {
		return changeCheck{}, errorValue
	}
	check.UnwrittenFields = map[string]string{}
	for index, fieldName := range unwrittenFields {
		answer := response.Answers[alreadySoQuestionKey(index)]
		if answer.Noul >= changeCarriedOutThreshold {
			continue
		}
		check.UnwrittenFields[changeQuestionKey(index)] = fieldName
		check.CarriedOut[changeQuestionKey(index)] = answer.Noul
	}
	return check, nil
}

func requestedFieldQuestionKey(index int) string {
	return fmt.Sprintf("field%d", index)
}

func alreadySoQuestionKey(index int) string {
	return fmt.Sprintf("already%d", index)
}
