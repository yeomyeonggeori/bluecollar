package approval

import (
	"context"

	"github.com/yeomyeonggeori/bluecollar/turnclock"
	"github.com/yeomyeonggeori/blueprotocol/holdrecord"
	"github.com/yeomyeonggeori/blueprotocol/model"
	"github.com/yeomyeonggeori/blueprotocol/taskstate"
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
	ledger := holdrecord.LedgerOf(gate.taskRuns.ListTaskEvent(request.taskRunID))
	if ledger.GrantsScope(request.approvalScope()) {
		return gate.spend(request, "")
	}
	if hold, isApproved := holdrecord.ApprovedHoldForCall(ledger.Holds, request.toolName(), request.toolInput); isApproved {
		return gate.spend(request, hold.ID)
	}
	return gate.holdAndAsk(ctx, request)
}

func (gate *Gate) holdAndAsk(ctx context.Context, request approvalRequest) outcome {
	hold := gate.recordHold(request, gate.wordQuestion(ctx, request))
	answer := gate.askWithTheTurnClockStopped(ctx, hold)
	if !answer.isGiven() {
		return outcome{kind: outcomeUnanswered}
	}
	if gate.settle(request.taskRunID, hold, answer, askerSource) == outcomeApproved {
		return gate.spend(request, hold.ID)
	}
	return outcome{kind: outcomeRejected}
}

func (gate *Gate) settle(taskRunID string, hold holdrecord.Hold, answer Answer, source string) outcomeKind {
	if answer == Rejected {
		holdrecord.Decide(gate.taskRuns, taskRunID, hold.ID, holdrecord.DecisionReject, source)
		return outcomeRejected
	}
	holdrecord.Decide(gate.taskRuns, taskRunID, hold.ID, holdrecord.DecisionApprove, source)
	return outcomeApproved
}

func (gate *Gate) spend(request approvalRequest, holdID string) outcome {
	holdrecord.Spend(gate.taskRuns, request.taskRunID, holdID, request.toolDefinition.Name, request.toolInput)
	return outcome{kind: outcomeApproved, holdID: holdID}
}

func (gate *Gate) askWithTheTurnClockStopped(ctx context.Context, hold holdrecord.Hold) Answer {
	resume := turnclock.Pause(ctx)
	defer resume()
	return gate.asker.Ask(ctx, hold)
}
