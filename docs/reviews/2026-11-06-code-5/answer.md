Found one medium and five low documentation defects. No high-severity defect or secret exposure was found.

| ID | Severity | Location | Contract | Trigger and consequence | Suggested fix | Decision? |
| --- | --- | --- | --- | --- | --- | --- |
| F01 | medium | [DESIGN.md:75](/C:/Projects/Vodafone-Case/DESIGN.md:75) | R5; D99 | “Every side effect waits for approval” overstates enforcement. A write tool with `requires_approval: false` starts as `approved` and executes automatically. The stated prompt-injection mitigation therefore does not cover every tool. | Say that tools marked `requires_approval` require approval, including the example’s issue tool. | no |
| F02 | low | [DESIGN.md:37](/C:/Projects/Vodafone-Case/DESIGN.md:37) | D79, D88, D94; implementation accuracy | Retries are attributed to the adapters, but `run/model.go:modelTurn` owns retry counting, waits and durable attempt records. Adapters classify errors and perform one streaming request. | Describe retries as owned by the agent loop, with error classification in the adapters. | no |
| F03 | low | [DESIGN.md:66](/C:/Projects/Vodafone-Case/DESIGN.md:66) | D91, D124, D125 | HTTP/1.1 alone does not prevent transparent GET retries on reused connections. The code also sets `DisableKeepAlives: true`; omitting this gives an incorrect explanation of the retry guarantee. | State “HTTP/1.1 only, with connection reuse disabled.” | no |
| F04 | low | [DESIGN.md:43](/C:/Projects/Vodafone-Case/DESIGN.md:43) | DESIGN framework deliverable; D7, D68 | The paragraph describes framework friction but omits why Next.js was selected. “Only for the UI” also understates its documented `/api/*` and SSE proxy role. | Add D7’s rationale—the reviewers’ stack—and describe UI plus same-origin API/SSE forwarding. | no |
| F05 | low | [PLAN.md:176](/C:/Projects/Vodafone-Case/PLAN.md:176), [187](/C:/Projects/Vodafone-Case/PLAN.md:187), [259](/C:/Projects/Vodafone-Case/PLAN.md:259), [285](/C:/Projects/Vodafone-Case/PLAN.md:285) | AGENTS.md: current requirement status and handoff | EA still says the recorded run is pending; the deliverables row says its pre-commit review is pending; the narrative says the quick path has not run; remaining work still lists the completed export and DESIGN. These conflict with the committed artifacts and later validation records. | Update current statuses; explicitly label any retained historical statements. | no |
| F06 | low | [DESIGN.md:89](/C:/Projects/Vodafone-Case/DESIGN.md:89) | D1, D95, D131 | “Become configurable per agent or team” presents a choice absent from the cited decision. D95 leaves raising limits versus configurability open and specifies no configuration scope. | Preserve D95’s alternatives without committing to per-agent/team configuration. | no |

**Wording suggestions — separate from defects**

“Already allows rotation” ([DESIGN.md:90](/C:/Projects/Vodafone-Case/DESIGN.md:90)) would be clearer as “reserves key-version metadata for future rotation.” The schema supports that future work; rotation itself is explicitly unimplemented.

**Checks performed / not performed**

- Inspected all four commits individually. Scanned patches, commit messages and changed file versions against seven sensitive `.env` values, their base64/URL forms, credential patterns and decoded candidates: no matches.
- Compared DESIGN against decisions, schema, README and relevant implementation. Retry limits are correct: four provider attempts maximum, one GET retry, and `Retry-After` up to 60 seconds. Permanent quota/spend failures are excluded from retries. F19 accurately describes the existing UI limitation.
- Verified all 34 exported events against stored database events using an in-memory snapshot of the database and committed WAL pages. IDs, types, order and exported values match.
- Verified transcript arguments, approval preview, result excerpts and omission counts, concatenated deltas, final answer and usage totals: no invented or reordered events.
- Runs 1–5 and the runs 1–4 aggregate match PLAN. Run 5’s issue call was rejected with zero execution attempts.
- No session data, internal paths or confidential personal data detected in the export. It does retain public GitHub profile metadata and OSV’s researcher attribution to **Liyuan Chen**, beyond the author’s GitHub account.
- No files changed; no network, paid calls, tests, builds or external writes performed.

**Unresolved verification gaps**

- Provider dashboards, model-list responses, browser arrival timings and current GitHub issue state were not independently verified offline. Stored run evidence supports the reported execution results.
- Recorded spend explicitly covers runs 1–4; run 5’s spend remains unreconciled in the committed record.
- The two-page print result is accepted as supplied evidence; it was not independently reproduced. Apart from the framework rationale above, the required DESIGN topics have credible coverage.