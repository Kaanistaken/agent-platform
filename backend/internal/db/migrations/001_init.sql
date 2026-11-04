-- Initial schema. See docs/schema.md for the meaning of every table and column.

CREATE TABLE teams (
  id   INTEGER PRIMARY KEY,
  name TEXT NOT NULL UNIQUE
);

CREATE TABLE users (
  id            INTEGER PRIMARY KEY,
  team_id       INTEGER NOT NULL REFERENCES teams(id),
  username      TEXT NOT NULL UNIQUE,
  password_hash TEXT NOT NULL,
  role          TEXT NOT NULL CHECK (role IN ('member', 'lead')),
  created_at    TEXT NOT NULL,
  UNIQUE (id, team_id)
);

CREATE TABLE sessions (
  token_hash BLOB PRIMARY KEY,           -- SHA-256 of the token (D38)
  user_id    INTEGER NOT NULL REFERENCES users(id),
  created_at TEXT NOT NULL,
  expires_at TEXT NOT NULL
);

CREATE TABLE user_model_keys (
  user_id     INTEGER NOT NULL REFERENCES users(id),
  provider    TEXT NOT NULL CHECK (provider IN ('openai', 'anthropic')),
  ciphertext  BLOB NOT NULL,
  nonce       BLOB NOT NULL,
  key_version INTEGER NOT NULL,
  aad_version INTEGER NOT NULL,
  updated_at  TEXT NOT NULL,
  PRIMARY KEY (user_id, provider)
);

CREATE TABLE user_secrets (
  id           INTEGER PRIMARY KEY AUTOINCREMENT, -- never reused
  user_id      INTEGER NOT NULL REFERENCES users(id),
  name         TEXT NOT NULL,
  allowed_host TEXT NOT NULL,
  ciphertext   BLOB NOT NULL,
  nonce        BLOB NOT NULL,
  key_version  INTEGER NOT NULL,
  aad_version  INTEGER NOT NULL,
  updated_at   TEXT NOT NULL,
  UNIQUE (user_id, name)
);

CREATE TABLE agents (
  id            INTEGER PRIMARY KEY AUTOINCREMENT,
  team_id       INTEGER NOT NULL,
  owner_id      INTEGER NOT NULL,
  name          TEXT NOT NULL,
  system_prompt TEXT NOT NULL,
  provider      TEXT NOT NULL CHECK (provider IN ('openai', 'anthropic')),
  model         TEXT NOT NULL,
  deleted_at    TEXT,
  created_at    TEXT NOT NULL,
  updated_at    TEXT NOT NULL,
  FOREIGN KEY (owner_id, team_id) REFERENCES users (id, team_id),
  UNIQUE (id, team_id)
);

CREATE TABLE tools (
  id                INTEGER PRIMARY KEY AUTOINCREMENT,
  team_id           INTEGER NOT NULL,
  owner_id          INTEGER NOT NULL,
  name              TEXT NOT NULL,
  description       TEXT NOT NULL,
  method            TEXT NOT NULL CHECK (method IN ('GET', 'POST', 'PUT', 'PATCH', 'DELETE')),
  url_template      TEXT NOT NULL,
  headers_template  TEXT NOT NULL DEFAULT '{}',
  body_template     TEXT NOT NULL DEFAULT '',
  params_schema     TEXT NOT NULL,
  requires_approval INTEGER NOT NULL DEFAULT 0 CHECK (requires_approval IN (0, 1)),
  deleted_at        TEXT,
  created_at        TEXT NOT NULL,
  updated_at        TEXT NOT NULL,
  FOREIGN KEY (owner_id, team_id) REFERENCES users (id, team_id),
  UNIQUE (id, team_id)
);

CREATE TABLE agent_tools (
  agent_id INTEGER NOT NULL,
  tool_id  INTEGER NOT NULL,
  team_id  INTEGER NOT NULL,
  PRIMARY KEY (agent_id, tool_id),
  FOREIGN KEY (agent_id, team_id) REFERENCES agents (id, team_id),
  FOREIGN KEY (tool_id, team_id) REFERENCES tools (id, team_id)
);
CREATE INDEX agent_tools_tool ON agent_tools (tool_id);

-- D46: a new link to a deleted tool is rejected in the database as well.
CREATE TRIGGER agent_tools_no_deleted_tool
BEFORE INSERT ON agent_tools
WHEN (SELECT deleted_at FROM tools WHERE id = NEW.tool_id) IS NOT NULL
BEGIN
  SELECT RAISE(ABORT, 'tool is deleted');
END;

