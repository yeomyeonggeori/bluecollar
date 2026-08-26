<img src="assets/bluecollar.logo.svg" alt="bluecollar" width="112">

# bluecollar

*An agent harness that does the work, keeps a record, and tells you when it can't.*

[![check](https://github.com/yeomyeonggeori/bluecollar/actions/workflows/check.yml/badge.svg)](https://github.com/yeomyeonggeori/bluecollar/actions/workflows/check.yml)
[![Go Reference](https://pkg.go.dev/badge/github.com/yeomyeonggeori/bluecollar.svg)](https://pkg.go.dev/github.com/yeomyeonggeori/bluecollar)
[![Go](https://img.shields.io/badge/go-1.26-00ADD8?logo=go&logoColor=white)](go.mod)
[![License](https://img.shields.io/badge/license-Apache--2.0-blue)](LICENSE)

> **Status: pre-alpha, under active development.** The exported API, the
> contract types and the event names all still change without notice, and there
> is no release, no versioning policy and no migration path between commits. It
> is published so the design can be read and argued with, not so it can be
> depended on. If you import it, pin a commit and expect to read diffs.

bluecollar is a headless, embeddable agent harness for unattended work: the loop that takes a
request, decides what to do, calls tools, and answers.

The name is the design. This loop works the way a good tradesperson works: it takes the job, does
it, and answers to whoever asked. It can prove what it finished, because every step went into a
ledger as it happened. When it cannot finish, it says so, to the person who asked, in their
language, with what it tried. And when a job would cost more than it is worth, it puts the tools
down and says that too, instead of looking busy for another hour on someone else's money.

It owns no tools, no identity and no storage. A host hands it a tool set and a task store and it
runs the turn, so the same loop runs behind a chat connector on a server or in a terminal in front
of you.

It is built for work nobody is watching. A request arrives from someone else, the person who sent it
goes back to their day, and the answer has to be right without anyone checking — or the failure has
to be reported like one. So the loop carries what an interactive coding agent has no use for:

- an outcome contract agreed before work starts
- a completion gate that will not take the model's word that it is done
- a clock derived from what a step measurably costs, so a slow model gets a longer shift and a
  hopeless task gets a plain stop
- approval as a state a task can sit in for days and resume from
- a tier ladder that picks the model from the difficulty of the work
- failure text written for the person who asked, not for a log

We measure all of this against a leaner open-source loop: same model, same tasks, same verifier,
only the harness swapped, and every number published, the rows we lose included. Pass rates land
close enough that the columns trade places between runs; one task class we still lose outright, and
[the write-up](./bench/terminalbench/README.md) names it. Where the two never trade places is
failure: across our failed runs the requester was told in about half of them, and across the other
loop's failed runs the requester was told in none — every one ended as a confident wrong answer.
For work nobody is watching, that column is the product.

It is heavier than an interactive loop. For sitting beside a developer and fixing code as they
watch, a coding agent is the better tool, and it will not pretend otherwise.

## The shape

```
host  ──── agentcontract.Harness ────  bluecollar
  │                                        │
  │ owns: tools, identity, task store,     │ owns: the turn loop, skills,
  │       routing, approvals, isolation    │       completion judgment
  │                                        │
  └──────── executes every tool call ──────┘
```

The host and the harness compile against one shared contract package,
[`agentcontract`](./agentcontract). A different harness — an AI SDK adapter, an external agent —
drops into the same socket. The loop itself is [`loop`](./loop); the root of the
repository holds the contract packages, the commands, and nothing else.

The port is one method:

```go
type Harness interface {
	RunTurn(context.Context, AgentTurnRequest) (AgentTurnResult, error)
}
```

It used to be nine. Routing, addressing, follow-up classification and one-shot replies were verbs on
the port until it became clear where they belong: a host that answers its own messenger decides
what an inbound message *means* before anything runs a turn, so the decision is its. Those
still live here — [`intake.Classifier`](./intake) routes and classifies, `AgentKernel` carries
`RunAgentRequest` and `CompleteLaunchFailure` — but a host is free to bring its own, and a harness
that implements only `RunTurn` is complete.

Tool execution never happens here. The harness decides *what* to call; the host decides *who* it runs
as. A harness that runs its own tools defeats the host's isolation boundary and is not a valid
implementation of this contract.

The harness has no identity of its own. The host supplies `AgentIdentity`, the workspace layout, the
instruction bundle and the company context; with none given, the agent is "the assistant" and knows
nothing about where it runs.

Delegation is the loop's, for the same reason. A `delegate` action hands one self-contained piece
of a task to a fresh turn carrying the same identity and the same tool set, with its own outcome
contract and completion gate, and reports back as an observation. The host still executes every tool
call, so a child reaches nothing its parent could not. It is off unless a host sets
`TurnOptions.DelegationLimit`, and off costs a turn nothing: no action variant, no paragraph.

## The machinery

Each of the bullets above is a mechanism with a sharp edge. This section is for reading them the
way an engineer reads them.

**Completion is proven, never claimed.** The model has a `finish` action, and taking it starts an
argument instead of ending the turn. A deterministic gate checks the recorded facts first: every
tool the outcome contract requires has a successful call in the ledger, side-effect evidence
exists where the contract demands it, promised artifacts validate. Only a turn that passes the
gate reaches the completion judge: a second model call that reads the ledger, expands the
observations it names as evidence, and can reject with reasons the loop must answer. Rejections
are remembered across attempts, so a turn cannot farm the judge by re-finishing until the dice
land well. When the judge itself is unavailable, the event says so; the loop degrades loudly and
in writing.

**The clock is measured, and it follows the model.** Every iteration's wall time is sampled per
model; the task budget is the tier's step count times the measured median times a margin, floored
and capped by plausible per-step costs rather than by a constant. A slow model gets a longer
shift because the arithmetic says so. Budgets refresh monotonically as samples accumulate, a
raised wall re-derives the working context's deadline, and the single free tier escalation fires
from whichever limit arrives first — step overflow, the elapsed wall, or a deadline that expires
mid-call — and spends exactly once. A wall the host set explicitly is the host's number and is
never escalated away.

**Progress is an output the model has not read before.** The stall watchdog does not count tool
calls; it counts novel results. A call whose output is byte-identical to one already in the
ledger adds nothing, whoever made it and however the arguments differed, and three consecutive
nothings stop the run. There is no hand-kept list of which tools count: the rule derives it.

**Failure is a budget, and each class has its own.** A failed call opens failure debt that the
loop must either pay or report. Recovery spends from typed allowances (corrected retry,
alternate route, adjacent tool, no-tool fallback), and a failure signature that repeats three
times is structural: that route closes, whatever the remaining budget says. What survives to the
requester is written by the model for the person who asked, carrying what was tried; the raw
error goes to the ledger, where raw things belong.

**The transport carries everything the model produces.** Native tool calling with parallel calls,
and the first sample of every step runs with `tool_choice: auto`: a text-only response is the
model thinking, and the loop replays it as the model's own turn before asking again with
`required`: typed actions without forbidding thought. The provider decodes `reasoning_content`
and replays it in the field it arrived in, so a reasoning model keeps its working memory across
steps. Tool schemas stay provider-portable: string enums, no `$ref`, no numeric-enum tricks that
one endpoint accepts and the next rejects.

**Everything above is a ledger read.** Every model call, tool call, decision, grant, rejection
and failure is an append-only event with its full body. The completion gate reads it, the judge
reads it, the bench measures from it, `--trace` renders it, and a postmortem replays it. There is
no second bookkeeping to disagree with the first.

None of this is free: the loop pays one model call to judge completion and some prompt bytes to
carry contracts. That price buys the property the whole design exists for: when this loop says
done, the ledger can prove it, and when it says it could not, that sentence was earned.

## Provider-agnostic

Models reach bluecollar through a provider port. Anything satisfying it works, and
the provider can change between steps of a running turn. The tier ladder relies on that: it escalates
a task from a cheap model to a strong one without restarting it.

`model/openaicompatible` is the one implementation shipped here, so the module runs against Ollama,
vLLM, OpenRouter or anything else speaking `/chat/completions`. Hosts that need routing, tiering or
usage accounting bring their own; the reference is an [AI SDK](https://ai-sdk.dev) sidecar in
[blueclaw](https://github.com/yeomyeonggeori/blueclaw).

## Running it

`cmd/bluecollar` runs one turn against a local model and prints the ledger to stderr, which is the
shortest way to see the loop work before embedding it. It brings a shell scoped to `--workspace`,
so the same command is what an external benchmark drives; `--without-tools` takes the shell away
again when you only want to watch the loop reason.

```bash
ollama serve &
go run ./cmd/bluecollar --model qwen3:4b "In one sentence, what is a POSIX user?"
```

```
task.created  In one sentence, what is a POSIX user?
task.running  assistant
agent.instructions_loaded  {"activeGoal":{"outcomeContract":{"artifactRequirement":"none"…
llm.call  {"kind":"structured","schemaName":"bluecollar_agent_turn_action","model":"qwen3:4b"…
agent.action  {"action":"finish"…
task.completed
```

Every step is a ledger entry, which is the point of reading it: the same events appear whether the
turn calls fifty tools or none.

`--record-tape <path>` writes every model request and answer of the turn, and `--replay-tape <path>`
answers from that file with no endpoint at all. A tape is for two things: giving the loop's own
guarantees real inputs, including the malformed ones nobody would hand-write, and walking a run that
went wrong again for nothing. It is never evidence that the agent works. That is measured live, by
[`bench`](./bench), against the benchmark's own verifier, and a tape that no longer answers the calls
the loop makes fails loudly instead of pretending.

`--trace <path>` writes the same run as one file instead of scrollback: the request, the reply, what
it cost, and every ledger entry in order. A path ending in `.json` gets JSON and any other path gets
Markdown, both from one snapshot. The file keeps whatever the task carried, so read it before
sending it anywhere.

## What it promises

The loop's guarantees are written as tests, so the names are the specification.

<img src="assets/guarantees.png" alt="go test output: approval continuation restores the selected tool decision, launch failure redacts the raw error, a waiting task resumes without flags, a checkpoint does not lose the work it absorbed, checkpoint bookkeeping never reaches the model" width="100%">

```bash
go test -run 'Checkpoint|Resume|Approval' -v .
```

## What is not here yet

- A tool set worth the name in the standalone runner. `cmd/bluecollar` brings one shell, which is
  enough to be put on a terminal benchmark and no more; anything richer belongs to a host.
  `cmd/bluecollar-acp` takes its tool set from the MCP servers the host names when it opens a
  session, so a host that publishes a catalog gives the loop everything it can do. The event
  ledger reaches that host on `session/update`: tool calls as the standard variants a generic
  client renders, every event's name and body in the `_meta` a co-designed one reads. A host that
  kept those records hands them back in `PromptRequest._meta`, and the turn resumes on the work
  they describe.
- An interactive terminal front end, and none is planned here. `cmd/bluecollar` prints a ledger and
  exits; the interface belongs to whichever host embeds the loop.
- Native multi-step tool calling. The loop currently forces one structured action per step, which
  costs a turn per tool call and blocks parallel calls. Migration is planned and staged.
- Mid-turn steering over ACP. A host embedding the loop injects a `task.steer.requested` event
  and the turn picks it up on its next step. The protocol has no construct for that: a second
  `session/prompt` cancels the first, and `session/cancel` is the only client-to-agent message
  during a turn. Until one is designed, a steer reaches only an in-process host.
## Measuring the harness

An agent is a model and a harness together, so a benchmark score alone does not
say which half earned it. [`bench`](./bench) exists to attribute the difference:
hold the model, the task and the verifier fixed, swap only the harness, and what
moves is the harness.

The numbers come out of the event ledger a turn already writes, so a measured
run is the run that ran.

| what it reports | why it is there |
|---|---|
| prompt tokens per turn | what a harness puts in front of the model is the thing a pass rate hides |
| turns, tool calls, failed tool calls | how much work it took to get there |
| approval holds | held calls are the point of an unattended harness, so they are counted apart from failures |
| recovery attempts | whether it got itself out of trouble or thrashed |
| cost per **passed** task | a harness that burns money failing is not the cheap one |
| wall clock, model latency | how much of the wait was the model and how much was the loop |

A verdict is never inferred here. `Runner` drives tasks through
`agentcontract.Harness` and asks the benchmark's own verifier. A task nobody
checked stays `unverified`, and a verifier that cannot decide counts against
the harness.

Because the port is the only thing `Runner` needs, any harness that implements
`RunTurn` goes on the same row — including the ones this one is measured
against, once an adapter speaks for them.

## Building and testing

The module depends on one library and nothing outside its own directory. The ACP
agent is a second module under [`cmd/bluecollar-acp`](./cmd/bluecollar-acp), so
the protocol adapter's dependencies stay out of the graph of anything that
embeds the loop.

```
go build ./...
go test ./...
```

Every check that runs in CI is in [`.github/workflows/check.yml`](./.github/workflows/check.yml):
`gofmt`, `go vet`, `go build`, `go test`. No network, no credentials, no database.

## Contributing

Pull requests open at alpha. Until then the design is moving too fast for
outside patches to be a kindness to whoever sends them. Issues are welcome now —
a decision you disagree with is the most useful one.

## License

Apache License 2.0. See [LICENSE](./LICENSE).
