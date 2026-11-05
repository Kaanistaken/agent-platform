Reviewed against HEAD `b7d3b37`, the original findings and repository contracts.

| ID | verdict (ok / change / problem) | issue | suggested change | needs a new author decision? (yes/no) |
|---|---|---|---|---|
| F01 | change | Save-time validation leaves previously saved definitions and persisted request previews executable with the forbidden alias. `Render` and `Client.Do` do not revalidate it. | Also reject both headers before sending; test a persisted request containing the alias. | no |
| F02 + F15 | ok | The locked handoff preserves notifications; the FIFO removes overflow loss. An unbounded queue does not violate D84’s bounded **worker pool**. | Also assert single execution and rejection-driven resumption. | no |
| F03 | ok | Scoping by authorized run closes the disclosure. | Apply to both approval verbs; propagate database errors separately from `ErrNotFound`. | no |
| F05 | change | The SDK switch is correct. However, the test can pass against old code when the environment already contains `ANTHROPIC_API_KEY`: that credential takes precedence over the environment bearer token. | Explicitly clear `ANTHROPIC_API_KEY` in the bearer-token regression test. | no |
| F07 | change | If expiry runs before resolving the requested call, an expired authorized run plus a foreign call ID can return 409 instead of F03’s required 404. | Resolve the scoped call first. Also test rejection after expiry and a stale sweep candidate that became `executing`. | no |
| F08 | ok | Closing executing calls in the terminal transaction prevents late completion from overwriting them. | Set `finished_at`. Make the gated fake return a late **successful** response despite cancellation, so the overwrite assertion exercises that path. | no |
| F09 + F11 | change | “Any snapshot tool host” is too broad when two tools reference the same secret name on different hosts. A replacement for host B can still replace the binding while the invalidated call targets A. | Require the relevant invalidated call’s host to match before changing its binding; test this multi-host snapshot case. | no |
| F10 | change | The backend expiry fix is sound, but the run page’s `tool_call_status` reducer ignores `approval_expires_at`. Its displayed expiry remains empty. | Consume the new field in the reducer; verify existing expiry remains unchanged through invalidation/restoration. | no |
| F12 | ok | Preserving retained links avoids the D46 insertion trigger. | Assert the deleted-tool link actually remains, alongside the 200 response. | no |
| F13 | change | Excluding deleted tools permits duplicate names among retained links. D49 and existing `setAgentTools` validation contain no such exception; later agent edits can consequently fail. | Check all other retained tool links, including deleted tools. | no |
| F14 | change | Graceful shutdown is missed: `attempt` marks the interrupted model call finished (`failed/interrupted`). Recovery’s unfinished-only query will not discard its persisted partial text. | Cover shutdown-interrupted finished attempts lacking a discard marker, while excluding terminal runs. Add a graceful-stop/restart test. | no |
| F16 | change | A buffer mutex alone does not establish ordering against cancellation. Also, `AfterFunc.Stop` does not wait for an already-started callback. | Hold synchronization through the event append; make final flush a barrier against timer callbacks. Ensure the test’s delta is actually buffered within the 250 ms window. | no |
| F17 | problem | `onAttempt func()` cannot report a failed database increment. Sending can proceed without a durable count, allowing extra attempts after restart (D94). | Use an error-returning callback; require successful committed accounting before sending. Test accounting failure → no send, plus remaining budgets of 0 and 1. | no |
| F18 | change | Closing after headers does not necessarily produce a body-read error: EOF can validly delimit an empty body. The proposed test could pass unchanged code. | Use POST with a declared nonzero `Content-Length`, flush 201 headers, then close before the body. Assert the diagnostic as well as status/no error. | no |

**F16 without F06 does make the existing problem worse:** the timer can append text after `run_finished` while the provider remains blocked. Today that append requires another delta or stream completion. Correct timer cleanup prevents additional lifecycle bugs, but does not remove this deferred terminal-event violation.

The plan also misses:

- **Transport retries:** Go can transparently replay a GET inside one `http.Client.Do`. Counting once per `once` invocation does not necessarily count actual sends. F17 needs to cover this against D91’s one-retry limit.
- **F18 retry interaction:** preserve the last received response when a later GET attempt receives no response; test this and masking of the body-read diagnostic.
- **Completion evidence:** update the existing documentation and explicitly retain F06/F19 as deferred. Run backend tests, the UI build if its reducer changes, and the required D117 review before push.

No files modified, tests run, or network calls made.