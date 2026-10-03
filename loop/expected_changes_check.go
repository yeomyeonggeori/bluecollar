package loop

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/yeomyeonggeori/bluecollar/model"
	"github.com/yeomyeonggeori/bluecollar/toolcontract"
)

const (
	changeCheckSchemaName     = "bluecollar_change_check"
	changeCarriedOutThreshold = 0.6
	changeLookupLimit         = 2
	unrecordedWorkLimit       = 4
	changeValueMaximumLength  = 1500
)

type changeCheck struct {
	ExpectedChanges []expectedChange   `json:"expectedChanges"`
	CarriedOut      map[string]float64 `json:"carriedOut,omitempty"`
	Unrecorded      []expectedChange   `json:"unrecorded,omitempty"`
	Unmet           []expectedChange   `json:"unmet,omitempty"`
}

type changedRecord struct {
	Record  string       `json:"record"`
	History []changeStep `json:"history"`
}

type changeStep struct {
	Change string     `json:"change"`
	Input  any        `json:"input,omitempty"`
	Result any        `json:"result,omitempty"`
	File   *fileFacts `json:"file,omitempty"`
}

type fileFacts struct {
	Filename    string `json:"filename,omitempty"`
	ContentType string `json:"contentType,omitempty"`
	SizeBytes   int64  `json:"sizeBytes,omitempty"`
}

type unrecordedCall struct {
	Tool   string `json:"tool"`
	Input  any    `json:"input,omitempty"`
	Result any    `json:"result,omitempty"`
}

type changeLookup struct {
	Tool   string `json:"tool"`
	Result any    `json:"result"`
}

func checkExpectedChanges(ctx context.Context, decisionModel model.DecisionModel, request AgentTurnRequest, expected []expectedChange, observations []turnObservation) (changeCheck, error) {
	check := changeCheck{ExpectedChanges: expected}
	changedObjectTypes := changedObjectTypes(observations)
	objectTypeByKind := objectTypeByChangeKind(request.ToolSet)
	recordedIndexes := []int{}
	for index, change := range expected {
		if changedObjectTypes[objectTypeByKind[change.Change]] {
			recordedIndexes = append(recordedIndexes, index)
			continue
		}
		check.Unrecorded = append(check.Unrecorded, change)
	}
	check.Unmet = append(check.Unmet, check.Unrecorded...)
	if len(recordedIndexes) == 0 {
		return check, nil
	}
	location := companyLocation(request.Company.TimeZone)
	state := changeCheckState(request, location, expected, observations)
	response, errorValue := decisionModel.Decide(ctx, model.DecisionRequest{
		State:     state,
		Questions: changeCheckQuestions(recordedIndexes, hasDocuments(state)),
	})
	if errorValue != nil {
		return changeCheck{}, errorValue
	}
	check.CarriedOut = map[string]float64{}
	for _, index := range recordedIndexes {
		answer := response.Answers[changeQuestionKey(index)]
		check.CarriedOut[changeQuestionKey(index)] = answer.Noul
		if answer.Noul < changeCarriedOutThreshold {
			check.Unmet = append(check.Unmet, expected[index])
		}
	}
	return check, nil
}

func changeCheckQuestions(indexes []int, hasDocuments bool) map[string]model.DecisionQuestion {
	questions := map[string]model.DecisionQuestion{}
	for _, index := range indexes {
		questions[changeQuestionKey(index)] = model.NoulQuestion{
			Instructions: strings.Join(append([]string{
				fmt.Sprintf("Was expectedChanges[%d] carried out? asked is the user's own words asking for it; read them in request and conversationBefore, with relative words (now, tomorrow, by 6:30) read against now.", index),
				"changedRecords lists every record a tool recorded changing, with its changes in order; judge a record by where its history ends. unrecordedWork lists calls that can change things but record no change of their own, such as commands: a record they made or changed shows in changedRecords only through a later recorded change, so read the two together.",
				"It is carried out when the records asked about end up the way asked says, as changedRecords and unrecordedWork show together, or when lookups show they already were, or when asked covers every record meeting a condition and none met it.",
				"A value counts as the same when it means the same in another format or spelling.",
				"Do not require anything asked does not state; a value the task chose where the user said nothing is never a failure.",
			}, documentInstructions(hasDocuments)...), "\n"),
			TrueDescription:  "carried out, or already so",
			FalseDescription: "not carried out, done to a different record, or something asked states differs",
		}.Question()
	}
	return questions
}

