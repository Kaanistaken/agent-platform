Read `AGENTS.md`, `PLAN.md`, and both B1 review files. No files changed; no runtime tests performed. This review uses the repository’s requirement summary; the original PDF was unavailable.

**Most decisions can stay with conditions. D28 needs reconsideration as a sufficient confidentiality control.** Shared secret use also needs an explicit trust boundary.

Library and browser-security facts marked **verified** were checked against the linked documentation. Mitigations below are recommendations, not recorded decisions.

## D12, D18 — Per-user model keys and UI provisioning

1. **Verdict: keep with conditions.**

2. **Risks or gaps**
   - A teammate runs Alice’s agent, but execution resolves Alice’s credentials through agent ownership instead of the run’s starter: R7 violation.
   - Restart recovery or a shared mutable provider client accidentally selects another user’s key.
   - Key submission leaks through Next.js request logging, analytics, error reporting or browser storage.
   - D8 still says the development Anthropic key comes through `.env`; D18 supersedes that platform provisioning path, but the wording is inconsistent.

3. **Mitigations**
   - Persist the initiating user and provider; resolve credentials from that identity on every call, including resumed calls. Reject missing credentials before invoking the provider.
   - Keep credential-bearing requests isolated between users; never mutate shared credentials.
   - Submit keys through the authenticated backend path, exclude payloads from diagnostics, and avoid persistent browser storage.
   - Clarify D8. Decide separately what happens when the starter rotates or deletes a key during a paused run; ownership must never change.

## D14, D15 — AES-GCM, associated data and master key

1. **Verdict: keep with conditions.**

