package loop

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"slices"
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
	ExpectedChanges  []expectedChange   `json:"expectedChanges"`
	CarriedOut       map[string]float64 `json:"carriedOut,omitempty"`
	Unrecorded       []expectedChange   `json:"unrecorded,omitempty"`
	Unmet            []expectedChange   `json:"unmet,omitempty"`
	StateDigest      string             `json:"stateDigest,omitempty"`
	RepeatsRefusalOf string             `json:"repeatsRefusalOf,omitempty"`
}

type changedRecord struct {
	Record  string       `json:"record"`
	History []changeStep `json:"history"`
}

type changeStep struct {
	Change     string     `json:"change"`
	Input      any        `json:"input,omitempty"`
	Result     any        `json:"result,omitempty"`
	SameCallAs string     `json:"sameCallAs,omitempty"`
	File       *fileFacts `json:"file,omitempty"`
}

type fileFacts struct {
	Filename    string          `json:"filename,omitempty"`
	ContentType string          `json:"contentType,omitempty"`
	SizeBytes   int64           `json:"sizeBytes,omitempty"`
	Holds       json.RawMessage `json:"holds,omitempty"`
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
	check := changeCheck{ExpectedChanges: expected, Unrecorded: unrecordedChanges(request.ToolSet, expected, observations)}
	location := companyLocation(request.Company.TimeZone)
	heldObjectTypes := heldObjectTypes(observations)
	state := changeCheckState(request, location, expected, heldObjectTypes, observations)
	check.StateDigest = judgedStateDigest(state)
	if refusal, isRefused := refusalOverState(observations, check.StateDigest); isRefused {
		return refusal, nil
	}
	response, errorValue := decisionModel.Decide(ctx, model.DecisionRequest{
		State:     state,
		Questions: changeCheckQuestions(len(expected), len(heldObjectTypes) > 0),
	})
	if errorValue != nil {
		return changeCheck{}, errorValue
	}
	check.CarriedOut = map[string]float64{}
	for index, change := range expected {
		answer := response.Answers[changeQuestionKey(index)]
		check.CarriedOut[changeQuestionKey(index)] = answer.Noul
		if answer.Noul < changeCarriedOutThreshold {
			check.Unmet = append(check.Unmet, change)
		}
	}
	return check, nil
}

func unrecordedChanges(toolSet *toolcontract.ToolSet, expected []expectedChange, observations []turnObservation) []expectedChange {
	changedObjectTypes := changedObjectTypes(observations)
	objectTypeByKind := objectTypeByChangeKind(toolSet)
	var unrecorded []expectedChange
	for _, change := range expected {
		if !changedObjectTypes[objectTypeByKind[change.Change]] {
			unrecorded = append(unrecorded, change)
		}
	}
	return unrecorded
}

func judgedStateDigest(state map[string]any) string {
	judged := map[string]any{}
	for key, value := range state {
		if key != "now" {
			judged[key] = value
		}
	}
	document, _ := json.Marshal(judged)
	digest := sha256.Sum256(document)
	return hex.EncodeToString(digest[:])
}

func refusalOverState(observations []turnObservation, stateDigest string) (changeCheck, bool) {
	for _, observation := range observations {
		if observation.Action != "evidence_missing" || observation.ChangeCheck == nil || observation.ChangeCheck.StateDigest != stateDigest {
			continue
		}
		refusal := *observation.ChangeCheck
		refusal.CarriedOut = nil
		refusal.RepeatsRefusalOf = observation.ObservationID
		return refusal, true
	}
	return changeCheck{}, false
}

func changeCheckQuestions(changeCount int, isAnyFileHeld bool) map[string]model.DecisionQuestion {
	questions := map[string]model.DecisionQuestion{}
	for index := range changeCount {
		instructions := []string{
			fmt.Sprintf("Was expectedChanges[%d] carried out? asked is the user's own words asking for it; read them in request and conversationBefore, with relative words (now, tomorrow, by 6:30) read against now.", index),
			"changedRecords lists every record a tool recorded changing, with its changes in order; judge a record by where its history ends. unrecordedWork lists calls that can change things but record no change of their own, such as commands: a record they made or changed shows in changedRecords only through a later recorded change, so read the two together.",
			"It is carried out when the records asked about end up the way asked says, as changedRecords and unrecordedWork show together, or when lookups show they already were, or when asked covers every record meeting a condition and none met it.",
			"It counts as already so only when lookups or changedRecords show it; with no such evidence it was not carried out.",
		}
		if isAnyFileHeld {
			instructions = append(instructions,
				"A delivered file shows by a neutral reference such as file 1, never by its name. A file's holds is what its writer recorded the file holds, such as its tables, its views as they display, its charts, the values it was given and the blanks it leaves. Judge that file by its holds alone: each part asked names must be there, and each value asked states, such as a name, an amount, a quantity or a date, must be the one its holds shows.",
				"A blank left for a value the user has not given, a part or value asked does not mention, and the file's layout are never failures.",
			)
		}
		instructions = append(instructions,
			"A value counts as the same when it means the same in another format or spelling.",
			"Do not require anything asked does not state; a value the task chose where the user said nothing is never a failure.",
		)
		questions[changeQuestionKey(index)] = model.NoulQuestion{
			Instructions:     strings.Join(instructions, "\n"),
			TrueDescription:  "carried out, or already so",
			FalseDescription: "not carried out, done to a different record, or something asked states differs",
		}.Question()
	}
	return questions
}

func changeQuestionKey(index int) string {
	return fmt.Sprintf("expected%d", index)
}

