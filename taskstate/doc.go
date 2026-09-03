// Package taskstate is the service over the durable record of work.
//
// The record itself belongs to agentcontract: TaskRun, TaskStatus, TaskAttempt,
// TaskEvent and the ledger event names are contract data, so a reader can name
// them without depending on where they are stored. taskstate creates runs,
// advances them through their nine statuses, and appends to the event ledger.
//
// Event names follow a fixed grammar — tool.<name>.requested, tool.<name>.result,
// approval.pending_call, approval.executed — so a reader can reconstruct a run
// without access to any harness's internal types.
//
// The ledger is what makes a task survive its process. A run is resumed by
// re-driving a turn from what the ledger says, never by attaching to a live one,
// which is why a task can wait days for an approval and continue afterwards.
package taskstate