CREATE TABLE runs (
  id                  INTEGER PRIMARY KEY AUTOINCREMENT,
  team_id             INTEGER NOT NULL,
  agent_id            INTEGER NOT NULL,
  started_by          INTEGER NOT NULL,
  task                TEXT NOT NULL,
  status              TEXT NOT NULL CHECK (status IN
                        ('queued', 'running', 'waiting_approval', 'succeeded', 'failed', 'cancelled')),
  snapshot            TEXT NOT NULL,
  final_output        TEXT,
  error_code          TEXT,
  error_message       TEXT,
  step_count          INTEGER NOT NULL DEFAULT 0,
  invalid_call_streak INTEGER NOT NULL DEFAULT 0,
  active_ms           INTEGER NOT NULL DEFAULT 0,
  cancelled_by        INTEGER,
  created_at          TEXT NOT NULL,
  started_at          TEXT,
  finished_at         TEXT,
  FOREIGN KEY (agent_id, team_id) REFERENCES agents (id, team_id),
  FOREIGN KEY (started_by, team_id) REFERENCES users (id, team_id),
  FOREIGN KEY (cancelled_by, team_id) REFERENCES users (id, team_id),
  UNIQUE (id, started_by)
);
CREATE INDEX runs_team_time ON runs (team_id, created_at);
CREATE INDEX runs_status ON runs (status);

CREATE TABLE run_messages (
  run_id       INTEGER NOT NULL REFERENCES runs(id),
  seq          INTEGER NOT NULL,
  role         TEXT NOT NULL CHECK (role IN ('user', 'assistant', 'tool')),
  content      TEXT NOT NULL DEFAULT '',
  tool_calls   TEXT,              -- JSON list of {id, name, arguments}
  tool_call_id TEXT,
  raw_content  TEXT,              -- provider's own content, sent back verbatim (D96)
  created_at   TEXT NOT NULL,
  PRIMARY KEY (run_id, seq)
);

CREATE TABLE tool_calls (
  id                  INTEGER PRIMARY KEY AUTOINCREMENT,
  run_id              INTEGER NOT NULL,
  provider_call_id    TEXT NOT NULL,
  tool_id             INTEGER,       -- null: unknown tool (D48)
  requested_name      TEXT NOT NULL,
  raw_arguments       TEXT NOT NULL,
  arguments           TEXT,          -- null when the JSON is malformed
  status              TEXT NOT NULL CHECK (status IN ('invalid', 'pending_approval', 'approved', 'rejected',
                        'invalidated', 'executing', 'succeeded', 'failed', 'outcome_unknown')),
  reason_code         TEXT,
  rejection_note      TEXT,
  approval_revision   INTEGER NOT NULL DEFAULT 1,
  approval_expires_at TEXT,
  request_preview     TEXT,          -- secret-free JSON (D33)
  response            TEXT,          -- masked, truncated
  attempt_count       INTEGER NOT NULL DEFAULT 0,
  decided_by          INTEGER,
  decided_at          TEXT,
  started_at          TEXT,
  finished_at         TEXT,
  created_at          TEXT NOT NULL,
  updated_at          TEXT NOT NULL,
  FOREIGN KEY (run_id) REFERENCES runs (id),
  FOREIGN KEY (run_id, decided_by) REFERENCES runs (id, started_by)
);
CREATE INDEX tool_calls_run_status ON tool_calls (run_id, status);
CREATE INDEX tool_calls_expiry ON tool_calls (status, approval_expires_at);

CREATE TABLE run_events (
  run_id     INTEGER NOT NULL REFERENCES runs(id),
  seq        INTEGER NOT NULL,
  type       TEXT NOT NULL,
  payload    TEXT NOT NULL,
  created_at TEXT NOT NULL,
  PRIMARY KEY (run_id, seq)
);

CREATE TABLE model_calls (
  id            INTEGER PRIMARY KEY AUTOINCREMENT,
  run_id        INTEGER NOT NULL REFERENCES runs(id),
  turn          INTEGER NOT NULL,
  attempt       INTEGER NOT NULL,
  provider      TEXT NOT NULL,
  model         TEXT NOT NULL,
  outcome       TEXT CHECK (outcome IN ('succeeded', 'retried', 'failed', 'discarded', 'interrupted')),
  input_tokens  INTEGER,
  output_tokens INTEGER,
  usage_status  TEXT NOT NULL DEFAULT 'unavailable' CHECK (usage_status IN ('complete', 'partial', 'unavailable')),
  error_code    TEXT,
  started_at    TEXT NOT NULL,
  finished_at   TEXT
);
CREATE INDEX model_calls_run ON model_calls (run_id);

CREATE TABLE key_check (
  key_version INTEGER PRIMARY KEY,
  ciphertext  BLOB NOT NULL,
  nonce       BLOB NOT NULL,
  aad_version INTEGER NOT NULL
);
