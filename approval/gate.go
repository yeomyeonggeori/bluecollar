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

func (gate *Gate) awaitApproval(ctx context.Context, request approvalRequest) ruling {
	if gate.asker == nil || request.taskRunID == "" {
		return ruling{verdict: verdictUnanswerable}
	}
	state := readLedger(gate.taskRuns.ListTaskEvent(request.taskRunID))
	if state.grantsScope(request.approvalScope()) {
		return gate.spend(request, "")
	}
	if hold, isApproved := state.approvedHoldForCall(request.toolName(), request.input); isApproved {
		return gate.spend(request, hold.ID)
	}
	return gate.holdAndAsk(ctx, request)
}

func (gate *Gate) holdAndAsk(ctx context.Context, request approvalRequest) ruling {
	hold := gate.recordHold(request, gate.wordQuestion(ctx, request))
	answer := gate.asker.Ask(ctx, hold)
	if !answer.isAnswer() {
		return ruling{verdict: verdictUnanswered}
	}
	if gate.settle(hold, answer, askerSource) == verdictApproved {
		return gate.spend(request, hold.ID)
	}
	return ruling{verdict: verdictRejected}
}

func (gate *Gate) settle(hold Hold, answer Answer, source string) verdict {
	if hold.state != holdPending || !answer.isAnswer() {
		return verdictUnanswered
	}
	if answer == Rejected {
		recordDecision(gate.taskRuns, hold, decisionCancel, source)
		return verdictRejected
	}
	recordDecision(gate.taskRuns, hold, decisionConfirm, source)
	grantScope(gate.taskRuns, hold)
	return verdictApproved
}

func (gate *Gate) spend(request approvalRequest, holdID string) ruling {
	recordSpent(gate.taskRuns, request.taskRunID, holdID, request.tool.Name, request.input)
	return ruling{verdict: verdictApproved, approvedCallID: holdID}
}
