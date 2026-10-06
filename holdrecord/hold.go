package holdrecord

import (
	"encoding/json"
	"strings"

	"github.com/yeomyeonggeori/bluecollar/agentcontract"
	"github.com/yeomyeonggeori/bluecollar/taskstate"
	"github.com/yeomyeonggeori/bluecollar/toolcontract"
)

type State string

const (
	StatePending  State = "pending"
	StateApproved State = "approved"
	StateRejected State = "rejected"
	StateDeferred State = "deferred"
	StateSpent    State = "spent"
)

const (
	DecisionApprove = string(agentcontract.ApprovalSignalApprove)
	DecisionReject  = string(agentcontract.ApprovalSignalReject)
	DecisionDefer   = "defer"
)

type Choice struct {
	Key      string `json:"key"`
	StartsAt string `json:"startsAt,omitempty"`
}

func (choice Choice) DefersTheCall() bool {
	return strings.TrimSpace(choice.StartsAt) != ""
}

type Hold struct {
	ID      string
	Call    agentcontract.HeldCall
	Choices []Choice
	State   State
}

type openedBody struct {
	agentcontract.HeldCall
	Choices []Choice `json:"choices,omitempty"`
}

type decidedBody struct {
	HoldID   string `json:"holdID"`
	Decision string `json:"decision"`
	Source   string `json:"source"`
}

type spentBody struct {
	HoldID    string          `json:"holdID,omitempty"`
	ToolName  string          `json:"toolName"`
	ToolInput json.RawMessage `json:"toolInput,omitempty"`
}

type scopeGrantedBody struct {
	Scope string `json:"scope"`
}

func Open(taskRunStore taskstate.TaskRunStore, taskRunID string, call agentcontract.HeldCall, choices []Choice) Hold {
	call.HoldID = taskstate.NewIdentifier()
	taskRunStore.AppendTaskEvent(taskRunID, agentcontract.TaskEventApprovalHoldOpened, marshal(openedBody{HeldCall: call, Choices: choices}))
	return Hold{ID: call.HoldID, Call: call, Choices: choices, State: StatePending}
}

func Decide(taskRunStore taskstate.TaskRunStore, taskRunID string, holdID string, decision string, source string) {
	pendingHold, isPending := pendingHoldByID(taskRunStore.ListTaskEvent(taskRunID), holdID)
	taskRunStore.AppendTaskEvent(taskRunID, agentcontract.TaskEventApprovalDecided, marshal(decidedBody{HoldID: holdID, Decision: decision, Source: source}))
	if isPending && decision == DecisionApprove {
		grantScope(taskRunStore, taskRunID, pendingHold)
	}
}

func Spend(taskRunStore taskstate.TaskRunStore, taskRunID string, holdID string, toolName string, toolInput json.RawMessage) {
	taskRunStore.AppendTaskEvent(taskRunID, agentcontract.TaskEventApprovalHoldSpent, marshal(spentBody{HoldID: holdID, ToolName: strings.TrimSpace(toolName), ToolInput: toolInput}))
}

func SpendApprovedCall(taskRunStore taskstate.TaskRunStore, taskRunID string, toolName string, toolInput json.RawMessage) (Hold, bool) {
	approved, isApproved := ApprovedHoldForCall(Holds(taskRunStore.ListTaskEvent(taskRunID)), toolName, toolInput)
	if !isApproved {
		return Hold{}, false
	}
	Spend(taskRunStore, taskRunID, approved.ID, toolName, toolInput)
	return approved, true
}

func grantScope(taskRunStore taskstate.TaskRunStore, taskRunID string, approvedHold Hold) {
	approvalScope := strings.TrimSpace(approvedHold.Call.ApprovalScope)
	if approvalScope == "" {
		return
	}
	taskRunStore.AppendTaskEvent(taskRunID, agentcontract.TaskEventApprovalScopeGranted, marshal(scopeGrantedBody{Scope: approvalScope}))
}

type Ledger struct {
	Holds         []Hold
	grantedScopes map[string]bool
}

