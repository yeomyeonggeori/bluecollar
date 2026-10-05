package approval

import (
	"encoding/json"
	"strings"

	"github.com/yeomyeonggeori/bluecollar/agentcontract"
	"github.com/yeomyeonggeori/bluecollar/toolcontract"
)

type holdState string

const (
	holdPending  holdState = "pending"
	holdApproved holdState = "approved"
	holdRejected holdState = "rejected"
	holdSpent    holdState = "spent"
)

const (
	decisionConfirm = "confirm"
	decisionCancel  = "cancel"
)

var stateAfterDecision = map[string]holdState{
	decisionConfirm: holdApproved,
	decisionCancel:  holdRejected,
}

type Hold struct {
	ID        string
	Call      agentcontract.HeldCall
	taskRunID string
	state     holdState
}

type decidedBody struct {
	HoldID   string `json:"approvalToken"`
	Decision string `json:"decision"`
	Source   string `json:"source"`
}

type spentBody struct {
	HoldID    string          `json:"approvalToken,omitempty"`
	ToolName  string          `json:"toolName"`
	ToolInput json.RawMessage `json:"toolInput,omitempty"`
}

type scopeGrantedBody struct {
	Scope string `json:"scope"`
}

type holdLedger struct {
	holds         []Hold
	grantedScopes map[string]bool
}

func holdLedgerOf(taskEvents []agentcontract.TaskEvent) holdLedger {
	state := holdLedger{grantedScopes: map[string]bool{}}
	for _, taskEvent := range taskEvents {
		switch taskEvent.Name {
		case agentcontract.TaskEventApprovalPendingCall:
			state.holds = append(state.holds, holdFromEvent(taskEvent))
		case agentcontract.TaskEventApprovalDecided:
			decided := decodeEventBody[decidedBody](taskEvent.Body)
			state.update(decided.HoldID, func(hold *Hold) { hold.decide(decided.Decision) })
		case agentcontract.TaskEventApprovalExecuted:
			state.update(decodeEventBody[spentBody](taskEvent.Body).HoldID, func(hold *Hold) { hold.spend() })
		case agentcontract.TaskEventApprovalScopeGranted:
			state.grantedScopes[strings.TrimSpace(decodeEventBody[scopeGrantedBody](taskEvent.Body).Scope)] = true
		}
	}
	return state
}

func holdFromEvent(taskEvent agentcontract.TaskEvent) Hold {
	call := decodeEventBody[agentcontract.HeldCall](taskEvent.Body)
	call.ToolName = toolcontract.CanonicalToolName(call.ToolName)
	if call.ApprovalToken == "" {
		call.ApprovalToken = taskEvent.TaskEventID
	}
	return Hold{ID: call.ApprovalToken, Call: call, taskRunID: taskEvent.TaskRunID, state: holdPending}
}

func (state holdLedger) update(holdID string, update func(*Hold)) {
	for index := range state.holds {
		if state.holds[index].ID == holdID {
			update(&state.holds[index])
			return
		}
	}
}

func (state holdLedger) grantsScope(approvalScope string) bool {
	return approvalScope != "" && state.grantedScopes[approvalScope]
}

func (state holdLedger) approvedHoldForCall(toolName string, toolInput json.RawMessage) (Hold, bool) {
	for _, hold := range state.holds {
		if hold.state == holdApproved && hold.isCall(toolName, toolInput) {
			return hold, true
		}
	}
	return Hold{}, false
}

func (hold *Hold) decide(decision string) {
	if state, isKnown := stateAfterDecision[decision]; isKnown && hold.state == holdPending {
		hold.state = state
	}
}

func (hold *Hold) spend() {
	if hold.state == holdPending || hold.state == holdApproved {
		hold.state = holdSpent
	}
}

func (hold Hold) isCall(toolName string, toolInput json.RawMessage) bool {
	return hold.Call.CanonicalCallKey() == agentcontract.CanonicalToolCallKey(toolName, toolInput)
}

func decodeEventBody[Body any](body string) Body {
	var decodedBody Body
	json.Unmarshal([]byte(body), &decodedBody)
	return decodedBody
}
