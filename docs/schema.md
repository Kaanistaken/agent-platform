# Schema and API

Database tables and REST endpoints, derived from the decisions in `PLAN.md`
(D-numbers in brackets). Status: **decided (B2b), reviewed with Codex**
(`docs/reviews/2026-11-02-B2b/`); not yet implemented. Changes to this file
follow recorded decisions only. Items marked _provisional (B3)_ depend on run
execution decisions that are not made yet.

## Tables (SQLite)

Foreign keys are enforced on every connection. Team consistency is enforced in
the database, not only in handlers: parents expose `UNIQUE (id, team_id)` and
children reference them with composite foreign keys (D46). Soft deletes set
`deleted_at` only; no destructive `ON DELETE CASCADE` on agents, tools, runs or
their children (D45).

| Table | Columns and keys | Decisions |
| --- | --- | --- |
| `teams` | **id** PK, name | R6 |
| `users` | **id** PK, team_id → teams, username (unique), password_hash (bcrypt), role (`member` / `lead`; set in the seed; several leads allowed), created_at; `UNIQUE (id, team_id)` | D16, D22, D41, D44, D50 |
| `sessions` | **token_hash** PK (SHA-256 of the token; the token is never stored), user_id → users, created_at, expires_at | D17, D30, D38 |
| `user_model_keys` | **(user_id, provider)** PK, ciphertext, nonce, key_version, aad_version, updated_at | D12, D14, D18 |
| `user_secrets` | **id** PK (never reused), user_id → users, name (unique per user), allowed_host, ciphertext, nonce, key_version, aad_version, updated_at | D32, D47, D54 |
| `agents` | **id** PK, team_id, owner_id, name, system_prompt, provider, model, deleted_at, created_at, updated_at; `(owner_id, team_id)` → `users (id, team_id)`; `UNIQUE (id, team_id)` | R1, D22, D43, D46 |
| `tools` | **id** PK, team_id, owner_id, name (model-visible identifier), description, method, url_template, headers_template (JSON), body_template, params_schema (JSON), requires_approval, deleted_at, created_at, updated_at; `(owner_id, team_id)` → `users (id, team_id)`; `UNIQUE (id, team_id)` | R2, D27, D34, D46 |
| `agent_tools` | **(agent_id, tool_id)** PK, team_id; `(agent_id, team_id)` → agents, `(tool_id, team_id)` → tools; a trigger rejects inserting a link to a deleted tool | D23, D46 |
| `runs` | **id** PK, team_id, agent_id, started_by, task, status, snapshot (one JSON column), final_output (copy of the last assistant message; `run_messages` is authoritative), error_code, error_message, step_count, created_at, started_at, finished_at; `(agent_id, team_id)` → agents, `(started_by, team_id)` → users; `UNIQUE (id, started_by)` | R4, D25, D29, D42, D46, D57 |
| `run_messages` | **(run_id, seq)** PK, role, content, tool_calls (JSON), tool_call_id, provider_extra (JSON: provider data needed to continue) | D20 |
| `tool_calls` | **id** PK, run_id → runs, provider_call_id, tool_id (nullable: unknown tool), requested_name (as sent by the model, masked), raw_arguments (text as sent, masked), arguments (parsed JSON, nullable when malformed), status, invalidation_reason, approval_revision, request_preview (secret-free JSON), response (masked), decided_by, decided_at, created_at, updated_at; `(run_id, decided_by)` → `runs (id, started_by)` when decided | R3, R5, D21, D26, D33, D48, D55 |
| `run_events` | **(run_id, seq)** PK, type, payload (JSON), created_at | R4; replay/cursor contract _provisional (B3)_ |
| `model_calls` | **id** PK, run_id → runs, provider, model, input_tokens, output_tokens (nullable), usage_status (`complete` / `partial` / `unavailable`), error_code, started_at, finished_at. User, team and agent are read through `run_id`. | R8, D47 |
| `key_check` | **key_version** PK, ciphertext, nonce, aad_version — detects a wrong master key at start-up | D31 |
| `schema_migrations` | **version** PK, applied_at | — |

Statuses (labels finalised in B3):

- `runs.status` must include the terminal states `succeeded`, `failed`,
  `cancelled`, and distinguish active execution from waiting for approval (R4, R5).
- `tool_calls.status` must distinguish: waiting for approval, approved,
  rejected, **invalidated** (a bound secret was deleted, D29/D55), and execution
  outcome (attempted / succeeded / failed). Approval is not execution success.

Indexes are chosen during implementation (D57), starting from
`agent_tools (tool_id)`, team-scoped run listing by time, `tool_calls (run_id, status)`
and usage queries.

### Snapshot (`runs.snapshot`)

Agent definition and the definitions of its non-deleted tools at run start (D25,
D40), plus a binding for every secret reference: `{{secret.NAME}}` → the
**starter's** `user_secrets.id`, checked against `started_by` (D29, D32). Never
secret values, ciphertext or other users' secret metadata.

## Data rules

- **Deleted agents and tools** stay readable through `GET /{id}` (read-only,
  marked deleted) and in past runs; they are hidden from lists; deleted tools
  cannot be attached to agents; deleted sources cannot be cloned (D34, D43, D52).
