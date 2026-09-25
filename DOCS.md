# Overview

bluecollar is an embeddable agent harness written in Go. It is the loop that takes a request, decides what to do, asks a host to run tools, and answers. It is built for unattended work: someone sends a request and goes back to their day, so the answer has to be right without anyone checking, and a failure has to be reported as one.

## What it does

- It proves completion from a ledger. Every model call, tool call, decision and rejection is appended to a task's event ledger as it happens. A reply that claims the work is finished passes a deterministic completion gate that reads that ledger, and then a check that every change the request asked for is among the changes the ledger recorded.
- It sizes the work before doing it. Intake decides what an inbound message means and how hard it is. The difficulty tier sets the model, the number of steps, the number of tool calls and the clock.
- It reports failure to the person who asked. A failed call opens failure debt that the loop has to repair or report. The report is written by the model, in the requester's language, from the recorded attempts.
- It owns no tools, identity or storage. A host hands it a tool set and a task store and calls `RunTurn`. Every tool call executes back in the host, as whoever asked for the work.

## What it is not

It is heavier than an interactive coding agent. For sitting beside a developer while they watch, a coding agent is the better tool. It is also pre-alpha: the exported API, the contract types and the event names change without notice, there is no release and no versioning policy, and anyone importing it should pin a commit.

## Where to go next

