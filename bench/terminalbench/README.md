# Putting this loop on Terminal-Bench

Terminal-Bench builds a container per task, hands an agent one instruction, and
runs its own test suite against what the agent left behind. The agent here is
`cmd/bluecollar`: the loop stays on the host and reaches the task through
`docker exec`, so the container is exactly the environment the benchmark made.

## Once

```bash
go build -o /usr/local/bin/bluecollar ./cmd/bluecollar
ollama serve &                 # or any OpenAI-compatible endpoint
ollama pull qwen3:4b
```

Terminal-Bench needs a running Docker daemon. `litellm` 1.95 fails to build on
a rustc older than its Rust bridge wants, so pin it:

```bash
uvx --from terminal-bench --with 'litellm==1.77.0' tb --help
```

## A run

```bash
uvx --from terminal-bench --with 'litellm==1.77.0' tb run \
  --dataset terminal-bench-core==0.1.1 \
  --agent-import-path bench.terminalbench.bluecollar_agent:BluecollarAgent \
  --model ollama/qwen3:4b \
  --task-id hello-world
```

`BLUECOLLAR_BINARY`, `BLUECOLLAR_MODEL_ENDPOINT` and `BLUECOLLAR_TIMEOUT_SECOND`
override the binary, the endpoint and the per-task budget.

Each task leaves `bluecollar-ledger.txt` and `bluecollar-metrics.json` in the
run's logging directory. The metrics file is `bench.RunMetrics`, so a suite's
worth of them summarises through `bench.SuiteReport` and lands on the same row
as any other harness measured the same way.

## Comparing against another harness

Hold the model, the dataset and the task list fixed, change only
`--agent-import-path`, and the difference is the harness. Reading a pass rate
alone hides the part that separates two harnesses on one model, which is what
each put in front of it — that figure is `promptTokensPerTurn`.

