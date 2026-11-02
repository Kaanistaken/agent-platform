# AI usage

How AI tools were used in this project, and which of their suggestions were
rejected or corrected. All technical decisions are made by the author
(`PLAN.md`, D1); AI tools implement, propose options or review. Entries are
added as the work happens.

## Tools

| Tool | Model | Used for |
| --- | --- | --- |
| Claude Code | Claude Opus 5.5 | Adapting the agent working protocol (`AGENTS.md`, `CLAUDE.md`); laying out options and trade-offs for each planning batch; drafting `PLAN.md`, `DESIGN.md` and this file from the author's decisions; implementation (later). |
| Codex CLI | `gpt-6.1-sol` (reasoning effort `high`) | Read-only review of planning decisions, batch by batch. Prompts and answers: `docs/reviews/`. |

## Rejected or corrected suggestions

| Date | Source | Suggestion | Outcome |
| --- | --- | --- | --- |
| 2026-11-02 | Claude Code | The first protocol draft stated "no money is spent" and limited the platform to free model endpoints. | **Corrected by the author.** The case only says spending is not expected; the author chose the paid OpenAI API as the main provider (D2). |
| 2026-11-02 | Claude Code | The first protocol draft gave the agent technical autonomy and pre-decided several choices (SQLite driver, UI approach, encryption algorithm, retry policy, branch flow, document language). | **Corrected by the author.** All technical decisions are the author's; the pre-made choices were turned back into open decisions (D1). |
| 2026-11-02 | Codex (B1) | Validate the Next.js setup and authenticated streaming path with an early spike instead of re-checking setup at the end. | **Rejected by the author.** Next.js stays; the author expects setup to stay well under 10 minutes (D7). |
| 2026-11-02 | Codex (B1) | Other B1 findings: decide a single retry owner and deadlines; pass model keys explicitly because SDKs fall back to environment keys; treat tool HTTP templates as a security boundary (destinations, redirects, secret leakage); distribute open questions into later batches. | **Accepted**, with the author's refinement that retries depend on the cause of the error (D10, D11, D12; open questions added to B2–B5). |

| 2026-11-02 | Codex (B2) | Most B2 decisions: keep with conditions (AES-GCM associated-data format, fresh nonces, refuse start on a wrong master key, safe seeding, CSRF protection, stricter approval update). | **Accepted** (D21 amended, D30, D31). |
| 2026-11-02 | Codex (B2) | Team-shared tool secrets let a teammate point a tool at their own server and receive another member's secret. | **Accepted as a problem.** Fixed with the author's idea (per-user secrets, a teammate's tool is used with your own secret) combined with Claude Code's host binding (D32). |
| 2026-11-02 | Codex (B2) | Response masking (D28) is not sufficient on its own; build previews and records without secrets. | **Accepted** (D33). Codex's further suggestion to withhold "suspicious" responses was **rejected** on Claude Code's recommendation: hard to define, noisy, and the remaining risk is small after D32. |
| 2026-11-02 | Claude Code | Pointed out that the author's per-user-secret idea alone still lets a tool owner send a user's own secret to a host the owner chooses. | **Accepted**; host binding added (D32). |

## Accepted AI-proposed options

| Date | Source | Proposal | Decision |
| --- | --- | --- | --- |
| 2026-11-02 | Claude Code | When asked, recommended an own agent loop in Go with `openai-go` and `anthropic-sdk-go` over a Python service on the OpenAI Agents SDK. | Chosen by the author (D9). |
