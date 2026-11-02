## 1. Consistency with decisions

The core entities fit the recorded design, but several contracts remain incomplete. This was a read-only review of the draft, D1–D44, `AGENTS.md`, and both earlier review directories. No files changed; no runtime tests were performed. The original PDF was unavailable.

**Needs decision** marks a proposed mechanism or behavior that is not already settled.

| Finding and location | Why it matters | Suggested fix |
| --- | --- | --- |
| **SSE and reconnect are presented as settled:** `run_events`, `GET /runs/{id}/events`, `Last-Event-ID`. | Streaming protocol and reconnect behavior are explicitly open in B3; D17’s EventSource rationale does not select SSE. | Mark these as provisional, pending B3. **Needs decision.** |
| **Cancellation is already exposed:** `POST /runs/{id}/cancel`. | Cancellation is a nice-to-have, and its authorization and execution semantics are undecided. R4’s terminal `cancelled` state does not itself require this endpoint. | Mark the endpoint provisional. **Needs decision.** |
| **Placeholder validation rejects valid secret references:** tool-save rule says “every placeholder exists in the parameter schema.” | `{{secret.NAME}}` belongs to a separate namespace under D27/D32. Putting secrets in model parameter schemas would undermine R2. | Validate parameter placeholders against `params_schema`; validate secret references separately and allow them only in headers. Do not require the tool creator to possess every eventual starter’s secret. |
| **Agent/tool association has no API contract:** agent POST/PUT bodies are unspecified. | R1 and D23 require configuring allowed tools through data/API. | Specify how agent creation/update supplies tool IDs, including replacement/removal semantics. A separate endpoint is unnecessary if these bodies support it. **Needs decision.** |
| **Secret deletion/rebinding is missing:** secrets DELETE and approval endpoints. | D29 requires invalidating affected pending approvals, collecting a replacement secret, and approving again. CRUD alone does not specify how the paused call receives that replacement. | Document deletion’s invalidation effect and an explicit rebinding operation. The operation’s shape and binding revision mechanism **need decision**. |
| **Deleted-tool confirmation is underspecified:** run POST accepts only `{task}` in its documented body. | D40 requires an explicit second submission. The confirmation payload and its relationship to the displayed deleted tools are missing. | Define the confirmation response/request contract and how changed deleted-tool sets are detected. **Needs decision.** |
| **Session guarantees are omitted from the API contract.** | D17 requires HttpOnly; D30 requires `SameSite=Lax`, Origin checks for state-changing requests, and server-side logout invalidation. | State those guarantees explicitly. “Every non-GET” should be aligned with “state-changing”; read-only methods must remain read-only. |
| **Several schema choices are proposals rather than consequences of their cited decisions:** global username uniqueness, `(user_id, name)` secret uniqueness, `cloned_from_id`, and `usage_status` categories. | These are reasonable candidates, but D16/D22/D32/R8 do not uniquely determine them; D1 reserves schema choices to the author. | Explicitly ratify or revise them. **Needs decision.** Secret-name uniqueness is discussed in §5. |
| **“Open points: None recorded” is misleading.** | The draft contains unresolved API details and B3 choices. | List these open points and distinguish existing obligations from proposed mechanisms. |

The draft correctly follows the amendments: secrets are user-owned under D32, snapshots contain secret IDs under D29/D42, and previews are described as secret-free under D33. D19/D24 should not be revived.

## 2. Requirement coverage

“Supported” below means the structure can support the requirement, not that behavior is verified.

