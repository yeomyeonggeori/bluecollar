// Package holdrecord is the contract for the approval hold events of a task
// run: their typed bodies, the writers that open, decide and spend a hold, and
// the one fold that reads a run's events back into holds. Every host and
// harness that holds a call for approval writes and reads through it.
package holdrecord