- **Agent tool names** must be unique within one agent; attaching a second tool
  with the same name is rejected (D49).
- **Tool templates**: model parameters (`{{param}}`) are validated against
  `params_schema`; secret references (`{{secret.NAME}}`) are validated separately
  and allowed only in headers (D27, D45). Sensitive headers (`Authorization`,
  `Proxy-Authorization`, `X-API-Key`, `Cookie`, and similar) accept only a secret
  reference, never a literal value (D54).
- **Secrets**: `PUT` changes only the value. Changing a name or host means
  deleting the secret and adding a new one. Deleting a secret durably
  invalidates the pending calls bound to it (D29, D54, D55).
- **Never in a response, error or log** (D45): password hashes, session tokens
  or hashes, ciphertext, nonces, key versions of credentials, submitted
  passwords/keys/secret values. Login, model-key and secret request bodies are
  not logged. Responses are built from allowlisted fields, never by serializing rows.
- **Masking (D33, D45)** happens before anything is stored, sent to the model
  or emitted as an event: response body, response headers, error messages,
  `runs.error_message`, message content and `provider_extra`. It uses the secret
  values actually sent with that request, plus their base64 and URL-encoded forms.

## REST API

All endpoints under `/api`. Acting user, team and role come from the session,
never from the request (D45).

- **Session rules (D17, D30, D38):** HttpOnly cookie, `SameSite=Lax`; `Origin`
  checked on every state-changing request; GET never changes state; logout
  deletes the session server-side.
- **Errors (D39, D51):** another team's resource — also an ID inside a request
  body — returns `404`. A same-team resource the caller may not change returns `403`.

| Endpoint | Who may call it | Notes |
| --- | --- | --- |
| `POST /login` | anyone | returns no hashes; `Origin` checked |
| `POST /logout` | session | deletes only the current session |
| `GET /me` | session | id, username, team, role |
| `GET /me/model-keys` | session (own only) | providers and "key set", never values; a lead gets no access to others' keys |
| `PUT`, `DELETE /me/model-keys/{provider}` | session (own only) | |
| `GET`, `POST /me/secrets` | session (own only) | metadata only (id, name, host, date) |
| `PUT /me/secrets/{id}` | owner of the secret | value only (D54) |
| `DELETE /me/secrets/{id}` | owner of the secret | invalidates bound pending calls (D55) |
| `GET /agents`, `GET /tools` | team | deleted rows hidden |
| `GET /agents/{id}`, `GET /tools/{id}` | team | deleted rows readable, marked deleted (D52) |
| `POST /agents` | team | body includes `tool_ids` (D46); owner and team from session; tools from the same team, not deleted, unique names |
| `PUT /agents/{id}` | owner | `tool_ids` replaces the list; links that already exist to deleted tools may stay, new deleted links are rejected (D46); owner/team/deleted fields cannot be changed |
| `DELETE /agents/{id}` | owner or team lead | soft delete; UI asks for confirmation (D43, D44, D58) |
| `POST /tools` | team | template validation (above) |
| `PUT /tools/{id}` | owner | revalidated; past snapshots unchanged |
| `DELETE /tools/{id}` | owner | soft delete; UI asks for confirmation (D34); no lead override |
| `POST /agents/{id}/clone` | team | caller becomes owner; same tool references; deleted tools are dropped and the response lists them (D52); deleted source cannot be cloned |
| `POST /tools/{id}/clone` | team | copies the definition; no credentials are copied |
| `POST /agents/{id}/runs` | team | `{task}`; starter = caller; starter's model key and secrets; missing key or secret → clear error, no fallback. If the agent has deleted tools: `409` with their IDs, no run created; resend with `{task, confirm_missing_tool_ids: [...]}`; if the set changed meanwhile → `409` again (D40, D56) |
| `GET /runs` | team | includes runs of deleted agents (D35) |
| `GET /runs/{id}` | team | messages, tool calls, snapshot (without secret bindings' details); token counts only for the starter and the team lead (D53) |
| `GET /runs/{id}/events` | team | live stream; protocol and reconnect _provisional (B3)_; GET never resumes work |
| `POST /runs/{id}/cancel` | _provisional (B3)_ | authority and semantics decided in B3 |
| `POST /runs/{id}/tool-calls/{callId}/approve`, `.../reject` | run starter only | `{approval_revision}` must match the current revision (D55); atomic conditional update (D21); lead or agent owner has no approval authority |
| `GET /approvals` | session | the caller's pending approvals in non-terminal runs; calls waiting for a replacement secret are marked; previews are secret-free |
| `GET /usage` | session | member: own totals; lead: every member's totals and team total (D41) |

## Open points (later batches)


- B3: run and tool-call status labels and transitions; streaming protocol,
  replay cursor and reconnect; cancellation authority and semantics; how a
  replacement secret is bound to an invalidated call (D55 requires a new
  approval); recovery after restart; execution claims and ambiguous side
  effects; retry policy and usage recording per attempt; HTTP destination and
  redirect rules (D11); SQLite connection and WAL settings.
