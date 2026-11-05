# Code review request: fix of the third review (pre-push review, D117)

You are reviewing code before it is pushed. You are an advisor: you do not make
decisions and you do not modify files. Work read-only. Make no network calls,
paid model calls or external writes.

## Scope

- Your previous review: `docs/reviews/2026-11-05-code-3/answer.md`: N02 — the
  N01 fix read the `PUT /tools/{id}` body inside the write transaction, so a
  client withholding its body could block every write.
- Now under review: `7f5ddf5..57c2aed` (one commit, `57c2aed`).
  Diff: `git diff 7f5ddf5..57c2aed`. Nothing relevant is uncommitted.
- Also check the commit for secrets.

## Decision behind the fix

`PLAN.md` D126 (author): fix N02 as you suggested, with a blocked-body test,
and extend the expired-run foreign-call test to rejection. Your other two
notes (HTTPS `$ref` case; timer callback racing close) are left as they are.
Contracts: `PLAN.md` D1–D126, `docs/schema.md`, `AGENTS.md` (R1–R8, EA).

## What changed

- `backend/internal/api/definitions.go`: `updateTool` calls a new
  `toolWriteAccess` on the read pool (`s.DB.R`), then reads and validates the
  body, then opens the write transaction and calls `toolWriteAccess` again
  before the name-collision check and the update.
- `TestSlowToolBodyDoesNotBlockWrites`: a `PUT /tools/{id}` whose body is held
  open; another user's model-key write must complete within 3 s. On the
  previous code it fails (the write waits), on the new code it passes.
- `TestDecisionOnForeignCall` also rejects with a foreign call on an expired
  run (`ErrNotFound`); checked by a mutation that moves the expiry check first.

## Evidence already gathered

- `cd backend; go test ./... -count=3` passes; `go vet`, `gofmt -l` clean.
- Not run: real provider calls, `-race` (needs CGO, D102).

## What we want

1. Is N02 closed, and is there any other handler that reads a request body
   (or waits on the client) while holding the write transaction?
2. Any new defect the commit introduces (for example a time-of-check gap
   between the two authorization checks that matters, or a changed status
   code).
3. Does the new test prove what it claims?

Keep design suggestions separate from defects. Avoid style-only findings.

## Output format

Markdown. One table:

| ID | Severity | Location | Contract | Trigger and consequence | Suggested fix | Decision? |

Severity: high / medium / low. "Decision?" says **needs decision** when the
fix would change a recorded behaviour, dependency or schema; otherwise "no".
Then "Tests inspected / run / not run" and "Unresolved verification gaps".
Mark anything uncertain. If you find nothing, say so explicitly.