- [Quickstart](#quickstart) runs the command-line runner and embeds the loop in a few lines.
- [Architecture](#architecture) draws the line between the host and the harness.
- [Concepts](#concepts) follows a request from intake to the last word.
- [Contract](#contract) lists the types a host implements or fills in.

# Quickstart

### Build

```bash
go build ./...
go test ./...
```

The module needs Go 1.26 and depends on `github.com/google/jsonschema-go` and `github.com/ergochat/readline`. The ACP agent in `cmd/bluecollar-acp` is a second module, so its protocol dependencies stay out of anything that embeds the loop.

### Serve a model locally

```bash
OLLAMA_CONTEXT_LENGTH=32768 ollama serve &
ollama pull qwen3.5:4b
```

Ollama loads a model with a 4096-token window unless told otherwise. The loop's instructions and a reasoning model's thinking do not fit in that, and the turn fails with finish reason `length`.

### Run the command-line runner

```bash
go run ./cmd/bluecollar --model qwen3.5:4b "In one sentence, what is a POSIX user?"
```

`cmd/bluecollar` talks to any OpenAI-compatible endpoint (`--endpoint`, default `http://127.0.0.1:11434/v1`) and prints the ledger to stderr as the turn runs. It registers `bash`, `file_read`, `write`, `edit`, `image_read` and `plan` scoped to `--workspace`, plus `equip` when a decision model is configured. With a prompt it runs one turn and exits; with no arguments and a terminal it becomes a conversation.

| flag | effect |
| --- | --- |
| `--model` | the model to ask, or `BLUECOLLAR_MODEL` |
| `--endpoint`, `--api-key` | the chat completions endpoint and its bearer token |
| `--workspace` | the directory shell commands and file tools work in |
| `--exec-prefix` | a wrapper for every shell command, such as `docker exec -i <container>` |
| `--without-tools` | no tools at all, to watch the loop reason |
| `--without-intake` | skip intake and start a `low` task directly |
| `--timeout` | how long one turn may run, default five minutes |
| `--trace` | write the run as one file, JSON when the path ends in `.json` and Markdown otherwise |
| `--metrics` | write what the turn cost as `bench.RunMetrics` JSON |
| `--record-tape`, `--replay-tape` | record every model request and answer, or answer from a recording with no endpoint |

Intake needs a decision model, read from `BLUECOLLAR_DECISION_ENDPOINT`, `BLUECOLLAR_DECISION_API_KEY` and `BLUECOLLAR_DECISION_MODEL`. Without one the runner says so on stderr and starts the task anyway. A trace keeps everything the task carried, so read it before sending it anywhere. A tape is for replaying a run that went wrong; it is never evidence that the agent works.

### Embed the loop

`examples/clock` gives the model one tool and asks it the time in Paris.

```bash
go run ./examples/clock
# The current time in Paris is Thursday, September 24, 2026 at 2:42 AM.
```

```go
type clockInput struct {
	TimeZone string `json:"timeZone"`
}

type clockOutput struct {
	Time string `json:"time"`
}

func main() {
	ctx := context.Background()

	kernel := loop.NewAgentKernel(taskstate.NewTaskRunService(taskstate.NewTaskEventService()), taskstate.NewTaskStepService())
	kernel.UseLanguageModelProvider(openaicompatible.NewProvider("http://127.0.0.1:11434/v1", "", "qwen3.5:4b"))

	tools := toolcontract.NewToolSet(nil)
	toolcontract.RegisterToolFunction(tools, toolcontract.ToolFunction[clockInput, clockOutput]{
		Definition: toolcontract.ToolDefinition{
			Name:            "time_get",
			Description:     "Get the current date and time in one time zone.",
			Visibility:      toolcontract.ToolVisibilityModel,
			SideEffectClass: toolcontract.ToolSideEffectRead,
			InputSchema: json.RawMessage(`{"type":"object","additionalProperties":false,"required":["timeZone"],
				"properties":{"timeZone":{"type":"string","description":"an IANA time zone, such as Europe/Paris"}}}`),
			ResultContract: &toolcontract.ToolResultContract{Schema: json.RawMessage(`{"type":"object","additionalProperties":false,"required":["time"],
				"properties":{"time":{"type":"string"}}}`)},
		},
		Handler: func(_ context.Context, input clockInput) (clockOutput, error) {
			location, errorValue := time.LoadLocation(input.TimeZone)
			if errorValue != nil {
				return clockOutput{}, errorValue
			}
			return clockOutput{Time: time.Now().In(location).Format("Monday 2 January 2006, 15:04")}, nil
		},
	})

	startTask := agentcontract.TurnDecision{
		Route:             agentcontract.TurnRouteStartTask,
		Classification:    agentcontract.IntakeClassificationBoundedTask,
		TaskShape:         agentcontract.TaskShapeMaintenanceTask,
		TaskLevel:         agentcontract.TaskLevelLow,
		InitialToolNames:  []string{"time_get"},
		ExpectedToolCount: agentcontract.ExpectedToolCountOne,
	}
	result, errorValue := kernel.RunTurn(ctx, agentcontract.AgentTurnRequest{
		RequesterPersonID:       "person-1",
		RequesterName:           "Alex",
		ConversationID:          "conversation-1",
		Prompt:                  "What time is it in Paris right now?",
		ToolSet:                 tools,
		PrecomputedTurnDecision: &startTask,
	})
	if errorValue != nil {
		log.Fatal(errorValue)
	}
	fmt.Println(result.FinishMessage)
}
```

- A tool reaches the model only when its descriptor is `visible` and carries a `ResultContract`, and the result is a JSON object.
- `RunTurn` refuses a turn without `PrecomputedTurnDecision`. The example fills one in by hand, naming the tool the work needs; the next section has intake decide it.
- The `taskstate` services keep everything in memory until a host that needs durability gives each one a repository through `UseRepository`.

### Route, then run

```go
request := agentcontract.AgentTurnRequest{
	RequesterPersonID: "person-1",
	RequesterName:     "Alex",
	ConversationID:    "conversation-1",
	Prompt:            "What time is it in Paris right now?",
	ToolSet:           tools,
}
planner := intake.NewDecisionPlanner(decisions.ConfiguredDecisionModel(os.Stderr), nil, nil)
router := intake.NewTurnRouter(openaicompatible.NewProvider("http://127.0.0.1:11434/v1", "", "qwen3.5:4b"), planner, agentcontract.IntakeOptions{IsEnabled: true})
decision, errorValue := router.Plan(ctx, agentcontract.AgentRequest{
	RequesterPersonID: request.RequesterPersonID,
	RequesterName:     request.RequesterName,
	ConversationID:    request.ConversationID,
	Prompt:            request.Prompt,
	ToolSet:           request.ToolSet,
})
if errorValue != nil {
	decision = agentcontract.TurnDecision{
		Route:            agentcontract.TurnRouteStartTask,
		Classification:   agentcontract.IntakeClassificationBoundedTask,
		TaskShape:        agentcontract.TaskShapeMaintenanceTask,
		TaskLevel:        agentcontract.TaskLevelLow,
		InitialToolNames: []string{"time_get"},
	}
}
request.PrecomputedTurnDecision = &decision

result, errorValue := kernel.RunTurn(ctx, request)
```

`RunTurn` fails a turn that arrives without `PrecomputedTurnDecision`: the host routes before it hands a turn to the harness. `result.TaskRun.Status` is where the task ended and `result.FinishMessage` is what the requester reads.

# Architecture

A host and a harness compile against one contract package. The host decides who a tool call runs as; the harness decides which call to make.

```
host  ──── agentcontract.Harness ────  bluecollar
  │                                        │
  │ owns: tools, identity, task store,     │ owns: the turn loop, skills,
  │       routing, approvals, isolation    │       completion judgment
  │                                        │
  └──────── executes every tool call ──────┘
```

### The port

```go
type Harness interface {
	RunTurn(context.Context, AgentTurnRequest) (AgentTurnResult, error)
}
```

The port used to be nine methods. Routing, addressing, follow-up classification and one-shot replies moved off it once it was clear they are host policy: a host that answers its own messenger decides what an inbound message means before anything runs a turn. bluecollar still ships those pieces in `intake`, and a host is free to use them or bring its own. A harness that implements only `RunTurn` is complete.

### Who owns what

| layer | owns |
| --- | --- |
| host | connectors and messengers, tool execution and its isolation boundary, the task store, approvals, the agent's identity, the workspace layout, company context |
| `agentcontract`, `toolcontract`, `model`, `taskstate` | the vocabulary both sides speak: requests and results, tool descriptors and results, model ports, task runs and ledger events |
| `loop` | the turn: action schema, plan, tool exposure, completion gate and change check, recovery, budgets, context building and compaction |
| `intake` | what a message means: route, addressing, follow-up, level, likely tools |

A harness that executes its own tools defeats the host's isolation boundary and is not a valid implementation of this contract. With no identity supplied the agent calls itself "the assistant" and knows nothing about where it runs.

[blueclaw](https://github.com/yeomyeonggeori/blueclaw) is one host. It projects each requester to a POSIX user and runs every tool call as that user, so the permission boundary is the operating system's. `cmd/bluecollar` and `cmd/bluecollar-acp` are two more, small enough to read in one sitting.

### Packages

| path | holds |
| --- | --- |
| `agentcontract/` | the harness port, turn requests and results, task runs, statuses and event names |
| `toolcontract/` | tool descriptors, tool sets, results, the kernel tool names |
| `model/` | the language model and decision model ports; `openaicompatible`, `decisions` and `tape` implement them |
| `loop/` | the agent loop, `AgentKernel` and `AgentTurnRunner` |
| `intake/` | the turn router and the decision planner |
| `taskstate/` | the in-memory services over task runs, steps, events and artifacts |
| `turnstream/` | a view of a turn's ledger events as they are appended |
| `trace/` | one run's ledger rendered as a single JSON or Markdown file |
| `bench/` | run metrics and a runner that measures any `Harness` |
| `cmd/bluecollar/` | the command-line runner |
| `cmd/bluecollar-acp/` | the loop as an Agent Client Protocol agent, in its own module |

### ACP

`cmd/bluecollar-acp` runs the loop as an [Agent Client Protocol](https://agentclientprotocol.com) agent. It owns no tools: the tool catalog arrives on the MCP servers the host names when it opens a session, and a tool's `blueclaw/sideEffectClass` and `blueclaw/approvalScope` metadata become its descriptor. Ledger events go out on `session/update`, tool calls as the standard variants and every event's name and body in `_meta`. A host that kept those records hands them back in the prompt's `_meta`, and the turn resumes on the work they describe. A steer injected mid-turn reaches only an in-process host, because the protocol has no message for it during a turn.

# Concepts

These pages follow one request through the loop. The names match the code, so each page can be read beside the package it describes.

## Intake

Decides what an inbound message means before a turn runs.

`intake.DecisionPlanner` asks every closed question about a message in one call to a decision model. Each question is a `choice` among named options or a `noul`, a probability that a statement is true. The questions cover the route, the difficulty level, the expected number of tools, whether independent work is present, whether an external send is requested, the task shape, the deliverable kind, the language the message is mainly written in (asked only when the host names none), requested output formats and, when relevant, addressing, the reply to a pending choice or confirmation, and whether the message continues a running task. The chat model is asked only for words.

`intake.TurnRouter` turns those answers into a `TurnDecision`. The route is one of:

| route | meaning |
| --- | --- |
| `start_task` | work that takes tools and time |
| `continue_task` | add to, or approve, work already running |
| `revise_task` | redirect running work toward a changed target or scope |
| `answer_question` | answer in words now |
| `answer_meta` | answer a question about the agent itself |
| `clarify` | ask the one thing only the sender can resolve |
| `consume` | acknowledge with an emoji and say nothing |
| `give_up` | the request is impossible or plainly improper on its face |

Intake chooses the level from `low`, `medium` and `high`. A request for slides, a site prototype or another visual deliverable is raised to `xhigh`. When the route starts, continues or revises work, one chat call writes the expected results: what should exist when the work is done and which tool result, file or link proves it. The other routes that need words (`clarify`, `answer_question`, `answer_meta`, `give_up`) get them from the same kind of call.

## Plan

Records the goal, its steps and the task's level, and settles which tools the model sees for each step.

`plan` is a kernel tool whose input is a goal, a list of steps with statuses, and a `level`. A level above the current one widens the budget to that level's profile; a plan never narrows it. Tasks at `medium` and above are expected to plan, and a state-changing call made before any plan exists gets one nudge to record one.

When the step marked `in_progress` changes, the loop asks the tool selector for the tools that step needs, capped at `toolcontract.ToolNamesOnePlanStepIsExpectedToNeed` (five). Every iteration inside one step then sends a byte-identical system instruction, tool catalog and unchanging context, which `loop/step_tool_selection_test.go` asserts.

## Tools and the kernel

The fixed tool names every host is expected to provide, plus whatever else the host registers.

The model's kernel is `read`, `write`, `edit`, `bash`, `plan` and `equip`. The first four are the work. `plan` sizes and steps the task. `equip` takes a sentence describing a need and answers with the tools that serve it, through the same `agentcontract.ToolSelector` used by intake, which `intake.DecisionPlanner` implements; the tools it names are pinned for the next turn.

The names are constants in `toolcontract/kernel_tools.go`. The descriptors behind them belong to the host, which marks a tool as kernel by setting its `ProviderID` to `kernel`. Kernel tools stay callable, and stay valid as required evidence, even when the host marks their availability denied; a command the actor may not run fails at execution. `toolcontract` also names tools the runtime calls on the model's behalf: `ask_input` and `file_deliver` run behind a reply, and `file_read`, `file_preview`, `file_delete`, `image_read`, `skill_search` and `conversation_history` are available to hosts that register them. Records written under the retired names `shell`, `file_write`, `file_edit` and `find_tools` are read as `bash`, `write`, `edit` and `equip`.

Beyond the kernel, each turn exposes at most `MaxExtensionCallableToolCount` (15) host tools, filled in order from recovery tools, the pending required tool, required evidence, pinned tools, selected skills and evidence alternatives.

## Actions

The four ways a model can end a step.

| action | effect |
| --- | --- |
| `continue` | call one tool with its input |
| `reply` | speak; `final` makes it the last word, `expectsAnswer` pauses on a question, `choices` offers buttons, `attachments` delivers files |
| `fail` | report a blocker; `message` is the reply the requester reads |
| `delegate` | hand a self-contained piece to a child turn, only when `TurnOptions.DelegationLimit` is set |

Actions are native tool calls, one function per action, with parallel calls enabled and `tool_choice` left unset on the first sample of each step. Assistant text that ends with `finish_reason: stop` is a proposed final reply and goes through the same gate. A malformed action comes back as a typed validation issue naming the field, and the step is asked again, at most twice. `finish`, `ask_input`, `file_deliver`, `request_tools` and `plan_update` are retired from the model's vocabulary, and `loop/model_facing_vocabulary_test.go` fails if any of them reaches a prompt.

A reply with attachments invokes `file_deliver` and a reply with `expectsAnswer` invokes `ask_input`, so both leave a tool observation the completion gate can read. A question pauses the task in `waiting_user_input` through the host's `ask_input`, and the answer rebuilds the turn from the ledger and its checkpoint, so a restart in between loses no work.

A delegated child runs with the same identity and tool set, its own outcome contract and completion gate, and no delegation of its own. The host still executes every call, so a child reaches nothing its parent could not.

## Outcome contract

What the task must produce, agreed before the work starts.

An `OutcomeContract` carries required evidence tools, groups of which any one will do, required attachment suffixes, required effects, expected results and an artifact requirement. It is built from intake's expected results, the selected skills and the tool descriptors, and it is reduced to the tools the turn can actually call.

A contract from an earlier task is a hypothesis. When a requester retries, the host supplies the previous task's recorded calls, failures and effects separately from its assistant-written report, and the current intake's expected results replace the old interpretation when present.

## Completion gate

The deterministic check a final reply has to pass before the task completes.

A final reply must claim `goalSatisfied`, report no remaining work, and cite the observations that did the work. The gate then checks the recorded facts: every required tool has a successful call, a required send has send evidence, a tool whose side-effect class changes something has a cited successful observation, and delivered attachments exist and validate. The gate decides by side-effect class, never by tool name.

When the turn starts, and while the work runs, one model call lists the changes the request asks for. Each entry is a kind taken from the effects the offered tools declare (`task deleted`, `event created`) and the request's own words that ask for it, copied exactly. A quote the request does not contain is asked for once more and then dropped, so a change the model invented has nothing to be checked against. A request that asks only for words expects no change and skips the check. The list is recorded as `completion.expected_changes`.

After the gate, an expected kind with no recorded effect of that kind is unmet without further judgment. For the rest, the decision model reads the changed records, each with its inputs and results in order, and the last two lookups in the same domain, and answers per change whether it was carried out; below 0.6 it is unmet. The verdict is `completion.change_check`, and an unmet change sends the loop back with the asked words and why. When the check cannot run, the ledger records `completion.check_degraded` and the gate's verdict stands.

After two refusals with nothing done in between, the loop withdraws the final reply from the action schema; after three it offers both the reply and `fail`.

## Recovery and fail

What happens after a tool call fails.

A failed call opens failure debt. While debt exists, the action schema offers `fail`, and a final reply has to say how the debt was resolved: `recovered_with_success`, or `no_tool_fallback` when the answer can be given without the failed tool. A fallback can waive a failed requirement only when that requirement needed no side-effect evidence and no attachment.

Recovery spends from typed allowances:

| allowance | default |
| --- | --- |
| corrected retry | 1 |
| alternate route | 1 |
| adjacent tool | 2 |
| no-tool fallback | 1 |

These are ceilings. The model can report a blocker immediately when the available tools cannot repair it. Another attempt needs evidence that it addresses the recorded cause, and a failure signature that repeats three times closes that route. A result-validation failure can arrive after a mutation, so an uncertain write calls for inspecting current state before writing again.

`fail` carries `usedFailureFacts` from the recorded attempts and a `message`. Once those facts validate, the message is the reply, delivered through the terminal report path. Runtime recovery guidance is a system message; only executed calls become tool-call and result pairs, so one failed call never looks like two.

Progress is counted in novel results: a successful call whose output does not repeat an earlier one, a new failure fingerprint, a delivered attachment. After three consecutive actions without one, the loop steers the model toward a suggested tool, a recovery route or the exit, up to four times, then stops the run.

## Budgets

How many steps, tool calls and minutes a task may spend.

The level picks a profile. The first working tier is the 95th percentile of measured successful runs, and each tier above doubles:

| level | steps | tool calls | clock before measurement |
| --- | --- | --- | --- |
| `xlow` | 4 | 1 | 1.4 min |
| `low` | 20 | 13 | 7 min |
| `medium` | 40 | 26 | 14 min |
| `high` | 80 | 52 | 28 min |
| `xhigh` | 160 | 104 | 56 min |
| `max` | 320 | 208 | 112 min |

The clock is steps × cost of one step × 2. Before any call is measured, one step is assumed to cost 200 ms plus 205 output tokens at 20 tokens a second, the slowest speed the product plans for. After that, it is the median wall time of the model in use over its last hundred calls, bounded below by one second and above by two minutes a step. A host that sets an explicit wall keeps it.

When the step, tool call or time limit arrives first, the loop finalizes if the recorded evidence already satisfies the contract. Otherwise, once per task and only when the budget came from the level, it extends to the next level's profile and records `agent.budget_extended_one_level`. After that it stops and reports how far it got. The model stays the one chosen when the task started.

A single model call that runs past twelve times the model's median is cancelled and reissued on the caller's deadline, and the ledger records the cut. Intake calls follow the same rule. A caller cancelling the turn is never retried.

## Context

What the model reads on each step, and what happens when it grows.

The system instruction is fixed at the start of the run. Each request carries the requester, the company, the visible conversation, memory facts, the active goal, the plan, a step budget and the observations so far, replayed as native tool calls and results.

A tool result larger than its share of the context has its middle elided. A host that registers a `ToolResultSpillStore` receives the full output and the model is told where to read it. The share comes from the declared context window.

Compaction starts when the estimated prompt passes 60 percent of the context window, or 96,000 tokens when no window is declared. Long tool results are pruned first. If that is not enough, older observations are summarized into a pinned `TaskContextSummary` (goal, completed steps, artifacts, key decisions, exhausted routes, active failure debt, next plan), keeping the ten most recent, and a new summary needs at least six new observations and 20,000 new characters.

## Model tiers

Which model answers a task.

`AgentKernel.UseTaskTierLanguageModels` takes one provider per level. The kernel picks the provider for the level intake decided, falling back to the provider set with `UseLanguageModelProvider`. Classification uses the `xlow` provider when there is one. Routing, tiering and usage accounting beyond that belong to the host.

## Decision model

The port every closed question goes through.

```go
type DecisionModel interface {
	Decide(context.Context, DecisionRequest) (DecisionResponse, error)
}
```

A request carries a state document and a map of named questions. Each answer carries the chosen option with its probabilities, or a `noul` value between 0 and 1. `model/decisions` implements it against a decisions endpoint configured by `BLUECOLLAR_DECISION_ENDPOINT`, `BLUECOLLAR_DECISION_API_KEY` and `BLUECOLLAR_DECISION_MODEL`. Tool selection splits a catalog too large for one request into byte-balanced batches that never ask about a tool twice.

## Ledger

The append-only record every other mechanism reads.

Each model call, tool call, decision, grant, rejection and failure is a `TaskEvent` with a name and its full body. Tool events follow `tool.<name>.requested` and `tool.<name>.result`; approvals use `approval.pending_call` and `approval.executed`. The completion gate and change check read the ledger, `bench` measures from it, `--trace` renders it, `turnstream` mirrors it, and a restart resumes from it. A task that waits days for an approval continues by re-driving a turn from what the ledger says.

# Contract

The types a host fills in or implements. They live in `agentcontract`, `toolcontract`, `model` and `taskstate`, so a host can depend on them without depending on the loop.

## AgentTurnRequest

Everything the harness refuses to assume about a turn.

| field | carries |
| --- | --- |
| `RequesterPersonID`, `RequesterName`, `RequesterCallingName`, `RequesterHandle`, `RequesterCircles` | who is asking |
| `AgentIdentity` | the name and handle the agent answers to |
| `Prompt`, `InputParts` | the message and its attachments |
| `ResponseLanguage` | the language of every reply, as a code or an English name from `toolcontract.ResponseLanguages`; empty lets intake read it from the message |
| `ConversationID`, `ConversationType`, `VisibleContext` | where it was said and what surrounds it |
| `MemoryFacts` | what the host recalled for this requester |
| `ToolSet`, `PinnedToolNames`, `LikelyToolNames`, `AvailableSkills` | what the agent may call and read |
| `WorkspaceRootPath`, `WorkspaceDefaultPath`, `ActivePaths` | where files live |
| `ActiveGoal`, `PriorTask`, `ScheduledRun`, `CarriedOutCalls` | work already in flight or on record |
| `PrecomputedTurnDecision` | the host's routing decision, required |
| `TurnStartedAt`, `ExecutionStartedAt`, `EnvironmentNow` | the clocks |
| `CheckpointSender` | where progress updates go |

`ExecutionStartedAt` lets a host start the execution budget after its own routing phase while the caller's deadline still caps the run. `AgentTurnResult` returns the task run and its status, the finish message or user notice, attachments and recovery actions.

## Tool descriptor

What a tool is, as the harness sees it.

A `ToolDescriptor` has a name, a description, optional `WhenToUse` and `WhenNotToUse` sentences, input and output schemas, a result contract, a visibility, a side-effect class, and approval, idempotency and timeout fields. The model reads the description followed by the two sentences; the negative one should name the tool that is correct instead.

| visibility | meaning |
| --- | --- |
| `visible` | offered to the model; also requires a `ResultContract` |
| `hidden` | never offered; the runtime may call it |
| `control` | a control surface outside the model's choices |

Input schemas sent to a model stay provider-portable: string enums, no `const`, and no `$ref` without its `$defs`. `loop/action_schema_portability_test.go` walks every native tool's parameters to enforce it. A tool that arrives through a `ToolProvider` is validated before it is registered: a model-visible tool needs a result contract, a model-visible tool that changes something needs an `InputIntentSchema`, and a provider that fails is quarantined.

## Side-effect class

What running a tool changes, which decides approval and completion evidence.

| class | needs side-effect evidence |
| --- | --- |
| `none`, `read`, `computation` | no |
| `state_change`, `workspace_write`, `external_write`, `external_send`, `external_publish`, `site_publish`, `platform_reply`, `local_file`, `connect`, `destructive`, `approval` | yes |

The loop reads the class from `SideEffectClass`, or from the recovery card's `SideEffect` when that is empty, and accepts `readonly`, `compute` and `write` style spellings. `RequiresApproval` and `ApprovalScope` let a host pause a call for a person's decision, so the decision comes from what a tool does.

## Tool result

What a tool hands back.

A `ToolResult` carries content, optional JSON data, attachments, resource effects and, on failure, a `ToolFailure` with a kind, a code, a stage and a summary safe to show a user. The kinds are `dependency_unavailable`, `permission_denied`, `invalid_input`, `not_found`, `rate_limited`, `external_service`, `interaction_required`, `policy_blocked` and `unknown`. A `ResultContract` can declare the effects a result proves (`ObjectType`, `Effect`, the result field holding the identity) and an `EvidenceCondition` naming the field and value that make it count, which is what lets the gate match a recorded effect to the contract.

## Memory facts

What a host recalled, rendered into the turn's context.

```go
type MemoryFact struct {
	FactID            string
	ScopeType         string
	NamespaceID       string
	Content           string
	Score             float64
	SourceEpisodeID   string
	SourceKind        string
	ValidAt           time.Time
	SecurityLevelRank int
	RequiredClasses   []string
}
```

`BuildMemoryContext` groups facts by scope (`user`, `circle`, `workspace`, `conversation`) under a "Relevant memory" heading, prefixes each with its score, kind, source and valid date, and cuts content at 240 characters. Filtering what a requester may see is the host's job before the facts arrive.

## Task state

The record of a task, and the services over it.

A `TaskRun` moves through nine statuses: `planned`, `running`, `waiting_user_input`, `waiting_approval`, `blocked`, `interrupted`, `completed`, `failed` and `cancelled`. `taskstate.TaskRunStore`, `TaskStepStore` and `TaskArtifactStore` are the interfaces the kernel needs. The shipped services keep everything in memory and write through to a repository set with `UseRepository`. A cancel can be registered against a running task so it arrives while the turn is running, and an interrupted run is resumed at most once.

## Model ports

How a language model reaches the harness.

```go
type LanguageModelProvider interface {
	GenerateResponse(context.Context, string) (string, error)
	GenerateStructuredResponse(context.Context, StructuredResponseRequest) (StructuredResponse, error)
}
```

`model/openaicompatible` implements it against any `/chat/completions` endpoint. It retries a transient failure up to three times with an exponential delay starting at one second and capped at 30 seconds, honoring `Retry-After`, and replays `reasoning_content` in the field it arrived in. `model/tape` records and replays a run. Hosts that need routing or accounting bring their own provider.

# Evaluation

An agent is a model and a harness together, so a benchmark score alone does not say which half earned it. `bench` holds the model, the task and the verifier fixed and swaps only the harness.

### Run metrics

`bench.RunMetrics` is derived from the event ledger a turn already writes, so a measured run is the run that ran. `bench.Runner` drives tasks through any `agentcontract.Harness` and asks the benchmark's own verifier; a task nobody checked stays `unverified`.

| what it reports | why |
| --- | --- |
| prompt tokens per turn | what a harness puts in front of the model is what a pass rate hides |
| cached prompt tokens | two harnesses can send the same bytes and pay very differently |
| turns, tool calls, failed tool calls | how much work it took |
| approval holds | held calls are counted apart from failures |
| recovery attempts | whether it recovered or thrashed |
| cost per passed task | a harness that burns money failing is not the cheap one |
| wall clock, model latency | how much of the wait was the model |

### Terminal-Bench

[`bench/terminalbench/README.md`](https://github.com/yeomyeonggeori/bluecollar/blob/main/bench/terminalbench/README.md) puts `cmd/bluecollar` on Terminal-Bench, reaching each task container through `docker exec`, and compares it with another open-source loop on the same model, tasks and verifier. It is a running log of every sweep. Its pass rates are not terminal-bench-core scores, because both harnesses run with raised timeouts.

The column the write-up keeps returning to is failure reporting: across 188 runs the grader failed, bluecollar told the requester it could not finish in 91; across 30 failed runs of the other loop, in none.

### Where the budgets come from

`bench/derive-budgets` reads the step and tool call distributions off runs that succeeded. The first working tier is their 95th percentile and each tier doubles, and `loop/task_level_profile_test.go` fails when the first tier drifts from that percentile. The measurement came from container coding tasks, and a thin sample of successes, so it is a starting point to re-derive from product runs.

### Prompt budget

`loop/testdata/prompt-budget.json` records what two fixture turns assemble, and `maximumTotalBytes` (16 KiB) is the ceiling. Any change that grows or shrinks what the model reads fails the test until the file is updated, so the difference lands in the diff a reviewer reads. Today the fixtures assemble 11.4 KB and 9.7 KB, and the largest item in each is the action schema, larger than the instruction and the tool catalog together. The test needs no credentials and calls no model.

### Guarantees as tests

The loop's guarantees are written as tests, so their names are the specification:

```bash
go test -run 'Checkpoint|Resume|Approval' -v ./loop
```

CI runs `gofmt`, `go vet`, `go build` and `go test`, then the same inside the ACP module, with no network, credentials or database. Live model evaluations sit behind the `llmeval` build tag and never run by default.

# Q&A

**Why does the host have to route?** Deciding what a message means depends on the messenger, the running tasks and who is asking, and those belong to the host. `intake` is there for a host that wants bluecollar's answer to that question.

**Why a change check after a deterministic gate?** The gate can check that a required call succeeded. It cannot read whether the calls did what was asked. The check compares the request's own words against the recorded changes, and it never reads the reply, so a confident reply cannot pass it.

**Why is the budget a ceiling on steps and not on tokens?** A step's cost is dominated by the round trip to the model, so steps and tool calls track the clock. Tokens price the cheaper half.

**Does the loop escalate to a stronger model when it runs out?** No. It extends the budget one level once, and the model chosen at the start stays.

**Can I run it without a decision model?** The loop runs; intake does not. `cmd/bluecollar` starts a `low` task when intake is unavailable, and a host can do the same by setting `PrecomputedTurnDecision` itself.
