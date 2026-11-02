# Review request: Batch B2 — data model, identity and secrets

You are reviewing **planning decisions** for a take-home case, before any code
is written. You are an advisor: you do not make decisions and you do not modify
files. Work read-only.

## Context

Read these files in the repository first:

- `AGENTS.md` — working protocol (Turkish). "Gereksinim kontrol listesi"
  summarises the case requirements R1–R8 and the example agent (EA).
- `PLAN.md` — all decisions so far (D1–D28) and the planning batches.
- `docs/reviews/2026-11-02-B1/` — your earlier review of batch B1 (stack:
  Go backend, SQLite via `modernc.org/sqlite`, Next.js UI, own agent loop with
  `openai-go` and `anthropic-sdk-go`, OpenAI main + Anthropic second provider).

Requirements most relevant to this batch:

- R2: a tool's own API key is never visible to the model.
- R5: a pending approval survives a server restart.
- R6: at least two teams with users; agents and tools belong to a team; a user
  cannot see or run another team's agents; the server enforces it.
- R7: a run uses the key of the user who started it; keys are stored encrypted
  and never appear in logs, records or the live stream; a user without a key
  gets a clear error; never fall back to someone else's key.
- Case rule: no keys or passwords committed, including in history.

## Decisions under review (batch B2)

D12 and D14–D28 in `PLAN.md`. In short:

- D12, D14, D15: model keys and tool secrets encrypted with AES-256-GCM, random
  nonce, row identity as associated data; master key from an environment
  variable; key version on encrypted rows; keys passed explicitly per call.
- D16, D17: users in the database with bcrypt hashes; seeded users get initial
  passwords from `.env`; server-side sessions in an HttpOnly cookie.
- D18: users enter their own model keys in the UI; the platform never reads
  model keys from `.env`.
- D19, D24, D27, D28: team-scoped encrypted secrets referenced as
  `{{secret.NAME}}`; only the creator may change/delete a secret; values are
  write-only; secrets allowed only in request headers (rejected in URL/body at
  save time); secret values used in a request are masked as `***` in the tool
  response before it reaches the model, the stream or records.
- D20, D25: normalized provider-independent message table; runs snapshot agent
  and tool definitions at start and resume from the snapshot.
- D21, D26: approval transitions by conditional update including team check;
  only the user who started the run may approve or reject.
- D22, D23: one team per user; every member manages their own agents/tools;
  others cannot modify/delete them except by cloning; team members can see and
  run each other's agents (with their own model key); agents may use any team
  tool; cloning an agent references the same tools.

The table schema and REST API are **not** part of this review (batch B2b).

## What we want from you

For each decision (or tight group of decisions above):

1. **Verdict**: keep / keep with conditions / reconsider.
2. **Concrete risks or gaps**, as failure scenarios, especially against
   R2, R5, R6, R7 and the no-committed-secrets rule.
3. **Mitigations** that do not change the decision, if any.

Please cover at least:

- Interactions between the ownership model (D22–D24) and team isolation (R6):
  e.g. what happens when a tool owner edits or deletes a tool used by a
  teammate's agent, or by a paused run that holds a snapshot (D25); whether the
  snapshot must also capture secret references or values.
- Whether masking (D28) is sufficient: encodings (base64, URL-encoding, JSON
  escaping), partial echoes, secrets in error messages and in the request record
  shown on the approval screen.
- Associated-data choice for AES-GCM on secrets and keys when rows are cloned,
  re-keyed or rotated (D14, D15); nonce handling; what happens if the master
  key is missing or wrong at start-up.
- Session and cookie details that matter for a Next.js frontend talking to a Go
  backend (SameSite, CSRF for state-changing requests, logout/revocation).
- Seeding users from `.env` (D16) without leaking passwords to logs, and
  behaviour when the variables are missing.
- Whether D26 (only the run's starter approves) conflicts with anything in the
  case or in D23 (teammates run each other's agents).
- Anything in these decisions that the schema/API batch (B2b) or the run
  execution batch (B3) must take into account.

## Output format

Markdown. One section per decision or group, with the three items above; then
"Cross-cutting risks"; then "Items for B2b and B3". Be specific and brief. Mark
anything you are not sure about as uncertain. If you state a fact about a
library or standard, say whether you verified it or are recalling it. Do not
propose decisions for later batches as if they were made.
