# Agent platform (AI Platform Engineer take-home case)

A small platform for running LLM agents against real models. Agents and tools are data: an agent is a name, a system
prompt, a model and a list of allowed tools; a tool is an HTTP request template. Runs stream live to the browser,
risky tool calls wait for human approval (and survive a server restart), teams are isolated, and every user brings
their own model key.

- Backend: Go (one process: REST API, SSE streams, run workers), SQLite (`modernc.org/sqlite`, no CGO).
- UI: Next.js; the browser talks only to Next.js, which forwards `/api/*` to Go.
- Model providers: **OpenAI** (Responses API) and **Anthropic** (Messages API). Supported models: `gpt-6-luna`,
  `gpt-6-sol`, `claude-haiku-5-5`, `claude-sonnet-5-5`.
- Design and decisions: [`DESIGN.md`](DESIGN.md) · decision log: [`PLAN.md`](PLAN.md) · schema and API:
  [`docs/schema.md`](docs/schema.md) · AI usage: [`AI_USAGE.md`](AI_USAGE.md).

## Requirements

- Go 1.26 (`go.mod` pins the toolchain `go1.26.4`; Go downloads it if needed)
- Node.js 24 (tested with 24.18.0, see `web/.nvmrc`) and npm
- Your own OpenAI or Anthropic API key (entered in the UI, never in a file)

Tested on Windows 11 with PowerShell. macOS and Linux are expected to work but were not tested. No Docker.

## Run it

```powershell
# 1. Create .env in the repository root: a random master key and passwords for the
#    four seeded users. Existing values are never changed. Note the printed passwords.
cd backend
go run ./cmd/initenv

# 2. Start the backend (terminal 1). It creates backend/data/app.db, runs the
#    migrations and seeds two teams: alpha (alice = lead, bob) and beta (carol = lead, dave).
go run ./cmd/server

# 3. Start the UI (terminal 2).
cd web
npm ci
npm run build
npm start
```

Open <http://localhost:3000>, log in (for example as `alice`) and add your model key under **Settings**. The backend
listens on `127.0.0.1:8080`, the UI on port 3000. For development, `npm run dev` replaces the last two UI commands.

The setup time has not been measured on a clean machine yet (planned before delivery).

## The example agent: dependency check

The agent reads `package.json` from a public GitHub repository, looks up every dependency in
[OSV.dev](https://osv.dev), and opens a GitHub issue with the findings. Opening the issue is a real side effect and
**requires approval**. Tool and agent definitions are in [`examples/dependency-check/`](examples/dependency-check/);
they are created through the platform's own REST API (as a user would in the UI), not built into the platform:

```powershell
cd backend
go run ./cmd/example -user alice      # the platform must be running; reads alice's password from .env
```

This creates three tools (`read_repo_file`, `osv_query`, `create_github_issue`) and two agents:

**Quick path: no GitHub token needed.** Run **Dependency check (read-only)** with a task such as
`Check the dependencies of Kaanistaken/vodafone-case-sandbox`. It reads the public manifest, queries OSV and answers with
a table. The approval step is not part of this agent.

**Full path: about 3 minutes of GitHub preparation (not yet timed).**

1. On GitHub, create a repository of your own with Issues enabled, and a
   [fine-grained token](https://github.com/settings/personal-access-tokens/new) limited to that repository with
   *Issues: Read and write*.
2. In the platform, go to **Settings → Your tool secrets** and add `GITHUB_TOKEN` with allowed host `api.github.com`
   and the token as value. The secret is only ever sent to that host, only in runs you start.
3. Run **Dependency check** with a task such as
   `Check Kaanistaken/vodafone-case-sandbox and open the issue in <you>/<your-repo>`.
4. The run pauses at `create_github_issue`, showing the exact request without the secret. Approve it (or reject it
   with a note) and the run continues live.

The sandbox manifest is [`examples/dependency-check/package.json`](examples/dependency-check/package.json):
`lodash@4.17.15`, `minimist@1.2.0`, `jsonwebtoken@8.5.1` (known advisories) and `ms@2.0.0` (none).

## Tests

```powershell
cd backend
go test ./...
```

No test needs a real key or a paid call: model providers are replaced by a scripted fake and by hand-written stream
fixtures, and tool requests go to local TLS test servers. The tests cover the agent loop (invalid calls, limits,
retries, final states), tool templates and the HTTP safety rules, team permissions, and the approval flow (restart,
races, credential changes, expiry, interrupted writes).

## Assumptions

- Login is username and password for seeded users only; there is no sign-up or password change.
- Each user belongs to one team. Within a team, everyone can see and run all agents and tools; only the owner can
  change them (others can clone). A run always uses the model key and tool secrets of the user who starts it.
- Only the user who started a run can approve or reject its tool calls; the starter and the team lead can cancel it.
- An approval waits 24 hours (until Monday if that falls on a weekend), then the run is cancelled.
- A run allows at most 20 model turns; a model answer is limited to 4096 output tokens; at most 128 KB of a tool
  response goes to the model.
- Tools can only call `https` URLs on port 443 of public addresses; redirects are not followed; secrets can only be
  used in headers.
- One backend process; runs that were in progress resume when the server starts again.
