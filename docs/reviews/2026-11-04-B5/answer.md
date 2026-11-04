## 1. Consistency

B5 broadly follows D84. D111 correctly preserves cancellation through D97; D115 and D116 are consistent with the existing history and language rules.

| Finding / where | Why it matters | Suggested fix |
| --- | --- | --- |
| **Live-call timing is ambiguous — D112/D118.** Nov 5 requires an OpenAI slice; development is fixtures-only, while formal live runs are scheduled for Nov 7. | A real browser→OpenAI path cannot be demonstrated with fixtures. | Explicitly count one Nov 5 integration validation within D118’s allowance. Moving or adding that validation **needs decision**. |
| **Security cannot start entirely on Nov 6 — D112 versus D12–D18/D30/D33.** | Nov 5’s login, saved key and live approval already depend on encrypted starter-owned credentials, authorization, CSRF checks and secret-free events. | Treat Nov 6 as completing security coverage. Implement the protections used by the Nov 5 slice before its live call. This follows existing decisions. |
| **A delivery obligation lacks a scheduled slot — D112 versus D95.** | The output and tool-response limits must be reviewed before delivery; D111 does not cancel that commitment. | Put the review before Nov 8’s final setup measurement. Raising limits or choosing configurability **needs decision**, as does rescinding D95. |
| **Review does not authorize Git operations — D115/D117 versus `AGENTS.md`, “Git ve sırlar.”** | Direct-to-main and review-before-push specify procedure; commits and pushes still require the author’s request. | Retain an explicit author-requested commit/push checkpoint. No policy change is needed. |

D118’s “two provider runs and the recorded example” need not mean three separate runs: D104 already describes recording the OpenAI D103 run.

## 2. Schedule — D112

**This is a stretch plan, with little dependable buffer.** Nov 6 contains the largest risk: restart recovery, approval races, ambiguous writes, retry/discard behavior, credential invalidation and the second adapter. A thin UI helps, but these recorded behaviors remain substantial.

Suggested completion gates:

| Day | Concrete exit criterion |
| --- | --- |
| **Nov 5** | Through Next.js: login, enter an encrypted model key, save definitions through the API/UI, start a run, observe incremental SSE, reach a secret-free pending approval, restart Go, recover the same proposal/revision, reject it and reach a terminal state. Approval persistence has a passing fixture test. A real OpenAI validation requires the D118 clarification above. |
| **Nov 6** | Both adapters complete fixture tool round trips. Lifecycle/security tests establish final-state immutability, cancellation, restart recovery, approval races/invalidation/expiry, `outcome_unknown`, retry/discard and HTTP restrictions. SSE works manually in development and production modes, including reconnect/replay and session revocation. |
| **Nov 7** | Both D103 live runs complete with usage evidence. OpenAI produces an approved GitHub `201`, issue URL and final answer; Anthropic demonstrates tool use and continuation per D110. Capture the OpenAI events immediately and rehearse the recording. |
| **Nov 8** | `go test ./...` and the UI production build pass; no required test area remains unverified. Clean setup is timed. README, ≤2-page DESIGN, current AI_USAGE and sanitized `examples/` are ready. D95 is resolved. |
| **Nov 9** | Fixes receive relevant verification and D117 review before any requested push. The author records ≤5 minutes including approval. Repository access or the `.git`-containing delivery archive is verified. |

Do not leave the first tests until Nov 8. Follow D110’s priority order during implementation; “remaining tests” should mean closing gaps.

**Order inside Nov 5:**

1. Establish the pinned build, SQLite migrations and seed users; write the first file-backed approval-persistence test.
2. Connect the browser→Next.js→Go path with login, cookies and Origin checks.
3. Implement encrypted key entry and the minimal agent/tool save/start endpoints from `docs/schema.md`.
4. Build the shortest durable run path with a scripted provider: persisted proposal, pending approval, conditional decision, restart recovery and terminal event.
5. Expose persisted events through SSE. Verify incremental delivery, cookies and replay through Next.js before expanding the UI.
6. Add the OpenAI adapter, per-attempt usage and raw continuation content; implement the HTTP/secret protections needed by the example.
7. Perform the agreed live validation: reach the issue proposal, restart/reconnect, reject and finish. Keep the successful real write for Nov 7.

The fixture stage validates plumbing; it does not establish live provider compatibility.

**Dependencies to clear early:**

- **D107/D108:** the author must prepare the public sandbox, root manifest, enabled Issues and scoped token before the full example can start. Missing secrets deliberately prevent run creation under D105.
- **D78:** verify all four model IDs through provider endpoints before adapter assumptions harden. Listing a model does not prove streaming/tool compatibility; the live runs validate the two selected models. Substitution **needs decision**.
- **D8/D103:** Anthropic credentials, credit and account access must be ready before Nov 7.
- **D68/D110:** proxy buffering, cookie or Origin failures must surface on Nov 5, not during recording.
- **D112:** if Nov 5’s gate slips, replan with the author immediately. Changing commitments **needs decision**; an unfinished requirement cannot be relabeled complete.

