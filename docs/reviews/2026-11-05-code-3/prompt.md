# Code review request: fixes of the second review (pre-push review, D117)

You are reviewing code before it is pushed. You are an advisor: you do not make
decisions and you do not modify files. Work read-only. Make no network calls,
paid model calls or external writes.

## Scope

- Your previous review: `docs/reviews/2026-11-05-code-2/answer.md` (of
  `4eaf1a5..ef327c1`): F17 still open (HTTP/2 internal retry), N01
  (`PUT /tools/{id}` authorization order), four missing tests.
- Now under review: `ef327c1..7f5ddf5` (one commit, `7f5ddf5`).
  Diff: `git diff ef327c1..7f5ddf5`. Nothing relevant is uncommitted.
- Also check the commit for secrets.

## Decision behind the fixes

`PLAN.md` D125 (author): fix F17 and N01 and add the four tests you suggested.
Contracts: `PLAN.md` D1–D125, `docs/schema.md`, `AGENTS.md` (R1–R8, EA).

## What changed

- `backend/internal/tool/client.go`: the transport uses
  `http.Protocols` with only HTTP/1 enabled (no `ForceAttemptHTTP2`), plus
  `DisableKeepAlives`. Test `TestClientUsesHTTP1Only`: a server that offers h2
  (checked with an h2-capable client) receives HTTP/1.1 for GET and POST.
- `backend/internal/api/definitions.go`: `updateTool` checks the tool's team
  and owner before reading and validating the body. Test
  `TestToolUpdateAuthorizationFirst`.
- Tests: `TestSchemaExternalLoadsRefused` (file and https `$ref`, custom and
  file `$schema`) plus argument validation through a local `$ref`; a foreign
  call on an expired run returns `ErrNotFound`; the approval expiry is unchanged
  after restoration; the main flush-timer test uses a fixed clock.

## Evidence already gathered

- `cd backend; go test ./... -count=3` passes; `go vet`, `gofmt -l` clean.
- Mutation checks: each new test fails when its fix is undone (HTTP/2 enabled
  again → the server sees `HTTP/2.0`; loader removed → file `$ref` and file
  `$schema` load; old `updateTool` → `400`; expiry check before the call lookup
  → conflict instead of `404`; expiry overwritten → test fails).
- Not run: real provider calls, `-race` (needs CGO, D102).

## What we want

1. Are F17 and N01 closed, with no remaining path (for example another way
   Go's HTTP/1 transport can re-send a request, or a TLS ALPN detail that still
   negotiates h2)?
2. Any new defect the commit introduces.
3. Do the new tests prove what they claim?

Keep design suggestions separate from defects. Avoid style-only findings.

## Output format

Markdown. One table:

| ID | Severity | Location | Contract | Trigger and consequence | Suggested fix | Decision? |

Severity: high / medium / low. "Decision?" says **needs decision** when the
fix would change a recorded behaviour, dependency or schema; otherwise "no".
Then "Tests inspected / run / not run" and "Unresolved verification gaps".
Mark anything uncertain. If you find nothing, say so explicitly.
