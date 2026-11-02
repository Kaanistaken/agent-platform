# Plan

Working plan for the AI Platform Engineer take-home case. All technical
decisions are made by the author; the coding agent implements them
(see `AGENTS.md`). This file is the source of truth for decisions, requirement
status and next steps; `DESIGN.md` is compiled from it.

- Start: 2026-11-02 (assumed: repository creation date). Time box: 7 days.
- Deadline: _not yet recorded_.
- Budget cap for model calls: _not set_.

## Decisions

| # | Date | Decision | Rationale |
| --- | --- | --- | --- |
| D1 | 2026-11-02 | All technical decisions are made by the author; the coding agent implements them and asks when a choice is not covered by a recorded decision. | This is a case study that assesses the author. |
| D2 | 2026-11-02 | OpenAI API is the primary model provider. Real calls use the author's own key. | The case does not expect spending money, but does not forbid it. |
| D3 | 2026-11-02 | `DESIGN.md` and `PLAN.md` are written in English. | Reviewers read both; the case is in English. |
| D4 | 2026-11-02 | Plan decisions are reviewed in batches with Codex (`gpt-6.1-sol`). Prompts and answers are kept in the repository under `docs/reviews/`. Reasoning effort `high` for every batch unless the author changes it. | Independent second opinion; the reviews also document AI usage for `AI_USAGE.md`. |
| D5 | 2026-11-02 | Backend in Go. (B1, before Codex review) | Case preference; the reviewers' own stack. |
| D6 | 2026-11-02 | Database: SQLite with the pure-Go driver `modernc.org/sqlite`. (B1, before Codex review) | No CGO / C compiler needed; keeps setup short. |
| D7 | 2026-11-02 | UI in Next.js. It stays; the setup will be kept under 10 minutes. Codex's suggestion of an early setup/streaming spike was not adopted. | The reviewers' own stack; the author is confident setup stays well under 10 minutes. |
| D8 | 2026-11-02 | Model providers: OpenAI API is the main provider; Anthropic API is the second, runnable provider. The author keeps development keys in the git-ignored `.env` only for personal reference; they reach the platform by being entered in the UI like any user's key (D18). | R8 requires two providers; Anthropic's different tool-call format actually exercises the provider abstraction. |
| D9 | 2026-11-02 | Agent layer: own agent loop in Go, using the official SDKs `openai-go` and `anthropic-sdk-go` behind a thin provider adapter. No agent framework, no Python service. (B1, before Codex review) | The assessed behaviour (failure handling, final states, durable approval, per-user keys, metering, cancellation) sits around the loop and has to be built either way; one process keeps secrets and run state in one place, keeps resume-after-restart simple, avoids a third runtime and keeps every line explainable. Cost: provider adapters are ours; chaining, MCP and tracing are not free. Alternative considered: Python service on the OpenAI Agents SDK. |
| D10 | 2026-11-02 | Retries and timeouts depend on the cause of the error. Errors that cannot succeed on retry (e.g. invalid key, account without credit) fail fast with a clear message; transient errors (e.g. rate limiting, temporary provider/network failure) may be retried; tool-use errors are handled in the loop (e.g. returned to the model). Exact classes, limits and the single retry owner (SDK built-in retries vs. ours) are decided in B3. | Retrying a call that cannot succeed only wastes time and money; errors the model can fix should go back to the model. |
| D11 | 2026-11-02 | Tool HTTP templates must not be usable to reach unintended destinations or leak injected tool secrets (e.g. via configurable URLs or redirects). Mechanism decided in B2/B3. | Codex B1 finding; accepted by the author. |
| D12 | 2026-11-02 | Model keys are stored per user in the database, never in a shared place, and passed explicitly on every provider call; the platform never relies on SDK environment-variable defaults. Keys are encrypted at rest with reversible encryption and decrypted only in memory at call time (a hash would make the key unusable). Algorithm and master-key source: B2. | R7; Codex B1 finding (SDKs fall back to environment keys). |
| D13 | 2026-11-02 | Raw Codex transcripts (`codex-run.log`) are git-ignored; only `prompt.md` and `answer.md` are committed. `AI_USAGE.md` is kept up to date during the work. | Transcripts contain tool internals and local paths; the prompt/answer pair is the useful record. |
| D14 | 2026-11-02 | Model keys and tool secrets are encrypted with AES-256-GCM (Go standard library), a random nonce per value, and the row identity (e.g. user and provider) bound as associated data. (B2, before Codex review) | Standard AEAD, no extra dependency; associated data prevents moving a ciphertext to another row. |
| D15 | 2026-11-02 | The master encryption key comes from an environment variable (`.env`); the README explains how to generate it. Encrypted rows carry a key version. (B2, before Codex review) | Explicit source, kept apart from the database file; key version allows rotation later. |
| D16 | 2026-11-02 | Users live in the database with bcrypt password hashes. Predefined users are seeded; their initial passwords come from `.env`, never from the repository. (B2, before Codex review) | Simple login allowed by the case; no committed passwords. |
| D17 | 2026-11-02 | Sessions are stored server-side and carried in an HttpOnly cookie. (B2, before Codex review) | Revocable; works with the browser's native `EventSource`, which cannot send custom headers. |
| D18 | 2026-11-02 | Users enter their own model keys in the UI; keys are encrypted and stored in the database. The platform never reads model keys from `.env`. (B2, before Codex review) | Cleanest fit for R7; same path the reviewers will use. |
| D19 | 2026-11-02 | **Superseded by D32.** Tool secrets live in a team-scoped, encrypted secrets table and are referenced from templates (e.g. `{{secret.NAME}}`). (B2, before Codex review) | One secret can be reused by several tools in a team. |
| D20 | 2026-11-02 | Conversation history is stored in a normalized, provider-independent message table; resuming a run loads it and continues the loop. (B2, before Codex review) | Provider-independent and testable. |
| D21 | 2026-11-02 | Approval state changes use conditional updates that check, in the same statement, the call's pending state, the run's eligible state (e.g. not cancelled), the team and the run's starter (D26); only the update that affects one row wins. Amended after Codex B2 review. | Prevents competing approve/reject transitions and works across replicas. It does **not** by itself guarantee an external side effect happens exactly once; that needs the execution-claim and ambiguous-outcome decisions in B3. |
| D22 | 2026-11-02 | Each user belongs to exactly one team. Every team member can create and manage their own agents and tools. A user cannot delete or modify another user's agent or tool; to change it they clone it into their own account. (B2, before Codex review) | Ownership per user inside team-level isolation. |
| D23 | 2026-11-02 | Team members can see and run each other's agents; a run always uses the model key of the user who starts it (R7). An agent may use any tool of its team, including tools owned by other members. Cloning an agent keeps references to the same tools (tools are not copied). (B2, before Codex review) | Team-level sharing as in the case; ownership only limits who may modify or delete. |
| D24 | 2026-11-02 | **Superseded by D32.** Any team member can add team secrets; only the member who added a secret can change or delete it. A secret's value is write-only: once stored it cannot be read back through any path. (B2, before Codex review) | Same ownership rule as agents and tools (D22); write-only values limit leakage. |
| D25 | 2026-11-02 | A run snapshots the agent and tool definitions at start; a paused run resumes with that snapshot, so the approved request cannot change underneath the user. (B2, before Codex review) | Owners may edit definitions while a teammate's run is waiting for approval. |
| D26 | 2026-11-02 | Only the user who started a run can approve or reject its pending tool calls. (B2, before Codex review) | The person whose key and intent drive the run decides its side effects. |
| D27 | 2026-11-02 | Tool secrets may be used only in request headers; `{{secret.*}}` in the URL or body is rejected when a tool is saved. (B2, before Codex review) | Smallest leakage surface: URLs and bodies end up in logs, errors and redirects more easily. Cost: APIs that require a key in the query or body cannot be connected. |
| D28 | 2026-11-02 | **Amended by D33.** Before a tool response reaches the model, the event stream or any record, the values of the secrets used in that request are replaced with `***`. (B2, before Codex review) | Some APIs echo request headers back; without masking the secret would reach the model (R2) and the records. |
| D29 | 2026-11-02 | Run snapshots hold stable secret IDs, never secret values. At execution the current value is used. If a secret used by a pending call is deleted, the approval is invalidated and the user is asked for a new secret, then approves again. (B2, after Codex review) | Plaintext must not enter snapshots; the run follows credential replacement; a deleted credential must not be silently substituted. |
| D30 | 2026-11-02 | CSRF: session cookie with `SameSite=Lax`, and the `Origin` header is checked on every state-changing request. Logout deletes the session server-side. GET requests never change state. (B2, after Codex review) | HttpOnly alone does not stop CSRF; simple and sufficient for this setup. |
| D31 | 2026-11-02 | Conditions accepted from Codex B2 review: (1) AES-GCM associated data has an explicit, versioned format built from immutable row IDs; copying encrypted material to a new identity decrypts and re-encrypts; a fresh random nonce on every encryption. (2) The server refuses to start if the master key is missing, malformed or wrong (checked by decrypting a known value). (3) Seeding never resets existing users' passwords, reports missing variables by name only, and rejects (never truncates) passwords over bcrypt's 72-byte limit. (4) Local database, WAL and backup files are added to `.gitignore` once their paths are known. | Codex B2 findings; accepted as a group by the author. |
| D32 | 2026-11-02 | Tool secrets belong to **users**, not teams. A user binds only their own secrets; a teammate's tool can be used, but always with the user's own secret. Each secret is bound to an allowed host when it is added (e.g. `GITHUB_TOKEN` → `api.github.com`); the platform attaches it only to requests whose host equals that host, otherwise the call fails with a clear error. In a run, the secret of the user who **started** the run is used (same rule as model keys, R7); if they have no secret for that host the call fails with a clear error. Supersedes D19 and D24. (B2, after Codex review) | Codex B2 finding: with team secrets, a teammate could point a tool at their own server and receive another member's secret. Per-user secrets stop using someone else's secret; host binding stops sending your own secret to a host chosen by the tool's owner, including after the tool's URL is changed. |
| D33 | 2026-11-02 | Secret-free by construction, masking as backup. (1) Approval previews, stream events and stored request records are built from the request **before** secrets are attached; secret headers show a placeholder/`***`. Secrets are added only in memory at the final send step, and the expanded request is never serialized. (2) Masking of tool responses (D28) stays as defence in depth and covers the response body, response headers and error messages, matching the exact secret value plus its base64 and URL-encoded forms. Codex's suggestion to withhold "suspicious" responses is not adopted. (B2, after Codex review) | A preview built from our own data does not need masking if secrets never enter it. With per-user, host-bound secrets (D32) the remaining echo risk is a legitimate API reflecting its request, which simple masking covers. |
| D34 | 2026-11-02 | Deleting a tool is a soft delete: the tool is kept with a deleted marker, and the UI asks for confirmation before deleting. (B2b) | Avoids dangling references from agents and snapshots; prevents accidental deletion. |
| D35 | 2026-11-02 | Team members can see each other's runs and run history (within their own team only, R6). (B2b) | Team-level visibility, consistent with D23. |
| D36 | 2026-11-02 | Commit messages are written in English. The GitHub repository is private (case deliverable); the author switches its visibility. | Reviewers read the history; the case asks for a private repository. |

