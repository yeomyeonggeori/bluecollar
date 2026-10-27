package loop

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/yeomyeonggeori/bluecollar/toolexposure"
	"github.com/yeomyeonggeori/blueprotocol/model"
	"github.com/yeomyeonggeori/blueprotocol/toolcontract"
)

const (
	changeCheckSchemaName     = "bluecollar_change_check"
	changeCarriedOutThreshold = 0.6
	changeLookupLimit         = 4
	unrecordedCallLimit       = 6
	changeValueMaximumLength  = 1500

	changeGapWorkLeft             = "work_left"
	changeGapRequesterInformation = "requester_information"
)

type changeCheck struct {
	ExpectedChanges  []string           `json:"expectedChanges"`
	CarriedOut       map[string]float64 `json:"carriedOut,omitempty"`
	Unmet            []string           `json:"unmet,omitempty"`
	AwaitsRequester  []string           `json:"awaitsRequester,omitempty"`
	RequesterOnly    map[string]float64 `json:"requesterOnly,omitempty"`
	GapCheckError    string             `json:"gapCheckError,omitempty"`
	StateDigest      string             `json:"stateDigest,omitempty"`
	RepeatsRefusalOf string             `json:"repeatsRefusalOf,omitempty"`
	RequestedFields  map[string]string  `json:"requestedFields,omitempty"`
	UnwrittenFields  map[string]string  `json:"unwrittenFields,omitempty"`
}

func (check changeCheck) awaitsOnlyTheRequester() bool {
	return len(check.Unmet) > 0 && len(check.AwaitsRequester) == len(check.Unmet)
}

type sideEffect struct {
	Tool      string         `json:"tool"`
	Input     any            `json:"input,omitempty"`
	Succeeded bool           `json:"succeeded"`
	Result    any            `json:"result,omitempty"`
	Error     string         `json:"error,omitempty"`
	Changed   []recordChange `json:"changed,omitempty"`
}

type recordChange struct {
	Record string     `json:"record"`
	Change string     `json:"change"`
	File   *fileFacts `json:"file,omitempty"`
}

type fileFacts struct {
	Filename    string          `json:"filename,omitempty"`
	ContentType string          `json:"contentType,omitempty"`
	SizeBytes   int64           `json:"sizeBytes,omitempty"`
	Holds       json.RawMessage `json:"holds,omitempty"`
}

type heldFile struct {
	reference         string
	latestObservation int
}

type heldFiles struct {
	files       map[string]heldFile
	objectTypes map[string]bool
}

type changeLookup struct {
	Tool   string `json:"tool"`
	Result any    `json:"result"`
}

func checkExpectedChanges(ctx context.Context, decisionModel model.DecisionModel, request AgentTurnRequest, expected []string, observations []turnObservation) (changeCheck, error) {
	check := changeCheck{ExpectedChanges: expected}
	state := changeCheckState(request, expected, observations)
	check.StateDigest = judgedStateDigest(state)
	if refusal, isRefused := refusalOverState(observations, check.StateDigest); isRefused {
		return refusal, nil
	}
	response, errorValue := decisionModel.Decide(ctx, model.DecisionRequest{
		State:     state,
		Questions: changeCheckQuestions(len(expected), len(heldFilesOf(observations).files) > 0),
	})
	if errorValue != nil {
		return changeCheck{}, errorValue
	}
	check.CarriedOut = map[string]float64{}
	for index := range expected {
		check.CarriedOut[changeQuestionKey(index)] = response.Answers[changeQuestionKey(index)].Noul
	}
	check, errorValue = withUnwrittenFieldsChecked(ctx, decisionModel, request, state, expected, observations, check)
	if errorValue != nil {
		return changeCheck{}, errorValue
	}
	unmetIndexes := []int{}
	for index, change := range expected {
		if check.CarriedOut[changeQuestionKey(index)] < changeCarriedOutThreshold {
			check.Unmet = append(check.Unmet, change)
			unmetIndexes = append(unmetIndexes, index)
		}
	}
	if len(unmetIndexes) == 0 {
		return check, nil
	}
	return withWhatUnmetChangesLack(ctx, decisionModel, state, expected, unmetIndexes, check), nil
}

