Read `AGENTS.md` and `PLAN.md`. No files changed. **D5 can stay; D6–D9 should stay with conditions.** The main risks concern lifecycle and integration details, rather than the stack itself.

**Verification:** Library/API facts labelled “verified” below were checked against official documentation or maintainer sources during this review. Nothing was runtime-tested; version-specific behaviour should be checked again against the versions eventually pinned.

## D5 — Backend in Go

**Verdict: keep.** Fits the reviewers’ preference and the requirement for a small, explainable platform.

**Risks or consequences**

- A goroutine is not durable execution state. A restart can lose an approval wait, conversation context or an unfinished run unless these are persisted explicitly (R4–R5).
- Connecting execution directly to the streaming HTTP request can make a browser disconnect cancel the model call without successfully recording a terminal state.
- A slow stream consumer can block the executor if event delivery uses an unbounded wait.

**Mitigations**

- In B3, define run ownership, disconnect behaviour and restart recovery explicitly.
- Keep transactions short and separate network delivery from durable lifecycle transitions.
- Bound subscriptions and clean up streams and goroutines. Terminal-state persistence needs a usable context even after the execution context is cancelled.

## D6 — SQLite with `modernc.org/sqlite`

**Verdict: keep with conditions.** Appropriate for the case’s local setup and modest concurrency.

**Risks or consequences**