func LedgerOf(taskEvents []agentcontract.TaskEvent) Ledger {
	ledger := Ledger{grantedScopes: map[string]bool{}}
	for _, taskEvent := range taskEvents {
		switch taskEvent.Name {
		case agentcontract.TaskEventApprovalHoldOpened:
			ledger.Holds = append(ledger.Holds, holdFromEvent(taskEvent))
		case agentcontract.TaskEventApprovalDecided:
			decided := decode[decidedBody](taskEvent.Body)
			update(ledger.Holds, decided.HoldID, func(held *Hold) { held.decide(decided.Decision) })
		case agentcontract.TaskEventApprovalHoldSpent:
			update(ledger.Holds, decode[spentBody](taskEvent.Body).HoldID, func(held *Hold) { held.spend() })
		case agentcontract.TaskEventApprovalScopeGranted:
			ledger.grantedScopes[strings.TrimSpace(decode[scopeGrantedBody](taskEvent.Body).Scope)] = true
		}
	}
	return ledger
}

func Holds(taskEvents []agentcontract.TaskEvent) []Hold {
	return LedgerOf(taskEvents).Holds
}

func (ledger Ledger) GrantsScope(approvalScope string) bool {
	trimmedScope := strings.TrimSpace(approvalScope)
	return trimmedScope != "" && ledger.grantedScopes[trimmedScope]
}

func holdFromEvent(taskEvent agentcontract.TaskEvent) Hold {
	record := decode[openedBody](taskEvent.Body)
	record.ToolName = toolcontract.CanonicalToolName(record.ToolName)
	return Hold{ID: record.HoldID, Call: record.HeldCall, Choices: record.Choices, State: StatePending}
}

func update(holds []Hold, holdID string, change func(*Hold)) {
	for index := range holds {
		if holds[index].ID == holdID {
			change(&holds[index])
			return
		}
	}
}

func (held *Hold) decide(decision string) {
	if held.State != StatePending {
		return
	}
	switch decision {
	case DecisionApprove:
		held.State = StateApproved
	case DecisionReject:
		held.State = StateRejected
	case DecisionDefer:
		held.State = StateDeferred
	}
}

func (held *Hold) spend() {
	if held.State == StatePending || held.State == StateApproved {
		held.State = StateSpent
	}
}

func pendingHoldByID(taskEvents []agentcontract.TaskEvent, holdID string) (Hold, bool) {
	for _, held := range Holds(taskEvents) {
		if held.ID == holdID && held.State == StatePending {
			return held, true
		}
	}
	return Hold{}, false
}

func LatestHold(holds []Hold, state State) (Hold, bool) {
	for index := len(holds) - 1; index >= 0; index-- {
		if holds[index].State == state {
			return holds[index], true
		}
	}
	return Hold{}, false
}

func ApprovedHoldForCall(holds []Hold, toolName string, toolInput json.RawMessage) (Hold, bool) {
	for _, held := range holds {
		if held.State == StateApproved && held.answersCall(toolName, toolInput) {
			return held, true
		}
	}
	return Hold{}, false
}

func (held Hold) answersCall(toolName string, toolInput json.RawMessage) bool {
	canonicalInput := agentcontract.CanonicalToolInput(toolInput)
	if canonicalInput != agentcontract.CanonicalToolInput(held.Call.ToolInput) {
		return false
	}
	heldToolName := strings.TrimSpace(held.Call.ToolName)
	if heldToolName != "" {
		return heldToolName == toolcontract.CanonicalToolName(toolName)
	}
	return hasArguments(canonicalInput)
}

func hasArguments(canonicalInput string) bool {
	return canonicalInput != "{}" && canonicalInput != "null"
}

func marshal(value any) string {
	document, errorValue := json.Marshal(value)
	if errorValue != nil {
		return ""
	}
	return string(document)
}

func decode[Body any](body string) Body {
	var decoded Body
	json.Unmarshal([]byte(body), &decoded)
	return decoded
}
