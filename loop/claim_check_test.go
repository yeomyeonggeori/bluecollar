package loop

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/yeomyeonggeori/bluecollar/agentcontract"
	"github.com/yeomyeonggeori/bluecollar/toolcontract"
)

const notesRequest = "Write a postmortem PDF from the attached log. Action item: add a connection-pool alert (owner: Jordan Example)."

func deliveredFileWithSource(t *testing.T, source map[string]any) (string, []turnObservation) {
	t.Helper()
	filePath := "/home/bc_person_sample/documents/postmortem.pdf"
	attachment := toolcontract.FileAttachment{DevicePath: filePath, Filename: "postmortem.pdf", ContentType: "application/pdf"}
	if source != nil {
		document, _ := json.Marshal(source)
		parsed, isParsed := toolcontract.DeliveredSourceOf(document)
		if !isParsed {
			t.Fatalf("the test source does not parse: %s", document)
		}
		attachment.Source = parsed
	}
	deliverInput, _ := json.Marshal(map[string]string{"path": filePath})
	return filePath, []turnObservation{{
		ObservationID: "obs-001",
		Action:        "continue",
		Tool:          toolcontract.FileDeliverToolName,
		ToolInput:     deliverInput,
		Output:        toolcontract.ToolOutput{Content: "files delivered"},
		Effects:       []toolcontract.ResourceEffect{{ObjectType: "file", Effect: "attached", Path: filePath}},
		Attachments:   []toolcontract.FileAttachment{attachment},
	}}
}

func postmortemSource() map[string]any {
	return map[string]any{
		"schema": "report",
		"known": map[string]any{"author": "이샘플", "company": map[string]any{"name": "Sampletech Co., Ltd."}},
		"claims": []map[string]string{
			{"path": "title", "at": "title", "text": "Checkout Outage Postmortem"},
			{"path": "sections[0].blocks[0].text#0", "at": "sections > Root Cause > blocks > text", "text": "No alert covered the connection-pool level itself."},
			{"path": "sections[1].blocks[0].items[0].text", "at": "sections > Action Items > blocks > items > text", "text": "add a connection-pool alert"},
			{"path": "sections[1].blocks[0].items[0].owner", "at": "sections > Action Items > blocks > items > owner", "text": "Jordan  Example"},
			{"path": "sections[2].blocks[0].text#0", "at": "sections > Timeline > blocks > text", "text": "rollback complete, connections recovering"},
			{"path": "sections[3].blocks[0].fields[0].value", "at": "sections > Footer > blocks > fields > value", "text": "Sampletech Co., Ltd."},
		},
	}
}

func postmortemRequest() AgentTurnRequest {
	return AgentTurnRequest{
		Prompt:  notesRequest,
		ToolSet: kernelFileToolSet(),
		InputParts: []agentcontract.AgentPart{{
			Type: agentcontract.AgentPartTypeFile,
			File: &agentcontract.AgentFilePart{Filename: "incident-log.txt", MarkdownPreview: "14:48 KST rollback complete, connections recovering"},
		}},
	}
}

var makeThePostmortem = expectedChange{Change: "file created", Asked: "Write a postmortem PDF from the attached log"}

func TestClaimsBesideADeliveredFileAreAskedInTheSameChangeCheck(t *testing.T) {
	_, observations := deliveredFileWithSource(t, postmortemSource())
	request := postmortemRequest()
	decisionModel := &scriptedDecisionModel{noul: map[string]float64{"expected0": 1, "claim0": 0.9, "claim1": 0.2}}

	check, errorValue := checkExpectedChanges(context.Background(), decisionModel, request, []expectedChange{makeThePostmortem}, observations, deliveredClaims(request, observations))

	if errorValue != nil || len(decisionModel.requests) != 1 {
		t.Fatalf("expected one decision request, got %d (%v)", len(decisionModel.requests), errorValue)
	}
	questions := decisionModel.requests[0].Questions
	for _, key := range []string{"expected0", "claim0", "claim1"} {
		if _, isAsked := questions[key]; !isAsked {
			t.Fatalf("expected question %s, got %v", key, questions)
		}
	}
	if len(questions) != 3 {
		t.Fatalf("expected only the two claims the sources do not already hold word for word, got %d questions", len(questions))
	}
	state, _ := json.Marshal(decisionModel.requests[0].State)
	for _, want := range []string{`"claimRule":`, `"runtimeFacts":`, `"attachments":[{"name":"incident-log.txt"`, `"text":"No alert covered the connection-pool level itself."`} {
		if !strings.Contains(string(state), want) {
			t.Fatalf("expected %s in the change check state, got %s", want, state)
		}
	}
	if len(check.JudgedClaims) != 2 || check.JudgedClaims[1].Noul != 0.2 {
		t.Fatalf("expected both claims judged, got %+v", check.JudgedClaims)
	}
}