func changeCheckState(request AgentTurnRequest, location *time.Location, expected []expectedChange, heldObjectTypes map[string]bool, observations []turnObservation) map[string]any {
	state := map[string]any{
		"request":         strings.Join(requestWordings(request), "\n\nLatest message about it:\n"),
		"now":             environmentNow(request).In(location).Format("2006-01-02 (Mon) 15:04 MST"),
		"expectedChanges": expected,
		"changedRecords":  append(changedRecords(observations, location, heldObjectTypes), heldFileRecords(observations, heldObjectTypes)...),
	}
	if conversation := conversationBeforeRequest(request); len(conversation) > 0 {
		state["conversationBefore"] = conversation
	}
	changesBeyondHolds := changesNotJudgedByHolds(request.ToolSet, expected, heldObjectTypes)
	if lookups := changeLookups(request.ToolSet, changesBeyondHolds, observations, location); len(lookups) > 0 {
		state["lookups"] = lookups
	}
	if len(changesBeyondHolds) == 0 {
		return state
	}
	if work := unrecordedWork(request.ToolSet, observations, location); len(work) > 0 {
		state["unrecordedWork"] = work
	}
	return state
}

func changesNotJudgedByHolds(toolSet *toolcontract.ToolSet, expected []expectedChange, heldObjectTypes map[string]bool) []expectedChange {
	objectTypeByKind := objectTypeByChangeKind(toolSet)
	changes := []expectedChange{}
	for _, change := range expected {
		if !heldObjectTypes[objectTypeByKind[change.Change]] {
			changes = append(changes, change)
		}
	}
	return changes
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

func changedRecords(observations []turnObservation, location *time.Location, heldObjectTypes map[string]bool) []changedRecord {
	records := []changedRecord{}
	recordIndexes := map[string]int{}
	for _, observation := range successfulToolObservations(observations) {
		callShownAt := ""
		for _, effect := range observation.Effects {
			if heldObjectTypes[strings.TrimSpace(effect.ObjectType)] {
				continue
			}
			identity := firstNonEmptyString(effect.ID, effect.Path, effect.URL, effect.ObjectType)
			key := effect.ObjectType + "\x00" + identity
			index, isKnown := recordIndexes[key]
			if !isKnown {
				index = len(records)
				recordIndexes[key] = index
				records = append(records, changedRecord{Record: identity})
			}
			step := changeStep{Change: changeKind(effect.ObjectType, effect.Effect), File: attachedFileFacts(observation.Attachments, effect.Path)}
			if callShownAt == "" {
				step.Input = boundedValue(inLocalTime(decodedJSON(observation.ToolInput), location))
				step.Result = boundedValue(inLocalTime(decodedJSON(observation.Output.Data), location))
				callShownAt = identity
			} else if callShownAt != identity {
				step.SameCallAs = callShownAt
			}
			records[index].History = append(records[index].History, step)
		}
	}
	for index := range records {
		records[index].History = withoutRepeatedSteps(records[index].History)
	}
	return records
}

func withoutRepeatedSteps(history []changeStep) []changeStep {
	seen := map[string]bool{}
	kept := []changeStep{}
	for index := len(history) - 1; index >= 0; index-- {
		document, _ := json.Marshal(history[index])
		if seen[string(document)] {
			continue
		}
		seen[string(document)] = true
		kept = append(kept, history[index])
	}
	slices.Reverse(kept)
	return kept
}

func heldObjectTypes(observations []turnObservation) map[string]bool {
	objectTypes := map[string]bool{}
	for _, observation := range successfulToolObservations(observations) {
		for _, effect := range observation.Effects {
			if attachment, isAttached := attachmentAt(observation.Attachments, effect.Path); isAttached && len(attachment.Holds) > 0 {
				objectTypes[strings.TrimSpace(effect.ObjectType)] = true
			}
		}
	}
	return objectTypes
}

func heldFileRecords(observations []turnObservation, heldObjectTypes map[string]bool) []changedRecord {
	paths := []string{}
	latestSteps := map[string]changeStep{}
	for _, observation := range successfulToolObservations(observations) {
		for _, effect := range observation.Effects {
			attachment, isAttached := attachmentAt(observation.Attachments, effect.Path)
			if !isAttached || !heldObjectTypes[strings.TrimSpace(effect.ObjectType)] {
				continue
			}
			path := strings.TrimSpace(effect.Path)
			if _, isKnown := latestSteps[path]; !isKnown {
				paths = append(paths, path)
			}
			latestSteps[path] = changeStep{Change: changeKind(effect.ObjectType, effect.Effect), File: &fileFacts{ContentType: strings.TrimSpace(attachment.ContentType), SizeBytes: attachment.SizeBytes, Holds: attachment.Holds}}
		}
	}
	records := make([]changedRecord, 0, len(paths))
	for index, path := range paths {
		records = append(records, changedRecord{Record: fmt.Sprintf("file %d", index+1), History: []changeStep{latestSteps[path]}})
	}
	return records
}

func attachedFileFacts(attachments []toolcontract.FileAttachment, path string) *fileFacts {
	attachment, isAttached := attachmentAt(attachments, path)
	if !isAttached {
		return nil
	}
	return &fileFacts{Filename: strings.TrimSpace(attachment.Filename), ContentType: strings.TrimSpace(attachment.ContentType), SizeBytes: attachment.SizeBytes}
}

func attachmentAt(attachments []toolcontract.FileAttachment, path string) (toolcontract.FileAttachment, bool) {
	if strings.TrimSpace(path) == "" {
		return toolcontract.FileAttachment{}, false
	}
	for _, attachment := range attachments {
		if strings.TrimSpace(attachment.DevicePath) == strings.TrimSpace(path) {
			return attachment, true
		}
	}
	return toolcontract.FileAttachment{}, false
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
