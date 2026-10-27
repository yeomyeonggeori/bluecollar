//go:build llmeval

package loop

import (
	"context"
	"encoding/json"
	"os"
	"testing"

	"github.com/yeomyeonggeori/blueprotocol/model/decisions"
	"github.com/yeomyeonggeori/blueprotocol/toolcontract"
)

const meetingRequestPreamble = "The person asking is 이샘플.\n\nNow: 2026-10-20 (Tue) 12:58 +09:00 KST\n\n"

type completedMeetingCheckCase struct {
	name             string
	prompt           string
	change           string
	conversation     []string
	participantNames []string
	withLookup       bool
	isUnfulfilled    bool
}

const meetingAnnouncement = "이샘플: https://meet.example.com/abc-defg-hij @박예시 @최견본 블루세이지 소회의입니다."

var completedMeetingCheckCases = []completedMeetingCheckCase{
	{name: "addressed to the agent, requester alone", prompt: "@김인턴 완료 업무로 추가해줘", change: "블루세이지 소회의를 완료 업무로 추가", conversation: []string{meetingAnnouncement}, participantNames: []string{"이샘플"}},
	{name: "addressed to the agent, after a lookup", prompt: "@김인턴 완료 업무로 추가해줘", change: "블루세이지 소회의를 완료 업무로 추가", conversation: []string{meetingAnnouncement}, participantNames: []string{"이샘플"}, withLookup: true},
	{name: "addressed to the agent, everyone in it", prompt: "@김인턴 완료 업무로 추가해줘", change: "블루세이지 소회의를 완료 업무로 추가", conversation: []string{meetingAnnouncement}, participantNames: []string{"이샘플", "박예시", "최견본"}},
	{name: "not addressed", prompt: "완료 업무로 추가해줘", change: "블루세이지 소회의를 완료 업무로 추가", conversation: []string{meetingAnnouncement}, participantNames: []string{"이샘플"}},
	{name: "asked for somebody the record leaves out", prompt: "@김인턴 박예시 님 업무로 완료 처리해서 추가해줘", change: "블루세이지 소회의를 박예시 님의 완료 업무로 추가", conversation: []string{meetingAnnouncement}, participantNames: []string{"이샘플"}, isUnfulfilled: true},
}

func TestLiveChangeCheckOnACompletedMeetingAddedFromItsThread(t *testing.T) {
	apiKey := os.Getenv("OPENROUTER_API_KEY")
	if apiKey == "" {
		t.Skip("set OPENROUTER_API_KEY")
	}
	decisionModel := decisions.Endpoint{URL: decisions.DefaultEndpointURL, ModelName: firstNonEmptyString(os.Getenv("BLUECOLLAR_DECISION_MODEL"), "~typesafe/jev-latest"), APIKey: apiKey}.DecisionModel()
	toolSet := completedMeetingToolSet()
	wrongVerdicts := 0
	attempts := 0
	for _, liveCase := range completedMeetingCheckCases {
		request := AgentTurnRequest{Prompt: liveCase.prompt, ToolSet: toolSet, AgentIdentity: AgentIdentity{Name: "김인턴", Handle: "김인턴"}}
		request.ActiveGoal = ActiveGoal{OriginalInstruction: meetingRequestPreamble + liveCase.prompt}
		request.VisibleContext.Messages = visibleMessages(liveCase.conversation)
		expected := []string{liveCase.change}
		for attempt := 0; attempt < 3; attempt++ {
			attempts++
			check, errorValue := checkExpectedChanges(context.Background(), decisionModel, request, expected, completedMeetingObservations(liveCase))
			if errorValue != nil {
				t.Fatal(errorValue)
			}
			isRefused := len(check.Unmet) > 0
			verdict := "ok"
			if isRefused != liveCase.isUnfulfilled {
				verdict = "WRONG"
				wrongVerdicts++
			}
			t.Logf("%s refused=%v [%s] #%d: %v", verdict, isRefused, liveCase.name, attempt, check.CarriedOut)
		}
	}
	t.Logf("wrong: %d of %d", wrongVerdicts, attempts)
}

func completedMeetingToolSet() *toolcontract.ToolSet {
	add := testToolDescriptor("task_add")
	add.Namespace = "task"
	add.Description = "Create a new workspace task with typed task fields."
	add.SideEffectClass = toolcontract.ToolSideEffectStateChange
	add.ResultContract.Effects = []toolcontract.ResourceEffectContract{{ObjectType: "task", Effect: "created", ResultField: "taskID", EffectIdentity: "id"}}
	list := testToolDescriptor("task_list")
	list.Namespace = "task"
	list.SideEffectClass = toolcontract.ToolSideEffectRead
	return newTestToolSetWithDefinitions([]toolcontract.ToolDefinition{add, list})
}

