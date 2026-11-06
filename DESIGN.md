# Design

Compiled from the decision log in [`PLAN.md`](PLAN.md) (`Dn` = decision number); schema and API in
[`docs/schema.md`](docs/schema.md). Every technical decision was made by the author; Claude Code implemented them and
Codex reviewed each planning batch and every push ([`AI_USAGE.md`](AI_USAGE.md)).

## Architecture

```text
browser ──► Next.js (UI; forwards /api/* incl. SSE) ──► Go backend, one process ──► SQLite (WAL, one writer)
                                                         │  REST + SSE handlers
                                                         │  run workers (agent loop) ──► OpenAI Responses / Anthropic Messages
                                                         │                           └─► tool HTTP client (guarded)
```

- **One Go process** (D84): REST API, SSE streams and a bounded pool of run workers. Creating a run stores it as
  `queued`; a worker picks it up. On startup, `queued` and `running` runs are resumed from their last checkpoint.
- **Agent loop** (D9): our own loop over the official SDKs, which are used only for transport, errors and stream
  parsing. Each provider sits behind a thin adapter; the provider's raw assistant content is stored and sent back
  verbatim (D96). One tool call per turn (D64). At most 20 turns; 3 invalid calls in a row fail the run (D80).
- **Agents and tools are data** (R1, R2): an agent is a name, a system prompt, a model and tool IDs. A tool is an HTTP
  template with `{{param}}` placeholders in a small grammar (D93), plus a JSON Schema for its parameters. The example
  agent is created through the REST API (`go run ./cmd/example`), not built in.
- **Durability**: messages, tool calls and events are written before they are shown (D20, D98). A run ends in one
  conditional transaction that also writes its final event (D94). Every run ends `succeeded`, `failed` or
  `cancelled`, including after provider errors, timeouts, a closed page (D61) or an expired approval (D90).
- **Streaming**: SSE (D67). Events are written first, then delivered by in-process notification. A reconnecting page
  replays from `Last-Event-ID`. Text deltas are batched every 250 ms (D70). Approve, reject and cancel are POSTs.

## Decisions and alternatives

| Decision | Alternative not taken, and why |
| --- | --- |
| Go backend, SQLite via pure-Go `modernc.org/sqlite` (D5, D6) | Postgres: one more service to install, against the 10-minute setup. |
| Own agent loop, no agent framework (D9) | Python service on the OpenAI Agents SDK: a third runtime, and secrets and run state split over two processes. The behaviour under assessment (final states, durable approval, per-user keys, metering) sits around the loop and has to be built either way. |
| OpenAI Responses API with `store: false`; full history from our database on every turn (D73, D83) | `previous_response_id`: a resume after restart would depend on the provider's state. |
| Retries in our adapter, SDK retries off; permanent errors fail at once; 429/5xx retried up to 4 attempts; `Retry-After` honoured up to 60 s (D79, D88, D94) | SDK retries: invisible attempts, unmetered usage, and quota exhaustion confused with rate limiting. |
| One process, no leases or replicas (D84) | Leased workers for several replicas (D59): the case does not require it; prioritising the core is part of the assessment. |
| A side-effecting call with an unknown outcome becomes `outcome_unknown` and is never retried; only `GET` is retried, once (D63, D91) | Automatic retry: risks opening the same issue twice. |
| Approval expires after 24 h, moved past weekends; the run then ends `cancelled` (D90) | Waiting forever (D65): a run would never reach a final state. |
| SSE behind the Next.js proxy (D67, D68) | WebSocket: a two-way channel is not needed; SSE works with the session cookie and `Last-Event-ID`. |

**Framework use.** No agent framework. Next.js is used only for the UI. Where it got in the way: response
compression is turned off so SSE is not buffered by the proxy (D123), and `next dev` writes files into the tree (D129).

## Deliberately left out

- All nice-to-haves (D111): agent chaining, protection against prompt injection in tool responses, isolated code
  execution, MCP tools, an eval set, multi-replica streaming. Cancellation was kept in the core (D97).
- Multiple backend replicas (D84). Live 429 handling is covered only by fixture tests.
- Automated UI tests and `-race` in the default test command, which needs CGO (D102). Supported OS: only Windows
  was tested (D113).
- **Known limitation (F19):** a successful model call whose usage the provider did not report shows `0` tokens on the
  run page. The API and the usage totals do mark it as unknown (`calls_with_unknown_usage`, D47, D75).

## Security risks and mitigations

- **Model keys (R7)**: AES-256-GCM with the row identity as associated data (D14, D31); master key from `.env`; the
  server refuses to start with a wrong key. Keys are entered in the UI, never read from the environment (D12, D18). A
  run always uses its starter's key, with no fallback. Provider 401 messages are replaced because they quote part of
  the key (D123).
- **Tool secrets (R2)**: they belong to a user and are bound to one host (D32). They may appear only in headers
  (D27, D106). Approval previews, events and records are built before secrets are attached (D33). Responses are
  masked, including base64 and URL-encoded forms, before the model or any record sees them.
- **SSRF and steering (D81, D93, D94)**: `https` on port 443 only; private, loopback, link-local and metadata
  addresses are refused on the connected IP (covers DNS rebinding); no redirects; HTTP/1.1 only, so the transport
  cannot silently re-send a request (D125). Parameters cannot change a request's structure.
- **Team isolation (R6)**: every query is scoped by the session's team; composite foreign keys enforce it in the
  schema (D46); another team's resource is `404` (D39). Sessions are server-side, HttpOnly, `SameSite=Lax`, with an
  `Origin` check (D17, D30, D38).
- **Residual risks.** These are inherent to an agent platform, not known defects. Each one is reduced by the
  measures already in place (D130):
  - *Prompt injection through tool responses:* a response can contain text that reads like instructions to the
    model. Mitigations: the model never sees a secret (D33); secrets go only to their bound host (D32); private
    addresses cannot be reached (D81); every side effect waits for approval and shows the exact request (R5); the
    system prompt treats tool results as data. Dedicated injection detection is a nice-to-have left out (D111).
  - *Master key plus database file:* anyone holding both can decrypt the stored keys, as with any encryption at rest.
    Mitigations: the key is kept outside the database; associated data bound to each row (D14, D31); the server
    refuses to start with a wrong key; a key version on every row allows rotation (D15).
  - *Seeded passwords:* the initial passwords live in the local, git-ignored `.env`. Mitigations: they are stored
    only as bcrypt hashes; seeding never resets an existing password; no password is in the repository (D16, D31).
  - *Lead deletes a teammate's agent (D44):* a deliberate power. Mitigations: deletion is confirmed (D58), soft
    (D43) and limited to the lead's own team; past runs keep showing the agent.

## At several thousand developers

- **Postgres instead of SQLite** (D82), and several backend replicas with leased run ownership, takeover and fencing
  (D59, D84). Events then reach SSE clients on other replicas through a shared channel or a database poll (D69).
- **Limits**: the budget-motivated output and tool-response limits become configurable per agent or team (D95).
- **Secrets**: the master key moves from `.env` to a KMS; the key version on every row already allows rotation
  (D15, D131).
- **Identity**: SSO instead of seeded passwords; per-team budgets and alerting built on the existing usage records
  (R8, D131).
- **Tools**: an allowlist of hosts per team and review of shared tools, since any member's tool runs with the
  starter's secret for its host (D32, D131).
