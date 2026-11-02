B3 covers the right concerns, but execution ownership, approval-to-dispatch transitions and terminal-state persistence remain underspecified. Those gaps should be closed before implementation.

Read `PLAN.md`, `docs/schema.md`, `AGENTS.md`, and the B1/B2/B2b review directories. No files changed; no runtime tests or paid API calls performed. The original PDF was unavailable. External API/library claims below were checked against official documentation; account-specific model access remains unverified.

**Needs decision** marks a proposed mechanism or behavior not already settled.

## 1. Consistency

| Finding / where | Why it matters | Suggested fix |
|---|---|---|
| **No durable approved-but-unclaimed state.** D60 omits `approved`; `docs/schema.md` explicitly requires distinguishing approval from execution. | D21/R5: approval can succeed, then the process can crash before a worker starts the request. Neither `pending_approval` nor `executing` accurately describes that interval. | Add `approved`, or separate approval state from execution state. Define the atomic worker claim. **Needs decision.** |
| **Secret replacement by host conflicts with named bindings.** D76 says adding a secret for the host restores the call; D29/D32 and `runs.snapshot` bind `{{secret.NAME}}` to a stable ID. | Two secrets can share a host. Host equality cannot identify the intended replacement. | Require the starter, expected reference name and allowed host to match; explicitly replace the deleted ID and increment the revision. **Needs decision.** |
| **Retry timing contradicts itself.** D79 caps each wait at 30 seconds, honours `Retry-After`, and fails only above 60 seconds. | A server delay of 45 seconds cannot satisfy all three rules. R8 requires sensible rate-limit handling. | Select one maximum accepted delay; honour accepted delays without shortening them. **Needs decision.** |
| **Forced tool use conflicts with a supported model.** D72 requires Anthropic `tool_choice: {type:"any"}`; D78 includes `claude-sonnet-5-5`, which rejects it. | Rejection continuation would fail with HTTP 400. | Change the rejection policy or supported model policy; do not silently substitute a model. **Needs decision.** [Anthropic documentation](https://platform.claude.com/docs/en/api/errors#forced-tool-use-not-supported) |
| **Indefinite approval versus unconditional eventual completion.** D65 excludes approval waiting from D74’s deadline. | A run abandoned at approval never reaches a terminal state. Literal “every run eventually ends” under R4 cannot coexist with indefinite waiting. | Clarify the requirement interpretation against the PDF: either waiting is an intentional exception, or a finite abandonment policy is needed. **Needs decision.** |
| **Method mismatch.** D63 permits retrying `HEAD`; D81 excludes `HEAD` from allowed methods. | Small contract inconsistency. | Remove the unreachable exception or permit `HEAD`. Permitting it **needs decision**. |
| **D83 overstates retention.** `store:false` disables stored Responses state; “no second copy at OpenAI” is broader. | Documentation should distinguish application state from provider retention. | Narrow the rationale; default abuse-monitoring retention is separate. [OpenAI data controls](https://developers.openai.com/api/docs/guides/your-data) |

B3 also requires the following schema/API amendments. The capabilities follow from existing decisions; the proposed representations **need decision**.

| Location | Required amendment |
|---|---|
| `runs` | Lease owner, lease expiry and ownership generation; durable execution checkpoint; consecutive-invalid-call count; restart-safe active-time accounting. Define terminal-state immutability and failure codes. |
| `tool_calls` | Durable approval/execution distinction; approved revision; execution claim identity; execution timestamps and outcome reason. Define how cancellation closes pending calls within D60’s status vocabulary. |
| `user_model_keys` | A credential revision/generation distinct from encryption `key_version`; pending approvals must identify the model-key generation they acknowledge. `updated_at` alone is a weak concurrency token. |
| `model_calls` | One row per actual provider attempt, linked to a logical turn with an attempt number and outcome. An unfinished attempt must be representable and recoverable as interrupted/usage unavailable. |
| `run_messages` | Define accepted-turn boundaries and structured provider continuation material. Tool-call IDs must be scoped to the run/turn. Discarded attempts must not become authoritative history. |
| `run_events` | Existing `(run_id, seq)` can supply SSE IDs—another ID column is unnecessary. Specify event types, attempt identifiers, ordering, discard markers and terminal events. |
| Approval/key/secret endpoints | Specify invalidation, replacement eligibility, revision changes and stale-request responses. Define reject’s optional reason in its request body. |
| `/runs/{id}/cancel` | Replace provisional authority with starter-or-own-team-lead; define terminal-run and repeated-request behavior. |
| `/runs/{id}/events` | Replace provisional notes with replay, cursor, terminal, authorization and session-expiry contracts. |
| `/usage` | Return known totals together with incomplete/unavailable accounting indicators; unknown consumption must not disappear into zero. |

`PLAN.md`’s B3 status and “Next step” still describe part 2 as open. Reconcile them after the author resolves this review.

## 2. Final states — R4

**The current decisions do not yet establish exactly one terminal transition on every execution path.** They describe outcomes, but not the shared finalization and recovery rules.

| Path | Intended outcome | Gap / suggested fix |
|---|---|---|
| Normal assistant completion | `succeeded` | Require a provider-recognized complete response, persist the final message, and finalize atomically. EOF alone is insufficient. |
| Transient provider error | Retry, then continue or `failed` | Persist attempt/retry progress so restarts cannot reset the allowance. |
| Permanent provider error | `failed` | D79 is clear; define stable, sanitized error codes/messages. |
| Retries exhausted | `failed` | Explicitly state the terminal transition and whether “3 retries” means four total attempts. |
| Model-attempt deadline | Retry or `failed` | D74 bounds the attempt; D79 does not clearly classify this timeout. **Needs decision.** |
| Tool deadline | Tool failure or `outcome_unknown`; loop continues | For a potentially dispatched write, use D63. Distinguish this from failures proven to occur before dispatch. |
| Active-run deadline | `failed` | Persist accumulated active time; do not grant a fresh hour after restart. Stop outstanding work and classify an executing write as unknown. |
| Step limit | `failed(step_limit)` | Define the boundary: a normal final answer on turn 20 should be accepted; needing turn 21 should fail. **Needs decision.** |
| Three consecutive invalid calls | `failed(invalid_tool_calls)` | Persist the counter. Define what resets it and how an unexpected multiple-call response is counted. |
| Crash during model call | Recover and repeat under D62 | Mark the old attempt interrupted, discard partial output, and retain its metering uncertainty. Bound repeated crash recovery. |
| Crash during tool call | Read retry, or write `outcome_unknown` | Recover from the persisted call checkpoint; never regenerate a call whose dispatch/result is already recorded. |
| Server restart | Resume queued/expired work; preserve approval | Scan queued runs as well as expired leases. In-process wakeups alone can be lost. |
| Cancel during model call | `cancelled` | Cancel locally, persist independently of the cancelled request context, and prevent a late completion from changing the run. |
| Cancel during retry wait | `cancelled` | Cancellable waits are specified; durable cancellation detection across workers is not. |
| Cancel while awaiting approval | `cancelled` | Atomically close approval eligibility; a concurrent approval must not revive the run. |
| Browser closes | Execution continues | D61 is consistent with R4, subject to worker recovery and the indefinite-wait qualification above. |

Suggested common rule: terminal transitions use one conditional transaction that updates an eligible nonterminal run, sets `finished_at`, and inserts its terminal event. Competing completion/cancellation updates have one winner; no later worker may resume it. Use a bounded persistence context independent of the cancelled network context. **Needs decision.**

Progress checkpoints also need atomic boundaries: accepted model output plus pending call, and completed tool outcome plus its conversation result. Otherwise recovery can repeat work or omit a result. **Needs decision.**

Completion necessarily assumes the server eventually returns and storage becomes writable; permanent process/database failure cannot provide an unconditional liveness guarantee.

## 3. Approval and side effects

**D21 prevents competing approval decisions; it does not yet prevent competing dispatches.**

| Race / where | Failure scenario | Suggested fix |
|---|---|---|
| Approve versus reject — D21/D55 | Both browsers submit the same pending revision. | Existing conditional-update rule is sound if it checks call/run relationship, starter, team, pending state and revision together. Persist the winner and continuation atomically. |
| Approve versus cancel — D66 | Approval succeeds, cancellation commits, then a worker dispatches using its earlier read. | Serialize cancellation against a durable dispatch claim. Define dispatch authorization as the boundary: cancellation before it prevents dispatch; after it cannot promise prevention. **Needs decision.** |
| Approval versus secret deletion — D29/D55 | A call has left `pending_approval` but has not been dispatched; deletion only invalidates “pending” rows. | Cover approved-but-unclaimed calls too, and revalidate credentials when claiming execution. **Needs decision.** |
| Secret value replacement — D29/D54 | Same secret ID now authenticates a different external account. | Current-value use is already intentional. Document that approval does not pin account identity. Reapproval on value rotation would change policy and **needs decision**. |
| Model-key change — D77 | Key changes invalidate the call, but the old browser revision becomes valid again after restoration. | Increment `approval_revision` on key invalidation/restoration; compare credential generations atomically. **Needs decision.** |
| Multiple invalidation causes — D76/D77 | Recreating a secret makes the call approvable while its model key is still missing. | Restore `pending_approval` only after all prerequisites pass; retain clear reasons. Define model-key restoration as explicitly as secret restoration. **Needs decision.** |
| Lease takeover — D59/D62/D63 | Old worker is paused, lease expires, new worker resumes; old worker later returns. | Fence state/message/event writes by ownership generation. Persist an exclusive call claim before network dispatch. **Needs decision.** |
| Rejection recovery — D72 | Crash after rejection loses the “next turn requires a tool” obligation. | Persist or unambiguously derive that obligation and its consumption. A new proposal must receive fresh approval when required. |

For takeover, define this sequence explicitly: acquire the expired run with a new generation; find any side-effecting call left `executing`; conditionally mark it `outcome_unknown`; append exactly one corresponding tool result/event; never dispatch that call again. A previously persisted result must be reused. **Needs decision.**

An old worker may already have sent the request. Database fencing cannot cancel an external effect or establish exactly-once execution. D63 is a reasonable conservative response, but D59’s “no double execution” rationale should be narrowed.

Also, “never retried automatically” must cover model-driven repetition: the model might propose the same operation as a **new** call after receiving `outcome_unknown`. For approval-required tools, show the previous uncertainty prominently and require fresh approval. Blocking equivalent operations would be a further policy, **needs decision**.

## 4. Leases and workers

- **Renew during every long operation — D59/D74/D79.** Renewal cannot depend on stream deltas or retry-loop iterations: a legitimate model request may remain silent for minutes. Use an independent heartbeat and stop work if renewal cannot preserve ownership. Lease duration, renewal interval and safety margin **need decision**; for example, 60 seconds renewed every 15 seconds is a candidate, not a guarantee against process pauses.

- **Define clock and expiry rules — D59/D62.** Use a consistent persisted time source for claims/renewals. Same-machine replicas share the clock, but wall-clock jumps remain possible; Go’s in-memory monotonic time does not survive persistence. Document the assumption and fence all post-expiry writes. **Needs decision.**

- **Release workers while waiting — D65/D74.** A durable approval should not occupy a worker or require perpetual lease renewal. Define approval/rejection as an atomic transition back to claimable work. **Needs decision.**

- **Poll durable work — D59/D62.** Startup and periodic scans must discover queued runs, expired leases and resumed approvals. Otherwise a crash between database commit and notification can strand a run.

- **Bound recovery — D62/D79/D80.** Repeated crashes must not reset retries, steps or active-time limits. Specify whether downtime counts toward active time and how partially elapsed execution intervals are charged. **Needs decision.**

## 5. Retries and errors

D10/D79’s cause-based approach is sound, but the classification needs these amendments:

| Finding / where | Why / suggested fix |
|---|---|
| **OpenAI quota detection is too narrow — D79.** | Inspect structured `error.code` **and** `error.type`. Current documentation lists exhausted-credit and spend/usage-limit codes beyond `insufficient_quota`; these require intervention, not backoff. [OpenAI error codes](https://developers.openai.com/api/docs/guides/error-codes) |
| **Anthropic 429 is not always temporary — D79.** | Current documentation includes tier spend-cap failures under 429, alongside temporary rate limits; 402 is `billing_error`, and configured spend limits can produce 400. Use documented structured signals; do not infer permanence solely from an absent retry header. Exact fallback for ambiguous responses **needs decision**. [Anthropic errors](https://platform.claude.com/docs/en/api/errors) |
| **Missing timeout/status classes — D79.** | Explicitly handle 408, 409 and 504. Anthropic documents 504 `timeout_error`. Bounded retry can be appropriate, but 409 classification is endpoint-specific. **Needs decision.** |
| **“Network errors” is too broad — D79.** | Connection resets may be transient; malformed URLs and certificate-validation failures generally are not. User cancellation and run-deadline expiry must never trigger another attempt. Define the adapter categories. **Needs decision.** |
| **Streaming errors require payload inspection — D73/D79.** | OpenAI has `response.failed`, `response.incomplete` and error events after streaming begins. Anthropic can emit `overloaded_error` inside an HTTP-200 stream. Apply the same cause classification to these events. [Responses events](https://developers.openai.com/api/reference/resources/responses/streaming-events), [Anthropic streaming](https://platform.claude.com/docs/en/build-with-claude/streaming) |
| **Context overflow and output exhaustion differ — D79/D80.** | An overlong input cannot succeed unchanged. Output-token exhaustion/incomplete generation must not be treated as ordinary success or blindly replayed with identical limits. Define its terminal/recovery policy. **Needs decision.** |
| **Malformed model arguments differ from a failed generation — D48/D80.** | A completed response with malformed tool JSON goes through R3’s corrective loop. Truncated JSON from an interrupted generation belongs to the discarded attempt and must never execute. |
| **Tool retry eligibility is incomplete — D63/D79.** | “GET may retry once” should not mean retrying authentication errors or every HTTP failure. Define eligible transient read failures; classify ambiguous writes conservatively. **Needs decision.** |

Keep SDK retries disabled. Explicitly define **initial attempt plus three retries**, if that is the intended meaning, and preserve the allowance across recovery. For valid `Retry-After`, wait at least the requested duration or stop retrying; never cap it downward and send early. [OpenAI retry guidance](https://developers.openai.com/api/docs/guides/rate-limits)

D75 is sound: retain known usage from failed attempts; otherwise mark unavailable. Anthropic’s streamed usage counters are cumulative, so take the latest values rather than summing deltas. [Anthropic streaming](https://platform.claude.com/docs/en/build-with-claude/streaming)

## 6. Tool HTTP safety

- **IP checking needs a precise implementation contract — D81.** `net.Dialer.Control` runs before dialing, rather than inspecting an already-connected peer. Validate every numeric dial candidate on the actual transport path, including IPv4-mapped IPv6; keep TLS verification enabled and proxies disabled. A preflight DNS check followed by another unrestricted resolution is insufficient. [Go Dialer](https://pkg.go.dev/net#Dialer)

- **The denied-range policy is incomplete — D81.** Private/link-local checks do not cover every non-public destination, including shared address space such as `100.64.0.0/10`. Specify supported public-address ranges and treatment of special-use/transition addresses. `IsGlobalUnicast` alone is insufficient because it includes private addresses. **Needs decision.** [Go address predicates](https://pkg.go.dev/net/netip#Addr.IsGlobalUnicast)

- **Authority matching must include a port policy — D32/D81.** Define whether a binding to `api.example.com` permits `api.example.com:8443`. Parse and canonicalize once; reject malformed/empty hosts, scoped IPv6 addresses and URL fragments; ensure the actual request authority matches the checked authority. Restricting tools to port 443 is a simple option, **needs decision**.

- **Do not permit template headers to override transport routing — D11/D81.** Reject `Host`, proxy-routing and transport-controlled headers; preserve normal TLS hostname verification. Compare header names case-insensitively. The exact forbidden-header policy **needs decision**.

- **Escaping does not define template semantics — D81.** `PathEscape` does not neutralize `.`/`..` path segments; servers may decode escaped separators. Restrict placeholder positions and parameter types. Render parsed JSON values rather than performing unrestricted textual replacement, and never interpret substituted parameter text as new template syntax. **Needs decision.**

- **HTTP method does not prove absence of side effects — D63.** A poorly designed GET endpoint can mutate state. Treat the GET/HEAD retry rule as an explicit trust assumption; stronger classification would **need decision**.

- **Transport retries can bypass D63.** Go’s transport can automatically retry eligible requests, including requests marked idempotent by an `Idempotency-Key` header. Audit this behavior so a template header cannot silently enable write replay. **Needs decision** on how to enforce the intended restriction. [Go Transport](https://pkg.go.dev/net/http#Transport)

- **Bound and sanitize before exposure — D33/D80.** Specify decompression limits, response-header limits, UTF-8-safe truncation and truncation order. An arbitrary response’s original size is unknown once reading stops, so do not report a fabricated `Y` in “X of Y bytes.” Whole-secret masking after truncation can miss a secret cut at the boundary. **Needs decision** for these limit details.

Host binding prevents sending a credential to another host; it does not constrain accounts, paths or operations on that host. D33’s finite masking forms also cannot guarantee removal of arbitrary transformed echoes. These remain trust assumptions to document; this review does not treat the previously rejected suspicious-response withholding proposal as adopted.

## 7. Streaming

- **Ordering and durability — D69/D70:** allocate event sequence numbers transactionally; persist lifecycle changes with their events. Deliver committed rows ordered by `seq`. Notifications should only wake readers; polling must repair missed notifications. **Needs decision** on the exact transaction/event contract.

- **Replay — D67/D69, `/runs/{id}/events`:** use `(run_id, seq)` as the cursor scope and replay `seq > cursor` before continuing live reads. Define missing/malformed/future cursors and fresh-page behavior. Native EventSource’s automatic `Last-Event-ID` applies to reconnection, not arbitrary new page instances; full replay or an explicit initial cursor is needed. **Needs decision.** [SSE standard](https://html.spec.whatwg.org/multipage/server-sent-events.html)

- **Discarded attempts — D75/D79:** tag deltas by turn and attempt; persist an attempt-discarded event before replacement output. Replay must produce the same visible result as live delivery. Only accepted complete output enters `run_messages`; tools must not execute from an attempt that might still fail. **Needs decision** on event payloads.

- **Coalescing — D70:** 250 ms is reasonable. Also bound batch bytes and flush before call-completion, discard, approval and terminal events. An unflushed crash tail can be lost; mark that attempt interrupted rather than treating the tail as authoritative. Avoid switching to per-delta writes without evidence that batching is inadequate.

- **Proxy risk — D68:** realistic, but configuration-specific. Next.js supports streaming; buffering anywhere in the path can defeat it. Verify the chosen rewrite/Route Handler in both development and production modes, forwarding cookies and `Last-Event-ID`, disabling caching, and avoiding body collection. Direct Go SSE changes credential/CORS configuration and is not a drop-in fallback. [Next.js self-hosting](https://nextjs.org/docs/app/guides/self-hosting)

- **Authorization and consumers — D53/D71:** apply token-usage filtering to live events and replay, not only run details. Check session validity before sending heartbeat-cycle data; document the approximately 15-second revocation delay. Bound slow-consumer buffers and disconnect lagging readers without blocking execution. Close EventSource client-side after terminal delivery. **Needs decision** for these delivery limits.

## 8. SQLite

**D82 is a sound starting configuration**, with these qualifications:

| Finding / where | Why / suggested fix |
|---|---|
| **Driver settings are supported — D82.** | `modernc.org/sqlite` documents repeated `_pragma` parameters and `_txlock=immediate`. Verify effective values on newly opened connections under the pinned version. Do not rely on an assumed `synchronous` default without checking it. [Driver documentation](https://pkg.go.dev/modernc.org/sqlite) |
| **One writer connection is per process.** | Several replicas create several competing writer connections. SQLite serializes them, but busy errors remain possible. Narrow “single writer” wording and define bounded handling of lock contention. **Needs decision.** |
| **Reader-pool initialization needs ordering.** | Establish WAL/migrations before admitting readers/workers. Do not attempt journal-mode changes after enabling `query_only`; use read transactions for readers, not the writer’s immediate transactions. |
| **Short transactions remain essential — D21/D59.** | Put claim conditions and dependent writes in one transaction. Do not read from the reader pool and later perform an unconditional writer update. Network calls and SSE delivery must stay outside transactions. |
| **WAL is not unlimited concurrency.** | Long-lived read transactions can prevent checkpoint progress, and WAL can still return `SQLITE_BUSY`. Read a bounded event page, close rows, then deliver it. [SQLite WAL](https://sqlite.org/wal.html) |
| **Replica scope must remain explicit — D59/D69/D82.** | Same-host processes sharing one local database are plausible; independent databases or network-filesystem replicas are not. Shared sessions, encryption configuration and durable polling are also required. “Multiple replicas” must not imply multi-host support. |

D82’s same-machine restriction resolves much of the earlier ambiguity. It does not establish duplicate-free external effects.

## 9. Scope

The highest-cost B3 work is stronger recovery/concurrency behavior than the case requires.

| Costly item / where | Simpler version preserving requirements |
|---|---|
| **Multiple active replicas and lease takeover — D59/D62/D69.** | One backend process with a bounded worker pool, durable checkpoints, restart recovery and durable approvals. Explicitly exclude concurrent backend processes. R3–R5/R8 remain covered. **Needs decision** to narrow current commitments. |
| **Immediate cross-replica cancellation — D66.** | One-process context cancellation plus durable terminal state and conservative unknown-outcome handling. Describe cancellation as best effort against external services. **Needs decision.** |
| **Forced tool call after rejection — D72.** | Return rejection/reason to the model and allow either a revised tool proposal or a final explanation. This satisfies R5 and avoids the Sonnet incompatibility and unnecessary corrective loops. **Needs decision.** |
| **Retrying partially streamed generations — D79.** | Retry transient failures before visible output; after partial output, record usage uncertainty and fail clearly. R4/R8 do not require transparent stream recovery. **Needs decision.** |
| **Broad template language — D81.** | Support a small, explicit JSON-template grammar and documented URL-placeholder positions. This still supports data-defined HTTP tools without a general templating engine. **Needs decision.** |

Keep per-attempt metering, durable approval revisions, starter-bound credentials, team authorization, request safety and replay correctness. Those address core requirements.

The USD 5 cap also deserves explicit verification planning: long deadlines and repeated full-history calls do not bound spend. Output-token limits and a small authorized live-call allowance would help; exact limits **need decision**.

## Items for B4

All proposed test mechanisms and example choices **need decision**.

1. **Example agent and authority:** choose two real HTTP tools, an approval-required write, and the exact authorized test repository/resource. Configure them through API/UI. Confirm the template grammar can express the required authentication headers.

2. **Provider evidence:** demonstrate a complete tool round trip with both providers, including selected model access, continuation state, stop reasons and usage. A model-list response alone does not prove tool/stream compatibility. Preserve OpenAI replayable Items and Anthropic content blocks/call IDs rather than flattening everything into text. [OpenAI continuation guidance](https://developers.openai.com/api/docs/guides/reasoning)

3. **Deterministic lifecycle tests:** use fake adapters, controlled clocks and file-backed SQLite. Cover permanent/transient errors, quota exhaustion, in-stream overload, abrupt EOF, incomplete output, retry cancellation, deadlines and both loop limits.

4. **Crash/race tests:** inject interruption before/after approval, dispatch claim, external success and result persistence. Race approve/reject/cancel, credential changes and lease takeover. Assert terminal immutability, no unauthorized dispatch and conservative unknown outcomes.

5. **Security/streaming evidence:** test cross-team access and usage filtering on replay; host/port mismatches; mapped/private IPs; redirects; escaping; secret echoes; size limits; reconnect without gaps; discarded-attempt rendering; and the actual Next.js SSE path.

6. **Delivery evidence:** record one sanitized run with approval and final output, reserve a small live-call budget, and test README setup from a clean clone. Keep simulated verification distinct from real-provider evidence.