`pi_agent.py` puts [pi](https://github.com/earendil-works/pi) on the same row:

```bash
OPENAI_BASE_URL=http://host.docker.internal:11434/v1 OPENAI_API_KEY=ollama \
uvx --from terminal-bench --with 'litellm==1.77.0' tb run \
  --dataset terminal-bench-core==0.1.1 \
  --agent-import-path bench.terminalbench.pi_agent:PiAgent \
  --model qwen3:4b \
  --task-id hello-world
```

`run-comparison` raises both of Terminal-Bench's timeouts well above the task
definitions — `--global-agent-timeout-sec 3600` and `--global-test-timeout-sec
600`, from `BENCH_AGENT_TIMEOUT` and `BENCH_TEST_TIMEOUT`. Tasks ship with 600
and 60. The reason is that this row is asking whether the harness does the work,
and a task killed at its own clock answers a different question; the earlier
`test_timeout` rows came from a loaded machine rather than from anything either
agent did. Both harnesses get the same numbers, so the comparison holds — but a
pass rate from here is not a terminal-bench-core score and must not be reported
as one.

Raising them does not make the underlying behaviour correct. A budget derived
from measured iteration cost reached 979 seconds on `count-dataset-tokens`
against the task's own 600, so the loop planned to work longer than it was
allowed and was killed rather than finishing and reporting. The environment
knows that limit and the loop is never told it.

The two agents reach the model from different sides, and getting this wrong
silently measures a harness that never spoke to a model. bluecollar runs on the
host and reaches only the container's shell, so it uses the endpoint directly.
pi is installed inside the container, so it needs a base URL the container can
resolve: `host.docker.internal`, not `127.0.0.1`.

## What the rows have said so far

Three benchmarks, `google/gemini-3.1-flash-lite` on both harnesses, one
attempt each, small task samples. Run-to-run variance on this model is one to
two tasks out of eight, so nothing here separates the harnesses by less than
that.

| benchmark | tasks | bluecollar | pi |
|---|---|---|---|
| terminal-bench-core | 8 | 2 | 5 |
| quixbugs | 6 | 0 | 4 |
| aider-polyglot | 6 | 1 | 3 |

pi is ahead. One row reads differently underneath: on quixbugs every
functional test passed for all six of bluecollar's runs — it found and fixed
each bug — and all six failed only `test_one_line_change`, because it rewrote
the file instead of copying it and changing one line. pi solved four
outright. On finding the bug bluecollar was 6/6 against pi's 4/6; on minimal
diff discipline it was 0/6.

## The benchmark that measured the harness

The three rows above mostly measure the model. The same harness scored 2/8 on
terminal-bench with gemini-3.1-flash-lite and 3/8 with gpt-5.6-luna, and on
that stronger model it drew level with pi in a single run:

| benchmark | model | tasks | bluecollar | pi |
|---|---|---|---|---|
| terminal-bench-core | gpt-5.6-luna | 8 | 4 | 4 |

Level on verdicts, and on one task — grid-pattern-transform — bluecollar
solved what pi did not.

The first time this row was level, it was level on verdicts alone: the median
bluecollar run took 355 seconds and 44 turns against pi's 19 seconds, and
three of eight runs reached a proper end. Four defects were behind that, all
of them found by reading a ledger rather than by reasoning about the loop:

- The finalizer was rejected eighteen times on one task over an observation ID
  the runtime had written itself. It supplies the identifier now, after
  letting the model try once.
- The agent was told its workspace was the host directory the binary launched
  from while its shell ran inside a container. fix-git spent all hundred of
  its tool calls on sixty variations of pwd.
- The workspace path reached the runtime and stopped there, because the
  description carrying it was written to omit the concrete path.
- Four copies of the outcome contract disagreed, and the gates read the one
  that was rebuilt after the reduction ran.

The median run is now 18 turns. Two tasks still spend their whole tool budget:
fix-git and chess-best-move reach a hundred calls, and on fix-git a quarter of
them are finally git commands rather than pwd, which is progress and not a
fix.

AppWorld is the one that measured the harness rather than the model. It gives
the agent a supervisor's phone, contacts, venmo and file system apps and asks
for something like "message the family members who have no venmo account" —
long, stateful, across apps, verified against the apps' own databases. On the
first task bluecollar spent zero turns and called no tool, replying "tell me
the names or phone numbers of your parents and siblings". pi looked them up.

Same model, opposite behaviour: the intake planner listed the contacts as
missing information, which pauses a task before the loop runs, and the agent
never got to the instruction telling it to try before claiming it lacks data.
Fixing the planner's definition of missing took that task from zero turns to
sixteen. No coding benchmark surfaced this in a day of running; AppWorld did
in twenty minutes.

```bash
BENCH_DATASET=appworld-dev bench/terminalbench/run-comparison 0d8a4ee_1
```

A `Failed to activate server: 500` from the verification step is not an
infrastructure row, and reading it as one costs a whole run. The full dev set
returned that 500 on 52 of 57 tasks and scored bluecollar 0, which looked like
a broken benchmark until pi ran the same task and passed it. AppWorld's server
answers `/evaluate` with a 500 when the task was never completed on it, so the
500 *is* the failure, reported one layer away from where it happened.

What the run found, on `0d8a4ee_1`: the agent spent all 26 of its tool calls
re-reading help. `cli phone --help` returned `login`, `search_contacts` and
`send_text_message` — everything the task needed — and it returned that at
least eight times. The agent never called one of them. The command string was
different every time (`2>&1 | cat`, `sed -n '1,240p'`, `set -o pipefail`), so
nothing that compares commands would call this a repeat; the *output* was
byte-identical.

The runtime did notice something was wrong and said the wrong thing about it.
It raised limit pressure twice and ended on an exhausted budget, which tells an
agent it is running out of room, not that it has already been told this answer
eight times.

## Where the budgets come from

The iteration and tool call ceilings used to be chosen. They are now derived:

```bash
bench/derive-budgets /tmp/bench-luna6 /tmp/bench-quixbugs5 /tmp/bench-polyglot2
```

It reads the runs that succeeded and reports the distribution of what they
actually cost. Across 52 measured runs, 14 successful:

| | p50 | p90 | p95 | max |
|---|---|---|---|---|
| successful, turns | 7 | 20 | 20 | 21 |
| successful, tool calls | 4 | 13 | 13 | 15 |
| failed, tool calls | 4 | 100 | 220 |  |

Successful runs and failed runs do not overlap. Every run that solved its task
did it inside 21 turns and 15 tool calls. The failed ones pile up against
whatever ceiling was above them, which is why they set no budget here.

The first working tier is the 95th percentile of the successful runs, the same
rule an SRE timeout follows: pick the false-stop rate you can live with, then
read the number off the success distribution. Five percent of successful runs
get stopped early, and progress-gated escalation gives them their next budget.
Each tier doubles, which bounds overshoot at twice the true need and reaches
any ceiling in a logarithmic number of steps. The ladder used to step from 220
tool calls to 260 — an eighteen percent raise is not an escalation.

| tier | was | now |
|---|---|---|
| low | 40 / 30 | 20 / 13 |
| medium | 180 / 100 | 40 / 26 |
| high | 400 / 220 | 80 / 52 |
| xhigh | 500 / 260 | 160 / 104 |
| max | 700 / 340 | 320 / 208 |

Two limits on this. The measurement comes from container coding tasks holding
four tools; the product runs twenty-five tools against longer workplace work,
and its distribution may sit elsewhere. And fourteen successful runs is a thin
basis for a 95th percentile. Both are reasons to re-run derive-budgets against
product data rather than reasons to keep numbers nobody derived.

The tests hold the shape rather than the digits: every tier must at least
double the one below it, and the first working tier must equal the measured
percentile.

## What removing the escalation cost

Progress-gated escalation and the budgets it escalated were both replaced —
the budgets derived from successful runs, the escalation deleted outright.
The same eight tasks, same model, before and after:

| | before | after |
|---|---|---|
| solved | 4 | 4 |
| median turns | 18 | 14 |
| median wall clock | 131s | 115s |
| runs reaching a proper end | 4/8 | 5/8 |

Nothing was lost. What changed is what the failures cost:

| task | before | after | verdict |
|---|---|---|---|
| fix-git | 101 turns | 27 | unsolved either way |
| chess-best-move | 102 turns | 27 | unsolved either way |
| count-dataset-tokens | 102 turns | 28 | unsolved either way |
| csv-to-parquet | 34 turns, nothing passing | 12 turns, half the assertions | better |

Three tasks spent a hundred turns to arrive exactly where twenty-seven takes
them. Those seventy-three turns were waste, and escalation was what bought
them: two durable progress events was enough to buy a bigger budget and a
better model, and a task grinding on shell commands produces those easily.

csv-to-parquet got better rather than merely cheaper. It had been grinding to
its ceiling and finishing with nothing; it now completes and passes half the
benchmark's assertions.

## Codex is not on the row

Terminal-Bench ships a codex agent and the scripts here discover whatever
harnesses a run produced, so adding it is a one-line change. Getting it to
answer is not.

codex speaks OpenAI's Responses API and no longer accepts
`wire_api = "chat"`, so it cannot be pointed at the endpoint the other two
share by configuration alone. Named as a provider with
`wire_api = "responses"` it does run against OpenRouter — three tasks passed
that way before the run was stopped — so that path works if an API key is
what you want to spend.

On a subscription there is no API key. codex login leaves its credential in
`~/.codex/auth.json`, and copying that file into the task container was not
enough: codex inside the container still reached for an API key and got an
empty one. Whatever it wants beyond that file was not worth more guessing for
one column, and a harness adapter that does not authenticate is worse than an
absent one.

## Reading a run back

```bash
bench/terminalbench/explain-run /tmp/bench-quixbugs5            # every task
bench/terminalbench/explain-run /tmp/bench-quixbugs5 bitcount   # one of them
```

For each task it says whether it was solved, how many of the benchmark's own
assertions passed and which ones did not, how many turns it took, which tools
it reached for, why it stopped, what the runtime refused it, and where the
full ledger sits. A pass rate cannot tell you that nine of ten assertions
passed and the tenth was about diff size; this can, and that is usually the
whole finding.

pi's rows read "no ledger — this harness does not record what it did", which
is the asymmetry, stated rather than hidden.

Two audiences need the same facts. The operator gets them here. The agent
holds its own observations while a turn runs, but nothing yet lets it read
its ledger back across a run — its refusals, its gate decisions, the shape of
its own thrash. That is the open half of this, and it is the half that would
let the agent diagnose itself instead of waiting for someone to read the file.

## What the measurement has found

Each of these was found by running a row, and each is invisible to a pass
rate.

- `terminal_run` sat in the sanitized-presenter list, which reduced a
  successful run to `exitCode` and `timedOut`. That list exists for the
  companion privacy boundary — browser snapshots, screenshots, file picks,
  whose raw output carries cookies and local paths — and a command the agent
  ran itself is not on it. Every terminal call this loop had ever made came
  back to the model as `exitCode=0`, and a second path had the summariser
  reading `stdout` from a result this repo's tool reports under `output`.
  Both had to go. Three runs before and two after, same eight tasks: solved
  went 4, 4, 3 to 6, 5, which is a gain the size of the run-to-run variance
  and not yet separable from it. What is separable is the cost — median turns
  13, 14, 14 to 7, 6, and median wall clock 99s, 83s, 75s to 46s, 31s. The
  agent was never looping. It was searching for something it had already been
  handed and could not see, and it now stops searching.
- The action schema was a root-level `oneOf`, which gemini answers with `{}`
  and no error. Every run died on an empty action. The native tool path, one
  function per tool, has no root oneOf; the reference provider now takes it.
- Tool calls were decoded into a type tagged `toolCalls` while endpoints send
  `tool_calls`, so every call was dropped and the loop reported none.
- Truncation cut on byte offsets, so a cut inside a character lost a whole
  solved run's measurement.
- The completion contract required a delivery tool the task did not hold, and
  the gate asked fifteen times for the one action the task could not take.
- `completionEvidenceIDs` was a free string, so the model cited observations
  that did not exist, twenty-five turns running.
- The harness had one tool. A model asked to fix a file finished with "the
  fixed code has been saved" — a file it never wrote.
- A tool descriptor without a result contract registers without error and
  never reaches the model, which is indistinguishable from a model choosing
  not to call it.
- A result carrying effects its descriptor never declared is rejected, and
  the rejection reached the model as an ordinary retryable failure: 106 turns
  on a call that could not succeed.
- `Retryable` carried the zero value on every tool failure, so the loop told
  the model "do not retry" about failures worth retrying and enforced nothing
  about the ones that were not.

## What has not worked

Three changes were made, measured, and judged by the measurement rather than by
the reasoning behind them.

Restoring the contract's file requirement wherever a write tool existed took
the median run from 9 turns to 118 and from seven of eight runs finishing to
one. It was reverted.

Telling the agent to check its own work before finishing solved no additional
task, though it did cut the median run from 32 turns to 5 and took clean
finishes from four of six to five. What it did not do is make the agent
verify anything: `terminal_run` was still called about once per task, the
tests sitting in the container were never run, and the agent read files 210
times across six tasks instead. Whatever makes an agent check its work, a
sentence in the system instruction is not it.

Letting one response carry several tool calls was aimed at the round trip
between them, which is where most of a task's wall clock goes. The mechanism
works — the ledger shows an action running with no `llm.call` before it, which
had never happened — but it fired twice in 125 actions, and the second of
those two came only after the instruction stopped merely permitting batches
and started naming the cases worth batching. It is not what makes these runs
slow. Terminal work is sequential by nature: you cannot run the tests before
the edit, or read the file before the listing that names it. The round trip is
real and it is most of the clock, but on this dataset there is almost nothing
independent sitting on either side of it to collapse.

The change stayed in. It is correct, it costs nothing when no batch is
offered, and a dataset whose work is genuinely independent is where it would
show. This dataset is not that, and the pass rate moving from four to three
across those runs belongs to `grid-pattern-transform`, which batched nothing
and has been flaky for this model all along.

## Open

The runs that fail now fail in two shapes, and both come from the same place:
bluecollar has a `finish` action a model can take at any moment, and a gate
that judges it. A weak model declares completion early — `fix-permissions`
ended after two reads — and a strict gate deadlocks. pi has neither: its run
ends when the model stops calling tools, so completion cannot be claimed, only
reached. Changing that touches the completion gate, the evidence ledger and
the reply path, which are the parts of this loop that are deliberately its
own, so it wants a design decision rather than another patch.

pi still reports only its wall clock, so the middle columns of every row above
are bluecollar's alone.
