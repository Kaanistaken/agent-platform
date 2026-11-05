# Schema and API

Database tables and REST endpoints, derived from the decisions in `PLAN.md`
(D-numbers in brackets). Status: **decided (B2b), reviewed with Codex**
(`docs/reviews/2026-11-02-B2b/`); **updated for B3** (run execution, D59–D98,
reviewed in `docs/reviews/2026-11-02-B3/`); not yet implemented. Changes to this
file follow recorded decisions only.

## Tables (SQLite)

Foreign keys are enforced on every connection; pragmas are set in the DSN and
verified at start-up (D82, D94). Team consistency is enforced in the database,
not only in handlers: parents expose `UNIQUE (id, team_id)` and children
reference them with composite foreign keys (D46). Soft deletes set `deleted_at`
only; no destructive `ON DELETE CASCADE` on agents, tools, runs or their
children (D45).

| Table | Columns and keys | Decisions |
| --- | --- | --- |
| `teams` | **id** PK, name | R6 |
| `users` | **id** PK, team_id → teams, username (unique), password_hash (bcrypt), role (`member` / `lead`; set in the seed; several leads allowed), created_at; `UNIQUE (id, team_id)` | D16, D22, D41, D44, D50 |
| `sessions` | **token_hash** PK (SHA-256 of the token; the token is never stored), user_id → users, created_at, expires_at | D17, D30, D38 |
| `user_model_keys` | **(user_id, provider)** PK, ciphertext, nonce, key_version, aad_version, updated_at | D12, D14, D18 |
| `user_secrets` | **id** PK (never reused), user_id → users, name (unique per user), allowed_host (normalised, D81), ciphertext, nonce, key_version, aad_version, updated_at | D32, D47, D54 |
| `agents` | **id** PK, team_id, owner_id, name, system_prompt, provider, model (one of the supported models, D78), deleted_at, created_at, updated_at; `(owner_id, team_id)` → `users (id, team_id)`; `UNIQUE (id, team_id)` | R1, D22, D43, D46 |
| `tools` | **id** PK, team_id, owner_id, name (model-visible identifier), description, method, url_template, headers_template (JSON), body_template, params_schema (JSON), requires_approval, deleted_at, created_at, updated_at; `(owner_id, team_id)` → `users (id, team_id)`; `UNIQUE (id, team_id)` | R2, D27, D34, D46 |
| `agent_tools` | **(agent_id, tool_id)** PK, team_id; `(agent_id, team_id)` → agents, `(tool_id, team_id)` → tools; a trigger rejects inserting a link to a deleted tool | D23, D46 |
| `runs` | **id** PK, team_id, agent_id, started_by, task, status, snapshot (one JSON column), final_output (copy of the last assistant message; `run_messages` is authoritative), error_code, error_message (masked), step_count, invalid_call_streak, active_ms (active time used so far), cancelled_by (nullable), created_at, started_at, finished_at; `(agent_id, team_id)` → agents, `(started_by, team_id)` → users, `(cancelled_by, team_id)` → users; `UNIQUE (id, started_by)` | R4, D25, D29, D42, D46, D57, D66, D74, D80, D94 |
| `run_messages` | **(run_id, seq)** PK, role, content, tool_calls (JSON), tool_call_id, raw_content (JSON: the provider's own content for this message, sent back verbatim), created_at. Only accepted, complete model output is stored here; discarded attempts never are. | D20, D92, D96 |
| `tool_calls` | **id** PK, run_id → runs, provider_call_id, tool_id (nullable: unknown tool), requested_name (as sent by the model, masked), raw_arguments (text as sent, masked), arguments (parsed JSON, nullable when malformed), status, reason_code (nullable), rejection_note (starter's optional text), approval_revision, approval_expires_at (nullable), request_preview (secret-free JSON), response (masked, truncated), attempt_count, decided_by, decided_at, started_at, finished_at, created_at, updated_at; `(run_id, decided_by)` → `runs (id, started_by)` when decided | R3, R5, D21, D26, D33, D48, D55, D63, D86, D90 |
| `run_events` | **(run_id, seq)** PK, type, payload (JSON), created_at. `seq` is assigned in the same transaction that writes the change it reports. | R4, D67, D69, D94, D98 |
| `model_calls` | **id** PK, run_id → runs, turn (the run's step number), attempt (1–4), provider, model, outcome, input_tokens (nullable), output_tokens (nullable), usage_status (`complete` / `partial` / `unavailable`), error_code, started_at, finished_at (null while in flight). One row per provider attempt. User, team and agent are read through `run_id`. | R8, D47, D75, D79, D88 |
| `key_check` | **key_version** PK, ciphertext, nonce, aad_version — detects a wrong master key at start-up | D31 |
| `schema_migrations` | **version** PK, applied_at | — |

Indexes are chosen during implementation (D57), starting from
`agent_tools (tool_id)`, team-scoped run listing by time, `runs (status)` for
the start-up scan, `tool_calls (run_id, status)`, `tool_calls (status,
approval_expires_at)` and usage queries.

### Snapshot (`runs.snapshot`)

Agent definition and the definitions of its non-deleted tools at run start (D25,
D40), plus a binding for every secret reference: `{{secret.NAME}}` → the
**starter's** `user_secrets.id`, checked against `started_by` (D29, D32). Never
secret values, ciphertext or other users' secret metadata. The only later
change to a snapshot is replacing a deleted secret's ID with its replacement
(D87).

## Statuses and transitions

### Runs (D60)

`queued`, `running`, `waiting_approval`, `succeeded`, `failed`, `cancelled`.

| From | To | When |
| --- | --- | --- |
| `queued` | `running` | a worker picks the run up (D59, D84) |
| `running` | `waiting_approval` | the model proposes a call to a tool that requires approval |
| `waiting_approval` | `running` | the call is approved, or rejected by the starter (the model gets the rejection, D89) |
| `running` | `succeeded` | the model gives a complete final answer (on turn 20 at the latest, D94) |
| `running` | `failed` | permanent provider error, retries exhausted, `Retry-After` above 60 s, incomplete output, step limit, invalid-call limit, run deadline, missing model key (D74, D79, D80, D88, D94) |
| `queued`, `running`, `waiting_approval` | `cancelled` | cancel by the starter or a team lead (D66, D97), or approval expiry (D90) |

- A terminal transition is one conditional update on a non-terminal run that
  sets `finished_at`, `error_code` and writes the final event in the same
  transaction; terminal states never change (D94).
- On start-up, `queued` and `running` runs are resumed from their last
  checkpoint; `waiting_approval` runs keep waiting (D62, D84). Time while the
  server is down is not counted in `active_ms`.
- `error_code` values: `provider_error` (permanent), `quota_exhausted`,
  `rate_limited`, `retries_exhausted`, `incomplete_output`, `context_too_long`,
  `step_limit`, `invalid_tool_calls`, `run_deadline`, `model_key_missing`,
  `cancelled`, `approval_expired`, `internal`.

### Tool calls (D60, D86)

`invalid`, `pending_approval`, `approved`, `rejected`, `invalidated`,
`executing`, `succeeded`, `failed`, `outcome_unknown`.

| From | To | When |
| --- | --- | --- |
| (proposed) | `invalid` | unknown tool, malformed JSON or schema violation; the error goes back to the model (D48, D80) |
| (proposed) | `pending_approval` | the tool requires approval; `approval_expires_at` is set (D90) |
| (proposed) | `approved` | the tool does not require approval (`decided_by` stays null) |
| `pending_approval` | `approved` / `rejected` | the starter decides with the current `approval_revision` (D21, D26, D55) |
| `pending_approval`, `approved` | `invalidated` | a bound secret or the model key is deleted (D29, D77, D86) |
| `pending_approval`, `approved` | `pending_approval` (revision + 1) | the starter's model key is replaced (D77, D94) |
| `invalidated` | `pending_approval` (revision + 1) | every missing prerequisite is restored, in any order: a secret with the same name and host (D87), a model key; each restoration re-checks every invalidated call, and a call still missing something keeps waiting with the reason that is still true (D94, D124) |
| `pending_approval`, `approved`, `invalidated` | `rejected` | approval expiry (`approval_expired`) or run cancellation (`run_cancelled`); expiry is checked inside every decision and claim, not only by the sweep, so an expired request is never decided or sent (D90, D124) |
| `approved` | `executing` | a worker claims the call in one conditional update after re-checking the secrets and the model key (D86) |
| `executing` | `succeeded` | an HTTP response was received (any status code; it goes to the model), also when reading its body failed afterwards: the status is kept and the text notes the failure (D99, D124) |
| `executing` | `failed` | no response and the request was not sent (e.g. host check, missing secret, refused address), or a `GET` failed after its one retry (D91). `attempt_count` is incremented and committed before every send, so a `GET` has two sends in total across restarts; with none left it fails (`interrupted`). The tool client speaks HTTP/1.1 only and does not reuse connections, so Go never re-sends a request inside one counted attempt (D91, D94, D124, D125) |
| `executing` | `outcome_unknown` | a non-`GET` call was interrupted by a crash, timeout or cancellation after it may have been sent; never retried (D63). On cancellation this happens in the cancelling transaction, before `run_finished` (`run_cancelled`); a late HTTP completion does not change it (D124) |
| `executing` | `failed` (`run_cancelled`) | a `GET` being sent when the run is cancelled (D124) |

`reason_code` values: `secret_deleted`, `model_key_changed`, `model_key_deleted`,
`rejected_by_starter`, `approval_expired`, `run_cancelled`, `unknown_tool`,
`malformed_json`, `schema_violation`, `host_mismatch`, `secret_missing`,
`address_refused`, `timeout`, `interrupted`.

`approval_expires_at` is set once, when the call first waits for the starter
(`pending_approval` or `invalidated`, including a call of a tool without
approval that is re-opened or invalidated later), and is never moved: 24 hours later, moved to Monday at the same time
if that falls on a Saturday or Sunday, in server local time (D90).

### Model-call attempts (D75, D79, D88)

`outcome`: `succeeded`, `retried` (transient error, another attempt follows),
`failed`, `discarded` (stream broke mid-way, partial output dropped, D92),
`interrupted` (found unfinished at start-up; usage `unavailable` unless reported).
Usage reported before an interruption is kept; Anthropic's streamed counters are
cumulative, so the latest value is stored (D94).

## Data rules

- **Deleted agents and tools** stay readable through `GET /{id}` (read-only,
  marked deleted) and in past runs; they are hidden from lists; deleted tools
  cannot be attached to agents; deleted sources cannot be cloned (D34, D43, D52).
- **Agent tool names** must be unique within one agent; attaching a second tool
  with the same name is rejected (D49).
- **Tool templates** are validated when a tool is saved:
  - `https` only, port 443, a literal host (no placeholder in scheme, host or
    port), no userinfo, ASCII host; methods `GET`, `POST`, `PUT`, `PATCH`,
    `DELETE` (D81, D94).
  - Model parameters (`{{param}}`) are validated against `params_schema` and
    allowed only as a whole path segment, a query value, a header value or a
    whole JSON string value in the body (D93).
  - Secret references (`{{secret.NAME}}`) are validated separately and allowed
    only in headers (D27, D45). A header value with a secret is an optional
    literal prefix followed by exactly one `{{secret.NAME}}` at the end, e.g.
    `Bearer {{secret.GITHUB_TOKEN}}`; the prefix has no placeholder and no
    CR/LF, and a secret never shares a header value with a model parameter
    (D106). Sensitive headers (`Authorization`, `Proxy-Authorization`,
    `X-API-Key`, `Cookie`, and similar) must contain a secret reference, never
    a literal value alone (D54, D106).
  - `Host`, hop-by-hop headers, `Idempotency-Key` and `X-Idempotency-Key` are rejected (D94); the client checks this again before sending (D124).
- **Secrets**: `PUT` changes only the value. Changing a name or host means
  deleting the secret and adding a new one. Deleting a secret durably
  invalidates the pending and approved calls bound to it (D29, D54, D55, D86).
- **Never in a response, error or log** (D45): password hashes, session tokens
  or hashes, ciphertext, nonces, key versions of credentials, submitted
  passwords/keys/secret values. Login, model-key and secret request bodies are
  not logged. Responses are built from allowlisted fields, never by serializing rows.
- **Masking (D33, D45)** happens before anything is stored, sent to the model
  or emitted as an event: tool response body, response headers, error messages,
  `runs.error_message`. It uses the secret values actually sent with that
  request, plus their base64 and URL-encoded forms, and runs before truncation
  (D94). Assistant `raw_content` is stored and sent back unchanged (D96); it
  only contains what the model produced from already-masked input.

## REST API

All endpoints under `/api`. Acting user, team and role come from the session,
never from the request (D45).

- **Session rules (D17, D30, D38):** HttpOnly cookie, `SameSite=Lax`; `Origin`
  checked on every state-changing request; GET never changes state; logout
  deletes the session server-side.
- **Errors (D39, D51):** another team's resource — also an ID inside a request
  body — returns `404`. A same-team resource the caller may not change returns
  `403`. A state change that is no longer possible (terminal run, stale
  revision, call not in the expected state) returns `409` with the current state.

| Endpoint | Who may call it | Notes |
| --- | --- | --- |
| `POST /login` | anyone | returns no hashes; `Origin` checked |
| `POST /logout` | session | deletes only the current session |
| `GET /me` | session | id, username, team, role |
| `GET /models` | session | the supported models per provider (D78); used by the agent form |
| `GET /me/model-keys` | session (own only) | providers and "key set", never values; a lead gets no access to others' keys |
| `PUT`, `DELETE /me/model-keys/{provider}` | session (own only) | in the same transaction, the caller's pending and approved calls in runs on that provider are re-opened for approval (`PUT`) or invalidated (`DELETE`) (D77, D94) |
| `GET`, `POST /me/secrets` | session (own only) | metadata only (id, name, host, date). `POST` restores the caller's invalidated calls whose reference has the same name and host (D87) |
| `PUT /me/secrets/{id}` | owner of the secret | value only (D54) |
| `DELETE /me/secrets/{id}` | owner of the secret | invalidates bound pending and approved calls (D55, D86) |
| `GET /agents`, `GET /tools` | team | deleted rows hidden |
| `GET /agents/{id}`, `GET /tools/{id}` | team | deleted rows readable, marked deleted (D52) |
| `POST /agents` | team | body includes `tool_ids` (D46); owner and team from session; tools from the same team, not deleted, unique names; model from the supported list (D78) |
| `PUT /agents/{id}` | owner | `tool_ids` replaces the list; links that already exist to deleted tools may stay, new deleted links are rejected (D46); owner/team/deleted fields cannot be changed |
| `DELETE /agents/{id}` | owner or team lead | soft delete; UI asks for confirmation (D43, D44, D58) |
| `POST /tools` | team | template validation (above) |
| `PUT /tools/{id}` | owner | authorization first (another team's tool `404`, a teammate's `403`, whatever the body), then revalidated; past snapshots unchanged; `409` if the new name duplicates another tool (deleted ones included) linked to an agent this tool is linked to (D49, D124) |
| `DELETE /tools/{id}` | owner | soft delete; UI asks for confirmation (D34); no lead override |
| `POST /agents/{id}/clone` | team | caller becomes owner; same tool references; deleted tools are dropped and the response lists them (D52); deleted source cannot be cloned |
| `POST /tools/{id}/clone` | team | copies the definition; no credentials are copied |
| `POST /agents/{id}/runs` | team | `{task}`; starter = caller; starter's model key and secrets; missing key or secret → clear error, no fallback. If the agent has deleted tools: `409` with their IDs, no run created; resend with `{task, confirm_missing_tool_ids: [...]}`; if the set changed meanwhile → `409` again (D40, D56). Creates the run as `queued` (D59) |
| `GET /runs` | team | includes runs of deleted agents (D35) |
| `GET /runs/{id}` | team | messages, tool calls, snapshot (without secret bindings' details); token counts only for the starter and the team lead (D53) |
| `GET /runs/{id}/events` | team | SSE (D67). Each event's `id` is its `seq`. Replays events after `Last-Event-ID`, or all events when absent (a newly opened page), then continues live. Token usage events only for the starter and the team lead (D53, D94). Session checked at every heartbeat (~15 s); the stream closes when it is no longer valid (D71) and after the final event. GET never resumes work |
| `POST /runs/{id}/cancel` | run starter or a team lead | UI asks for confirmation; non-terminal run → `cancelled`, in-flight model call and HTTP request stopped, open approvals closed (`run_cancelled`), an executing non-`GET` call becomes `outcome_unknown`; terminal run → `409` (D66, D97) |
| `POST /runs/{id}/tool-calls/{callId}/approve` | run starter only | `{approval_revision}` must match (D55); atomic conditional update (D21); lead or agent owner has no approval authority; a call that is not in the run is `404` (D39); an expired request is `409` and the run is cancelled (`approval_expired`, D90) |
| `POST /runs/{id}/tool-calls/{callId}/reject` | run starter only | `{approval_revision, note?}`; the note goes to the model with the rejection (D72, D89) |
| `GET /approvals` | session | the caller's pending and invalidated approvals in non-terminal runs, with reason and expiry; previews are secret-free |
| `GET /usage` | session | member: own totals; lead: every member's totals and team total (D41). Totals count only known usage and also report how many attempts had unavailable usage, so unknown is never shown as zero (D47, D75) |

### Event types (`run_events.type`)

`run_status`, `text_delta` (coalesced ~250 ms, tagged with its model-call
attempt, D70; a timer flushes text that would otherwise wait for the next
delta), `attempt_discarded` (D92; also emitted at start-up for an attempt cut
by a stop or crash after it streamed text), `model_retry` (wait in seconds,
D79), `tool_call` (proposal with secret-free preview), `tool_call_status`
(with `approval_expires_at` when set), `tool_result` (masked, truncated),
`usage` (D53), `run_finished` (final status and `error_code`; always the last
event: text still buffered when the run finishes is dropped). Pending text is
flushed before any other event type (D94, D124).
