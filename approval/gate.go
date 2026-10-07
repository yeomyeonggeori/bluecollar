package approval

import (
	"context"

	"github.com/yeomyeonggeori/blueprotocol/approvalcore"
	"github.com/yeomyeonggeori/blueprotocol/holdrecord"
	"github.com/yeomyeonggeori/blueprotocol/model"
	"github.com/yeomyeonggeori/blueprotocol/taskstate"
)

const askerSource = "asker"

var eventNames = approvalcore.EventNames{
	ConfirmationRequested: "agent.approval_confirmation_requested",
	AskRequested:          "agent.approval_ask_requested",
	WordingFailed:         "agent.approval_wording_failed",
}

type Gate struct {
	core     approvalcore.Core
	taskRuns taskstate.TaskRunStore
	worder   holdrecord.QuestionWorder
	asker    Asker
}

func New(taskRuns taskstate.TaskRunStore, languageModel model.LanguageModelProvider, asker Asker) *Gate {
	return &Gate{core: approvalcore.New(taskRuns, eventNames), taskRuns: taskRuns, worder: NewWorder(languageModel), asker: asker}
}

func (gate *Gate) awaitApproval(ctx context.Context, request approvalRequest) approvalcore.Outcome {
	if gate.asker == nil {
		return approvalcore.Outcome{Verdict: approvalcore.Unanswerable}
	}
	return gate.core.Await(ctx, request.call(), askerHost{gate: gate, request: request})
}

type askerHost struct {
	gate    *Gate
	request approvalRequest
}

func (host askerHost) Prepare(ctx context.Context, call approvalcore.Call) (approvalcore.Question, bool) {
	return approvalcore.Question{Text: host.gate.core.Word(ctx, host.gate.worder, call, host.request.questionFacts())}, true
}

func (host askerHost) Ask(ctx context.Context, hold holdrecord.Hold) approvalcore.Verdict {
	return host.gate.settle(host.request.taskRunID, hold, host.gate.asker.Ask(ctx, hold), askerSource)
}

func (gate *Gate) settle(taskRunID string, hold holdrecord.Hold, verdict approvalcore.Verdict, source string) approvalcore.Verdict {
	switch verdict {
	case approvalcore.Approved:
		holdrecord.Decide(gate.taskRuns, taskRunID, hold.ID, holdrecord.DecisionApprove, source)
	case approvalcore.Rejected:
		holdrecord.Decide(gate.taskRuns, taskRunID, hold.ID, holdrecord.DecisionReject, source)
	}
	return verdict
}