func documentInstructions(hasDocuments bool) []string {
	if !hasDocuments {
		return nil
	}
	return []string{
		"documents holds the text each changed file now carries. A value the request states for a file (an amount, rate, date, period, quantity, name, or a clause asked for) is carried out only when that text says the same; a different value there, or an asked clause absent from text that is not truncated, means it is not.",
		"Wording, layout, order, formatting, and content the request did not ask about are never failures; text cut off by truncated is not evidence that a value is missing.",
	}
}

func hasDocuments(state map[string]any) bool {
	_, isPresent := state["documents"]
	return isPresent
}

func changeQuestionKey(index int) string {
	return fmt.Sprintf("expected%d", index)
}

func changeCheckState(request AgentTurnRequest, location *time.Location, expected []expectedChange, observations []turnObservation) map[string]any {
	state := map[string]any{
		"request":         strings.Join(requestWordings(request), "\n\nLatest message about it:\n"),
		"now":             environmentNow(request).In(location).Format("2006-01-02 (Mon) 15:04 MST"),
		"expectedChanges": expected,
		"changedRecords":  changedRecords(observations, location),
	}
	if conversation := conversationBeforeRequest(request); len(conversation) > 0 {
		state["conversationBefore"] = conversation
	}
	if lookups := changeLookups(request.ToolSet, expected, observations, location); len(lookups) > 0 {
		state["lookups"] = lookups
	}
	if work := unrecordedWork(request.ToolSet, observations, location); len(work) > 0 {
		state["unrecordedWork"] = work
	}
	if documents := changedDocuments(request.WorkspaceRootPath, observations); len(documents) > 0 {
		state["documents"] = documents
	}
	return state
}

func changedDocuments(workspaceRootPath string, observations []turnObservation) []documentExtract {
	documents := []documentExtract{}
	for _, recordedPath := range changedPathsNewestFirst(observations) {
		if len(documents) == documentExtractLimit {
			break
		}
		if document, isRead := documentExtractOf(workspaceRootPath, recordedPath); isRead {
			documents = append(documents, document)
		}
	}
	return documents
}

func changedPathsNewestFirst(observations []turnObservation) []string {
	paths := []string{}
	isSeen := map[string]bool{}
	successful := successfulToolObservations(observations)
	for index := len(successful) - 1; index >= 0; index-- {
		for _, effect := range successful[index].Effects {
			recordedPath := strings.TrimSpace(effect.Path)
			if recordedPath == "" || isSeen[recordedPath] {
				continue
			}
			isSeen[recordedPath] = true
			paths = append(paths, recordedPath)
		}
	}
	return paths
}

func objectTypeByChangeKind(toolSet *toolcontract.ToolSet) map[string]string {
	objectTypes := map[string]string{}
	if toolSet == nil {
		return objectTypes
	}
	for _, toolName := range toolNamesThatCanChange(toolSet) {
		definition, isFound := toolSet.ToolDefinition(toolName)
		if !isFound || definition.ResultContract == nil {
			continue
		}
		for _, effect := range definition.ResultContract.Effects {
			objectTypes[changeKind(effect.ObjectType, effect.Effect)] = strings.TrimSpace(effect.ObjectType)
		}
	}
	return objectTypes
}

func changedObjectTypes(observations []turnObservation) map[string]bool {
	objectTypes := map[string]bool{}
	for _, observation := range successfulToolObservations(observations) {
		for _, effect := range observation.Effects {
			objectTypes[strings.TrimSpace(effect.ObjectType)] = true
		}
	}
	return objectTypes
}

func changedRecords(observations []turnObservation, location *time.Location) []changedRecord {
	records := []changedRecord{}
	recordIndexes := map[string]int{}
	for _, observation := range successfulToolObservations(observations) {
		for _, effect := range observation.Effects {
			identity := firstNonEmptyString(effect.ID, effect.Path, effect.URL, effect.ObjectType)
			key := effect.ObjectType + "\x00" + identity
			index, isKnown := recordIndexes[key]
			if !isKnown {
				index = len(records)
				recordIndexes[key] = index
				records = append(records, changedRecord{Record: identity})
			}
			records[index].History = append(records[index].History, changeStep{
				Change: changeKind(effect.ObjectType, effect.Effect),
				Input:  boundedValue(inLocalTime(decodedJSON(observation.ToolInput), location)),
				Result: boundedValue(inLocalTime(decodedJSON(observation.Output.Data), location)),
				File:   attachedFileFacts(observation.Attachments, effect.Path),
			})
		}
	}
	return records
}

