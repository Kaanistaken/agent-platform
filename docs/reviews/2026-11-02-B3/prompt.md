# Review request: Batch B3 — run execution

You are reviewing the **run execution decisions** of a take-home case, before
any code is written. You are an advisor: you do not make decisions and you do
not modify files. Work read-only.

## Context

Read these files in the repository first:

- `PLAN.md` — all decisions. The ones under review are **D59–D83** (B3). D1–D58
  are earlier, already reviewed decisions they must be consistent with. D19,
  D24 and D28 are superseded or amended (by D32, D33).
- `docs/schema.md` — tables and REST API (B2b). Endpoints for SSE/replay and
  cancellation were marked provisional until B3 (D45).
- `AGENTS.md` — "Gereksinim kontrol listesi" summarises requirements R1–R8 and
  the example agent (Turkish).
- `docs/reviews/2026-11-02-B1/`, `-B2/`, `-B2b/` — your earlier reviews; their
  "Items for B3" sections list what this batch was expected to settle.

Stack: Go backend, SQLite via `modernc.org/sqlite`, Next.js UI (proxying
`/api/*` to Go), own agent loop with `openai-go` (Responses API) and
`anthropic-sdk-go`. Deadline 2026-11-09 for one developer; model budget USD 5.

## What we want from you

1. **Consistency.** Where D59–D83 contradict each other, earlier decisions or
   `docs/schema.md` (cite D-numbers and table/endpoint). List schema or API
   changes that B3 now requires (e.g. lease columns, event IDs for
   `Last-Event-ID`, failure reason codes, attempt-level usage rows).
2. **Final states (R4).** Walk through: provider error, permanent error,
   retries exhausted, deadline hit, step limit, invalid-call limit, worker crash
   mid model call / mid tool call, server restart, cancel during a model call,
   cancel during a retry wait, cancel while waiting for approval. Does every
   path end in `succeeded`, `failed` or `cancelled` exactly once? Name gaps.
3. **Approval and side effects (R5, D21, D29, D55, D63, D66, D72, D76, D77).**
   Race conditions between approve, reject, cancel, secret replacement,
   model-key change and lease takeover. Can a side-effecting call execute twice
   or without a valid approval? Is the transition from `executing` to
   `outcome_unknown` well defined on takeover?
4. **Leases and workers (D59, D62).** Lease length vs. renewal vs. the long
   deadlines in D74 (10 min model call, 1 h run) and the retry waits in D79.
   What happens to a lease during a long single call? Clock assumptions.
5. **Retries and errors (D10, D79).** Is the error classification correct and
   complete for the OpenAI Responses API and the Anthropic Messages API
   (including streaming errors, `insufficient_quota` vs rate limit, Anthropic
   529 / `overloaded_error`, context overflow)? Mark anything you are not sure
   about as uncertain rather than guessing.
6. **Tool HTTP safety (D11, D32, D33, D81).** Gaps in the SSRF, redirect, host
   normalisation and template escaping rules; anything that still lets a tool
   owner or the model reach an unintended destination or leak the starter's
   secret.
7. **Streaming (D67–D71, D75).** Event ordering, replay with `Last-Event-ID`,
   discarded partial output on a retried stream, delta coalescing, and whether
   the Next.js proxy is a realistic risk for SSE.
8. **SQLite (D82).** Whether the single-writer / reader-pool setup is sound
   with `modernc.org/sqlite`, WAL and the lease and conditional-update patterns
   (D21, D59); anything that makes "multiple replicas" (D59, D69) misleading.
9. **Scope.** Given 7 days and one developer, which B3 items are most likely
   to cost more time than they are worth, and what simpler version would still
   satisfy R3–R5 and R8. Do not drop requirements.

## Output format

Markdown. Sections in the order 1–9 above, then "Items for B4" (example agent
and testing). For each finding: what, where (D-number / table / endpoint), why
it matters (requirement or D-number), and a suggested fix. Mark suggestions
that would need a new decision by the author as **needs decision**. Be specific
and brief; mark anything you are not sure about as uncertain. Do not present
suggestions as decisions.
