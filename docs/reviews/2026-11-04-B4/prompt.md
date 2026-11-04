# Review request: Batch B4 — example agent and verification

You are reviewing the **example agent and verification decisions** of a
take-home case, before any code is written. You are an advisor: you do not make
decisions and you do not modify files. Work read-only.

## Context

Read these files in the repository first:

- `PLAN.md` — all decisions. The ones under review are **D100–D104** (B4).
  D1–D99 are earlier, already reviewed decisions they must be consistent with
  (superseded/amended ones are marked). D84 ("core first, no over-engineering,
  one backend process") governs scope.
- `docs/schema.md` — tables and REST API, updated for B3 (D99).
- `AGENTS.md` — "Gereksinim kontrol listesi" and "Teslimatlar" summarise
  requirements R1–R8, the example agent (EA) and the deliverables (Turkish).
- `docs/reviews/2026-11-02-B3/answer.md` — your previous review; its "Items for
  B4" section lists what this batch was expected to settle.

Stack: Go backend, SQLite via `modernc.org/sqlite` (no CGO), Next.js UI
(proxying `/api/*` to Go), own agent loop with `openai-go` (Responses API) and
`anthropic-sdk-go`. Deadline 2026-11-09 for one developer; **no code exists
yet** and today is 2026-11-04, so roughly four and a half days remain for all
implementation, tests and documents. Model budget USD 5.

The case requires the example agent to use at least two real APIs as tools and
to send at least one real side-effecting step through the approval flow (e.g.
an issue, comment or gist in our own test repository), defined through the
platform (API/UI), not hard-coded. Reviewers must be able to set the platform
up in under 10 minutes without Docker, using their own model keys (R7).

## Not yet decided (do not treat as decided)

- Manifest ecosystem and sandbox contents. The agent's suggestion: an npm
  `package.json` at the repository root with 3–5 pinned, known-vulnerable
  versions. Comment on whether this fits OSV's query format and the step limit.

## What we want from you

1. **Consistency.** Where D100–D104 contradict each other, earlier decisions or
   `docs/schema.md` (cite D-numbers and table/endpoint).
2. **Example agent feasibility (D100, D101).** Can the three tools be expressed
   in the D81/D93 template grammar exactly as written (placeholders only as a
   whole path segment, query value, header value or whole JSON string value;
   `url.PathEscape` on path values; sensitive headers only via
   `{{secret.NAME}}`; forbidden headers)? Check concretely: the GitHub contents
   path (a nested path like `a/b/package.json` would be escaped to `a%2Fb...`),
   the raw `Accept` header, any `User-Agent` requirement, the OSV request body
   shape, and the issue body (multi-line Markdown as a JSON string). Estimate
   the number of model turns for a 3–5 dependency manifest against the 20-turn
   limit (D80), and the size of OSV responses against the 128 KB limit to the
   model (D80) and the 4096 output-token cap (D95). Mark anything you are not
   sure about regarding the real APIs as uncertain.
3. **Reviewer paths (D101).** Is the quick path (no GitHub token, reject at
   approval) acceptable evidence for the case, and is the full path realistic
   (fine-grained token scoped to the reviewer's own repository)? Unauthenticated
   GitHub rate limits, public-repo assumptions, and anything that would push
   setup over 10 minutes.
4. **Testing (D102).** Does the approach cover the four areas the case requires
   (agent loop, tool templates, team permissions, approval flow) and the most
   important B3 behaviours (final states, restart survival, races,
   `outcome_unknown`, secret masking, SSRF checks)? What is the smallest test
   set that gives credible evidence within the time left — name the tests you
   would write first and those you would drop. Is making the D81 address check
   replaceable "in tests only" safe (it must not be disableable in production)?
5. **Provider evidence and examples (D103, D104).** Is one manual run per
   provider enough for R8? What must the recorded run in `examples/` contain,
   and how should it be scanned for secrets before commit?
6. **Scope and schedule risk.** Given the time left, what in B3/B4 is most
   likely to overrun, and what simpler version would still satisfy R1–R8 and
   EA. Do not drop requirements.

## Output format

Markdown. Sections in the order 1–6 above, then "Items for B5" (scope,
schedule, process). For each finding: what, where (D-number / table /
endpoint), why it matters (requirement or D-number), and a suggested fix. Mark
suggestions that would need a new decision by the author as **needs decision**.
Be specific and brief; mark anything you are not sure about as uncertain. Do not
present suggestions as decisions.
