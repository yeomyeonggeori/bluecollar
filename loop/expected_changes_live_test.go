//go:build llmeval

package loop

import (
	"context"
	"encoding/json"
	"os"
	"slices"
	"strings"
	"testing"

	"github.com/yeomyeonggeori/bluecollar/model/openaicompatible"
	"github.com/yeomyeonggeori/bluecollar/toolcontract"
)

type expectedChangesLiveCase struct {
	name                string
	originalInstruction string
	prompt              string
	conversation        []string
	wantedKinds         []string
}

var expectedChangesLiveCases = []expectedChangesLiveCase{
	{name: "report of finished work", prompt: `김인턴 에이전트 완료 판정을 "요청이 요구한 변경 목록 ↔ 실제 기록 대조 + Jev 확인"으로 바꿔서 운영 배포까지 끝냈어`},
	{name: "correction asking to record it", originalInstruction: `김인턴 에이전트 완료 판정을 "요청이 요구한 변경 목록 ↔ 실제 기록 대조 + Jev 확인"으로 바꿔서 운영 배포까지 끝냈어`, prompt: "아니 업무에 완료로 추가하라고", wantedKinds: []string{"task created"}},
	{name: "task with deadline", prompt: "분기 결산 누락 확인 업무를 7월 24일 마감으로 추가해줘", wantedKinds: []string{"task created"}},
	{name: "task status change", prompt: "특허명세서 초안 검토 진행 중으로 올려줘", wantedKinds: []string{"task updated"}},
	{name: "bulk task update", prompt: "내 업무 중 사이즈 등록 안 된 것들 수정해서 사이즈 넣어줘", wantedKinds: []string{"task updated"}},
	{name: "calendar entry", prompt: "7월 13일에 샨보장 미팅을 오전 10시부터 11시까지 등록해줘", wantedKinds: []string{"calendar created"}},
	{name: "question only", prompt: "오늘 내 일정 뭐 있어?"},
	{name: "question whether done", prompt: "어제 말한 업무 등록됐어?"},
	{name: "short follow-up", conversation: []string{"이샘플: 다음 주 화요일에 박예시랑 제안서 리뷰해야 해", "김인턴: 업무로 등록할까요, 일정으로 잡을까요?"}, prompt: "업무로 추가해줘 예정.", wantedKinds: []string{"task created"}},
	{name: "report then nothing asked", prompt: "특허명세서 초안 검토 끝났어. 월요일에 보낼 거야"},
}

func TestLiveExpectedChangesDefinition(t *testing.T) {
	catalogPath := os.Getenv("BLUECOLLAR_CAPABILITY_CATALOG")
	if catalogPath == "" {
		t.Skip("set BLUECOLLAR_CAPABILITY_CATALOG to the generated capability-tools.json")
	}
	provider, errorValue := (openaicompatible.Endpoint{URL: os.Getenv("BLUECOLLAR_MODEL_ENDPOINT"), ModelName: os.Getenv("BLUECOLLAR_MODEL_NAME"), APIKey: os.Getenv("BLUECOLLAR_MODEL_API_KEY")}).Provider()
	if errorValue != nil {
		t.Fatal(errorValue)
	}
	services := newTurnRunnerTestServices(provider, TurnOptions{})
	toolSet := catalogToolSet(t, catalogPath)
	failures := 0
	for _, liveCase := range expectedChangesLiveCases {
		for attempt := 0; attempt < 3; attempt++ {
			changes, isDefined := services.runner.defineExpectedChanges(context.Background(), "live", liveCaseRequest(liveCase, toolSet))
			kinds := []string{}
			for _, change := range changes {
				kinds = append(kinds, change.Change)
			}
			slices.Sort(kinds)
			wanted := slices.Clone(liveCase.wantedKinds)
			slices.Sort(wanted)
			verdict := "ok"
			if !isDefined || !slices.Equal(slices.Compact(kinds), wanted) {
				verdict = "WRONG"
				failures++
			}
			document, _ := json.Marshal(changes)
			t.Logf("%s [%s] #%d: %s", verdict, liveCase.name, attempt, document)
		}
	}
	t.Logf("wrong: %d of %d", failures, len(expectedChangesLiveCases)*3)
}

func liveCaseRequest(liveCase expectedChangesLiveCase, toolSet *toolcontract.ToolSet) AgentTurnRequest {
	request := AgentTurnRequest{Prompt: liveCase.prompt, ToolSet: toolSet}
	if liveCase.originalInstruction != "" {
		request.ActiveGoal = ActiveGoal{OriginalInstruction: liveCase.originalInstruction, CurrentObjective: liveCase.prompt}
	}
	for _, line := range liveCase.conversation {
		speaker, text, _ := strings.Cut(line, ": ")
		request.VisibleContext.Messages = append(request.VisibleContext.Messages, VisibleContextMessage{Speaker: speaker, Text: text})
	}
	return request
}

func catalogToolSet(t *testing.T, catalogPath string) *toolcontract.ToolSet {
	document, errorValue := os.ReadFile(catalogPath)
	if errorValue != nil {
		t.Fatal(errorValue)
	}
	var catalog struct {
		Tools []struct {
			Name           string `json:"name"`
			Description    string `json:"description"`
			ResultContract *struct {
				Effects []toolcontract.ResourceEffectContract
			} `json:"resultContract"`
		} `json:"tools"`
	}
	if errorValue := json.Unmarshal(document, &catalog); errorValue != nil {
		t.Fatal(errorValue)
	}
	definitions := []toolcontract.ToolDefinition{}
	for _, tool := range catalog.Tools {
		definition := testToolDescriptor(tool.Name)
		definition.Description = tool.Description
		if tool.ResultContract != nil {
			definition.ResultContract.Effects = tool.ResultContract.Effects
		}
		definitions = append(definitions, definition)
	}
	return newTestToolSetWithDefinitions(definitions)
}