func withWhatUnmetChangesLack(ctx context.Context, decisionModel model.DecisionModel, state map[string]any, expected []string, unmetIndexes []int, check changeCheck) changeCheck {
	response, errorValue := decisionModel.Decide(ctx, model.DecisionRequest{State: state, Questions: changeGapQuestions(unmetIndexes)})
	if errorValue != nil {
		check.GapCheckError = errorValue.Error()
		return check
	}
	check.RequesterOnly = map[string]float64{}
	for _, index := range unmetIndexes {
		answer := response.Answers[changeGapQuestionKey(index)]
		check.RequesterOnly[changeGapQuestionKey(index)] = answer.ChoiceProbability(changeGapRequesterInformation)
		if answer.Choice == changeGapRequesterInformation {
			check.AwaitsRequester = append(check.AwaitsRequester, expected[index])
		}
	}
	return check
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
			fmt.Sprintf("Was expectedChanges[%d] carried out? It is a change the request asks for, written down before the work started. request and its latest message are what the user actually said: read the change against them and conversationBefore, with relative words (now, tomorrow, by 6:30) read against now.", index),
			"sideEffects lists, in order, every call the agent made that can change something. succeeded says whether the call went through; a call that did not succeed changed nothing. changed lists the records a call recorded changing; judge a record by the last call that changed it. A command can change things without recording them: read what it did from its input and result.",
			"It is carried out when the side effects leave things the way the change says, or when lookups or sideEffects show it already was so, or when the change covers every record meeting a condition and none met it.",
			"It counts as already so only when lookups or sideEffects show it; with no such evidence it was not carried out.",
			addressedAgentInstruction,
		}
		if isAnyFileHeld {
			instructions = append(instructions,
				"A delivered file shows by a neutral reference such as file 1, never by its name. A file's holds is what its writer recorded the file holds, such as its tables, its views as they display, its charts, the values it was given and the blanks it leaves. Judge that file by its latest holds alone: each part the change names must be there, and each value it states, such as a name, an amount, a quantity or a date, must be the one its holds shows.",
				"A blank left for a value the user has not given, a part or value the request does not mention, and the file's layout are never failures.",
			)
		}
		instructions = append(instructions,
			"A value counts as the same when it means the same in another format or spelling.",
			"Do not require anything the request does not state: a detail the change adds that the request never gave, and a value the task chose where the user said nothing, are never failures.",
		)
		questions[changeQuestionKey(index)] = model.NoulQuestion{
			Instructions:     strings.Join(instructions, "\n"),
			TrueDescription:  "carried out, or already so",
			FalseDescription: "not carried out, done to a different record, or something the request states differs",
		}.Question()
	}
	return questions
}

const addressedAgentInstruction = "agent is who was asked to do the work. request and conversationBefore may address it by its name or handle, as in a mention; that names who is asked, never whom the work is for, who takes part in it, or what it is called."

func changeGapQuestions(unmetIndexes []int) map[string]model.DecisionQuestion {
	questions := map[string]model.DecisionQuestion{}
	for _, index := range unmetIndexes {
		questions[changeGapQuestionKey(index)] = model.ChoiceQuestion{
			Instructions: strings.Join([]string{
				fmt.Sprintf("expectedChanges[%d] was found not carried out. What does carrying it out still need? Read the change against request and conversationBefore, which are what the user actually said.", index),
				addressedAgentInstruction,
				"A blank a delivered file's holds mark as withdrawn is a value its writer took out because nothing the user gave or the work read supported it; the work cannot put it back without making it up.",
				"It needs requester information when what is missing is a value or fact that request and conversationBefore never give, and that the records show the work could not support: its blanks are marked withdrawn, or a lookup or a failed call came back without it. Asking for a value is not giving it.",
				"It is work left when the user gave the missing value or fact and the work left it out or got it wrong, when a part asked was never made, when a change went to a different record, or when the work never looked for something it could read. Do not count a value as given unless request or conversationBefore states it.",
			}, "\n"),
			OptionDescriptions: map[string]string{
				changeGapWorkLeft:             "the work can still carry it out from what the user gave and what it can read",
				changeGapRequesterInformation: "it needs a value or fact nobody gave that the records show was looked for or withdrawn for lack of support, which only the requester can supply",
			},
		}.Question()
	}
	return questions
}

