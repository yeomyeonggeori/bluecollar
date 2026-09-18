package agentcontract

type AddressingTarget string

const (
	AddressingTargetBot     AddressingTarget = "bot"
	AddressingTargetHuman   AddressingTarget = "human"
	AddressingTargetAnyone  AddressingTarget = "anyone"
	AddressingTargetNone    AddressingTarget = "none"
	AddressingTargetUnclear AddressingTarget = "unclear"
)

type AddressingDecision struct {
	Target         AddressingTarget
	ShouldRespond  bool
	ReactionEmoji  string
	DutyMatch      bool
	DutyName       string
	DutyConfidence float64
}
