# AGENTS.md

Repository-specific conventions for anyone — human or agent — changing this
code. The code style itself lives in the parent host's AGENTS.md and applies
here unchanged: no comments, no abbreviations, small functions, guard clauses.

Recovery allowances are ceilings. A model may stop with recorded failure facts while budget
remains. A retry must address the observed cause or use an independent route. Prior assistant
reports and inferred outcome contracts are hypotheses; recorded calls and effects carry the
evidence, and the current intake may replace the old interpretation.
Keep `fail` in the native action schema while failure debt exists, even when
recovery allowances remain. Runtime guidance is a system observation, never an
executed tool call. A live recovery test must verify the chosen action; a failed
task caused by a provider error is not evidence that the model chose to stop.

## Working on this repository

Nothing lands on `main` by direct push. Branch, open a pull request, let the
check run, merge.

### Branch names

`<type>/<subject-in-kebab-case>` — the type is the same word the commit will
carry, and the subject says what changes, not what you did to it.

```
feat/acp-context-injection
fix/approval-resume-after-restart
docs/tui-screenshots
refactor/turn-runner-split
test/completion-gate-evidence
chore/gofmt
ci/skip-docs-only-runs
```

Branch off `main`, rebase rather than merge when `main` moves under you, and
delete the branch once the pull request is merged.

### Commit messages

```
<type>: <what changes, imperative, lowercase, no trailing period>

<why it changes: the problem the reader would otherwise have to reconstruct.
Wrap at 72 columns. Say what you deliberately did not do.>
```

`type` is one of `feat`, `fix`, `docs`, `refactor`, `test`, `chore`, `ci`. A
scope is allowed when it disambiguates (`fix(acp): …`) and omitted when it does
not.

The subject line is a claim about the code, not a description of the work:
`fix: stop reading "the agent stopped talking" as "the task is done"` rather
than `fix: fixed the status bug`. The body exists to answer *why*; a commit
whose body only repeats the subject should not have one.

### Prose in documents

The README and the docs are read by people deciding whether to trust this
thing. Prose that reads as machine-written costs that trust, and it drifts back
in every time someone lets a model write a paragraph. Grep for it before
committing.

- **One negative-parallel construction per 500 words.** `X, not Y` ·
  `rather than` · `not merely … but`. Keep the ones where the alternative is
  what a reader would actually assume; cut the rest. They stop registering when
  they repeat, which wastes the ones that matter.
- **Under three em dashes per 500 words.** An em dash that bolts an appositive
  onto a finished sentence should be a period.
- **No sentence praising the document's own honesty.** "worth stating plainly",
  "to be clear", "honest list". Be plain and say nothing about it.
- **No section-closing restatement.** If the last sentence of a section adds no
  fact, delete it.
- **Three or more `A X is a Y that …` in a row is a definition list.**
- **A fact stated in a table is not restated in prose.**
- **Bold whole blocks, never words inside a sentence.**

Check with:

```bash
python3 - <<'EOF'
import re, pathlib
text = pathlib.Path("README.md").read_text()
words = len(text.split())
negations = len(re.findall(r", not |rather than |not merely", text))
print(f"{words} words · {negations} negations (1 per {words // max(negations, 1)}) · {text.count(chr(8212))} em dashes")
EOF
```

### Pull requests

One reviewable change per pull request. The description says what the reader
should look at and what evidence exists that it works — the test that fails
without the change, the scenario that was run, the screenshot. A pull request
that touches unrelated files should be split.

Branch names, commit messages, pull request titles and pull request
descriptions are written in English. Discussion in review can be in whatever
language the reviewers share; the repository's permanent record is English.
