# Code review request: output limit, setup time, sandbox rename (pre-push review, D117)

You are reviewing commits before they are pushed. You are an advisor: you do
not make decisions and you do not modify files. Work read-only. Make no
network calls, paid model calls or external writes.

## Scope

- Now under review: `0143c4d..fcc0f98` (one commit, `fcc0f98`). Diff:
  `git diff 0143c4d..fcc0f98`. Nothing relevant is uncommitted.
- Code change: one constant in `backend/internal/run/engine.go`
  (`MaxOutputTokens` 4096 → 16384) and one comment in
  `backend/internal/tool/client.go`. Everything else is documentation:
  `README.md`, `PLAN.md`, `AGENTS.md`, `AI_USAGE.md`.
- `0143c4d` was pushed without a review (D133, a one-off exception). It
  changed one sentence of `DESIGN.md` (the F01 fix of review 5) plus
  records; please check that sentence too: `git diff 6c4cb5c..0143c4d`.
- Check every commit in scope for secrets (keys, tokens, passwords, session
  cookies, encoded forms).

## Contracts

`PLAN.md` D1–D135 (the author's decisions; D134 and D135 are new),
`docs/schema.md`, `AGENTS.md` (R1–R8, EA, deliverables). README deliverable
from the case: setup and run, provider and model, how to run the example
agent, assumptions; up without Docker in a few commands, **under 10 minutes**.

## What changed

- D135 (limit review, D95/D80): `MaxOutputTokens` raised to 16,384 per model
  call, sent as `max_output_tokens` (OpenAI Responses) and `max_tokens`
  (Anthropic Messages). Hitting the limit still fails the run with
  `incomplete_output` (D94). The 128 KB tool-response limit (D80) is kept.
  Anthropic's `GET /v1/models/{id}` lists `max_tokens: 128000` for
  `claude-haiku-5-5` and `claude-sonnet-5-5`; the OpenAI limits of
  `gpt-6-luna`/`gpt-6-sol` could not be checked (the development key has
  expired). No live call has used the new value.
- D134: the sandbox repository was renamed on GitHub from
  `vodafone-case-sandbox` to `test-sandbox`. The old API path answers 301
  and the tool client does not follow redirects (D78), so `README.md` tasks
  use the new name. Records of the earlier runs (D101, D107, the 2026-11-06
  record, `examples/dependency-check/recorded-run/`) keep the old name on
  purpose. The manifest's `name` field stays `vodafone-case-sandbox` (the
  author's choice).
- D114: cold setup measured with a script from a clean clone of
  `origin/main` with empty Go module, Go build and npm caches; about
  2 minutes to a working login through the UI (record in `PLAN.md`).
  `README.md` now states this, and adds how to run the example with
  Anthropic (clone the agent in the UI, change its model), as done in run 2.

## Evidence already gathered

- Staged diff scanned for key and token patterns: clean.
- `cd backend; go vet ./...; go test ./... -count=1` pass;
  `cd web; npm run build` passes (2026-11-08).

## What we want

1. **Limit change:** is 16,384 sent everywhere the old value was, with no
   other place (tests, UI, docs, `docs/schema.md`, `DESIGN.md`) still
   stating 4096 as the current value? Any behaviour the larger value
   changes (e.g. stream handling, the D70 flush, storage of a long answer,
   token accounting)?
2. **README accuracy:** do the setup-time claim and the new Anthropic
   instructions match the code and the UI (`web/app/agents/[id]/page.tsx`:
   clone, then edit the model)? Is anything in the README now wrong after
   the rename?
3. **Records:** are `PLAN.md` (D134, D135, Next step, the setup record),
   `AGENTS.md` and `AI_USAGE.md` consistent with each other and with the
   diff? Is anything claimed as verified that was not?
4. The `DESIGN.md` sentence from `0143c4d`.

Keep wording suggestions separate from factual defects. Avoid style-only
findings.

## Output format

Markdown. One table:

| ID | Severity | Location | Contract | Trigger and consequence | Suggested fix | Decision? |

Severity: high / medium / low. "Decision?" says **needs decision** when the
fix would change a recorded behaviour, dependency or schema; otherwise "no".
