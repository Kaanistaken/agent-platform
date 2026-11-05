**F17 and N01 are closed by inspection.** One new medium defect remains.

For F17, Go 1.26.4 does not install HTTP/2 when `Protocols` enables only HTTP/1. The production TLS configuration advertises no h2 ALPN; unsolicited server selection fails the handshake. HTTP/1 retries require a reused connection, which `DisableKeepAlives` prevents. I found no remaining automatic resend path in this configuration.

For N01, team, ownership and deletion checks now precede body validation.

| ID | Severity | Location | Contract | Trigger and consequence | Suggested fix | Decision? |
| --- | --- | --- | --- | --- | --- | --- |
| N02 | medium | [definitions.go:506](C:/Projects/Vodafone-Case/backend/internal/api/definitions.go:506) | R4, D82: run progress with one shared writer | **New regression, established by inspection; not dynamically reproduced.** An authenticated owner starts `PUT /tools/{id}` and withholds the remainder of its JSON body. `readToolDef` now runs inside the transaction holding the sole write connection. The server sets only `ReadHeaderTimeout`, so body decoding can block indefinitely. Writes for every team—including cancellation, approval expiry and terminal run events—then wait behind this request. | Check authorization before decoding without retaining a write transaction; decode the body; then transactionally recheck authorization/deletion and perform the collision check and update. Add a blocked-body regression test proving an unrelated write completes before body release. | no |

**Tests inspected / run / not run**

- **Inspected:** every added or changed test and its corresponding implementation.
  - `TestClientUsesHTTP1Only` establishes that the server supports h2, then verifies HTTP/1.1 for GET and POST.
  - `TestToolUpdateAuthorizationFirst` proves foreign-team `404`, teammate `403`, and owner validation `400`.
  - Schema tests cover external-reference rejection and actual argument validation through a local `$ref`.
  - Lifecycle assertions cover expired-run foreign-call approval, expiry preservation through restoration, and timer flushing while the provider remains blocked. The fixed clock guarantees buffering.
- **Run:** `git diff --check ef327c1..7f5ddf5` passed. Inspected and scanned the commit’s changed files for secrets; no confirmed credentials found. Matches were identifiable test fixtures.
- **Not run:** Go tests, vet, builds, mutations, race detector or network reproductions. Supplied passing results remain author-provided evidence. No files were modified or network calls made.

**Unresolved verification gaps**

- N02 lacks a dynamic blocked-body reproduction.
- The expired-run foreign-call extension tests approval only; rejection follows the correct order by inspection.
- HTTPS schema cases assert rejection, but cannot independently prove invocation of the refusing loader: the library’s default file loader also rejects HTTPS. The file cases provide the stronger regression coverage.
- Timer callback already in progress versus cancellation/close remains untested explicitly.

No additional concrete defect or design recommendation was identified in scope.