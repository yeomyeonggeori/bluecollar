// Package intake plans a turn before the loop runs it.
//
// The decision planner asks every closed question about one message in a single
// call to a decision model: the route, how many tools the work will call, its
// task shape, level and deliverable, and which tools it is likely to need. The
// turn router turns those answers into a TurnDecision and has a chat model write
// only the words that decision needs.
//
// The facts come from the host: the prompt, the visible context, the active
// goal, the prior task and a scheduled run. Whether a message is for the agent
// at all, whether to react to it, and what to do with a task already running
// are the host's questions, and nothing here asks them.
package intake