func TestAValueCopiedFromTheRequestAnAttachmentOrARuntimeFactIsNotAsked(t *testing.T) {
	_, observations := deliveredFileWithSource(t, postmortemSource())

	evidence := deliveredClaims(postmortemRequest(), observations)

	asked := []string{}
	for _, claim := range evidence.Claims {
		asked = append(asked, claim.Text)
	}
	if strings.Join(asked, "|") != "Checkout Outage Postmortem|No alert covered the connection-pool level itself." {
		t.Fatalf("expected only the claims not copied from a source, got %q", asked)
	}
}

func TestAFileWithoutASourceAddsNoClaims(t *testing.T) {
	_, observations := deliveredFileWithSource(t, nil)
	request := postmortemRequest()
	decisionModel := &scriptedDecisionModel{noul: map[string]float64{"expected0": 1}}

	checkExpectedChanges(context.Background(), decisionModel, request, []expectedChange{makeThePostmortem}, observations, deliveredClaims(request, observations))

	if len(decisionModel.requests[0].Questions) != 1 {
		t.Fatalf("expected only the change question, got %v", decisionModel.requests[0].Questions)
	}
	if state, _ := json.Marshal(decisionModel.requests[0].State); strings.Contains(string(state), "claimRule") {
		t.Fatalf("expected no claim state, got %s", state)
	}
}

func TestClaimsAreNotAskedWhenNoChangeIsRecorded(t *testing.T) {
	decisionModel := &scriptedDecisionModel{}
	evidence := claimEvidence{Claims: []documentClaim{{File: "a.pdf", Path: "title", Text: "x"}}}

	checkExpectedChanges(context.Background(), decisionModel, postmortemRequest(), []expectedChange{makeThePostmortem}, nil, evidence)

	if len(decisionModel.requests) != 0 {
		t.Fatalf("expected no decision call without a recorded change, got %d", len(decisionModel.requests))
	}
}

func TestAJudgedClaimIsNotAskedAgainUntilItChanges(t *testing.T) {
	ledger := newClaimLedger()
	claim := documentClaim{File: "a.pdf", Path: "title", Text: "Outage"}
	ledger.record([]judgedClaim{{documentClaim: claim, Noul: 0.2}})

	revised := documentClaim{File: "a.pdf", Path: "title", Text: "Checkout outage"}
	pending := ledger.unjudged([]documentClaim{claim, revised})

	if len(pending) != 1 || pending[0] != revised {
		t.Fatalf("expected only the revised claim to be asked, got %+v", pending)
	}
}

func TestAnUnsupportedValueIsRefusedForThreeRoundsThenLeftToTheReply(t *testing.T) {
	ledger := newClaimLedger()
	claim := documentClaim{File: "a.pdf", Path: "sections[0].blocks[0].text#1", Text: "No alert covered the pool.", IsEnforced: true}
	ledger.record([]judgedClaim{{documentClaim: claim, Noul: 0.3}})

	rounds := []int{}
	for attempt := 0; attempt < claimRoundLimit; attempt++ {
		refused, abandoned := ledger.refuse([]documentClaim{claim})
		if len(refused) != 1 || len(abandoned) != 0 {
			t.Fatalf("attempt %d: expected a refusal, got %+v %+v", attempt, refused, abandoned)
		}
		rounds = append(rounds, refused[0].Round)
	}
	refused, abandoned := ledger.refuse([]documentClaim{claim})

	if len(refused) != 0 || len(abandoned) != 1 {
		t.Fatalf("expected the fourth attempt to stop refusing, got %+v %+v", refused, abandoned)
	}
	if rounds[0] != 1 || rounds[2] != claimRoundLimit {
		t.Fatalf("expected rounds 1..%d, got %v", claimRoundLimit, rounds)
	}
}

func TestASupportedValueIsNeverRefused(t *testing.T) {
	ledger := newClaimLedger()
	claim := documentClaim{File: "a.pdf", Path: "title", Text: "Checkout Outage Postmortem", IsEnforced: true}
	ledger.record([]judgedClaim{{documentClaim: claim, Noul: claimSupportedThreshold}})

	if refused, _ := ledger.refuse([]documentClaim{claim}); len(refused) != 0 {
		t.Fatalf("expected no refusal at the threshold, got %+v", refused)
	}
}

