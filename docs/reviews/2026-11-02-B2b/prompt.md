# Review request: Batch B2b — schema and REST API

You are reviewing a **draft schema and REST API** for a take-home case, before
any code is written. You are an advisor: you do not make decisions and you do
not modify files. Work read-only.

## Context

Read these files in the repository first:

- `docs/schema.md` — the draft under review (tables and endpoints).
- `PLAN.md` — all decisions D1–D44; the schema must follow them. Note that
  D19, D24 and D28 are superseded or amended (by D32, D33).
- `AGENTS.md` — "Gereksinim kontrol listesi" summarises requirements R1–R8 and
  the example agent (Turkish).
- `docs/reviews/2026-11-02-B1/` and `docs/reviews/2026-11-02-B2/` — your earlier
  reviews; their "Items for B2b" sections list what this batch should address.

Stack: Go backend, SQLite via `modernc.org/sqlite`, Next.js UI, own agent loop
with `openai-go` and `anthropic-sdk-go`. Run execution details (loop limits,
streaming path, retry policy, restart recovery, execution claims, ambiguous
side effects) belong to the next batch, B3, and are not decided yet.

## What we want from you

1. **Consistency with decisions.** Every place where `docs/schema.md`
   contradicts, omits or goes beyond a recorded decision (cite the D-number).
2. **Requirement coverage.** For R1–R8: can the schema and API support the
   requirement? Name concrete gaps (missing column, table, constraint, index or
   endpoint), as failure scenarios.
3. **Integrity and isolation.** Constraints that keep data consistent and teams
   isolated at the database level, not only in handlers — e.g. `agent_tools`
   linking tools of another team, `runs.team_id` disagreeing with the agent's
   team, a deleted tool being added to an agent, uniqueness rules, foreign keys
   and `ON DELETE` behaviour given soft deletes (D34, D43).
4. **Authorization per endpoint.** For each endpoint, what must be checked
   (team, owner, lead, run starter), including reads of runs, events, messages,
   tool calls and usage (D35, D41, D44), clones (D22, D23) and the deleted-tool
   confirmation flow (D40).
5. **Secret handling in the schema/API.** Where secret values, model keys or
   password hashes could leak through responses, snapshots, previews, events or
   errors (D12, D29, D32, D33); and whether `user_secrets` unique `(user_id,
   name)` plus `allowed_host` fits D32 (a template references `{{secret.NAME}}`
   and the starter's secret with that name is used).
6. **Statuses.** Whether the run and tool-call statuses implied here are enough
   for R4, R5, D29 (approval invalidated when a secret is deleted) and D40,
   without deciding B3's execution policy.
7. **Simplicity.** Anything that can be removed or merged without losing a
   requirement or decision.

## Output format

Markdown. Sections in the order 1–7 above, then "Items for B3". For each
finding: what, where (table/endpoint), why it matters (requirement or
D-number), and a suggested fix. Mark suggestions that would need a new decision
by the author as **needs decision**. Be specific and brief; mark anything you
are not sure about as uncertain. Do not present suggestions as decisions.