func completedMeetingObservations(liveCase completedMeetingCheckCase) []turnObservation {
	task := map[string]any{
		"taskID": "a8eff5bb", "content": "블루세이지 소회의", "status": "completed", "type": "회의", "business": "", "size": "",
		"ownerName": liveCase.participantNames[0], "participantNames": liveCase.participantNames, "requesterID": "", "requesterName": "",
		"startDate": "2026-10-20", "endDate": "2026-10-20", "weekCode": "2026-W43",
	}
	data, _ := json.Marshal(task)
	added := successfulSideEffectObservation("obs-001", "task_add", `{"status":"completed","title":"블루세이지 소회의"}`, string(data))
	added.Output.Data = data
	added.Effects = []toolcontract.ResourceEffect{{ObjectType: "task", Effect: "created", ID: "a8eff5bb"}}
	observations := []turnObservation{added}
	if !liveCase.withLookup {
		return observations
	}
	listed, _ := json.Marshal(map[string]any{"count": 1, "tasks": []any{task}})
	lookup := successfulSideEffectObservation("obs-003", "task_list", `{}`, string(listed))
	lookup.Output.Data = listed
	return append(observations, lookup)
}

type answeredQuestionCheckCase struct {
	name          string
	observations  []turnObservation
	isUnfulfilled bool
}

func TestLiveChangeCheckOnATaskAddedFromTheAnswerToWhatToAdd(t *testing.T) {
	apiKey := os.Getenv("OPENROUTER_API_KEY")
	if apiKey == "" {
		t.Fatal("set OPENROUTER_API_KEY")
	}
	decisionModel := decisions.Endpoint{URL: decisions.DefaultEndpointURL, ModelName: firstNonEmptyString(os.Getenv("BLUECOLLAR_DECISION_MODEL"), "~typesafe/jev-latest"), APIKey: apiKey}.DecisionModel()
	request := AgentTurnRequest{Prompt: "샘플방송 컨소시엄 ZONE-2 사업 종료", ToolSet: completedMeetingToolSet(), AgentIdentity: AgentIdentity{Name: "김인턴", Handle: "김인턴"}}
	request.ActiveGoal = ActiveGoal{OriginalInstruction: meetingRequestPreamble + "@김인턴 업무추가"}
	request.VisibleContext.Messages = visibleMessages([]string{"이샘플: @김인턴 업무추가", "김인턴: 어떤 업무를 추가할까요? 구체적인 내용(업무 종류, 마감, 산출물)을 알려주세요."})
	expected := []string{"샘플방송 컨소시엄 ZONE-2 사업 종료를 업무로 추가"}
	added := zoneTwoTaskAdded()
	failed := zoneTwoTaskAdded()
	failed.Output.Data = nil
	failed.Effects = nil
	failed.Failure = &toolcontract.ToolFailure{Code: "unavailable", UserSafeSummary: "The task store did not answer."}
	wrongVerdicts := 0
	attempts := 0
	for _, liveCase := range []answeredQuestionCheckCase{
		{name: "added as completed", observations: []turnObservation{added}},
		{name: "nothing added", isUnfulfilled: true},
		{name: "the add failed", observations: []turnObservation{failed}, isUnfulfilled: true},
	} {
		for attempt := 0; attempt < 3; attempt++ {
			attempts++
			check, errorValue := checkExpectedChanges(context.Background(), decisionModel, request, expected, liveCase.observations)
			if errorValue != nil {
				t.Fatal(errorValue)
			}
			isRefused := len(check.Unmet) > 0
			verdict := "ok"
			if isRefused != liveCase.isUnfulfilled {
				verdict = "WRONG"
				wrongVerdicts++
			}
			t.Logf("%s refused=%v [%s] #%d: %v", verdict, isRefused, liveCase.name, attempt, check.CarriedOut)
		}
	}
	if wrongVerdicts > 0 {
		t.Fatalf("wrong: %d of %d", wrongVerdicts, attempts)
	}
}

func zoneTwoTaskAdded() turnObservation {
	task := map[string]any{
		"taskID": "723edd1f", "content": "샘플방송 컨소시엄 ZONE-2 사업 종료", "status": "completed", "type": "운영", "size": "XL", "business": "",
		"ownerName": "이샘플", "participantNames": []string{"이샘플"}, "startDate": "2026-10-26", "endDate": "2026-10-26", "weekCode": "26W44",
	}
	data, _ := json.Marshal(task)
	added := successfulSideEffectObservation("obs-001", "task_add", `{"status":"completed","title":"샘플방송 컨소시엄 ZONE-2 사업 종료"}`, string(data))
	added.Output.Data = data
	added.Effects = []toolcontract.ResourceEffect{{ObjectType: "task", Effect: "created", ID: "723edd1f"}}
	return added
}