func TestTheGateMessageNamesTheValueItsPlaceAndTheLastRoundAsksForABlank(t *testing.T) {
	claim := documentClaim{File: "/home/sample/postmortem.pdf", Path: "sections[0].blocks[0].text#1", Text: "No alert covered the pool."}
	message := unmetChangesMessage(changeCheck{Unsupported: []unsupportedClaim{{documentClaim: claim, Round: 1}}})
	final := unmetChangesMessage(changeCheck{Unsupported: []unsupportedClaim{{documentClaim: claim, Round: claimRoundLimit}}})

	for _, want := range []string{`"No alert covered the pool."`, "sections[0].blocks[0].text#1", "/home/sample/postmortem.pdf", "change only this value"} {
		if !strings.Contains(message, want) {
			t.Fatalf("expected %q in %q", want, message)
		}
	}
	if strings.Contains(message, "Requested changes not done yet") {
		t.Fatalf("expected no unmet-change section, got %q", message)
	}
	if !strings.Contains(final, "set this value to null") || strings.Contains(final, "change only this value") {
		t.Fatalf("expected the last round to ask for a blank, got %q", final)
	}
}

func TestTheGateRefusesAnUnsupportedValueAndAsksOnlyItsRevision(t *testing.T) {
	languageModel := &stubStructuredLanguageModel{contents: []string{expectedChangesDocument(makeThePostmortem)}}
	services := newTurnRunnerTestServices(languageModel, TurnOptions{})
	decisionModel := &scriptedDecisionModel{noul: map[string]float64{"expected0": 1, "claim0": 0.9, "claim1": 0.2}}
	services.runner.UseDecisionModel(decisionModel)
	_, observations := deliveredFileWithSource(t, postmortemSource())
	request := postmortemRequest()
	taskRun := services.taskRunService.CreateTaskRun("person-1", "conversation-1", request.Prompt)

	refused := services.runner.evaluateExpectedChanges(context.Background(), taskRun.TaskRunID, request, observations)

	if refused.IsSatisfied || !strings.Contains(refused.Message, "No alert covered the connection-pool level itself.") {
		t.Fatalf("expected a refusal naming the unsupported value, got %+v", refused)
	}
	if !taskEventsContain(services.taskEventService.ListTaskEvent(taskRun.TaskRunID), "completion.change_check", `"unsupportedClaims":[{"file":`) {
		t.Fatal("expected the change check event to record the unsupported value")
	}

	revised := postmortemSource()
	revised["claims"].([]map[string]string)[1]["text"] = "Detection relied on the checkout error-rate alert."
	_, revisedObservations := deliveredFileWithSource(t, revised)
	decisionModel.noul["claim0"] = 0.8

	accepted := services.runner.evaluateExpectedChanges(context.Background(), taskRun.TaskRunID, request, revisedObservations)

	if !accepted.IsSatisfied {
		t.Fatalf("expected the revised value to pass, got %+v", accepted)
	}
	second := decisionModel.requests[1]
	if len(second.Questions) != 2 || !strings.Contains(mustJSON(second.State), "Detection relied on the checkout error-rate alert.") || strings.Contains(mustJSON(second.State), "Checkout Outage Postmortem") {
		t.Fatalf("expected only the change and the revised value asked again, got %v", second.Questions)
	}
}

func mustJSON(value any) string {
	document, _ := json.Marshal(value)
	return string(document)
}

func TestAValueInAFileWhoseLayoutTheModelWroteIsJudgedButNeverRefused(t *testing.T) {
	languageModel := &stubStructuredLanguageModel{contents: []string{expectedChangesDocument(makeThePostmortem)}}
	services := newTurnRunnerTestServices(languageModel, TurnOptions{})
	services.runner.UseDecisionModel(&scriptedDecisionModel{noul: map[string]float64{"expected0": 1, "claim0": 0.9, "claim1": 0.1}})
	deck := postmortemSource()
	delete(deck, "schema")
	deck["command"] = "office create"
	_, observations := deliveredFileWithSource(t, deck)
	request := postmortemRequest()
	taskRun := services.taskRunService.CreateTaskRun("person-1", "conversation-1", request.Prompt)

	result := services.runner.evaluateExpectedChanges(context.Background(), taskRun.TaskRunID, request, observations)

	if !result.IsSatisfied {
		t.Fatalf("expected a shadow verdict never to refuse, got %+v", result)
	}
	if !taskEventsContain(services.taskEventService.ListTaskEvent(taskRun.TaskRunID), "completion.change_check", `"enforced":false,"noul":0.1`) {
		t.Fatal("expected the shadow verdict in the change check event")
	}
}
