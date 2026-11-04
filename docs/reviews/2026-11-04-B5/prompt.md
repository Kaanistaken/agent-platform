# Review request: Batch B5 — scope, schedule and process

You are reviewing the **scope, schedule and process decisions** of a take-home
case, before any code is written. You are an advisor: you do not make decisions
and you do not modify files. Work read-only.

## Context

Read these files in the repository first:

- `PLAN.md` — all decisions. The ones under review are **D111–D118** (B5).
  D1–D110 are earlier, already reviewed decisions they must be consistent with.
  D84 ("core first, no over-engineering, one backend process") governs scope.
- `AGENTS.md` — working protocol, requirements R1–R8, the example agent (EA)
  and the deliverables (Turkish).
- `docs/schema.md` — tables and REST API.
- `docs/reviews/2026-11-04-B4/answer.md` — your previous review; its "Items for
  B5" section proposed the schedule the author adopted in D112.

Stack: Go 1.26 backend, SQLite via `modernc.org/sqlite` (no CGO), Next.js UI on
Node 24 (proxying `/api/*` to Go), own agent loop with `openai-go` (Responses
API) and `anthropic-sdk-go`. One developer working with a coding agent. **No
code exists yet**; today is 2026-11-04 (evening) and the deadline is
2026-11-09. Model budget USD 5. The author develops on Windows.

## What we want from you

1. **Consistency.** Where D111–D118 contradict each other, earlier decisions or
   `AGENTS.md` (cite D-numbers).
2. **Schedule (D112).** Is the day-by-day plan realistic for the scope fixed in
   D1–D110? For each day, name the concrete exit criterion that shows the day
   is done, and the order of work inside Nov 5 that gets a real browser →
   Next.js → Go → OpenAI → SSE → approval path running earliest. Point out
   dependencies between days that could block (e.g. the sandbox repository and
   GitHub token from D107, model-ID verification from D78).
3. **Code review before every push (D117).** How to keep a review round short
   and useful: what the prompt should contain (diff range, decisions to check
   against, required tests), what output format makes the author's
   accept/reject quick, and what to do with findings that need a new decision.
   Do not argue against the decision itself.
4. **Toolchain and setup (D113, D114).** Pinning (`go.mod` `go`/`toolchain`
   lines, `.nvmrc`, `package-lock.json`), Windows-specific pitfalls for Go and
   Next.js (line endings, paths, `.env` loading, ports), and what a clean-clone
   measurement on Nov 8 must include to support "under 10 minutes".
5. **Budget (D118).** Whether about USD 1 for live calls is plausible for
   model-ID checks, two provider runs and the recorded example with the D108
   manifest, and how to measure actual spend from the recorded token usage.
   Mark price assumptions as uncertain.
6. **Process risks.** Anything in D111–D118 or the protocol in `AGENTS.md`
   (e.g. how decisions are asked and recorded) that is likely to slow
   implementation without improving the result, and a smaller version that
   keeps the same intent. Do not drop requirements.

## Output format

Markdown. Sections in the order 1–6 above. For each finding: what, where
(D-number / file), why it matters (requirement or D-number), and a suggested
fix. Mark suggestions that would need a new decision by the author as **needs
decision**. Be specific and brief; mark anything you are not sure about as
uncertain. Do not present suggestions as decisions.
