# Code review request: first implementation (pre-push review, D117)

You are reviewing code before it is pushed. You are an advisor: you do not make
decisions and you do not modify files. Work read-only. Make no network calls,
paid model calls or external writes.

## Scope

- Base (last pushed): `3bb0474`. Target: `4eaf1a5`.
- Commits: `9382ecd` (Go backend and tests), `6d40b37` (Next.js UI),
  `4eaf1a5` (example agent, README, records).
- Diff: `git diff 3bb0474..4eaf1a5`. Nothing relevant is uncommitted.
- Also check each commit for secrets (a secret added and later removed still
  stays in history).

## Contracts to check against

- `PLAN.md` decisions D1–D123 (later decisions amend earlier ones; superseded
  ones are marked). Key ones: D21, D25–D33, D39, D45–D56, D60–D64, D66–D99,
  D105–D106, D110, D123.
- `docs/schema.md` (tables, statuses and transitions, data rules, REST API).
- `AGENTS.md` → "Gereksinim kontrol listesi" (R1–R8, EA) and "Gereksinimden
  doğan zorunlu davranışlar" (Turkish).

## Evidence already gathered

- `cd backend; go test ./...` passes (packages api, auth, crypto,
  provider/openai, provider/anthropic, run, tool); `go vet ./...` is clean;
  the engine tests passed 5 times in a row (`-count=5`).
- `cd web; npm run build` passes.
- Manual: SSE through the Next.js proxy in production mode (`next start`):
  events arrive immediately, heartbeat after 15 s. Not checked in dev mode.
- Not run: any real provider call (no key available), `-race` (needs CGO,
  D102), automated UI tests (none, D102), clean-clone setup timing.

## What we want

Concrete correctness, security and contract findings in the implementation:
for example a terminal state that can change, an event after `run_finished`,
a tool call executed without valid approval or twice, a secret or model key
that can reach the model, an event, a stored record, a log or a response, a
team-isolation hole, a missing `404`/`403`/`409`, a race between approve,
reject, cancel, credential changes and the worker, recovery after a crash, SSE
replay gaps, and gaps in the tests the case requires (agent loop, tool
templates, team permissions, approval flow). Keep new design suggestions
separate from implementation defects. Avoid style-only findings.

## Output format

Markdown. One table:

| ID | Severity | Location | Contract | Trigger and consequence | Suggested fix | Decision? |

Severity: high / medium / low. "Decision?" says **needs decision** when the
fix would change a recorded behaviour, dependency or schema; otherwise "no"
(the fix restores an agreed contract). Then a short list "Tests inspected / run
/ not run" and "Unresolved verification gaps". Mark anything you are not sure
about as uncertain.
