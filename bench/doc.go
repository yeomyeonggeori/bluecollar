// Package bench measures what a harness costs to finish a task.
//
// An agent is a model and a harness together, so a benchmark score alone says
// nothing about which half earned it. Holding the model, the task and the
// verifier fixed and swapping only the harness leaves the difference on the
// harness, and that difference shows up as tokens sent per turn, turns taken,
// tools called and money spent — not only as pass or fail.
//
// RunMetrics is derived from the event ledger a turn already writes, so a run
// measured here is the same run that ran, not an instrumented copy of it. What
// the ledger cannot know is whether the work was correct: that verdict belongs
// to whichever benchmark posed the task, and it arrives through Verdict.
package bench