## Planning batches

Open decisions are grouped into batches. For each batch: the agent lays out the
options and their trade-offs → the author decides → the batch is sent to
`gpt-6.1-sol` for review → the author finalises → decisions are recorded above.

| Batch | Topic | Open decisions | Status |
| --- | --- | --- | --- |
| B1 | Stack and foundations | ~~backend language~~ (D5); ~~database and driver~~ (D6); ~~UI approach~~ (D7); ~~second model provider~~ (D8); ~~agent layer~~ (D9) | Closed; reviewed (`docs/reviews/2026-11-02-B1/`) |
| B2 | Data model, identity and secrets | (entities, schema and API shape moved to B2b); login and seeded users (no committed passwords); model-key encryption algorithm and master-key source (D12); how development keys in `.env` reach the platform; tool secret storage and injection; which conversation, call, event and configuration snapshots survive approval/restart; ownership checks and concurrent approval updates | Closed; reviewed (`docs/reviews/2026-11-02-B2/`) |
| B2b | Schema and API | tables and columns derived from D14–D33; REST endpoint draft; ~~tool deletion~~ (D34); ~~visibility of teammates' runs~~ (D35); reviewed separately with Codex | Open |
| B3 | Run execution | agent loop and limits (steps, timeouts, output size); tool templating and validation, destination/redirect rules (D11); live streaming protocol (SSE or WebSocket), through Next.js or directly to Go, stream authentication, reconnect; run lifecycle and final states; behaviour when the page is closed; recovery after restart and runs found active after restart; approval flow, rejected approvals; error classes, retry owner, deadlines and limits (D10); side-effecting tool call with ambiguous outcome; OpenAI API surface (Responses or Chat Completions) and supported models; multiple tool calls per turn; malformed arguments and truncation; usage accounting for interrupted streams; SQLite connection/writer pattern, WAL, busy timeout | Open |
| B4 | Example agent and verification | which example agent; its two real tools and authorised test target; testing approach, adapter fixtures, restart/concurrency tests; what proves both providers work; recorded run under `examples/`; screen-recording script | Open |
| B5 | Scope, schedule and process | which nice-to-haves (if any); day-by-day schedule including time for one full end-to-end integration; toolchain/dependency versions and supported OSes; when cold setup is measured; branch and merge flow; language of README, AI_USAGE and code comments (commits: D36) | Open |

## Requirement status

| # | Requirement | Status | Evidence |
| --- | --- | --- | --- |
| R1 | Agents are data | Not started | |
| R2 | Tools are HTTP request templates | Not started | |
| R3 | Agent loop handles failure | Not started | |
| R4 | Runs stream live and always end in a final state | Not started | |
| R5 | Risky steps wait for approval; pending approval survives restart | Not started | |
| R6 | Teams are isolated (server-enforced) | Not started | |
| R7 | Each user brings their own model key | Not started | |
| R8 | Provider-agnostic and metered | Not started | |
| EA | Example agent, end to end | Not started | |

## Deliverables

| Deliverable | Status |
| --- | --- |
| Git history | In progress |
| `README.md` | Not started |
| `DESIGN.md` (≤ 2 pages) | Skeleton |
| `AI_USAGE.md` | Started; updated as the work happens |
| Tests (agent loop, tool templates, team permissions, approval flow) | Not started |
| Recorded run under `examples/` | Not started |
| Screen recording (≤ 5 min, includes approval) | Not started |

## Next step

Batch B2b (schema and API): the agent drafts tables and endpoints from the recorded
decisions; the author decides the open points; then review with `gpt-6.1-sol`.