func attachedFileFacts(attachments []toolcontract.FileAttachment, path string) *fileFacts {
	for _, attachment := range attachments {
		if strings.TrimSpace(path) != "" && strings.TrimSpace(attachment.DevicePath) == strings.TrimSpace(path) {
			return &fileFacts{Filename: strings.TrimSpace(attachment.Filename), ContentType: strings.TrimSpace(attachment.ContentType), SizeBytes: attachment.SizeBytes}
		}
	}
	return nil
}

func unrecordedWork(toolSet *toolcontract.ToolSet, observations []turnObservation, location *time.Location) []unrecordedCall {
	if toolSet == nil {
		return nil
	}
	calls := []unrecordedCall{}
	for _, observation := range successfulToolObservations(observations) {
		definition, isFound := toolSet.ToolDefinition(observation.Tool)
		if !isFound || len(observation.Effects) > 0 || !toolcontract.ToolDefinitionRequiresSideEffectEvidence(definition) {
			continue
		}
		calls = append(calls, unrecordedCall{
			Tool:   observation.Tool,
			Input:  boundedValue(inLocalTime(decodedJSON(observation.ToolInput), location)),
			Result: boundedValue(inLocalTime(decodedJSON(observation.Output.Data), location)),
		})
	}
	return calls[max(0, len(calls)-unrecordedWorkLimit):]
}

func changeLookups(toolSet *toolcontract.ToolSet, expected []expectedChange, observations []turnObservation, location *time.Location) []changeLookup {
	if toolSet == nil {
		return nil
	}
	namespaces := namespacesOfChanges(toolSet, expected)
	lookups := []changeLookup{}
	for _, observation := range successfulToolObservations(observations) {
		definition, isFound := toolSet.ToolDefinition(observation.Tool)
		if !isFound || definition.SideEffectClass != toolcontract.ToolSideEffectRead || !namespaces[definition.Namespace] {
			continue
		}
		lookups = append(lookups, changeLookup{Tool: observation.Tool, Result: boundedValue(inLocalTime(decodedJSON(observation.Output.Data), location))})
	}
	return lookups[max(0, len(lookups)-changeLookupLimit):]
}

func namespacesOfChanges(toolSet *toolcontract.ToolSet, expected []expectedChange) map[string]bool {
	expectedKinds := map[string]bool{}
	for _, change := range expected {
		expectedKinds[change.Change] = true
	}
	namespaces := map[string]bool{}
	for _, toolName := range toolNamesThatCanChange(toolSet) {
		definition, isFound := toolSet.ToolDefinition(toolName)
		if !isFound || definition.ResultContract == nil || strings.TrimSpace(definition.Namespace) == "" {
			continue
		}
		for _, effect := range definition.ResultContract.Effects {
			if expectedKinds[changeKind(effect.ObjectType, effect.Effect)] {
				namespaces[definition.Namespace] = true
			}
		}
	}
	return namespaces
}

func successfulToolObservations(observations []turnObservation) []turnObservation {
	successful := []turnObservation{}
	for _, observation := range observations {
		if observation.Action == "continue" && strings.TrimSpace(observation.Tool) != "" && !observation.Failed() {
			successful = append(successful, observation)
		}
	}
	return successful
}

func decodedJSON(document json.RawMessage) any {
	var value any
	if json.Unmarshal(document, &value) != nil {
		return strings.TrimSpace(string(document))
	}
	return value
}

func boundedValue(value any) any {
	document, errorValue := json.Marshal(value)
	if errorValue != nil || len(document) <= changeValueMaximumLength {
		return value
	}
	return truncateForLedger(string(document), changeValueMaximumLength)
}

func inLocalTime(value any, location *time.Location) any {
	switch typed := value.(type) {
	case string:
		if moment, errorValue := time.Parse(time.RFC3339Nano, typed); errorValue == nil {
			return moment.In(location).Format("2006-01-02 15:04 MST")
		}
		return typed
	case []any:
		converted := make([]any, 0, len(typed))
		for _, item := range typed {
			converted = append(converted, inLocalTime(item, location))
		}
		return converted
	case map[string]any:
		converted := make(map[string]any, len(typed))
		for key, item := range typed {
			converted[key] = inLocalTime(item, location)
		}
		return converted
	}
	return value
}

func environmentNow(request AgentTurnRequest) time.Time {
	if request.EnvironmentNow.IsZero() {
		return time.Now()
	}
	return request.EnvironmentNow
}
