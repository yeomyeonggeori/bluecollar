// Package agentcontract is the contract a host and a harness compile against.
//
// The port is one method:
//
//	type Harness interface {
//		RunTurn(context.Context, AgentTurnRequest) (AgentTurnResult, error)
//	}
//
// It used to be nine. Routing, addressing, follow-up classification and one-shot
// replies were verbs on the port until it became clear they are host policy
// rather than harness behaviour, so a harness that implements only RunTurn is
// complete.
//
// AgentTurnRequest is how a host tells the harness everything the harness
// refuses to assume: who is asking, what identity the agent answers to, where
// the workspace is, which instructions and skills apply, and what the company
// is. AgentTurnResult carries the answer back with the task state it reached.
//
// That task state lives here as well: TaskRun and its nine statuses, TaskAttempt,
// TaskEvent, and the ledger event names every producer emits. They are contract
// data, so taskstate is the service over them and imports this package.
package agentcontract
