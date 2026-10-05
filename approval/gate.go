package approval

import (
	"context"

	"github.com/yeomyeonggeori/bluecollar/model"
	"github.com/yeomyeonggeori/bluecollar/taskstate"
)

const askerSource = "asker"

type Gate struct {
	taskRuns      taskstate.TaskRunStore
	languageModel model.LanguageModelProvider
	asker         Asker
}

func New(taskRuns taskstate.TaskRunStore, languageModel model.LanguageModelProvider, asker Asker) *Gate {
	return &Gate{taskRuns: taskRuns, languageModel: languageModel, asker: asker}
}

func (gate *Gate) awaitApproval(ctx context.Context, request approvalRequest) outcome {
	if gate.asker == nil || request.taskRunID == "" {
		return outcome{kind: outcomeUnanswerable}
	}
	state := holdLedgerOf(gate.taskRuns.ListTaskEvent(request.taskRunID))
	if state.grantsScope(request.approvalScope()) {
		return gate.spend(request, "")
	}
	if hold, isApproved := state.approvedHoldForCall(request.toolName(), request.toolInput); isApproved {
		return gate.spend(request, hold.ID)
	}
	return gate.holdAndAsk(ctx, request)
}

func (gate *Gate) holdAndAsk(ctx context.Context, request approvalRequest) outcome {
	hold := gate.recordHold(request, gate.wordQuestion(ctx, request))
	answer := gate.asker.Ask(ctx, hold)
	if !answer.isGiven() {
		return outcome{kind: outcomeUnanswered}
	}
	if gate.settle(hold, answer, askerSource) == outcomeApproved {
		return gate.spend(request, hold.ID)
	}
	return outcome{kind: outcomeRejected}
}

func (gate *Gate) settle(hold Hold, answer Answer, source string) outcomeKind {
	if answer == Rejected {
		recordDecision(gate.taskRuns, hold, decisionCancel, source)
		return outcomeRejected
	}
	recordDecision(gate.taskRuns, hold, decisionConfirm, source)
	grantApprovalScope(gate.taskRuns, hold)
	return outcomeApproved
}

func (gate *Gate) spend(request approvalRequest, holdID string) outcome {
	recordSpent(gate.taskRuns, request.taskRunID, holdID, request.toolDefinition.Name, request.toolInput)
	return outcome{kind: outcomeApproved, holdID: holdID}
}
