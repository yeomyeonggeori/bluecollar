# What a task is allowed to spend

A task needs a ceiling for two opposite reasons, and both matter equally. Work
that is going nowhere must not be allowed to grind, and work that is going
somewhere must not be cut off for looking slow. Getting one right at the cost
of the other is not a win.

## Time is the ceiling, and the model owns almost all of it

Across 147 measured runs the model's own latency was **85% to 97% of the
task's wall clock**. Tool execution, the shell, the loop's own bookkeeping —
all of it together is the remainder. Whatever a task spends its time on, it
spends it waiting for the model.

So a ceiling written as a fixed number of minutes is really a ceiling on how
much model work the task may ask for, converted at some assumed rate. Change
the model and the same minutes buy a different amount of work. The next two
sections are about what that rate actually is, because it is not the one
number it looks like.

## Measuring throughput is fitting two constants, not reading one number

A model's speed is not a single rate. Every call pays a fixed cost — the
connection, the queue, the wait for the first token — and then a per-token
cost while it generates. Divide total tokens by total wait and the two blend
into a figure that changes with how a task happens to be shaped.

Fit them instead. Every run already records the three numbers needed:

    model wait  ≈  a × model calls  +  b × output tokens

`a` is what a round trip costs and `1/b` is the real generation rate. Fitted
over runs measured here:

| model | per call | generation |
|---|---|---|
| google/gemini-3.1-flash-lite | 899 ms | 339 tok/s |
| openai/gpt-5.6-luna | 3,383 ms | 596 tok/s |

The fitted rate for gemini lands within three percent of the 329 tok/s
published for it, which is the check that the fit means something. The blended
rate does not: dividing tokens by wait gives 127 and 65 tok/s for these two,
numbers that describe neither the model nor the task.

## Which means the work unit for time is the call, not the token

A successful run's median is 8 model calls at 205 output tokens each. On
gpt-5.6-luna that is 27 seconds of round trips against 4 seconds of
generation: **87% of a task's time is the cost of asking, not the cost of
answering.** One call is worth about two thousand output tokens.

So an iteration or tool call budget is a good proxy for time, because calls
and turns move together. A token budget is not — it prices the cheap half.

For money the ordering reverses. Cost is prompt plus output tokens against the
price sheet, and the number of calls does not appear, except that each one
resends a prompt that keeps growing.

## What speed to plan for

Output speeds across 101 models on the public leaderboard, and the 49 of them
priced at or below the median $0.22 per million tokens:

| set | median output speed |
|---|---|
| the 49 cost-effective models | 119 tok/s (p25 92, p75 175) |
| all 101 | 106 tok/s |

Running a model yourself is a different regime. Human reading speed is
5 to 10 tok/s, an assistant below about **20 tok/s** feels sluggish to the
people who run them, and **40 to 60 tok/s** is what consumer hardware should
be aiming at. An 8B model at Q4 reaches 90–140 tok/s on an RTX 4090 and 30–80
on an Apple M-series Max.

Those thresholds were set by people watching tokens arrive. An agent's output
is not read that way — it is tool calls and reasoning, and the requester sees
only how long the whole thing took. A model at 20 tok/s is not "slow but
readable" here; it is six times the wall clock of a 119 tok/s model for
identical work, delivered as one wait rather than a slow stream.

Those numbers are advice about whether a local model is worth running at all,
not parameters of the budget. Below roughly 20 tok/s an assistant generates
more slowly than a person reads and will not carry an agent that makes eight
calls before it says anything; 50 tok/s or better is what to aim for. The
deadline does not consult them. It follows what the model actually does.

## The ladder

`bench/derive-budgets` reads the iteration and tool call budgets off runs that
succeeded: the first working tier is their 95th percentile, and each tier
doubles. Duration is not stored, it is computed — iterations × (cost of a call
+ 205 tokens ÷ speed) × 2 — at the **slowest speed the product supports**,
because the asymmetry runs one way. A ceiling that fires late is caught by the
progress gate and the call budget. A ceiling that fires early is caught by
nothing, and takes work that was going somewhere with it.

