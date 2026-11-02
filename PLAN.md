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
| D37 | 2026-11-02 | The schema and REST API are kept in `docs/schema.md`. (B2b) | One place for tables and endpoints; `DESIGN.md` stays within two pages. |
| D38 | 2026-11-02 | Sessions are stored as the SHA-256 hash of the session token; the token itself is never stored. (B2b) | A leaked database does not expose usable sessions. |
| D39 | 2026-11-02 | Requests for another team's resources return `404`, never `403`. (B2b) | Does not reveal that the resource exists (R6). |
| D40 | 2026-11-02 | If an agent references deleted tools, starting a run shows which tools were deleted and asks whether to continue; if the user confirms, the run starts without those tools. (B2b) | The user decides knowingly; no silent change of the agent's capabilities. |
| D41 | 2026-11-02 | Usage visibility: each member sees only their own usage; the team lead sees every member's usage and the team total. Users have a role (`member` / `lead`). (B2b) | Per-user and per-team totals (R8) without exposing teammates' usage to everyone. |
| D42 | 2026-11-02 | The run snapshot is a single JSON column on `runs`. (B2b) | Simplest; the snapshot is read and written as a whole. |
| D43 | 2026-11-02 | Deleting an agent is a soft delete like tools (D34): kept with a deleted marker, hidden from lists, still resolvable for its past runs. (B2b) | Past runs stay visible to the team (D35) and keep showing which agent they ran. |
| D44 | 2026-11-02 | Each team's lead is defined in the seed. Besides seeing all usage (D41), a lead may delete (soft delete) other members' agents in their own team. (B2b) | A team needs someone who can remove agents of other members. |
| D45 | 2026-11-02 | Consistency fixes from the Codex B2b review, accepted as a group: SSE/replay and the cancel endpoint marked provisional until B3; parameter placeholders and secret references validated separately; session rules and `404` for IDs in request bodies written into the API; responses built from allowlisted fields, credential request bodies never logged; D33 masking applied before storage, model delivery and events on every output path, using the values actually sent; soft deletes never cascade; per-endpoint authorization table; real open points listed. (B2b) | These follow from D17, D27, D30, D32, D33, D34, D38, D39, D43. |
| D46 | 2026-11-02 | Agents get their tools through `tool_ids` in `POST`/`PUT /agents`; existing links to deleted tools may stay, new ones are rejected. Team consistency is enforced in the database with composite foreign keys on `(id, team_id)` and a trigger that rejects linking a deleted tool. (B2b) | R1 needs an API path for tools; R6 isolation should not depend on handlers alone. |
| D47 | 2026-11-02 | Schema choices ratified: usernames unique, secret names unique per user, `usage_status` kept so unknown usage is not read as zero. Removed: `cloned_from_id`, and `user_id` / `team_id` / `agent_id` on `model_calls` (read through `run_id`). (B2b) | Keep what a decision needs; derived fields cannot disagree with the run. |
| D48 | 2026-11-02 | Invalid model calls are recorded: `tool_calls.tool_id` may be null (unknown tool); the name and raw argument text as sent by the model are stored (masked); parsed arguments may be null when the JSON is malformed. (B2b) | R3: rejected calls must be recordable. |
| D49 | 2026-11-02 | Tool names are unique within an agent; attaching a second tool with the same name is rejected. (B2b) | The model calls tools by name. |
| D50 | 2026-11-02 | A team may have more than one lead; the seed defines one. (B2b) | No constraint needed. |
| D51 | 2026-11-02 | A same-team resource the caller may not change returns `403` (cross-team stays `404`, D39). (B2b) | Within a team the resource is visible anyway. |
| D52 | 2026-11-02 | Cloning an agent drops its deleted tools and lists them in the response; deleted agents and tools cannot be cloned. Deleted agents and tools stay readable by ID (read-only, marked deleted). (B2b) | Past runs must be able to show them (D35, D43). |
| D53 | 2026-11-02 | In run details, token counts are shown only to the run's starter and the team lead. (B2b) | Resolves the overlap between D35 (team sees runs) and D41 (members see only their own usage). |
| D54 | 2026-11-02 | `PUT /me/secrets/{id}` changes only the value; changing name or host means delete and re-add (D29 applies). Sensitive headers (`Authorization`, `X-API-Key`, `Cookie`, and similar) accept only `{{secret.NAME}}`, never a literal value. (B2b) | Renames would change lookups and approvals; literal tokens would spread through shared tools, clones and snapshots. |
| D55 | 2026-11-02 | Tool calls have an `invalidated` status with a reason (D29). Binding a replacement secret increments the call's `approval_revision`; approve/reject requests carry the revision and stale ones are refused. (B2b) | A stale browser tab must not approve a changed request. |
| D56 | 2026-11-02 | D40 contract: the first run request returns `409` with the deleted tool IDs and creates no run; the confirmation resends them in `confirm_missing_tool_ids`; if the set changed meanwhile, `409` again. (B2b) | The user confirms exactly what they saw. |
| D57 | 2026-11-02 | `runs.final_output` is a copy of the last assistant message for listing; `run_messages` is authoritative. Indexes are chosen by the agent during implementation, starting from those listed in `docs/schema.md`. (B2b) | Simple listing; index choice delegated by the author. |
| D58 | 2026-11-02 | Deleting an agent asks for confirmation in the UI, like deleting a tool (D34). | Prevents accidental deletion. |
| D59 | 2026-11-02 | Runs are executed by background workers: creating a run stores it as `queued`; a worker claims it from the database with a lease (time-limited, renewed ownership) and is woken immediately. (B3) | Restart recovery, multiple replicas and no double execution. |
| D60 | 2026-11-02 | Status labels — run: `queued`, `running`, `waiting_approval`, `succeeded`, `failed`, `cancelled`; tool call: `invalid`, `pending_approval`, `rejected`, `invalidated`, `executing`, `succeeded`, `failed`, `outcome_unknown`. (B3) | R4, R5, D29, D48, D55. |
| D61 | 2026-11-02 | Closing the page does not stop a run; it continues independently and reaches a final state; the user can come back and follow it. (B3) | R4. |
| D62 | 2026-11-02 | Runs left `running` after a restart are taken over by a worker when their lease expires and continue from the last persisted step; an interrupted model call is made again. Pending approvals keep waiting. (B3) | R4, R5; messages are persisted (D20). |
| D63 | 2026-11-02 | A side-effecting tool call with an unknown outcome (crash or timeout while executing) is marked `outcome_unknown`, never retried automatically; the model is told the outcome is unknown and was not retried, the user sees it, and the run continues. Only calls without side effects (`GET`, `HEAD`) may be retried automatically, once. (B3) | Avoids duplicate side effects such as opening the same issue twice. |
| D64 | 2026-11-02 | Parallel tool calls are disabled at the provider (`parallel_tool_calls: false` for OpenAI, `disable_parallel_tool_use` for Anthropic): at most one tool call per model turn. (B3) | Tools may need each other's results; parallel calls would complicate approval and ordering. |
| D65 | 2026-11-02 | Pending approvals have no time limit; they wait indefinitely and survive restarts. (B3) | Sufficient for the case; noted for `DESIGN.md` as a scaling point. |
| D66 | 2026-11-02 | Cancelling a running run is implemented. The run's starter **and** the team lead may cancel; the UI asks for confirmation ("Are you sure?"). Cancelling stops the in-flight model call and HTTP request immediately and closes pending approvals; a side-effecting call executing at that moment becomes `outcome_unknown`. (B3) | R4's `cancelled` state; nice-to-have. |
| D67 | 2026-11-02 | Live streaming uses SSE; approvals, rejections and cancellation are ordinary `POST` requests. (B3) | One-way stream is enough; works with the session cookie and reconnects with `Last-Event-ID`. |
| D68 | 2026-11-02 | The browser talks only to Next.js, which forwards `/api/*` to Go (single origin, no CORS). If the proxy buffers SSE, only `/events` is moved to a direct connection. (B3) | Simplest auth and cookie setup. |
| D69 | 2026-11-02 | Events are written to `run_events` first, then delivered to SSE connections by an in-process notification plus a short-interval database poll. (B3) | The database is the source of truth; polling lets a browser connected to another replica receive events. |
| D70 | 2026-11-02 | Model text deltas are coalesced into batches of about 250 ms before being stored as events. Tentative: the author may switch to one event per delta. (B3) | Live enough, bounded database writes, full text on reconnect. |
| D71 | 2026-11-02 | An open SSE stream checks the session at each heartbeat (about 15 s) and closes when the session is no longer valid (logout or expiry). (B3) | A revoked session must not keep receiving data. |
| D72 | 2026-11-02 | When the run's starter rejects a tool call, the run continues: the model receives the rejection plus the starter's optional reason, and **must** propose a new tool call (enforced by requiring a tool call on the next model turn: OpenAI `tool_choice: "required"`, Anthropic `tool_choice: {type: "any"}`). Repeated rejections are bounded by the starter cancelling or the step limit. (B3) | The rejected step gets corrected instead of silently dropped. |

