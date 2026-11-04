## 1. Consistency

**B4 is feasible, but the quick reviewer path currently contradicts the run-creation contract.** No code exists yet; these are design findings, not verified behaviour.

| Finding / where | Why it matters | Suggested fix |
| --- | --- | --- |
| **Quick path cannot start — D101; `POST /agents/{id}/runs`; `runs.snapshot`.** The endpoint rejects missing secrets, and the snapshot binds every secret reference to the starter’s secret ID. The three-tool agent requires `GITHUB_TOKEN`. | A reviewer without that secret cannot reach the proposed approval/rejection screen. | Simplest: make quick mode a read-only agent containing the first two tools, accompanied by the recorded approval run. Keeping interactive rejection without a secret requires changing credential-binding/precondition rules. Either revision **needs decision**. Do not use a dummy credential. |
| **GitHub authentication is underspecified — D100 versus D54/D93.** `Authorization: Bearer {{secret.GITHUB_TOKEN}}` violates the whole-header-value grammar. | The full example would fail template validation. | Specify `Authorization: {{secret.GITHUB_TOKEN}}`, with the encrypted secret containing the complete `Bearer …` value. Adopt this credential-entry convention explicitly: **needs decision**. See section 2. |
| **Successful EA evidence is implicit — D103/D104.** “End-to-end” and “approval” do not explicitly require an approved request that actually creates an issue. | EA requires a real side effect; rejection alone cannot establish it. | State that the recorded OpenAI run includes approval, GitHub HTTP `201`, the issue URL, and the final answer. This clarifies the existing EA obligation. |
| **Exact external target remains open — D101.** The repository name is still an example. | The previous B3 review requested an exact authorised test resource. | Record the actual sandbox repository and permitted action before the live write. Naming/authorising it **needs decision**. |

D102 is consistent with D84’s single-process scope: no lease/takeover tests are needed. D104’s single recorded run also does not contradict D103’s two live provider runs; both runs still need honest verification records.

## 2. Example agent feasibility — D100/D101

### Concrete template checks