- **Verified:** WAL permits concurrent readers and a writer, but still allows only **one writer at a time**. Long read transactions can prevent checkpoints from advancing. An SSE handler holding a query open while streaming could therefore cause WAL growth; concurrent run/event/approval writes can still encounter contention. [SQLite WAL documentation](https://www.sqlite.org/wal.html)
- **Verified:** Go’s `sql.DB` is a connection pool. Running connection-specific PRAGMAs once through `db.Exec` does not establish them on every future pooled connection. `modernc` documents DSN `_pragma` configuration and connection hooks. [Go connection management](https://go.dev/doc/database/manage-connections), [driver documentation](https://pkg.go.dev/modernc.org/sqlite)
- A busy timeout reduces transient failures; it does not resolve long transactions or every read-to-write upgrade conflict.
- A single connection simplifies serialization, but an unclosed reader can then prevent approval or final-state writes from obtaining that connection.
- **Verified:** The driver supports Windows, and requires care around its `modernc.org/libc` dependency version. Windows operation for this application remains untested. [Driver platform and dependency notes](https://pkg.go.dev/modernc.org/sqlite)

**Mitigations**

- Consider WAL, a bounded pool, per-connection busy timeout and foreign-key enforcement.
- Choose explicitly between one connection for all access and serialized writes with separate short-lived readers. Avoid introducing a writer queue unless its complexity is justified.
- Finish database reads before waiting on SSE clients; never hold a transaction during model calls, HTTP tools or human approval.
- Verify locking, concurrent approval submissions and restart recovery on a file-backed database on Windows. Use a writable local directory; WAL is unsuitable for a network filesystem. [SQLite WAL restrictions](https://www.sqlite.org/wal.html)
- Measure cold dependency download and compilation time; “no C compiler” does not prove the ten-minute target.

## D7 — Next.js UI

**Verdict: keep with conditions.** The greatest schedule risk is leaving the setup check until the end.

**Risks or consequences**

- Next.js adds Node/package-manager prerequisites, dependency installation and a second development process.
- **Verified:** Route Handlers support Web streams, and rewrites can proxy external destinations. Neither fact proves that the eventual SSE path preserves immediate delivery, cookies and disconnects under the selected version and runtime. That remains uncertain until tested. [Route Handlers](https://nextjs.org/docs/app/api-reference/file-conventions/route), [rewrites](https://nextjs.org/docs/app/api-reference/config/next-config-js/rewrites)

| Browser path | Consequences to evaluate |
| --- | --- |
| Through Next.js | Same-origin browser requests simplify CORS. A Route Handler must forward the stream without collecting its body and preserve authentication and relevant headers. Rewrites reduce custom code, but their streaming behaviour still needs verification. |
| Directly to Go | Removes the intermediary stream hop. Different ports are different origins, so credentials and CORS must be configured deliberately. Go remains responsible for authorization. |

- **Verified:** Native `EventSource` exposes a URL and `withCredentials`, but no arbitrary-header option. Bearer-header authentication therefore affects the choice between native EventSource, fetch-based SSE and cookie authentication. Credentialed cross-origin requests require an explicit allowed origin rather than `*`. [EventSource standard](https://html.spec.whatwg.org/multipage/server-sent-events.html#the-eventsource-interface), [Fetch CORS standard](https://fetch.spec.whatwg.org/#cors-protocol-and-credentials)

**Mitigations**

- Validate one authenticated streaming path early, including approval submission, browser close and reconnect.
- Measure both development and documented production startup paths. Pin dependencies and keep the UI small.
- For a proxy, verify cookie forwarding, caching, buffering and upstream cancellation. For direct access, verify credentialed CORS and cookie scope.
- Enforce team access in Go on every path; CORS and UI visibility do not provide R6 isolation.

## D8 — OpenAI primary, Anthropic second

**Verdict: keep with conditions.** Anthropic is a useful test of whether the abstraction preserves different provider semantics.

**Risks or consequences**

- “Runnable” needs working credentials and a tool-capable model for both providers. The OpenAI key cannot establish Anthropic readiness.
- Provider plus model must identify an agent’s configuration. An unsupported model/tool combination should produce a clear error.
- Metering cannot treat every stream event identically:
  - **Verified:** Chat Completions needs `stream_options.include_usage` for its final usage chunk. That chunk has an empty `choices` array and may never arrive after interruption. [OpenAI Chat Completions reference](https://developers.openai.com/api/reference/resources/chat/subresources/completions/methods/create)
  - **Verified:** Anthropic sends usage information through its message events; `message_delta` usage counts are cumulative. Adding every delta would overcount. [Anthropic streaming documentation](https://platform.claude.com/docs/en/build-with-claude/streaming)
- Missing usage after a broken stream is not evidence of zero consumption. Exact provider accounting may be unavailable for that call.

**Mitigations**

- Demonstrate a complete tool round trip with each provider early.
- Resolve credentials by initiating user **and provider**, rejecting missing credentials before constructing a request.
- Record each model invocation and distinguish complete, partial and unavailable usage. Preserve provider usage details so normalization does not lose cache or reasoning categories.
- Show incomplete accounting honestly in totals; decide its treatment in B2–B3.

## D9 — Own Go loop behind thin SDK adapters

**Verdict: keep with conditions.** The rationale is sound: the platform’s approval, isolation and lifecycle requirements remain application responsibilities with either approach.

**Risks or consequences**

- **Verified:** Both SDKs document streaming and tool calling. This supports feasibility, but does not establish that the chosen versions are bug-free for interleaved tool streams. [OpenAI Go SDK](https://github.com/openai/openai-go), [Anthropic Go SDK](https://platform.claude.com/docs/en/cli-sdks-libraries/sdks/go)
- **Verified:** Both automatically retry connection errors, 408, 409, 429 and server errors **twice by default**, using backoff; both expose `WithMaxRetries`. Three outer attempts combined with these defaults can produce nine HTTP attempts. [OpenAI retry documentation](https://github.com/openai/openai-go#retries), [Anthropic retry documentation](https://platform.claude.com/docs/en/cli-sdks-libraries/sdks/go#retries)
- Do not assume those retries resume an interrupted stream. Reissuing generation after partial output can repeat content or propose a side effect again.
- **Verified:** OpenAI documents no default overall request timeout; Anthropic documents no default timeout for streaming requests. Explicit deadlines remain necessary for R4. [OpenAI timeouts](https://github.com/openai/openai-go#timeouts), [Anthropic timeouts](https://platform.claude.com/docs/en/cli-sdks-libraries/sdks/go#timeouts)

The adapter must preserve these differences:

| Concern | Concrete pitfall |
| --- | --- |
| Call identifiers | OpenAI Responses uses `call_id` to correlate outputs; Anthropic uses `tool_use.id` → `tool_result.tool_use_id`. Confusing item IDs with call IDs breaks continuation. |
| Message structure | Anthropic results belong in a user message immediately after the assistant tool-use message, with results before ordinary text. A generic `role: tool` representation needs provider-specific conversion. |
| System prompt and continuation | Anthropic has a top-level `system` field. OpenAI’s representation depends on the selected API. Flattening history can also discard reasoning items required by some OpenAI models. |
| Multiple calls | A turn can contain several calls. Retaining only the first loses work; parallel execution also complicates approval and ordering. |
| Stop conditions | Tool use, normal completion, token exhaustion and errors must remain distinguishable. End-of-stream alone does not prove success. |

These protocol differences were **verified** in the [OpenAI function-calling guide](https://developers.openai.com/api/docs/guides/function-calling), [Anthropic tool-result rules](https://platform.claude.com/docs/en/agents-and-tools/tool-use/handle-tool-calls) and [Anthropic SDK examples](https://platform.claude.com/docs/en/cli-sdks-libraries/sdks/go).

**Mitigations**

- Keep orchestration outside adapters, while preserving structured calls, IDs, termination reasons, usage and continuation material inside their contract.
- Accumulate arguments separately by call/index. Partial JSON is normal during streaming; validate only completed arguments before execution. Anthropic explicitly documents partial JSON fragments. [Anthropic streaming](https://platform.claude.com/docs/en/build-with-claude/streaming)
- Choose one retry owner, a total attempt budget and an overall deadline. Verify `Retry-After` handling against pinned SDK versions.
- Recommend adapter fixtures covering interleaved calls, malformed completed JSON, stream failure and usage-only events.
- SDK examples with hard-coded function switches are examples, not the platform design: R1–R2 require database-defined agents and HTTP templates.

## Cross-cutting risks

- **Approval durability requires continuation state.** An approval row alone cannot resume a conversation. Persist the initiating identity, pending calls, relevant history and approved request version. Double approval or restart must not execute the same operation twice.
- **External effects cannot be made atomic with SQLite.** An issue might be created just before the process crashes while recording its result. B3 needs an explicit ambiguous-outcome policy; blindly retrying can duplicate it.
- **SDK defaults can violate BYOK.** Both document environment-key defaults. Reject missing user credentials and pass the selected key explicitly; avoid mutating a shared client’s credentials between users. [OpenAI configuration](https://github.com/openai/openai-go#usage), [Anthropic configuration](https://platform.claude.com/docs/en/cli-sdks-libraries/sdks/go#usage)
- **HTTP templates create a security boundary.** A configurable URL or redirect can reach internal services or disclose injected tool credentials. Define destination, redirect and secret-handling rules before implementing R2. Raw diagnostics and echoed HTTP responses also need scrutiny.
- **B5 should reserve integration time now.** One complete run—streaming, tool execution, durable approval and final state—will reveal more than separately polishing each component.

## Open questions for later batches

| Batch | Questions for the author |
| --- | --- |
| **B2 — data** | What conversation, call, event and configuration snapshots survive approval/restart? How are ownership and concurrent approval updates enforced? |
| **B2 — secrets** | Which encryption mechanism and persistent master-key source? How are provider keys and tool secrets provisioned without committed credentials? |
| **B3 — database** | Which connection/writer pattern, WAL settings, busy timeout and durability settings? |
| **B3 — streaming** | SSE or WebSocket? Through Next.js or directly to Go? Which authentication mechanism, reconnect behaviour and page-close policy? |
| **B3 — execution** | Which OpenAI API surface and supported models? How are multiple tool calls, rejected approvals, malformed arguments and truncation handled? |
| **B3 — recovery** | Which retry owner, deadlines and limits? What happens to ambiguous side effects, interrupted usage accounting and runs found active after restart? |
| **B4 — evidence** | Which two real tools and authorized test target? What proves both providers work? Which fixtures and restart/concurrency tests are required? |
| **B5 — delivery** | Which toolchain/dependency versions and supported OSes? When is cold setup measured, what triggers revisiting Next.js, and which optional work is excluded? |