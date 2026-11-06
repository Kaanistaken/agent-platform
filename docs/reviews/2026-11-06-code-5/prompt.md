# Code review request: live validation records, recorded run, DESIGN.md (pre-push review, D117)

You are reviewing commits before they are pushed. You are an advisor: you do
not make decisions and you do not modify files. Work read-only. Make no
network calls, paid model calls or external writes.

## Scope

- Now under review: `586c850..6c4cb5c` (four commits: `5cb939c`, `e7dcebd`,
  `161e84e`, `6c4cb5c`). Diff: `git diff 586c850..6c4cb5c`. Nothing relevant
  is uncommitted. **No Go or UI code changed**; the commits touch `PLAN.md`,
  `README.md`, `.gitignore`, `DESIGN.md`, `AI_USAGE.md` and add
  `examples/dependency-check/recorded-run/` (`events.json`, `transcript.md`).
- Check every commit for secrets (keys, tokens, passwords, session cookies,
  encoded forms).

## Contracts

`PLAN.md` D1–D131 (the author's decisions; D128–D131 are new), `docs/schema.md`,
`AGENTS.md` (R1–R8, EA, deliverables). Case deliverables relevant here:
`DESIGN.md` at most two pages with architecture, decisions and alternatives,
what was deliberately left out, security risks, and what changes at several
thousand developers; `examples/` holds one recorded run (task, streamed events,
approval, final answer).

## What changed

- `5cb939c` (D128): `PLAN.md` records the live validation on 2026-11-06 — model
  IDs listed by both providers; run 1 (OpenAI `gpt-6-luna`, approved issue #1),
  run 2 (Anthropic `claude-haiku-5-5`, approved issue #2), runs 3–4 (read-only
  agent; run 4 timed SSE through the Next.js dev proxy); spend about USD 0.01
  per provider. `README.md` replaces `<owner>` with `Kaanistaken`. D128: the
  agent object in `GET /runs` carries a meaningless `requires_approval`, left
  as it is.
- `e7dcebd` (D129): `web/AGENTS.md`, written by `next dev`, is git-ignored.
- `161e84e` (D104, D110 (6)): run 1 exported through the platform API
  (`GET /api/runs/1` and its SSE stream) with a per-event-type field
  allowlist; `transcript.md` is a readable rendering. The export script was
  not committed (it lived outside the repository).
- `6c4cb5c` (D130, D131): `DESIGN.md` compiled from the decision log; `PLAN.md`
  records the live rejection run (run 5) and D130/D131; `AI_USAGE.md` updated.

## Evidence already gathered

- Both staged diffs were scanned for the `.env` values (and for the export,
  their base64 and URL-encoded forms) and for token patterns: clean. The
  author's GitHub token is stored encrypted in the platform and was only
  pattern-scanned.
- `DESIGN.md` printed to A4 (11 pt, 2 cm margins) is two pages.
- Last code test run (unchanged code): `cd backend; go test ./... -count=3`
  passes (2026-11-05).

## What we want

1. **Accuracy of `DESIGN.md`:** is every statement true of the code and
   consistent with the cited decision? Flag claims the code does not
   implement, wrong decision numbers, and anything that contradicts
   `docs/schema.md` or `README.md`. In particular check the F19 known
   limitation wording, the SSRF/HTTP-1.1 statements, the retry numbers, and
   the "Framework use" paragraph.
2. **Coverage of the case's `DESIGN.md` contents:** is a required topic
   missing or too thin to be credible?
3. **Recorded run:** does `events.json` contain anything that should not be
   published (secrets, session data, internal paths, personal data beyond the
   public GitHub account)? Does `transcript.md` faithfully render
   `events.json` (no invented or reordered events)?
4. **Records:** are `PLAN.md` statements about the live runs and the
   requirement status consistent with each other and with the recorded run?

Keep wording suggestions separate from factual defects. Avoid style-only
findings.

## Output format

Markdown. One table:

| ID | Severity | Location | Contract | Trigger and consequence | Suggested fix | Decision? |

Severity: high / medium / low. "Decision?" says **needs decision** when the
fix would change a recorded behaviour, dependency or schema; otherwise "no".
Then "Checks performed / not performed" and "Unresolved verification gaps".
Mark anything uncertain. If you find nothing, say so explicitly.
