package loop

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/yeomyeonggeori/bluecollar/toolcontract"
)

func TestAHeldCallIsSettledOnlyByTheTokenTheLoopMintedForIt(t *testing.T) {
	heldCalls := []heldCallRecord{{
		ApprovalToken: "token-1",
		ToolName:      "message_send",
		ToolInputKey:  canonicalToolCallKey("message_send", json.RawMessage(`{"to":["alice"],"message":"회의록"}`)),
		ObservationID: "obs-1",
	}}

	sameCall := CarriedOutCall{ToolName: "message_send", ToolInput: json.RawMessage(`{"message":"회의록","to":["alice"]}`), ApprovalToken: "token-1"}
	if _, isMatched := heldCallForCarriedOutCall(heldCalls, sameCall); !isMatched {
		t.Fatal("the same call with its own token is the call that was approved, whatever order its fields arrive in")
	}

	widerCall := CarriedOutCall{ToolName: "message_send", ToolInput: json.RawMessage(`{"to":["everyone"],"message":"회의록"}`), ApprovalToken: "token-1"}
	if _, isMatched := heldCallForCarriedOutCall(heldCalls, widerCall); isMatched {
		t.Fatal("a token is bound to the exact call it was minted for; carrying a wider one back under it is how an approval for one thing becomes an approval for another")
	}

	untokenedCall := CarriedOutCall{ToolName: "message_send", ToolInput: json.RawMessage(`{"to":["alice"],"message":"회의록"}`)}
	if _, isMatched := heldCallForCarriedOutCall(heldCalls, untokenedCall); isMatched {
		t.Fatal("without the token the loop minted, a carried-out call is a claim about what was approved rather than proof of it")
	}
}

func TestACarriedOutCallThatWasNeverHeldIsRecordedAsItHappened(t *testing.T) {
	services := newTurnRunnerTestServices(&sequenceLanguageModel{modelTier: "xlow", contents: []string{finishMessageDocument("보냈습니다")}},
		TurnOptions{TaskLevel: TaskLevelXLow, MaxIterationCount: 2, MaxToolCallCount: 5})

	result, errorValue := services.runner.RunTurn(context.Background(), AgentTurnRequest{
		RequesterPersonID: "person-1",
		ConversationID:    "conversation-1",
		Prompt:            "회의록 보내줘",
		ToolSet:           newTestToolSet([]string{"message_send"}),
		CarriedOutCalls: []CarriedOutCall{{
			ToolName:  "message_send",
			ToolInput: json.RawMessage(`{"to":["alice"]}`),
			Result:    toolcontract.ToolSuccessData("sent to alice", json.RawMessage(`{"messageID":"m-1"}`)),
		}},
	})
	if errorValue != nil {
		t.Fatalf("expected the turn to run: %v", errorValue)
	}

	taskEvents := services.taskEventService.ListTaskEvent(result.TaskRun.TaskRunID)
	if taskEventsContain(taskEvents, approvalUnheldCallEventName, "message_send") {
		t.Fatal("a host that never held this call is not carrying back an approval it does not have; nothing changed for it")
	}
	if !taskEventsContain(taskEvents, "tool.message_send.result", "sent to alice") {
		t.Fatal("the effect still reaches the ledger as the loop's own observation")
	}
}

func TestAnUnmatchedCarriedOutCallLeavesTheHoldWaitingAndSaysSo(t *testing.T) {
	services := newTurnRunnerTestServices(&sequenceLanguageModel{modelTier: "xlow", contents: []string{finishMessageDocument("보냈습니다")}},
		TurnOptions{TaskLevel: TaskLevelXLow, MaxIterationCount: 2, MaxToolCallCount: 5})
	taskRun := services.taskRunService.CreateTaskRun("person-1", "conversation-1", "회의록 보내줘")
	services.runner.mintHeldCallApproval(taskRun.TaskRunID, turnObservation{
		ObservationID: "obs-1",
		Tool:          "message_send",
		ToolInput:     json.RawMessage(`{"to":["alice"]}`),
	})
	heldCalls := services.runner.heldCallsAwaitingApproval(taskRun.TaskRunID)
	if len(heldCalls) != 1 {
		t.Fatalf("a held call is minted once and waits in the ledger: %v", heldCalls)
	}

	settled := services.runner.settleHeldCallApproval(taskRun.TaskRunID, heldCalls, CarriedOutCall{
		ToolName:      "message_send",
		ToolInput:     json.RawMessage(`{"to":["everyone"]}`),
		ApprovalToken: heldCalls[0].ApprovalToken,
		Result:        toolcontract.ToolSuccess("sent to everyone"),
	})

	if !strings.Contains(settled.ContentText(), "sent to everyone") {
		t.Fatal("the send happened, so the ledger and the model have to carry it; the loop cannot unsend it by disagreeing")
	}
	if !strings.Contains(settled.ContentText(), approvalUnmatchedObservationNote) {
		t.Fatalf("the model has to be told that what ran is not what was waiting: %q", settled.ContentText())
	}
	if len(services.runner.heldCallsAwaitingApproval(taskRun.TaskRunID)) != 1 {
		t.Fatal("a call nobody approved does not spend the approval that is still waiting")
	}

	services.runner.settleHeldCallApproval(taskRun.TaskRunID, heldCalls, CarriedOutCall{
		ToolName:      "message_send",
		ToolInput:     json.RawMessage(`{"to":["alice"]}`),
		ApprovalToken: heldCalls[0].ApprovalToken,
		Result:        toolcontract.ToolSuccess("sent to alice"),
	})
	if len(services.runner.heldCallsAwaitingApproval(taskRun.TaskRunID)) != 0 {
		t.Fatal("the call that was held, carried back under its own token, spends it")
	}
}
