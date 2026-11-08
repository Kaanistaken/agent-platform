Found two low documentation defects. No code defect or secret exposure detected.

| ID | Severity | Location | Contract | Trigger and consequence | Suggested fix | Decision? |
| --- | --- | --- | --- | --- | --- | --- |
| F01 | low | [README.md:79](/C:/Projects/Vodafone-Case/README.md:79) | README example instructions; R1/R8 | A clone retains provider `openai`. The Model dropdown only lists that provider’s models, so the reader cannot select a Claude model using the stated steps alone. | Specify **Clone → Edit → Provider: anthropic → Model: claude-haiku-5-5 / claude-sonnet-5-5 → Save**. | no |
| F02 | low | [PLAN.md:150](/C:/Projects/Vodafone-Case/PLAN.md:150) | Decision traceability; D81 | D134 attributes the no-redirect policy to D78, which defines supported models. This points readers to the wrong contract. | Replace that reference with **D81**. | no |

Inspection confirms:

- **Limit:** 16,384 reaches OpenAI `max_output_tokens` and Anthropic `max_tokens`. Remaining `4096` occurrences are historical decisions, independent adapter fixtures or credential-size limits. No second runtime output cap was found.
- **Long output:** the 250 ms flush, message/final-output storage, SSE replay and token counters have no conflicting size restriction. SDK stream scanners allow 32 MiB lines. Larger output permits more generation, storage and spend; timeout, step-limit and `incomplete_output` handling remain unchanged.
- **README/records:** the setup claim matches the recorded 103-second scripted bootstrap with toolchains installed beforehand. Historical sandbox names and the manifest name are intentional; no additional rename mismatch found. D134/D135, AGENTS and AI_USAGE otherwise agree.
- **DESIGN:** the sentence corrected in `0143c4d` accurately limits mandatory approval to tools marked `requires_approval`.

Both commits’ messages, patches and changed file versions were scanned against seven local secret values, encoded variants, credential patterns and decoded candidates; no matches found.

No files changed or tests/builds rerun. Supplied passing results are accepted as reported evidence. Setup timing, remote rename/301 behaviour, provider metadata and live acceptance of 16,384 were not independently verified offline.