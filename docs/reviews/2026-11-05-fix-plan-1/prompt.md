# Fix plan review: findings of the first code review (D117)

You are an advisor reviewing a **fix plan** before any code is written. You do
not make decisions and you do not modify files. Work read-only. Make no network
calls, paid model calls or external writes.

## Context

- Repository: the AI Platform Engineer take-home case (Go backend in
  `backend/`, Next.js UI in `web/`). Contracts: `PLAN.md` decisions D1–D123,
  `docs/schema.md`, `AGENTS.md` (requirements R1–R8, EA; Turkish).
- Your previous review of `3bb0474..4eaf1a5` is in
  `docs/reviews/2026-11-04-code-1/answer.md` (findings F01–F19). HEAD is now
  `b7d3b37`; `919d85e` already fixed F04.
- The author's decision on the findings (2026-11-05): fix F01, F02 and every
  medium finding **except F06**. F06 (text after `run_finished`) and F19 (UI
  shows unknown usage as zero) are **not** fixed now. F04 is done.
- Core-first rule (D84): no new features, no generality that the fix does not
  need. Single backend process. Tests use the standard `testing` package with
  fake providers/executors (D102); no `-race` (needs CGO).

## Planned fixes

Each item: what changes, where, and the regression test.

**F01 — `X-Idempotency-Key`.** Add `X-Idempotency-Key` to `forbiddenHeaders`
in `backend/internal/tool/template.go` (keys are canonical header names).
Test: `Validate` rejects both `Idempotency-Key` and `X-Idempotency-Key`
(any case).

**F02 — notification lost while the worker is exiting**, and **F15 — queue
overflow**. Replace the 1,024-slot channel in `backend/internal/run/engine.go`
with an unbounded FIFO slice guarded by `e.mu`, plus a 1-slot `wake` channel.
- `Enqueue`: if the run is active in a worker, set `again[id] = true`;
  otherwise append it to the FIFO once (dedup by `queued[id]`); then a
  non-blocking send on `wake`.
- Worker: `claim()` pops the next run under the lock, skips (and marks
  `again`) a run that is active, registers a fresh cancel func in `active`,
  and re-signals `wake` if more work is queued (so idle workers chain-wake).
  After `process` returns, `release(id)` under the lock: if `again[id]` is set
  and the engine is not stopping, clear it, register a fresh context and run
  `process` again; otherwise delete `active[id]`.
- Recovery still enqueues before the workers start; nothing is dropped.
- Tests: (a) deterministic F02 test: mark a waiting run as active by hand,
  approve it (no worker picks it up), then call `release` as the exiting
  worker would and check the run runs again and succeeds; (b) F15: insert
  more than 1,024 resumable runs directly, restart, and check every one
  reaches a terminal state (the runs use an exhausted active-time budget so
  each finishes in two transactions).

**F03 — cross-team call state in `409`.** `callState` in
`backend/internal/run/approvals.go` reads the call with
`WHERE id = ? AND run_id = ?`; a call that is not in the authorized run returns
`ErrNotFound` (404) instead of a conflict. Test: alice's run ID with a call ID
from carol's run → `ErrNotFound`, no state returned.

**F05 — Anthropic environment credentials.** Pass
`option.WithoutEnvironmentDefaults()` (anthropic-sdk-go v1.84.0) in
`backend/internal/provider/anthropic/anthropic.go`, so `ANTHROPIC_API_KEY`,
`ANTHROPIC_AUTH_TOKEN`, `ANTHROPIC_PROFILE`, env federation and
`ANTHROPIC_BASE_URL` are all ignored; the explicit key and the test base URL
still apply. OpenAI is left as is: the explicit `WithAPIKey` overrides the same
`Authorization` header (its SDK's equivalent switch is internal API). Test:
`t.Setenv("ANTHROPIC_AUTH_TOKEN", ...)`, the fake server asserts there is no
`Authorization` header and `X-Api-Key` is the user's key.

**F07 — expired approvals.** One helper `expireTx(ctx, tx, runID, now)`:
if the run has an open call (`pending_approval`, `approved`, `invalidated`)
with `approval_expires_at <= now`, it calls `finishTx(... cancelled,
approval_expired ...)` in that transaction. It is used:
- in `Approve` and `Reject` before the conditional update: if it fired, the
  transaction commits (the run is cancelled) and the caller gets a `409`
  "the approval request expired";
- in the claim transaction of `executeCall`: if it fired, nothing is sent and
  the loop stops;
- by the sweep: the candidate query stays, but each run is cancelled only if
  `expireTx` confirms it inside its own transaction.
Tests: with the clock moved past expiry and no sweep, `Approve` returns a
conflict, the run is `cancelled/approval_expired` and nothing is sent; an
approved-but-unclaimed call past expiry is not sent.

**F08 — cancellation of executing calls.** In `finishTx`, before
`run_finished` is emitted, every `executing` call of the run is closed in the
same transaction, with a `tool_call_status` event: a non-`GET` call becomes
`outcome_unknown` (reason `run_cancelled`; it may have been sent), a `GET`
becomes `failed` (reason `run_cancelled`). `completeCall` and `releaseCall`
already update only `WHERE status = 'executing'`, so a late HTTP completion
cannot overwrite it. Test: cancel while a write is gated in the fake executor;
the call is `outcome_unknown` before `run_finished`, and stays so after the
request returns.

**F09 — restoration order**, and **F11 — host of a replacement secret.**
In `backend/internal/run/invalidate.go`, replace `prereqsOK` with
`missingPrereq(...) (reason string, err error)`: `model_key_deleted` if the
starter has no key for the run's provider, `secret_deleted` if a bound secret
is missing **or its `allowed_host` differs from the call's host**, `""` if all
are present.
- When a key is restored (`OnModelKeyChanged`, not deleted) or a secret is
  added (`OnSecretAdded`), **every** invalidated call of the starter's active
  runs is re-evaluated regardless of its stored reason: no missing
  prerequisite → `pending_approval` with a new revision; still missing → the
  stored reason is updated to the one that is missing now (no revision bump).
- `OnSecretAdded` replaces a run's binding only if the new secret's host is a
  host that the snapshot's tools send this secret name to (computed from the
  snapshot tool URL templates via a small exported `tool.Host(Definition)`).