## 3. Code review before every push — D117

Keep reviews focused on one coherent push containing several small, purposeful commits. That preserves D115 and D117 without reviewing after every edit.

The prompt should contain:

- **Immutable scope:** last pushed base SHA, target SHA, commit list, `git diff BASE..TARGET`, and whether any relevant work remains uncommitted.
- **Behavior and contracts:** intended change, relevant R-numbers/D-numbers, `AGENTS.md`, and affected schema/API sections. Later amendments take precedence over superseded decisions.
- **Evidence:** exact test/build commands, results, skipped checks and outstanding manual verification.
- **Review boundaries:** read-only, no paid calls or external writes; report concrete correctness/security/contract findings. Separate new design suggestions from implementation defects.
- **History check:** inspect individual commits for secrets as well as the final diff. A secret added and then removed still exists in history.

Required verification should match the change: D102’s `go test ./...`, affected fixture cases, and a production UI build for UI/proxy changes. D110 additionally requires manual SSE checks in both modes. Missing evidence must be reported as missing.

A compact output makes disposition quick:

| ID | Severity | Location | Contract | Trigger and consequence | Suggested fix | Decision? |
| --- | --- | --- | --- | --- | --- | --- |

End with “tests inspected/run/not run” and unresolved verification gaps. Avoid style-only findings unless they impair understanding or violate the protocol.

For each finding, the author can respond: **accept**, **reject with reason**, or **new decision required**. Record dispositions in the existing review answer/AI_USAGE workflow. Fixes that merely restore an agreed contract need no new design decision; changed behavior, dependencies or schema do.

After fixes, append focused commits and review the added diff plus affected behavior. Identify the final reviewed SHA before pushing.

**Self-review ambiguity:** committing the current review’s prompt/answer changes the push after its target was reviewed. Define a narrow treatment for that round’s generated review records, such as author inspection for secrets and accuracy; such an exception **needs decision**. Functional fixes must still receive review.

## 4. Toolchain and setup — D113/D114

**D113 identifies toolchain families, but not complete reproducibility.** Suggested concrete pins, using the versions recorded in PLAN:

| Artifact | Suggested content / treatment |
| --- | --- |
| `go.mod` | `go 1.26.0` and `toolchain go1.26.4`, subject to dependency requirements. The former is a minimum; the latter selects a preferred toolchain, not an absolute lock against newer installed versions. The exact minimum/patch policy **needs decision**. |
| `go.mod` / `go.sum` | Commit selected dependency versions and checksums. Approve SDK, SQLite and any additional dependency versions before implementation under D1. |
| `.nvmrc` | Exact `24.18.0`, rather than moving `24` or `lts/*`. Confirm the patch pin with the author. |
| UI manifests | Commit `package.json` and `package-lock.json`; use `npm ci` for setup and record the tested npm version. Next.js/React versions remain author choices. |