| Requirement | Assessment and concrete failure scenario | Suggested fix |
| --- | --- | --- |
| **R1 — Agents are data** | Core columns and `agent_tools` support it. However, without an association request body, an API client cannot define a usable agent without database intervention. | Specify agent/tool assignment and return the allowed-tool list in agent representations. **API shape needs decision.** |
| **R2 — HTTP template tools** | Required template fields exist. The placeholder rule can reject secret-bearing tools. Conversely, a raw template returned through history could expose a literal credential embedded in a header. | Correct namespace validation; define safe template representations and credential-input rules (§5). Destination/redirect enforcement remains B3. |
| **R3 — Loop error handling** | Recording invalid calls needs clarification. If `tool_id` must reference a real tool and `arguments` must contain valid JSON, an unknown tool or malformed JSON cannot be recorded as a rejected call. | Allow an unresolved tool reference and preserve a sanitized submitted name/raw argument representation, or document an equivalent message/event representation. Exact columns **need decision**. Loop limits remain B3. |
| **R4 — Live events and terminal runs** | `run_events`, run status, and timestamps provide a foundation. No status domain or terminal-state invariant is specified; an errored run could remain indefinitely “running.” | Specify required terminal states and event identity/order constraints. Recovery, deadlines, disconnect behavior and transport remain B3. |
| **R5 — Durable approval** | Persistent calls, arguments, previews and decision fields support restart durability. Missing invalidation/rebinding semantics can allow a stale approval after secret deletion. | Define the approval lifecycle and replacement contract (§6); preserve the exact pending call rather than regenerate it. |
| **R6 — Team isolation** | Team, owner and starter are distinct, correctly. But “both in the same team” is only prose: ordinary independent FKs would still permit cross-team associations. | Add explicit database enforcement and endpoint checks (§3–4). Seed at least two teams with users; no team-management endpoint is required. |
| **R7 — Per-user model keys** | The encrypted `(user_id, provider)` store and `started_by` support BYOK. Execution can still select the agent owner’s key, or a response can serialize encrypted credential rows accidentally. | Specify starter/provider lookup, explicit missing-key errors, no fallback, and response allowlists. Missing-key handling must not expose credentials. |
| **R8 — Providers and usage** | Agent provider/model and `model_calls` support two providers and per-call accounting. Denormalized attribution can disagree with the run; unavailable usage can be mistaken for zero. | Enforce attribution consistency and define nullable/incomplete accounting. Each actual provider attempt’s recording boundary belongs to B3. D41’s totals endpoint is sufficient structurally. |

**Example agent:** The API can support two real tools and an approval-required side effect once agent/tool assignment is defined. The example and authorized target remain B4 decisions.

## 3. Integrity and isolation

The draft is a column inventory, not yet an enforceable schema: types, nullability, primary keys, foreign keys, checks and deletion actions are mostly absent.