func changeGapQuestionKey(index int) string {
	return fmt.Sprintf("gap%d", index)
}

func changeQuestionKey(index int) string {
	return fmt.Sprintf("expected%d", index)
}

func changeCheckState(request AgentTurnRequest, expected []string, observations []turnObservation) map[string]any {
	location := companyLocation(request.Company.TimeZone)
	state := askedState(request, expected)
	state["now"] = environmentNow(request).In(location).Format("2006-01-02 (Mon) 15:04 MST")
	state["sideEffects"] = sideEffects(request.ToolSet, observations, location)
	if lookups := changeLookups(request.ToolSet, observations, location); len(lookups) > 0 {
		state["lookups"] = lookups
	}
	return state
}

func askedState(request AgentTurnRequest, expected []string) map[string]any {
	state := map[string]any{
		"request":         strings.Join(requestWordings(request), "\n\nLatest message about it:\n"),
		"expectedChanges": expected,
	}
	if conversation := conversationBeforeRequest(request); len(conversation) > 0 {
		state["conversationBefore"] = conversation
	}
	if agent := addressedAgent(request.AgentIdentity); len(agent) > 0 {
		state["agent"] = agent
	}
	return state
}

func addressedAgent(identity AgentIdentity) map[string]string {
	agent := map[string]string{}
	if name := strings.TrimSpace(identity.Name); name != "" {
		agent["name"] = name
	}
	if handle := strings.TrimSpace(identity.Handle); handle != "" {
		agent["handle"] = handle
	}
	return agent
}

func sideEffects(toolSet *toolcontract.ToolSet, observations []turnObservation, location *time.Location) []sideEffect {
	held := heldFilesOf(observations)
	calls := []sideEffect{}
	for _, index := range latestDistinctSideEffectCalls(toolSet, observations) {
		if call, isShown := sideEffectOf(observations[index], index, held, location); isShown {
			calls = append(calls, call)
		}
	}
	return withRecentUnrecordedCalls(calls)
}

func latestDistinctSideEffectCalls(toolSet *toolcontract.ToolSet, observations []turnObservation) []int {
	seen := map[string]bool{}
	indexes := []int{}
	for index := len(observations) - 1; index >= 0; index-- {
		observation := observations[index]
		if !isSideEffectCall(toolSet, observation) {
			continue
		}
		effects, _ := json.Marshal(observation.Effects)
		key := observation.Tool + "\x00" + string(observation.ToolInput) + "\x00" + string(effects) + "\x00" + strconv.FormatBool(observation.Failed())
		if seen[key] {
			continue
		}
		seen[key] = true
		indexes = append(indexes, index)
	}
	slices.Reverse(indexes)
	return indexes
}

func isSideEffectCall(toolSet *toolcontract.ToolSet, observation turnObservation) bool {
	if observation.Action != "continue" || strings.TrimSpace(observation.Tool) == "" {
		return false
	}
	if len(observation.Effects) > 0 || toolexposure.IsArtifactDeliveryTool(observation.Tool) {
		return true
	}
	if toolSet == nil {
		return false
	}
	definition, isFound := toolSet.ToolDefinition(observation.Tool)
	return isFound && ToolDefinitionRequiresSideEffectEvidence(definition)
}

