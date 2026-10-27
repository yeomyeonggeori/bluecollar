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

func requestedFieldQuestions(toolSet *toolcontract.ToolSet, observations []turnObservation, expected []string, carriedOut map[string]float64) map[string]model.DecisionQuestion {
	questions := map[string]model.DecisionQuestion{}
	fieldMeanings := inputFieldMeaningsOfTools(toolSet, recordChangingToolNames(observations))
	if len(fieldMeanings) == 0 {
		return questions
	}
	fieldMeanings[requestedFieldNone] = "the change names no particular field: it asks for the record as a whole, such as making or removing it, or for several fields at once"
	for index := range expected {
		if carriedOut[changeQuestionKey(index)] < changeCarriedOutThreshold {
			continue
		}
		questions[requestedFieldQuestionKey(index)] = model.ChoiceQuestion{
			Instructions:       fmt.Sprintf("expectedChanges[%d] is a change the request asks for; request is what the user actually said. Which field of the write holds what the request asks to set for this change? Choose by what each field means, not by how its name sounds.\n", index) + addressedAgentInstruction,
			OptionDescriptions: fieldMeanings,
		}.Question()
	}
	return questions
}

func recordChangingToolNames(observations []turnObservation) []string {
	held := heldFilesOf(observations)
	toolNames := []string{}
	for _, observation := range successfulToolObservations(observations) {
		if changesARecord(observation, held) {
			toolNames = appendUniqueStrings(toolNames, observation.Tool)
		}
	}
	return toolNames
}

func changesARecord(observation turnObservation, held heldFiles) bool {
	for _, effect := range observation.Effects {
		if !held.objectTypes[strings.TrimSpace(effect.ObjectType)] {
			return true
		}
	}
	return false
}

func inputFieldMeaningsOfTools(toolSet *toolcontract.ToolSet, toolNames []string) map[string]string {
	meanings := map[string]string{}
	if toolSet == nil {
		return meanings
	}
	for _, toolName := range toolNames {
		definition, isFound := toolSet.ToolDefinition(toolName)
		if !isFound {
			continue
		}
		for fieldName, description := range inputFieldDescriptions(definition.InputSchema) {
			if fieldName == reasoningSlotName || fieldName == requestedFieldNone {
				continue
			}
			meanings[fieldName] = strings.TrimSpace(meanings[fieldName] + " " + firstSentence(description))
		}
	}
	return meanings
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

func isFieldWrittenByARecordChange(observations []turnObservation, fieldName string) bool {
	held := heldFilesOf(observations)
	for _, observation := range successfulToolObservations(observations) {
		if !changesARecord(observation, held) {
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

func alreadySoQuestions(unwrittenFields map[int]string) map[string]model.DecisionQuestion {
	questions := map[string]model.DecisionQuestion{}
	for index, fieldName := range unwrittenFields {
		questions[alreadySoQuestionKey(index)] = model.NoulQuestion{
			Instructions: strings.Join([]string{
				fmt.Sprintf("expectedChanges[%d] asks to set the field %s, and no recorded write set that field. Did the record already hold what the request asks for in that field before this work, as lookups show, or did a call in sideEffects that recorded no change set it?", index, fieldName),
				"The same value written into a different field does not count. Do not require anything the request does not state.",
			}, "\n"),
			TrueDescription:  "lookups show the record already held it, or unrecordedWork set it",
			FalseDescription: "nothing shows that field holding what asked asks for",
		}.Question()
	}
	return questions
}

func requestedFieldAnswers(ctx context.Context, decisionModel model.DecisionModel, request AgentTurnRequest, expected []string, observations []turnObservation, carriedOut map[string]float64) (map[string]model.DecisionAnswer, error) {
	questions := requestedFieldQuestions(request.ToolSet, observations, expected, carriedOut)
	if len(questions) == 0 {
		return nil, nil
	}
	response, errorValue := decisionModel.Decide(ctx, model.DecisionRequest{State: askedState(request, expected), Questions: questions})
	return response.Answers, errorValue
}

func withUnwrittenFieldsChecked(ctx context.Context, decisionModel model.DecisionModel, request AgentTurnRequest, state map[string]any, expected []string, observations []turnObservation, check changeCheck) (changeCheck, error) {
	answers, errorValue := requestedFieldAnswers(ctx, decisionModel, request, expected, observations, check.CarriedOut)
	if errorValue != nil {
		return changeCheck{}, errorValue
	}
	unwrittenFields := map[int]string{}
	for index := range expected {
		fieldName := requestedField(answers, index)
		if fieldName == "" {
			continue
		}
		if check.RequestedFields == nil {
			check.RequestedFields = map[string]string{}
		}
		check.RequestedFields[changeQuestionKey(index)] = fieldName
		if !isFieldWrittenByARecordChange(observations, fieldName) {
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