Go’s selection behavior can trigger a toolchain download, which belongs in setup timing. `npm ci` requires a matching lockfile and does not update it. [Go toolchains](https://go.dev/doc/toolchain), [npm ci](https://docs.npmjs.com/cli/v11/commands/npm-ci/).

**Windows pitfalls — D113, D15/D16/D18, D68:**

- **Shell:** publish tested PowerShell commands. POSIX environment assignments and shell scripts are insufficient; `npm.cmd` is useful when PowerShell blocks `npm.ps1`.
- **Node selection:** `.nvmrc` does not select Node by itself. Verify `node --version`; document the actual Windows installation/selection procedure. POSIX nvm and Windows alternatives differ. [nvm documentation](https://github.com/nvm-sh/nvm#installing-and-updating).
- **Encoding and paths:** use consistent UTF-8, test CRLF handling in configuration, quote paths and exercise a checkout path containing spaces. Filesystem paths need Windows-aware handling; HTTP URLs remain URL paths. A repository-wide line-ending policy **needs decision**.
- **Environment loading:** document the working directory and how Go receives master-key/seed variables; Go does not automatically load a file merely named `.env`. Next.js loading does not populate the Go process. A loader dependency **needs decision**. Never expose credentials through `NEXT_PUBLIC_*`, and preserve D18’s UI-only model-key path. [Next.js environment variables](https://nextjs.org/docs/app/guides/environment-variables).
- **Ports and origin:** document both ports, use one consistent browser hostname, and check IPv4/IPv6 binding. Forward cookies, Origin and `Last-Event-ID` correctly. Verify the proxy streams without aggregating the response. Do not bypass CSRF checks to solve proxy configuration.

**Nov 8’s measurement must include:**

1. Exact commit, Windows/shell, hardware, Go/Node/npm versions and network conditions.
2. A clean clone with no database, `.next`, `node_modules` or inherited application configuration.
3. Empty isolated Go build/module and npm caches for an honestly cold dependency measurement.
4. Clone, dependency downloads, configuration/master-key/password preparation, migration/seed, compilation, both servers and first browser compilation/load. Include production build time if that is the recommended launch path.
5. Successful browser login, UI model-key entry and an example definition ready to run. Record elapsed time and any intervention.

Preinstalled toolchains, repository access and available provider credentials may be explicit prerequisites; disclose their exclusion. Time GitHub token/repository preparation and the full live demonstration separately under D101. Follow the README literally, and repeat affected setup steps after fixes. One measured result supports that tested environment, not a universal guarantee.

## 5. Budget — D118

**About USD 1 is plausible for the selected cheap models, but remains an estimate.**

D108 normally needs seven model turns: manifest, four OSV queries, issue proposal and final answer. Its roughly 40 KB of advisory results is repeatedly included in history under D83/D96; budget cumulative input across calls.

The fetched standard prices are:

| Model | Input / million tokens | Output / million tokens |
| --- | ---: | ---: |
| `gpt-6-luna`, short context | $0.10 | $0.50 |
| `claude-haiku-5-5`, prompts ≤100,000 tokens | $0.10 | $0.50 |

These are **uncertain planning assumptions until reconfirmed at execution**, with account access unverified. Haiku has higher prices above its prompt threshold. [OpenAI API pricing](https://developers.openai.com/api/docs/pricing), [Anthropic pricing](https://platform.claude.com/docs/en/about-claude/pricing).

Illustratively, **100k–200k aggregate input plus 5k–15k output tokens per run** costs approximately **$0.0125–$0.0275** at those rates, without caching adjustments. That token range is an estimate, not a measurement. Three such runs remain comfortably below $1; retries, reasoning output and revised proposals add cost.

Use model-list/retrieve endpoints for ID checks rather than generating answers solely to check names. Reuse the successful OpenAI run for `examples/`; a fresh screen-recorded run is additional usage.

For each attempt, calculate:

```text
estimated USD = (input tokens × input rate + output tokens × output rate) / 1,000,000
```

Include failed, retried and discarded attempts. Use reported output counts, including billed reasoning where applicable; do not add reasoning twice.

**Accounting limitation — `docs/schema.md`, `model_calls`:** input/output totals and `usage_status` do not establish exact charges when cache rates, tier differences or unavailable usage apply. Keep token-derived amounts labeled estimates and reconcile against provider billing for the relevant project/time window. Adding cache/cost fields **needs decision**; dashboard reconciliation avoids expanding the schema.

Record spend and remaining allowance in PLAN after each live session. Unknown usage is not zero. Before another run, allow for its possible retries; the USD 5 ceiling must remain intact.

**D117/D118 ambiguity:** establish whether Codex reviews use an existing subscription or billed API usage. If billed against the same USD 5 allowance, review costs must enter the budget. Clarifying that accounting boundary **needs decision**.

## 6. Process risks

| Risk / where | Why it slows implementation | Smaller version preserving intent |
| --- | --- | --- |
| **Serial decision requests — D1/AGENTS.md.** Package layout, dependency versions, environment loading and other choices remain unspecified despite 118 decisions. | One question per choice can consume the implementation window. | Present one small implementation decision packet with recommendations and alternatives; the author approves it together. New choices **need decision**. |
| **Repeated consultation of settled behavior — D4/D92/D109.** | Reopening rejected simplifications produces review churn. | Cite the settled decision; reopen only with concrete new incompatibility or failure evidence. Reversal **needs decision**. |
| **Oversized push reviews — D117.** | A whole-platform diff makes findings harder to assess and fixes slower. | Review coherent increments with tests and relevant contract references. Preserve review before every push. |
| **Documentation drift — D13/D37 and AGENTS.md’s document map.** | Copying the same rationale across documents creates contradictory records. | Keep decisions/evidence in PLAN, contracts in schema, and concise derived summaries elsewhere. Update AI_USAGE as work happens; add no extra process document. |
| **Security/testing treated as later phases — D112.** | Discovering transaction or credential flaws on Nov 8 can invalidate the core design. | Attach focused tests to each implemented slice; reserve Nov 8 for gaps and setup evidence. Keep all D102/D110 commitments. |
| **Recording first attempted on Nov 9 — D104/D112.** | A seven-turn live run may exceed five minutes or require a correction. | Rehearse by Nov 7/8, prepare the outline and sanitized capture plan, then retain Nov 9 for the author’s final recording. |
| **Optional UI expansion — D84/D111.** | Form builders and visual polish consume time without improving the assessed core. | Use basic controls and the agreed APIs; implement every required flow without adding UI machinery. Any scope change **needs decision**. |

Read-only review completed. No files were modified, implementation tests run, or paid model calls made.