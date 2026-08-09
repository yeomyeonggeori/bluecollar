<img src="assets/bluecollar.logo.svg" alt="bluecollar" width="112">

# bluecollar

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

It owns no tools, no identity and no storage. A host hands it a tool set and a task store and it
runs the turn, so the same loop runs behind a chat connector on a server or in a terminal in front
of you.

It is built for work nobody is watching. A request arrives from someone else, the person who sent it
goes back to their day, and the answer has to be right without anyone checking. So the loop carries
what an interactive coding agent has no use for:

- an outcome contract agreed before work starts
- a completion gate that will not take the model's word that it is done
- approval as a state a task can sit in for days and resume from
- a tier ladder that picks the model from the difficulty of the work
- failure text written for the person who asked, not for a log

It is heavier than an interactive loop. For sitting beside a developer and fixing code as they
watch, a coding agent is the better tool.

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