## Planning batches

Open decisions are grouped into batches. For each batch: the agent lays out the
options and their trade-offs → the author decides → the batch is sent to
`gpt-6.1-sol` for review → the author finalises → decisions are recorded above.

| Batch | Topic | Open decisions | Status |
| --- | --- | --- | --- |
| B1 | Stack and foundations | ~~backend language~~ (D5); ~~database and driver~~ (D6); ~~UI approach~~ (D7); ~~second model provider~~ (D8); ~~agent layer~~ (D9) | Closed; reviewed (`docs/reviews/2026-11-02-B1/`) |
| B2 | Data model, identity and secrets | (entities, schema and API shape moved to B2b); login and seeded users (no committed passwords); model-key encryption algorithm and master-key source (D12); how development keys in `.env` reach the platform; tool secret storage and injection; which conversation, call, event and configuration snapshots survive approval/restart; ownership checks and concurrent approval updates | Closed; reviewed (`docs/reviews/2026-11-02-B2/`) |
| B2b | Schema and API | `docs/schema.md` (D37–D57) | Closed; reviewed (`docs/reviews/2026-11-02-B2b/`) |
| B3 | Run execution | **Part 1 decided (D59–D72):** workers with leases, statuses, page close, restart recovery, unknown outcomes, one tool call per turn, approval wait, rejection, cancellation, SSE, Next.js proxy, event delivery, delta batching, session check on streams. **Part 2 open:** OpenAI API surface (Responses or Chat Completions) and supported models; error classes, retry owner (SDK built-in retries vs ours) and backoff (D10; note OpenAI returns HTTP 429 also for `insufficient_quota`); deadlines for model calls, tool calls and whole runs; step limit, invalid-call allowance, tool response size limit; HTTP destination and redirect rules, host normalisation, final secret-host check, template escaping (D11, D32); usage recording for interrupted streams and per provider attempt; SQLite connection/writer pattern, WAL, busy timeout, FK enforcement per connection; how a replacement secret is bound to an invalidated call (D55); model-key replacement or deletion during a paused run. **Then:** Codex review of all of B3. | Part 1 decided; part 2 open |
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

Stopped on 2026-11-02 after committing `12babd3` (pushed). To resume:

1. Read `AGENTS.md` (protocol: the author decides, the agent implements), this
   file, `docs/schema.md` and `AI_USAGE.md`.
2. **B3 part 2**: the agent presents options (with a recommendation per point,
   as in part 1) for the "Part 2 open" items in the B3 row above. The author
   decides; decisions are recorded as the author's own (not as accepted AI
   proposals, see `AI_USAGE.md`).
3. Send the whole of B3 (D59 onwards) to Codex `gpt-6.1-sol`, effort `high`,
   prompt and answer under `docs/reviews/<date>-B3/`; summarise findings; the
   author accepts or rejects each; record in `PLAN.md` and `AI_USAGE.md`.
4. Then B4 (example agent and verification), then B5 (scope, schedule, process).
   No code has been written yet.

Pending author inputs: deadline and budget cap (top of this file); keys go in
the git-ignored `.env` (`DEV_OPENAI_API_KEY`, `DEV_ANTHROPIC_API_KEY`) for the
author's own use and are entered through the UI (D18).
