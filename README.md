# bluecollar

*An agent harness that does the work, keeps a record, and tells you when it can't.*

[![check](https://github.com/yeomyeonggeori/bluecollar/actions/workflows/check.yml/badge.svg)](https://github.com/yeomyeonggeori/bluecollar/actions/workflows/check.yml)
[![Go Reference](https://pkg.go.dev/badge/github.com/yeomyeonggeori/bluecollar.svg)](https://pkg.go.dev/github.com/yeomyeonggeori/bluecollar)
[![Go](https://img.shields.io/badge/go-1.26-00ADD8?logo=go&logoColor=white)](go.mod)
[![License](https://img.shields.io/badge/license-Apache--2.0-blue)](LICENSE)

<img src="assets/demo/welcome.png" alt="the bluecollar welcome screen: the mascot above the model, workspace, and exit hint in a bordered box" width="420">

> **Status: pre-alpha.** The exported API, the contract types and the event names change without notice. If you import it, pin a commit.

bluecollar is a headless, embeddable Go agent harness for unattended work. A host hands it a tool set and a task store; it runs the turn, proves completion from its own event ledger, and reports failure to the person who asked. Every tool call executes back in the host.

```bash
go build ./...
go test ./...
OLLAMA_CONTEXT_LENGTH=32768 ollama serve &
go run ./cmd/bluecollar --model qwen3.5:4b "In one sentence, what is a POSIX user?"
```

The documentation is [DOCS.md](DOCS.md), published at [bluecollar.intern.kim](https://bluecollar.intern.kim).

| path | holds |
|---|---|
| `agentcontract/` | the harness port, turn requests and results, task runs and ledger event names |
| `toolcontract/` | tool descriptors, tool sets, results, the kernel tool names |
| `model/` | the language model and decision model ports, with OpenAI-compatible, decisions and tape implementations |
| `loop/` | the agent loop |
| `intake/` | the turn router and the decision planner |
| `taskstate/` | the services over task runs, steps, events and artifacts |
| `turnstream/`, `trace/` | a live view of a turn's ledger, and one run rendered as a file |
| `bench/` | run metrics, a harness runner, and the Terminal-Bench adapter |
| `cmd/bluecollar/` | the command-line runner |
| `cmd/bluecollar-acp/` | the loop as an Agent Client Protocol agent, in its own module |
| `docs/` | the documentation site, generated from `DOCS.md` |

## Contributing

Pull requests open at alpha. Issues are welcome now: a decision you disagree with is the most useful one. [AGENTS.md](AGENTS.md) has the repository's conventions.

## License

Apache License 2.0. See [LICENSE](./LICENSE).
