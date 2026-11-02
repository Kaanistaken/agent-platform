# Review request: Batch B1 — stack and foundations

You are reviewing **planning decisions** for a take-home case, before any code
is written. You are an advisor: you do not make decisions and you do not modify
files. Work read-only.

## Context

The repository is the author's submission for an "AI Platform Engineer"
take-home case (7 days). Read these files in the repository first:

- `AGENTS.md` — working protocol; the section "Gereksinim kontrol listesi"
  summarises the case requirements R1–R8 and the example agent (EA), and
  "Teslimatlar" lists the deliverables. The file is in Turkish.
- `PLAN.md` — decisions D1–D9 and the planning batches B1–B5.

Key constraints from the case:

- Build a small agent platform on a real model. Agents are data (R1); tools are
  HTTP request templates with `{{parameter}}` placeholders and a JSON schema,
  and a tool's own API key is never visible to the model (R2); the agent loop
  handles unknown tools, schema-breaking parameters, malformed JSON and runaway
  loops (R3); runs stream live (SSE or WebSocket) and always end in
  succeeded / failed / cancelled, even on provider errors, timeouts or when the
  user closes the page (R4); tools marked "requires approval" pause the run and a
  pending approval survives a server restart (R5); teams are isolated and the
  server enforces it (R6); each user brings their own model key, stored
  encrypted, never in logs / records / stream, no fallback to another key (R7);
  at least two providers, model is a setting, behaves well on HTTP 429, token
  usage recorded per call with totals per user and team (R8).
- The reviewers prefer Go for the backend (their stack: Go, Next.js,
  PostgreSQL). The project must start **without Docker, with a few commands, in
  under 10 minutes**. SQLite is acceptable. Visual design is not scored. The
  assessment weights a correct, secure and simple platform layer, and how the
  author prioritises.
- The author must be able to explain and change every part of the code.

## Decisions under review (batch B1)

- **D5** Backend in Go.
- **D6** SQLite with the pure-Go driver `modernc.org/sqlite`.
- **D7** UI in Next.js; setup time is re-checked at the end and revisited if it
  breaks the 10-minute target.
- **D8** OpenAI API as main provider; Anthropic API as second runnable provider.
- **D9** Own agent loop in Go using the official `openai-go` and
  `anthropic-sdk-go` SDKs behind a thin provider adapter; no agent framework, no
  Python service. Alternative considered: a Python service on the OpenAI Agents
  SDK connected to the Go backend.

The rationale for each decision is in `PLAN.md`.

## What we want from you

For each decision D5–D9:

1. **Verdict**: keep / keep with conditions / reconsider.
2. **Risks or consequences the author may have missed**, concretely
   (e.g. a failure scenario), especially regarding the requirements R1–R8 and the
   10-minute, no-Docker setup.
3. **Mitigations** that do not change the decision, if any.

Please cover at least:

- `modernc.org/sqlite` under concurrent access from the run executor, SSE
  readers and API handlers (locking, WAL, busy timeouts, single-writer patterns),
  and on Windows.
- Next.js and live streaming: SSE through Next.js (proxy / route handlers /
  rewrites) versus the browser connecting to the Go server directly; auth and
  CORS implications; effect on setup time.
- Maturity and pitfalls of `openai-go` and `anthropic-sdk-go` for streaming with
  tool calls, usage reporting in streams, and rate-limit (429) handling,
  including the SDKs' built-in retries interacting with our own policy.
- Pitfalls of normalising OpenAI and Anthropic message / tool-call formats behind
  one adapter (tool call IDs, parallel tool calls, tool results, system prompt,
  stop reasons, partial JSON in streamed tool arguments).
- Whether any B1 decision constrains later batches in a way the author should
  know now (B2 data model and secrets, B3 run execution, B4 example agent,
  B5 scope and schedule).

Finally list **open questions** that the author should decide in later batches
because of these choices. Do not propose decisions for later batches as if they
were made.

## Output format

Markdown. One section per decision (D5–D9) with the three items above, then
"Cross-cutting risks", then "Open questions for later batches". Be specific and
brief; mark anything you are not sure about as uncertain rather than stating it
as fact. If you state a fact about a library's current behaviour or API, say
whether you verified it or are recalling it.
