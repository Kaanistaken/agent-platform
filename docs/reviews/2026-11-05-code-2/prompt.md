# Code review request: fixes of the first review (pre-push review, D117)

You are reviewing code before it is pushed. You are an advisor: you do not make
decisions and you do not modify files. Work read-only. Make no network calls,
paid model calls or external writes.

## Scope

- Previously reviewed: `3bb0474..4eaf1a5` (your review:
  `docs/reviews/2026-11-04-code-1/answer.md`, findings F01–F19).
- Now under review: `4eaf1a5..ef327c1`. Commits:
  - `919d85e` refuses external `$ref` in tool parameter schemas (fix for F04,
    made before your first review finished; not reviewed yet).
  - `b7d3b37` records only (review files, `PLAN.md`).
  - `ef327c1` fixes F01–F03 and F05–F18 (F19 deferred by the author).
- Diff: `git diff 4eaf1a5..ef327c1`. Nothing relevant is uncommitted.
- Also check each commit for secrets.

## Decisions and plan behind the fixes

- `PLAN.md` D124: the author's decision on F01–F19 (fix F01–F18 except F04,
  already done; F06 added after your plan review; F19 deferred).
- `docs/reviews/2026-11-05-fix-plan-1/` is the fix plan and your review of it.
  Every change you requested there was meant to be applied; check that it was.
  One choice made during implementation: the tool HTTP client turns off
  connection reuse (`DisableKeepAlives`) so Go cannot transparently re-send a
  GET on a reused connection (your plan-review note on D91).
- Contracts: `PLAN.md` D1–D124, `docs/schema.md` (updated in `ef327c1`),
  `AGENTS.md` requirements R1–R8, EA (Turkish).

## Evidence already gathered

- `cd backend; go test ./... -count=3` passes; `go vet ./...` and `gofmt -l`
  clean; `cd web; npm run build` passes.
- Each new regression test was checked against a mutation that undoes its fix
  (15 mutations, all made the test fail): `internal/run/lifecycle_test.go`,
  `TestAgentToolLinks` (api), `TestEnvironmentCredentialsIgnored` (anthropic),
  `TestClientAttemptBudget`, `TestClientRefusesForbiddenHeader`,
  `TestClientKeepsStatusWhenBodyFails` (tool).
- Not run: real provider calls, `-race` (needs CGO, D102), UI tests (none).

## What we want

1. For each of F01–F18: is it actually fixed, and does the test prove it?
   Is any path to the same bug left?
2. New defects the fixes introduce: races between approve, reject, cancel,
   expiry, credential changes, the worker hand-off (`claim`/`release`) and the
   text-buffer timer; events after `run_finished`; a call sent twice or
   without valid approval; attempt counting; deadlocks (for example the text
   buffer's mutex held during a database write while another path waits);
   lost wake-ups in the queue; changed API status codes.
3. `919d85e`: does refusing external `$ref` still allow local references
   (`#/$defs/...`) and block every loader path?

Keep new design suggestions separate from defects. Avoid style-only findings.
Do not repeat F19 or the items the author deferred.

## Output format

Markdown. One table:

| ID | Severity | Location | Contract | Trigger and consequence | Suggested fix | Decision? |

Severity: high / medium / low. "Decision?" says **needs decision** when the
fix would change a recorded behaviour, dependency or schema; otherwise "no"
(the fix restores an agreed contract). Then a short list "Tests inspected / run
/ not run" and "Unresolved verification gaps". Mark anything you are not sure
about as uncertain. If you find nothing, say so explicitly.