func sideEffectOf(observation turnObservation, observationIndex int, held heldFiles, location *time.Location) (sideEffect, bool) {
	call := sideEffect{Tool: observation.Tool, Succeeded: !observation.Failed()}
	if !call.Succeeded {
		call.Input = boundedValue(inLocalTime(decodedJSON(observation.ToolInput), location))
		call.Error = firstNonEmptyString(observation.Failure.UserSafeSummary, observation.Failure.Code)
		return call, true
	}
	if len(observation.Effects) == 0 && len(held.files) > 0 {
		return call, false
	}
	isTouchingHeldType := false
	for _, effect := range observation.Effects {
		if !held.objectTypes[strings.TrimSpace(effect.ObjectType)] {
			call.Changed = append(call.Changed, recordChange{Record: firstNonEmptyString(effect.ID, effect.Path, effect.URL, effect.ObjectType), Change: changeKind(effect.ObjectType, effect.Effect), File: attachedFileFacts(observation.Attachments, effect.Path)})
			continue
		}
		isTouchingHeldType = true
		if file, isHeld := held.files[strings.TrimSpace(effect.Path)]; isHeld {
			call.Changed = append(call.Changed, heldFileChange(observation, observationIndex, effect, file))
		}
	}
	if !isTouchingHeldType {
		call.Input = boundedValue(inLocalTime(decodedJSON(observation.ToolInput), location))
		call.Result = boundedValue(inLocalTime(decodedJSON(observation.Output.Data), location))
	}
	return call, len(call.Changed) > 0 || !isTouchingHeldType
}

func heldFileChange(observation turnObservation, observationIndex int, effect toolcontract.ResourceEffect, file heldFile) recordChange {
	change := recordChange{Record: file.reference, Change: changeKind(effect.ObjectType, effect.Effect)}
	if file.latestObservation == observationIndex {
		change.File = heldFileFacts(observation.Attachments, effect.Path)
	}
	return change
}

func withRecentUnrecordedCalls(calls []sideEffect) []sideEffect {
	unrecordedCount := 0
	for _, call := range calls {
		if len(call.Changed) == 0 {
			unrecordedCount++
		}
	}
	kept := []sideEffect{}
	for _, call := range calls {
		if len(call.Changed) == 0 && unrecordedCount > unrecordedCallLimit {
			unrecordedCount--
			continue
		}
		kept = append(kept, call)
	}
	return kept
}

func heldFilesOf(observations []turnObservation) heldFiles {
	held := heldFiles{files: map[string]heldFile{}, objectTypes: map[string]bool{}}
	for index, observation := range observations {
		if !isSuccessfulCall(observation) {
			continue
		}
		for _, effect := range observation.Effects {
			attachment, isAttached := attachmentAt(observation.Attachments, effect.Path)
			if !isAttached || len(attachment.Holds) == 0 {
				continue
			}
			path := strings.TrimSpace(effect.Path)
			file, isKnown := held.files[path]
			if !isKnown {
				file.reference = fmt.Sprintf("file %d", len(held.files)+1)
			}
			file.latestObservation = index
			held.files[path] = file
			held.objectTypes[strings.TrimSpace(effect.ObjectType)] = true
		}
	}
	return held
}

func heldFileFacts(attachments []toolcontract.FileAttachment, path string) *fileFacts {
	attachment, _ := attachmentAt(attachments, path)
	return &fileFacts{ContentType: strings.TrimSpace(attachment.ContentType), SizeBytes: attachment.SizeBytes, Holds: attachment.Holds}
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

func changeLookups(toolSet *toolcontract.ToolSet, observations []turnObservation, location *time.Location) []changeLookup {
	if toolSet == nil {
		return nil
	}
	lookups := []changeLookup{}
	for _, observation := range successfulToolObservations(observations) {
		definition, isFound := toolSet.ToolDefinition(observation.Tool)
		if !isFound || definition.SideEffectClass != toolcontract.ToolSideEffectRead {
			continue
		}
		lookups = append(lookups, changeLookup{Tool: observation.Tool, Result: boundedValue(inLocalTime(decodedJSON(observation.Output.Data), location))})
	}
	return lookups[max(0, len(lookups)-changeLookupLimit):]
}

func successfulToolObservations(observations []turnObservation) []turnObservation {
	successful := []turnObservation{}
	for _, observation := range observations {
		if isSuccessfulCall(observation) {
			successful = append(successful, observation)
		}
	}
	return successful
}

func isSuccessfulCall(observation turnObservation) bool {
	return observation.Action == "continue" && strings.TrimSpace(observation.Tool) != "" && !observation.Failed()
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