| Tool / concern | Assessment and suggested fix |
| --- | --- |
| **GitHub contents URL — D81/D93** | `https://api.github.com/repos/{{owner}}/{{repo}}/contents/{{path}}` fits the grammar for a root filename such as `package.json`. A nested value becomes `a%2Fb%2Fpackage.json`. **Uncertain:** GitHub’s documentation does not establish that every encoded-slash routing case behaves as intended. Prefer the proposed root manifest; choosing that sandbox layout **needs decision**. A known nested path could instead be literal template text without extending the grammar. |
| **Raw `Accept` header** | Use the literal `Accept: application/vnd.github.raw+json`. GitHub documents this as returning raw file contents. It is neither sensitive nor forbidden by D94. Ensure the response handling recognises JSON/text media types rather than treating this manifest as binary. [GitHub contents API](https://docs.github.com/en/rest/repos/contents#get-repository-content) |
| **`User-Agent` and other headers** | GitHub requires a valid `User-Agent`. Put a literal application name in both GitHub tool definitions. `Accept`, `User-Agent`, `Content-Type`, and `X-GitHub-Api-Version` are compatible with D93/D94; specify a supported API version in the example. [GitHub request headers](https://docs.github.com/en/rest/using-the-rest-api/getting-started-with-the-rest-api#headers) |
| **OSV request body** | The body below fits D93 exactly: each placeholder occupies a whole JSON string value. OSV accepts package name/ecosystem plus a version. It does not accept an entire `package.json` as this query. [OSV query API](https://google.github.io/osv.dev/post-v1-query/) |
| **Issue request body** | `{"title":"{{title}}","body":"{{body}}"}` fits D93. JSON encoding preserves multiline Markdown, quotes and backslashes. The platform must encode the string value, rather than splice text into serialized JSON. GitHub accepts a string issue body. [Create issue API](https://docs.github.com/en/rest/issues/issues#create-an-issue) |
| **Issue authentication** | GitHub documents `Authorization: Bearer TOKEN`. Under D54/D93, the entire header must come from `{{secret.GITHUB_TOKEN}}`; a bare PAT alone is insufficient, and a literal prefix plus placeholder is invalid. [GitHub authentication](https://docs.github.com/en/rest/authentication/authenticating-to-the-rest-api) |

A compatible OSV body is:

```json
{
  "package": {
    "name": "{{name}}",
    "ecosystem": "npm"
  },
  "version": "{{version}}"
}
```

**Manifest recommendation — needs decision:** a root `package.json` with three exact dependency versions is a good minimum. `npm` is an OSV ecosystem identifier. Avoid ranges, tags, aliases, workspace references and Git URLs in this demonstration. Describe it as checking the listed direct dependencies, since it does not resolve a transitive dependency graph. The manifest is input data; installing its vulnerable packages is unnecessary. [OSV ecosystem definitions](https://ossf.github.io/osv-schema/#affectedpackage-field)

**Credential-masking consequence — D33/D54:** if the stored secret is `Bearer TOKEN`, existing exact-value masking catches that full string but does not necessarily catch an API echo of just `TOKEN`. Before adopting that convention, explicitly cover the underlying credential and its required encodings in masking tests; extending the masking rule **needs decision**.

### Turns and response sizes

With D64’s single tool per turn, the normal sequence is:

| Dependencies | Read manifest | OSV queries | Propose issue | Final answer after approval/result | Total model turns |
| --- | --- | --- | --- | --- | --- |
| 3 | 1 | 3 | 1 | 1 | **6** |
| 5 | 1 | 5 | 1 | 1 | **8** |

Approval itself consumes no model turn. A separate summary adds one; corrections, pagination and revised issue proposals add more. This leaves reasonable room below D80’s 20-turn limit. Provider retries are attempts within a turn, not additional logical turns.

**Response size remains unverified until dependencies are chosen.** OSV returns full vulnerability records and can paginate; its response allowance is much larger than D80’s 128 KB model limit. There is no safe inference from “only three dependencies” to “small responses.” [OSV responses and pagination](https://google.github.io/osv.dev/post-v1-query/#pagination)

Suggested verification:

- Measure the actual candidate responses before fixing the sandbox contents; prefer packages with modest advisory output.
- Treat truncation or pagination as incomplete evidence, never as “no vulnerabilities.”
- Keep issue output concise: package/version, advisory IDs, links and actionable findings.

D95’s 4096-token cap limits generated output, not OSV input. It is plausibly sufficient for a short issue, but repeated full-history inputs accumulate across turns. Five responses at the maximum would contribute approximately 640 KB before other history. Neither “a few cents” nor context fit is established yet. Changing limits or adding response extraction **needs decision**; selecting smaller demonstration inputs is simpler.

## 3. Reviewer paths — D101

**Quick mode is acceptable as an optional introductory path, once the contradiction in section 1 is resolved. It is insufficient as the sole EA evidence.** A recorded successful write plus a reproducible full path must establish the actual side effect.

**Full mode is realistic** for a reviewer’s personal repository with Issues enabled and a fine-grained token restricted to that repository with Issues write permission. Keep the manifest repository public because the read tool deliberately sends no credential. The reviewer can read the author’s public manifest while writing to their own repository, provided the task clearly distinguishes those targets. GitHub documents fine-grained token support for issue creation. [Issue permissions](https://docs.github.com/en/rest/issues/issues#create-an-issue)

| Risk / where | Suggested fix |
| --- | --- |
| **Unauthenticated reads — D100/D101.** GitHub allows 60 requests/hour per originating IP; a shared office/VPN IP may already be exhausted. Limits can return `403` or `429`. | Document this failure clearly and preserve relevant response headers. A normal demonstration needs only one contents request. D91’s automatic GET retry must not immediately resend against an explicit reset/wait instruction; any policy clarification **needs decision**. [GitHub rate limits](https://docs.github.com/en/rest/using-the-rest-api/rate-limits-for-the-rest-api) |
| **“About 3 minutes” is unverified — D101.** Login, reauthentication, token creation and repository configuration vary. Organization-owned targets can require token approval. | Use a personal repository in the instructions and label external preparation separately. Do not promise three minutes before timing it. [Fine-grained token management](https://docs.github.com/en/authentication/keeping-your-account-and-data-secure/managing-your-personal-access-tokens) |
| **Setup measurement — D7/D101 and README deliverable.** Separating external preparation does not prove the platform starts within ten minutes. | Time a clean-clone setup including dependency downloads, environment/master-key preparation, seed users, startup and model-key entry. Report prerequisites and the separately measured full-demo preparation honestly. |

## 4. Testing — D102

**The approach covers all four required areas.** Its credibility depends on assertions and failure boundaries, rather than the number of test files. The following are the first test groups I would write, using the mechanisms already selected in D102.

| Priority / proposed test | Essential assertions and requirement |
| --- | --- |
| **1. `TestApprovalPersistsAndDispatchesOnce`** | File-backed DB; no dispatch while pending; close/reopen backend; same proposal, revision and expiry survive; approval produces one dispatch and a final state. R5, D21/D84/D86. |
| **2. `TestLoopValidationAndLimits`** | Unknown tool, malformed JSON and schema violation return corrective results; valid calls reset the streak; three consecutive invalid calls fail; final answer on turn 20 succeeds; another required turn fails. R3, D48/D80/D94. |
| **3. `TestTerminalStateAndCancellation`** | Success, permanent provider error, exhausted retries, timeout and cancellation terminate correctly; late callbacks cannot change a terminal state or append events after `run_finished`. Disconnecting SSE does not cancel execution. R4, D61/D94/D97. |
| **4. `TestApprovalRacesAndCredentialChanges`** | Deterministically pause before dispatch. Race approve/reject and approve/cancel; check stale revisions, secret deletion, model-key replacement/deletion and expiry against `approved` too. No dispatch after a prerequisite loses eligibility. D21/D55/D77/D86/D90/D99. |
| **5. `TestInterruptedWriteOutcomeUnknown`** | Interrupt after durable execution claim and after external success but before result persistence. Restart produces `outcome_unknown`, one tool-result message and no automatic resend. Persisted completed results are reused. D63/D94. |
| **6. `TestTemplatesAndHTTPPolicy`** | Render the three example tools; check multiline JSON, escaping and nonrecursive substitution. Reject forbidden placeholder positions, sensitive literals, forbidden headers, CR/LF, bad host/port, private/mapped/special-use addresses; follow no redirects and ignore proxy environment settings. R2, D81/D93/D94. |
| **7. `TestSecretsAndStarterCredentials`** | A teammate’s agent uses the starter’s key/secret; missing credentials never fall back. Ciphertext is not plaintext and wrong identity/master key fails. Inspect DB records, previews, events, errors and model inputs for test-secret echoes, including encoded forms and truncation boundaries. R2/R7, D14/D31/D33. |
| **8. `TestTeamAndUsageAuthorization`** | Cross-team reads, writes, run starts, tool IDs in request bodies, approval, cancellation and SSE/replay return `404`. Same-team ownership/approval restrictions return `403`. Usage filtering holds in details, aggregates and replay. R6/R8, D39/D41/D53. |
| **9. `TestProviderStreamsAndReplay`** | For both adapters: complete tool round trip, call IDs/raw continuation content, usage, 429 handling, permanent errors, broken stream/discard/retry, incomplete output and cancellation during backoff. Check ordered replay, no replay/live subscription gap and session revocation. R4/R8, D75/D88/D92/D94/D96/D98. |

Use table cases and explicit barriers/channels for races, rather than sleeps. File reopening is useful restart evidence; one controlled abrupt-stop check should additionally exercise the ambiguous-write boundary.

**TLS test seam — D102/D81/D94:** safe if private dependency injection supplies test behaviour and the production constructor always installs the real policy. There must be no environment variable, API flag or startup option that disables it.

Address replacement alone is insufficient: `httptest.NewTLSServer` uses a test listener, normally on an ephemeral port, while D94 permits only 443. A test-only dial mapping can preserve a logical `:443` request and connect to that listener using an explicitly trusted test certificate. Test the production address/port checks separately. Do not globally disable TLS verification. [Go TLS test servers](https://pkg.go.dev/net/http/httptest#NewTLSServer)

**Drop/defer first:** exhaustive crash-point permutations, automated UI tests, randomized stress/fuzz suites, performance benchmarks, multi-process tests and duplicate happy paths. This prioritisation **needs decision** where it narrows intended test scope. Retain a manual Next.js SSE check in development and production modes; backend fixtures cannot establish that the proxy streams correctly. Omitting default `-race` is reasonable under D102, but deterministic concurrency tests remain necessary.

## 5. Provider evidence and examples — D103/D104

**One successful complete run per provider is sufficient minimum live evidence for R8, alongside deterministic error/metering tests.** Each run should show:

- The actual model ID and starter-owned credential path.
- Streaming, a tool call, its result and a valid continuation.
- Per-attempt usage and correct user/team aggregation.
- A terminal state.

At least the recorded OpenAI run must complete the real approved write. D103 calls both runs end-to-end; a text-only response from the second provider would not satisfy that commitment.

**Uncertain:** this review has not validated access to the four D78 model IDs. The two D103 runs validate only the two selected models. Do not silently substitute models or claim the stronger models were exercised. Any supported-model change **needs decision**. Likewise, measure actual spend before describing these runs as costing only a few cents.

For **D104 and `examples/`**, preserve:

- Task, manifest source/revision, provider/model and the credential-free agent/tool configuration or a reproducible reference to it.
- Ordered event IDs/types and streamed text, including discard markers if present.
- Tool parameters, sanitized request preview and real results.
- Approval revision, decision and timestamps.
- GitHub `201`, created issue URL, final answer and terminal event.
- Usage evidence, with unavailable usage distinguished from zero.

A raw SSE export can be represented as JSON, but retain event IDs, types and ordering. The readable transcript should identify any redaction. Export allowlisted application data rather than copying database rows or HTTP headers wholesale.

**Before commit — D33/D104 and AGENTS.md:**

1. Scan every example artifact for known development credential values and relevant encoded forms; report filenames/counts without printing matches.
2. Scan for token/key patterns, `Authorization`, `Cookie`, `Set-Cookie`, passwords, private-key blocks and credential-bearing URLs.
3. Inspect the readable transcript and `git diff --cached` manually, including tool arguments, error text and embedded JSON.

Pattern scans alone are insufficient. Screenshots or recordings need visual inspection as well. Choosing a new scanning dependency **needs decision**; the checks can initially use existing tools.

## 6. Scope and schedule risk

**The largest risk is implementing the B3 execution guarantees from scratch within four and a half days.** The example itself is comparatively small.

| Likely overrun / where | Simpler version preserving R1–R8 and EA |
| --- | --- |
| **Recovery, approval invalidation and races — D84/D86/D94/D99** | Keep one execution path, one durable dispatch claim and explicit checkpoints. Avoid a generic workflow/recovery framework. |
| **Partial-stream retry and provider continuation — D92/D96** | The simpler alternative is to fail after a stream breaks, preserving usage and a clear final event. This was previously rejected; reconsidering it **needs decision**. Otherwise implement and test the selected behaviour early. |
| **Complete configuration UI — D7/D18/D100** | Use simple JSON forms/API requests for agent/tool creation; retain login, key entry, live run view and approval UI. No visual polish or schema-form builder. |
| **General dependency analysis — D100** | Three pinned direct dependencies in a root manifest, one query each, one concise issue. No transitive resolution, installations, extra advisory-fetch tools or ecosystem expansion. Sandbox scope **needs decision**. |
| **Reviewer setup and proxy integration — D7/D68/D101** | Exercise the real browser→Next.js→Go→provider→approval path early. Do not leave streaming or clean setup to the final day. |
| **Budget — D95/D103** | Use fixtures for repeated development checks; reserve live calls for provider validation and the final recorded example. A concrete spending allocation **needs decision**. |

Do not remove team enforcement, encrypted starter-owned credentials, durable approval, final states, usage accounting or HTTP safety to recover time.

## Items for B5

All schedule/process proposals below **need decision**.

1. **Resolve B4 blockers immediately:** quick-mode semantics, complete authorization-header convention, exact sandbox target, ecosystem and manifest contents.
2. **Set a short implementation schedule:** by Nov 5, reach a thin vertical slice with login, saved definitions, one real provider, SSE and durable approval; Nov 6, complete lifecycle/security and the second adapter; Nov 7, run both providers and the real write; Nov 8, clean setup, required tests and deliverables; retain Nov 9 for fixes and recording.
3. **Pin toolchains/dependencies and supported OSes.** Measure the ten-minute setup on a clean clone before making the claim.
4. **Choose explicit fallback scope:** minimal configuration UI and three-dependency example; reconsider D92 only through an author decision.
5. **Set a live-call budget allocation within USD 5**, leaving room for a failed attempt and a corrected recording.
6. **Close branch/merge flow, review-before-push policy and document languages.** Continue `AI_USAGE.md` during implementation; compile the two-page `DESIGN.md` from accepted decisions.
7. **Use evidence-based completion gates:** automated core checks, two live provider runs, an approved real side effect, sanitized examples, measured setup and the author’s ≤5-minute recording.

Review performed read-only; no files were modified and no implementation tests or paid model calls were run.