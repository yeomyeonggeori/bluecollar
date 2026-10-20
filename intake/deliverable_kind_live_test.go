//go:build llmeval

package intake

import (
	"context"
	"testing"

	"github.com/yeomyeonggeori/bluecollar/decisionconfig"
	"github.com/yeomyeonggeori/bluecollar/llmcalls"
	"github.com/yeomyeonggeori/blueprotocol/agentcontract"
	"github.com/yeomyeonggeori/blueprotocol/evaltest"
	"github.com/yeomyeonggeori/blueprotocol/model/decisions"
)

type deliverableKindCase struct {
	prompt           string
	referencedRecord string
	want             agentcontract.DeliverableKind
}

var deliverableKindCases = []deliverableKindCase{
	{prompt: "그 업무 블루세이지 사업으로 넣어줘", referencedRecord: "분기 보고서 정리", want: agentcontract.DeliverableKindNone},
	{prompt: "그 업무 마감 금요일로 바꿔줘", referencedRecord: "분기 보고서 정리", want: agentcontract.DeliverableKindNone},
	{prompt: "그 일정 오후 3시로 옮겨줘", referencedRecord: "투자 제안서 검토 회의", want: agentcontract.DeliverableKindNone},
	{prompt: "그 업무 완료로 바꿔줘", referencedRecord: "발표자료 초안 작성", want: agentcontract.DeliverableKindNone},
	{prompt: "그 보고서 내용 세 줄로 요약해줘", referencedRecord: "", want: agentcontract.DeliverableKindNone},
	{prompt: "분기 보고서 워드 파일로 만들어줘", referencedRecord: "", want: agentcontract.DeliverableKindDocument},
	{prompt: "그 업무 내용으로 보고서 PDF 만들어서 보내줘", referencedRecord: "분기 보고서 정리", want: agentcontract.DeliverableKindDocument},
	{prompt: "회의록 문서로 정리해서 파일로 줘", referencedRecord: "", want: agentcontract.DeliverableKindDocument},
	{prompt: "투자자 미팅용 발표자료 만들어줘", referencedRecord: "", want: agentcontract.DeliverableKindPresentation},
	{prompt: "그 업무 결과로 발표 슬라이드 만들어줘", referencedRecord: "발표자료 초안 작성", want: agentcontract.DeliverableKindPresentation},
}

func TestDeliverableKindIsWhatTheMessageAsksTheWorkToProduce(t *testing.T) {
	endpoint, errorValue := decisions.EndpointFromEnvironment(decisionconfig.DecisionEnvironmentNames)
	evaltest.RequireConfigured(t, "the decision model", errorValue)
	planner := NewDecisionPlanner(endpoint.DecisionModel(), nil)
	wrong := 0
	for _, deliverableCase := range deliverableKindCases {
		request := addressedDecisionRequest(deliverableCase.prompt)
		if deliverableCase.referencedRecord != "" {
			request.VisibleContext.Messages = []agentcontract.VisibleContextMessage{
				{Speaker: "user", SpeakerCallingName: "이샘플", Text: deliverableCase.referencedRecord + " 업무 추가해줘"},
				{Speaker: "assistant", SpeakerCallingName: "인턴", Text: deliverableCase.referencedRecord + " 업무를 작업 목록에 등록했습니다."},
			}
			request.PriorTask = agentcontract.PriorTaskContext{TaskRunID: "prior-task", Status: "completed", Prompt: deliverableCase.referencedRecord + " 업무 추가해줘", Result: deliverableCase.referencedRecord + " 업무를 작업 목록에 등록했습니다."}
		}
		decided, errorValue := planner.Decide(context.Background(), request, &llmcalls.IntakeCallLedger{SchemaNames: llmcalls.IntakeSchemaNames})
		if errorValue != nil {
			t.Fatal(errorValue)
		}
		got := decided.Messages[0].TurnFields.DeliverableKind
		if got != deliverableCase.want {
			wrong++
			t.Errorf("%q after %q: want %s, got %s", deliverableCase.prompt, deliverableCase.referencedRecord, deliverableCase.want, got)
		}
	}
	t.Logf("wrong: %d of %d", wrong, len(deliverableKindCases))
}