SQLite foreign-key enforcement must be enabled on every connection. Composite referenced keys need suitable primary/unique constraints; nullable FK components can bypass a required relationship. These points are documented in [SQLite’s foreign-key documentation](https://www.sqlite.org/foreignkeys.html).

The concrete mechanisms below **need decision**; the isolation and retention obligations already follow from the cited decisions.

| Location and failure scenario | Suggested enforcement |
| --- | --- |
| **Basic identities:** duplicate session hashes, message sequence numbers or agent/tool links are possible. | Declare primary keys explicitly: entity `id`s; session `token_hash`; `(user_id, provider)` for model keys; `(agent_id, tool_id)` for associations; `(run_id, seq)` independently for messages and events; migration `version`; an explicit key for `key_check`. Required relationships need `NOT NULL`. |
| **`users.team_id`, agent/tool ownership:** a team-A agent could name a team-B owner. R6/D22. | Require `users.team_id → teams.id`. One option is `UNIQUE users(id, team_id)` plus composite FKs from each agent/tool `(owner_id, team_id)`. |
| **`agent_tools`:** two individually valid IDs can belong to different teams. R6/D23. | One option is adding `team_id` and composite FKs to `agents(id, team_id)` and `tools(id, team_id)`, backed by parent unique constraints. Triggers are an alternative. |
| **`runs`:** `team_id`, `agent_id` and `started_by` can disagree. R6/R7/D23. | Enforce both `(agent_id, team_id)` and `(started_by, team_id)` against their respective parents. Treat these identities as immutable after creation. |
| **Run children:** a tool call can reference another team’s tool; `decided_by` can name someone other than the starter. D21/D26. | FK every child to its run. Enforce the call’s tool belonging to the run’s snapshotted tool set. For non-null `decided_by`, a composite FK to `runs(id, started_by)` is one possible structural check. Authenticated authorization still belongs in handlers. |
| **`model_calls`:** valid user/team/agent IDs can attribute one person’s run to someone else. R8/D41. | Either derive these fields through `run_id`, or constrain them to match that run. Compare provider/model with the run configuration, not a subsequently edited live agent. |
| **`cloned_from_id`:** provenance can point across teams or become dangling. R6/D22/D23. | If retained, constrain provenance to the same team and retain its soft-deleted source. Clients must not assign arbitrary provenance. |
| **Deleted association targets:** an FK accepts a soft-deleted tool because the row still exists. D34/D40. | Guard new `agent_tools` inserts and changed tool references against `deleted_at`. A trigger can enforce this at database level; SQLite supports conditional trigger checks and rejection through [triggers](https://www.sqlite.org/lang_createtrigger.html). Preserve existing references when a tool is later deleted. |
| **Full agent updates:** deleting and recreating every association can turn an unchanged tombstone reference into a forbidden “new” association. | Define association updates so retained links remain retained. Cloning an agent containing deleted tools is a separate unresolved case (§4). |
| **Soft versus hard deletion:** cascades could erase associations, history or pending calls. D25/D34/D35/D43. | Agent/tool DELETE should update `deleted_at`, without deleting dependants. Prefer restricting physical deletion of referenced agents/tools. User/team/run purge behavior is undecided; do not introduce destructive cascades implicitly. |
| **Secret deletion:** restricting deletion merely because an ID appears in a snapshot would prevent D29; cascading would destroy history. | Preserve historical binding IDs while marking affected approvals invalid. JSON bindings need an explicit lookup/invalidation path; they do not obtain referential integrity automatically. |
| **Domain consistency:** invalid roles, negative token counts or malformed snapshot JSON can be stored. | Define checks for roles, booleans, approved status domains, nonnegative known counts and appropriate JSON fields. Allow unavailable counts to be null. Do not apply valid-JSON requirements to the only retained copy of malformed model arguments. |

Additional choices:

- **Tool naming:** uncertain whether `tools.name` is also the model-visible callable identifier. If two attached tools share that identifier, dispatch becomes ambiguous. Choose stable identifiers or an appropriate uniqueness rule; global tool-name uniqueness is not inherently required. **Needs decision.**
- **Lead cardinality:** D44 establishes seeded leads but does not explicitly settle whether multiple leads per team are allowed. Do not silently add a one-lead uniqueness rule. **Needs decision if constrained.**
- **Indexes:** consider `agent_tools(tool_id)`, team-scoped run ordering, calls by `(run_id, status)`, and usage by user/team and time. Sequence primary keys cover ordered message/event reads. Exact indexes **need decision**; avoid indexing every column.

## 4. Authorization per endpoint

Unless noted, require a valid, unexpired server session. Apply D30 to state-changing requests. Derive acting user, team and role from the session/database, never request-supplied authority fields.

Cross-team resources must return `404` under D39, including IDs nested in request bodies. The response for a same-team ownership denial is unspecified; **needs decision**.

| Endpoint | Required checks and behavior |
| --- | --- |
| `POST /login` | Public authentication endpoint; validate credentials without returning password hashes. Apply Origin protection. Team and role come from the authenticated user record. |
| `POST /logout` | Invalidate the current server-side session, not merely the cookie. No other user’s session may be targeted. |
| `GET /me` | Return an allowlisted identity/team/role representation; exclude password and session hashes. |
| `GET /me/model-keys` | Current user only; return provider/key-present metadata. Leads gain no access to teammates’ keys. |
| `PUT`, `DELETE /me/model-keys/{provider}` | Current user only; validate the provider identifier. Ignore/reject supplied user/team identities. Return no credential material. |
| `GET`, `POST /me/secrets` | Current user only. Creation binds the authenticated owner and validates name/host. Lists return metadata, including the ID needed by update/delete routes. |
| `PUT`, `DELETE /me/secrets/{id}` | Verify ownership of that secret, even within the same team. Delete must invalidate affected pending approvals under D29. Name/host update semantics remain open (§5). |
| `GET /agents`, `GET /tools` | Scope to the session’s team; hide deleted rows from lists. D23 permits teammates’ resources to be visible. |
| `GET /agents/{id}`, `GET /tools/{id}` | Require same team. Represent referenced deleted tools sufficiently for D40. Direct access to tombstones is unspecified and **needs decision**. |
| `POST /agents`, `POST /tools` | Derive owner/team from the session. Agent tool references must belong to that team; newly attached tools must not be deleted. Validate templates without resolving another user’s credentials. |
| `PUT /agents/{id}` | Same team **and owner**. Validate every changed association. A lead has no general edit override under D44. Prevent ownership/team/provenance/deletion-field reassignment through this body. |
| `PUT /tools/{id}` | Same team **and owner**. Revalidate templates. Editing a tool must not alter past run snapshots. |
| `DELETE /agents/{id}` | Same team and either owner or that team’s lead. Soft delete only. |
| `DELETE /tools/{id}` | Same team **and owner**. D44 grants no lead override for tools. Soft delete; the UI confirmation follows D34. |
| Both `POST .../{id}/clone` endpoints | Source must be visible within the same team; source ownership is unnecessary. New owner is the caller. Agent clones retain shared tool references under D23; tool clones copy definitions without copying another user’s credentials. |
| `POST /agents/{id}/runs` | Same-team, runnable agent; starter is the caller. Use the starter’s model key and named secrets. Apply D40 before execution and construct a consistent snapshot. Missing credentials must produce clear errors without fallback. Whether a failed precondition creates a failed run is **B3: needs decision**. |
| `GET /runs` | Team-scoped history under D35, including runs started by teammates and runs whose agents were later deleted. |
| `GET /runs/{id}` | Same-team run. Messages, calls and any exposed snapshot inherit that authorization. Do not require current agent ownership or an undeleted live agent. Apply secret and usage-field filtering. |
| `GET /runs/{id}/events` | Same-team run, including replay. Reconnect cursors must remain scoped to this run. GET must not implicitly execute/resume work. Session expiry/revocation during an open stream belongs to B3. |
| `POST /runs/{id}/cancel` | Permission is unspecified. Team visibility does not grant cancellation authority. Starter-only cancellation is a possible proposal, **needs decision**. |
| Both approval POSTs | Same team; caller equals `runs.started_by`; `callId` belongs to the path run. D21 requires an atomic conditional transition checking eligible run/call state and starter. Include protection against obsolete approval bindings (§6). Neither agent ownership nor lead role grants approval authority. |
| `GET /approvals` | Only the caller’s pending approvals, scoped through starter and team. Exclude terminal-run approvals and distinguish calls awaiting replacement credentials. Return secret-free previews. |
| `GET /usage` | Members: their own totals only. Leads: their own team’s members and team total. Do not trust requested user/team filters without checking them. |

**Clone/deletion conflict:** D23 says agent clones retain the same tools, while the draft prohibits adding deleted tools. Cloning an agent already referencing tombstones is therefore unresolved. Preserve tombstone links as an explicit clone exception, omit them with confirmation, or reject the clone—**needs decision**. Deleted source-agent/tool cloning also needs an explicit policy.

**Usage visibility conflict:** D35 permits team run history; D41 restricts usage. Raw token counts in `provider_extra`, events or run detail could bypass D41. Specify field-level filtering, or explicitly clarify a narrower interpretation of D41—**needs decision**.

## 5. Secret handling in the schema/API

- **Credential responses need allowlists.** `users`, `sessions`, `user_model_keys`, `user_secrets` and `key_check` contain sensitive fields. Generic row serialization through `/me`, CRUD responses or errors could expose password hashes, session hashes, ciphertext or envelopes. Return identity/metadata only; never echo submitted credentials. This supports D12/D16/D29/D33/D38.

- **`UNIQUE(user_id, name)` fits the named-reference design.** Resolve `{{secret.NAME}}` using the starter and name, then require the request host to match that row’s `allowed_host`. A secret with another name on the same host is not an automatic substitute. Two hosts needing equivalent credential names can use distinct names. Changing uniqueness to `(user_id, name, allowed_host)` would make name-only references ambiguous unless resolution changes. Ratifying the present naming policy **needs decision**.

- **Snapshot binding needs a defined structure.** A bare list of secret IDs does not explain which template reference uses each ID. Under D29/D32, specify reference-to-ID bindings, validate ownership against `started_by`, and never silently resolve a deleted ID by name. IDs must not be reused for replacement credentials. Exact JSON and ID strategy **need decision**.

- **Secret updates need boundaries.** `PUT /me/secrets/{id}` does not say whether it changes value, name or host. D29 permits current-value replacement; rename/host changes can alter lookup and approval behavior. Define these separately. **Needs decision.** Enforce the allowed-host check again at final dispatch.

- **Encryption identity must be explicit.** D14/D31 require versioned AAD derived from immutable identity. Document identities for model-key rows and secret rows; do not accidentally make mutable names part of an “immutable” identity. `key_check` also needs a defined AAD convention/version, whether stored or fixed by format. Nonce, ciphertext and versions must update together.

- **D33 must cover every output path.** `response (masked)` alone is insufficient documentation. Sanitization must precede storage/model delivery/event emission for response bodies, response headers and errors. This includes `runs.error_message`, message content, events and `provider_extra`. Mask using the credential actually dispatched, including its required base64/URL-encoded forms, rather than reloading a possibly rotated value.

- **Previews and snapshots must never receive expanded credentials.** Build them before attachment; do not serialize the final outbound request. Returning a snapshot to teammates must not expose encrypted credential rows or unrelated personal secret metadata.

- **Literal credentials in templates are unresolved.** Uncertain: the draft does not prohibit an author pasting a token directly into `headers_template`. Such a value can then enter shared tool reads, clones and snapshots without passing through `user_secrets`. Define credential-entry validation/UI rules; recognizing and rejecting credential-bearing literal headers is one possible approach. **Needs decision.**

- **Credential submission and errors are also boundaries.** Login, key and secret request bodies must be excluded from diagnostic logging; validation and encryption errors must not reproduce values. R7’s model-key protection is broader than tool-response masking.

D33 explicitly rejected suspicious-response withholding. This review does not treat that earlier recommendation as an adopted requirement.

## 6. Statuses

The draft does **not enumerate run or tool-call statuses**. The columns can accommodate the necessary distinctions, but their current specification is insufficient to assess transitions.

The following are required distinctions, not a proposed enum:

| Location | Distinction needed | Why / suggested fix |
| --- | --- | --- |
| `runs.status` | Terminal `succeeded`, `failed`, `cancelled`; distinguish active execution from approval waiting. | R4/R5. Define terminal states, completion timestamps and which transitions are eligible for approval. Exact intermediate labels remain **B3: needs decision**. |
| Pending tool calls | Waiting for approval versus approval invalidated by deleted credentials. | D29. An invalidated call must not remain ordinarily approvable. Represent this using status and/or a separate reason. **Needs decision.** |
| Approval decisions | Approved versus rejected versus undecided. | D21/D26. Persist the decision and actor before dispatch; a rejected call must not become executable after restart. |
| Execution outcome | Authorization to execute versus an attempted/completed/failed execution. | Approval is not execution success. Keep these meanings distinct; execution claims and ambiguous outcomes remain B3. |
| Replacement approval | Current approval generation versus an obsolete browser submission. | A stale page must not approve a newly rebound request accidentally. Bind approval submission to the currently displayed revision or equivalent identifier. **Needs decision.** |
| D40 preflight | No run started while confirmation is outstanding. | No additional run status is required if the first response is only a preflight result. On confirmation, excluded tools must be absent from the executable snapshot. |

Also specify:

- When a secret is deleted, invalidate affected pending approvals durably, not merely on the next page render.
- Rebinding credentials must be explicit and require approval again, while retaining frozen agent/tool definitions under D25.
- `decided_by`/`decided_at` must agree with the decision state.
- Cancellation’s effect on pending calls, rejected-call continuation, waiting deadlines and restart recovery remain B3 choices.

## 7. Simplicity

These are optional simplifications, each **needs decision**:

- **Derive redundant attribution:** `model_calls.user_id`, `team_id` and `agent_id` can be obtained through `run_id`. Keeping them requires consistency enforcement; removing them reduces that burden.
- **Omit clone provenance if unused:** `cloned_from_id` is not required by D22/D23. Cloning behavior does not depend on recording ancestry.
- **Avoid new relationship endpoints unnecessarily:** agent POST/PUT can manage `tool_ids`; existing run detail can expose authorized messages/calls. Separate endpoints are not required for coverage.
- **Avoid an approval table unless history needs it:** `tool_calls` can hold current approval state. Revision/history requirements should determine whether anything more is necessary.
- **Treat `final_output` as either derived or deliberately duplicated:** if it repeats the final message, document the single authoritative source and consistency rule.

Keep the single JSON snapshot under D42. Messages, events and model-call accounting serve different purposes; merging them would obscure conversation continuation, stream replay and usage. `key_check` directly supports D31 and should remain.

## Items for B3

All are **needs decision** unless already constrained by a recorded obligation:

1. **Run lifecycle:** intermediate states, finalization, disconnect behavior, pending deadlines and restart recovery.
2. **Execution ownership:** durable claims, duplicate dispatch prevention, and ambiguous external side effects. Approval conditional updates alone do not solve these.
3. **Approval execution:** multiple calls per turn, rejection continuation, cancellation authority, and interaction between cancellation and pending approvals.
4. **Streaming:** SSE versus WebSocket, Next.js versus direct Go path, replay/cursor contract, and open-stream session expiry/revocation.
5. **Retry/accounting:** one retry owner, error classes, backoff, deadlines, interrupted usage, and what counts as each recorded provider attempt.
6. **Provider continuation:** API surface, supported models, ordered messages, call-ID scope and required `provider_extra`; retain structured continuation material without exposing usage or credentials improperly.
7. **HTTP execution:** destination/redirect rules under D11, host normalization, final secret-host checks, template substitution/escaping and response-size limits.
8. **Credential lifecycle:** model-key replacement/deletion during paused runs, secret deletion/execution races, and dispatch-time sanitization boundaries.
9. **SQLite operation:** connection/writer pattern, FK enforcement on every connection, WAL/busy/durability settings and short transaction boundaries.

D29’s invalidation obligation and D40’s confirmation API should be made explicit in B2b; B3 should determine their execution mechanics.