Tests: delete secret, delete key, restore secret, restore key → pending with
a new revision (and the reverse order); a same-name secret on another host is
not bound and the call stays invalidated.

**F10 — expiry for calls that enter approval later.** `setCall` sets
`approval_expires_at = COALESCE(approval_expires_at, <expiry from now>)` when
the new status is `pending_approval` or `invalidated` (both wait for the
starter, and the sweep covers both); the claim-time invalidation in
`executeCall` does the same. An existing expiry is never moved. The
`tool_call_status` event carries `approval_expires_at`. Test: model key
replaced while a no-approval tool's call is `approved` → `pending_approval`
with an expiry, and the sweep with a later clock cancels the run.

**F12 — agent update with a retained deleted tool.** `setAgentTools` in
`backend/internal/api/definitions.go` deletes only links that are no longer
requested and inserts only new ones; retained links are untouched, so the
D46 trigger is not hit. Test: link a tool, delete it, update the agent keeping
it → `200`.

**F13 — tool rename collisions.** `updateTool` runs in one transaction; if
another non-deleted tool with the new name is linked to any agent this tool is
linked to, it returns `409` naming the agent. Test: two tools on one agent,
rename one to the other's name → `409`, nothing changed.

**F14 — discard marker after restart.** In `recover`, in one transaction:
for every model call left unfinished whose run is not terminal and which
already has `text_delta` events (matched by `json_extract(payload,
'$.model_call_id')`), emit `attempt_discarded`; then mark the calls
`interrupted` as today. Test: a run with a streamed partial delta and an
unfinished model call → after restart the event log has `attempt_discarded`
for that call before the retried answer.

**F16 — text flush timer.** `textBuffer` gets a mutex and a `time.AfterFunc`
timer: when a delta arrives less than 250 ms after the last flush, a timer is
armed for the remainder; every flush stops the timer; the final flush at the
end of the attempt stops it too. Event order is preserved because flushes are
serialized by the mutex. No terminal-state check is added (that is F06, not
fixed by the author's decision). Test: a provider that streams one delta and
then waits; the `text_delta` event appears within about a second while the
stream is still open.

**F17 — GET retry budget across restarts.** The claim no longer increments
`attempt_count`. The executor interface becomes
`Do(ctx, r, secrets, maxAttempts int, onAttempt func())`: `onAttempt` runs
before each send and increments `attempt_count` in the database. The engine
passes `maxAttempts = 2 - attempt_count` for `GET` (D91: one retry) and `1`
for writes (a write is never re-sent: after a crash it becomes
`outcome_unknown`, D63). A `GET` whose budget is used up is completed as
`failed` (reason `interrupted`) with a tool message for the model. Test: a GET
interrupted after two recorded attempts is not sent again after a restart.

**F18 — response status kept when the body read fails.** `Client.once`
returns the response's status and the formatted text with a note
`[reading the response body failed: …]`, together with a `network` failure so
a `GET` is still retried (D91). `Do` returns the last result with its status
if one was received (masked as usual), and a failure only when no response
arrived. Test: a TLS test server that sends `201` headers and then closes the
connection → `Result.Status == 201`, no error.

## What we want

For each item: is the plan correct and complete against the finding and the
contracts? Point out concrete problems: a remaining path to the same bug, a
new bug or race the change introduces, a contract (decision ID or schema rule)
it breaks, a test that would not actually catch the bug, or an unnecessary
change (D84). Also say if fixing F16 without F06 makes anything worse than
today.

Output a table: `ID | verdict (ok / change / problem) | issue | suggested
change | needs a new author decision? (yes/no)`. Keep it short; no rewrite of
the plan. Then list anything the plan misses.