2. **Risks or gaps**
   - Ambiguous associated-data serialization or overlapping row identifiers can weaken protection against ciphertext substitution.
   - Copying ciphertext into a cloned row fails authentication when identity changes. Mutable names in associated data make renaming unexpectedly require re-encryption.
   - Reusing a nonce under the same key compromises GCM security. **Verified:** Go requires nonce uniqueness per key and warns against exceeding \(2^{32}\) random nonces under one key. [Go cipher documentation](https://pkg.go.dev/crypto/cipher)
   - A key-version column alone provides no rotation procedure. Losing an old master key makes its rows unreadable.
   - A wrong master key can have a valid length; configuration validation alone cannot detect it.

3. **Mitigations**
   - Define an unambiguous, versioned associated-data format distinguishing credential type, immutable row identity, team/user and provider where applicable. Reconstruct expected identity from the authorized row.
   - When copying encrypted material into a new identity, decrypt and re-encrypt with new associated data and a fresh cryptographically random nonce. Ordinary agent clones can retain secret references without copying ciphertext.
   - Generate a fresh nonce for **every encryption**, including updates and rotation; persist nonce, ciphertext/tag and key version consistently.
   - Recommend refusing startup for missing/malformed key configuration or unavailable required versions. Authenticate a known encrypted value to detect a wrong key before accepting work. Never silently generate a replacement or overwrite unreadable rows.
   - Distinguish master-key rotation from replacing an API credential. Retain old master keys until required rows are migrated; document backup and recovery implications.

## D16 — Database users, bcrypt and `.env` seeding

1. **Verdict: keep with conditions.**

2. **Risks or gaps**
   - Missing variables result in empty/default passwords or partially seeded teams.
   - Re-running startup resets existing users’ passwords.
   - Configuration dumps, seed errors or shell commands expose initial passwords.
   - **Verified:** Go’s bcrypt implementation rejects password generation above 72 **bytes**, which matters for multibyte characters. [bcrypt documentation](https://pkg.go.dev/golang.org/x/crypto/bcrypt)

3. **Mitigations**
   - Validate all inputs required for initial seeding before writing; seed consistently and report missing **variable names**, never values.
   - Recommend creating missing users without resetting existing credentials. Missing seed variables need not prevent subsequent startup when seeding is unnecessary.
   - Reject unsupported password lengths explicitly; do not truncate. Select bcrypt cost and login throttling deliberately.
   - Keep real passwords and hashes out of fixtures, examples and committed database files. Templates should contain placeholders only.

## D17 — Server-side sessions and cookies

1. **Verdict: keep with conditions.**

2. **Risks or gaps**
   - HttpOnly does not prevent authenticated malicious requests or CSRF.
   - Frontend/backend routing can break cookie delivery or inadvertently broaden cookie scope.
   - Deleting the browser cookie alone leaves a stolen server-side session usable.
   - Restarted users may need to log in again; pending approvals must remain intact independently of session lifetime.

3. **Mitigations**
   - Use unpredictable session identifiers, expiration, renewal on login, and server-side invalidation on logout. Recommend host-only cookies with narrow scope and `Secure` under HTTPS; document local HTTP behavior.
   - Explicitly choose SameSite behavior for the eventual deployment. **Verified:** `SameSite=None` requires `Secure`; SameSite is distinct from origin isolation and should not be the sole assumed CSRF protection. Protect state-changing operations—including approval, secret changes and logout—with a deliberate CSRF mechanism, such as session-bound tokens and Origin validation. Keep GET requests free of application mutations. [OWASP CSRF guidance](https://cheatsheetseries.owasp.org/cheatsheets/Cross-Site_Request_Forgery_Prevention_Cheat_Sheet.html)
   - For direct cross-origin browser requests, configure credentials and an explicit permitted origin. **Verified:** credentialed CORS cannot use `Access-Control-Allow-Origin: *`. [Fetch standard](https://fetch.spec.whatwg.org/#cors-protocol-and-credentials)
   - Decide whether revocation terminates already-open streams. **Verified:** OWASP recommends server-side session invalidation and explains HttpOnly’s limited protection. [OWASP session guidance](https://cheatsheetseries.owasp.org/cheatsheets/Session_Management_Cheat_Sheet.html)

## D19, D24, D27 — Shared, write-only, header-only tool secrets

1. **Verdict: keep with conditions; reconsider any absolute “cannot be read back through any path” claim without destination restrictions.**

2. **Risks or gaps**
   - Bob creates a tool targeting his public server and references Alice’s team secret in a header. Bob receives the secret directly; response masking cannot prevent this.
   - Creator-only modification protects secret integrity, but does not restrict who can **use** the external account’s authority.
   - Deleting and recreating `secret.NAME` can silently bind existing tools to a different owner or credential.
   - Save-time validation alone is insufficient if parameter substitution can introduce secret syntax or another template-expansion pass.
   - Header-only placement does not prevent leakage through redirects, request dumps or diagnostic errors.

3. **Mitigations**
   - Keep values absent from read APIs, but distinguish direct non-disclosure from safe delegated use.
   - B3 must implement D11 before unrestricted secret-bearing tools are usable. Merely blocking private addresses is insufficient: permitted destinations must also prevent sending credentials to an attacker-controlled public endpoint. The exact destination-binding/authorization mechanism remains undecided.
   - Bind references to stable team-scoped secret identities; reject cross-team resolution and unauthorized mutation.
   - Validate templates on save and execution; avoid recursively interpreting model parameters as templates. Inject secrets only into the final outbound headers.
   - Define duplicate-name, deletion and recreation behavior explicitly.

## D22, D23 — Ownership, sharing and cloning

1. **Verdict: keep with conditions.**

2. **Risks or gaps**
   - Agent ownership is mistakenly treated as execution identity, causing credential or usage attribution errors.
   - A tool owner changes its destination, schema or approval flag; teammates’ future runs change behavior without editing their agents.
   - Tool deletion leaves dangling agent references. Deletion that cascades into snapshots can destroy pending-run recovery.
   - Cloning an agent does not isolate it from future tool changes because references remain shared.

3. **Mitigations**
   - Enforce team access on every read, run, clone and relationship assignment; add owner checks for mutation. Derive identity from the authenticated session.
   - Explain that cloned agents share tool dependencies.
   - Choose deletion behavior in B2b—such as rejecting deletion while referenced or retaining tombstones—and return clear errors for unavailable tools. Neither option is yet decided.
   - Construct a consistent start snapshot so concurrent edits cannot produce mixed definition versions. Existing snapshots must remain independent of live definition edits.

## D20, D25 — Durable messages and definition snapshots

1. **Verdict: keep with conditions.**

2. **Risks or gaps**
   - A normalized history loses call identifiers, ordering, multiple calls or provider continuation material needed to resume.
   - Snapshotting templates alone does not freeze the approval request: model arguments might be regenerated after restart.
   - A snapshot containing only `secret.NAME` can resolve to a different credential after rotation or recreation.
   - Copying plaintext secret values into snapshots violates confidentiality. Persisting encrypted historical values can also undermine deletion/revocation expectations.

3. **Mitigations**
   - Persist completed conversation/call state needed for continuation, including provider-required material rather than assuming plain role/text messages suffice.
   - Bind approval to immutable tool configuration, validated arguments and a sanitized request preview. Resume that call without asking the model to reconstruct it.
   - **Capture stable secret references; plaintext values are unnecessary and must not enter ordinary snapshots.** Whether to pin an encrypted credential version or resolve the current value remains a user decision:
     - Version pinning improves reproducibility but retains old credentials.
     - Current-value resolution respects replacement more naturally but can change the external account or permissions.
   - Define rotation/deletion behavior explicitly: continue, invalidate approval, require fresh approval, or fail clearly. A definition snapshot alone does not settle this.
   - Keep public snapshot/history representations separate from encrypted execution credentials.

## D21, D26 — Conditional approval and starter-only authority

1. **Verdict: keep with conditions.** D26 does not conflict with D23 or the supplied case summary: teammates may start runs, and each starter approves their own run. **Uncertain:** the unavailable PDF could contain a more specific approval rule.

2. **Risks or gaps**
   - The illustrated update checks team but omits the starter restriction.
   - A valid approval is applied to the wrong call or a cancelled run.
   - One successful approval update still allows duplicate worker dispatch or replay after a crash.
   - The external API succeeds, then the process crashes before recording success. Conditional approval does not establish exactly-once external effects.

3. **Mitigations**
   - Include starter authorization and eligible run/call state in the atomic transition, directly or through an equivalent transactional check.
   - Persist the pending call before displaying approval; persist the decision before dispatch.
   - Give execution its own durable claim/state transition; preserve rejection across restart.
   - Correct D21’s rationale: it prevents competing pending-state transitions, not every duplicate side effect. B3 still needs an ambiguous-outcome policy.
   - An expired login may require reauthentication; it must not transfer approval authority or erase pending state.

## D28 — Masking tool responses

1. **Verdict: reconsider as a sufficient R2 control; keep as defense in depth.**

2. **Risks or gaps**
   - Exact replacement misses base64, URL encoding, JSON escapes, partial echoes and split fragments.
   - An upstream error, response header or transport diagnostic can bypass a body-only sanitizer.
   - An approval preview or request record may already contain expanded secret headers before any response exists.
   - Secret rotation between dispatch and sanitization can make a lookup mask the wrong value.
   - No finite list of substitutions guarantees removal of arbitrary transformations by an untrusted endpoint.

3. **Mitigations**
   - Build approval previews and request records from secret-free structures, showing placeholders or `***` for credential headers. Never serialize the raw expanded request.
   - Sanitize success and failure paths before persistence, model delivery or event emission; include response metadata and diagnostics.
   - Use the exact credential values actually dispatched. Cover known encodings and parsed representations where supported; bound and sanitize responses before publishing chunks.
   - For unsupported or suspicious responses, withholding content is safer than emitting unchecked data. That behavior requires a decision.
   - Combine masking with trusted credential destinations and controlled response exposure. If arbitrary endpoints remain allowed, document that D28 cannot guarantee R2.

## Cross-cutting risks

- **Within-team trust is unresolved.** R6 isolates teams; it does not make every teammate a trusted recipient of every external credential. Ownership, credential use and credential disclosure are separate permissions.
- **Approval must bind the action actually executed.** Endpoint, method, arguments, body, approval flag and credential-binding policy all affect that action.
- **Encryption does not sanitize output.** Next.js, Go handlers, provider diagnostics, events and exports must all avoid credential-bearing payloads.
- **The current `.gitignore` covers `.env`, but not future local databases, WAL files, backups or alternative environment files.** Review those paths before implementation and inspect staged content; ignore rules do not protect files already tracked.
- **Documentation contains stale summaries.** D8’s provisioning wording and `AGENTS.md`’s open-decision list should eventually be reconciled with finalized B2 decisions.

## Items for B2b and B3

| Batch | Decisions or constraints to address |
| --- | --- |
| **B2b** | Represent team, owner and run starter distinctly; prevent cross-team relationships; retain immutable snapshots and pending-call arguments; define tool/secret deletion and stable reference behavior; specify encrypted-envelope identity/version fields; ensure read representations omit secret values and password hashes. |
| **B2b** | Define authorization for histories, snapshots, events and approval reads as well as writes. D23 grants agent visibility; run-history visibility is not yet specified. |
| **B3** | Define destination/redirect protections, secret rotation/revocation during paused runs, model-key replacement behavior, sanitization boundaries and unsupported-response handling. |
| **B3** | Define durable execution claims, restart recovery, ambiguous external outcomes, session expiry/revocation effects and the authenticated browser/proxy path. |
| **Verification planning** | Carry forward tests for cross-team references, teammate-owned tools, concurrent approval/rejection, restart while pending, tool deletion, secret rotation, wrong master keys, missing seed inputs and encoded/error-path echoes. Exact test approach remains the author’s decision. |