| tier | duration | iterations | tool calls |
|---|---|---|---|
| xlow | 1.4 min | 4 | 1 |
| low | 7 min | 20 | 13 |
| medium | 14 min | 40 | 26 |
| high | 28 min | 80 | 52 |
| xhigh | 56 min | 160 | 104 |
| max | 112 min | 320 | 208 |

At the hosted median of 119 tok/s the same work fits in a third of that — 0.5,
2.6, 5.2, 10.3, 20.6, 41.2 minutes. A deployment does not have to accept the
floor's clock: the deadline is computed from the throughput of the model that
is actually answering.

## The deadline follows the model in use

Every `llm.call` the ledger records also passes its latency to a throughput
observer, and a task's deadline is that model's median call multiplied by the
work its tier allows.

The estimate sharpens as evidence arrives, with no threshold to cross:

| calls seen | the estimate |
|---|---|
| none | the floor |
| one | that call |
| three | the median of three |
| ten | the median of ten |
| a hundred and more | the median of the last hundred |

It is one rule — the median of what is on hand, up to a hundred — and the
table is what that rule looks like as it fills. Nothing waits for a quorum,
so the first task on a new model is already on that model's clock rather than
a stranger's, and a single wild call cannot move a median once there are a few
around it. The window stops at a hundred so a model that has been running for months
still answers for how it behaves now.

A median call is what the budget needs, so it is what is kept. Separating the
fixed cost from the per-token rate says more about a model, and it is how the
numbers above were understood, but a deadline is iterations times what an
iteration costs, and that is measured directly.

The measurement sets the deadline outright, bounded at both ends. Below, by
the fastest call worth believing — without it a near-zero measurement hands a
task a near-zero clock and it is stopped before its second call. Above, by
what the tier is willing to spend, which is what stops a model answering once
an hour.

An earlier version bounded it the other way, taking the shorter of the
measurement and a fixed floor. That handed slow hardware a clock sized for
faster hardware, which is not a standard but a guarantee that everything it is
given fails. A model that needs longer gets longer, up to the tier's ceiling.

Nothing is queried. The samples are the values already being written to the
ledger, kept as they pass.

## Who decides what

The model classifies the tier, because judging what kind of work a request is
takes judgement. The runtime assigns what that tier is allowed to spend,
because a model that wants to keep working should not be the one setting how
long it may work.

Intake also estimates `estimatedMinutes` — how long a careful human
professional would spend on the task. That is planning metadata and must not
become the budget: measured against real runs the estimate ran 30 to 75 times
longer than the agent actually took, because it answers a different question.
Its value is elsewhere. It says what the work is worth, and an agent that
takes longer than the person it is standing in for has stopped being useful
however correct its answer is.

## What happens when a budget runs out

The loop asks the model whether the work is done. If it is, the task
finishes. If it is not, the task stops and reports what it did.

It used to grant itself more. Reaching a limit with two "durable progress
events" recorded since the tier began escalated the tier, which bought a
larger budget and a better model, up to twice per task. The counting was
wrong in both directions all day: a successful `pwd` scored as progress,
while in the reference harness nothing scored at all because the tool never
reported the field the counter read, so a task that had built and tested
successfully was refused in exactly the same way as one that had done
nothing.

The reason it kept being wrong is that it was the wrong kind of question.
Whether actions are moving toward a goal is a judgement about meaning, and a
file changing is not evidence that changing it helped. This repository draws
that line itself: the model judges outcomes and direction, deterministic code
supplies facts the model cannot know. A progress counter is code answering
the model's question.

No other harness does this. pi ends when the model stops calling tools and
bounds itself by the context window; Claude Code runs until the model stops
or someone interrupts. Neither tries to measure whether a run is going
anywhere.

So the loop no longer grants itself time. It finishes if it can, and
otherwise says how far it got — which is what a person does, and leaves the
decision to continue with whoever asked.

Continuing needs nothing new. The report is a message, and a reply asking to
carry on arrives as one: intake routes it as `outcome_recovery` against the
prior task, which carries that task's prompt, result and contract forward.
`ask_input` is not the shape for this — that is a tool the model calls, and a
budget running out is the runtime stopping, not the model asking.

## What is still owed

- Cost. Every run before today reported `costUSD` zero because the reference
  provider never asked the endpoint to account for it. It asks now, and once
  enough runs carry real cost the same percentile method applies to money.
