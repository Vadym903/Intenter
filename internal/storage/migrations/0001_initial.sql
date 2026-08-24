-- Schema v1 (contracts/storage-schema.md). Applied inside one transaction.

CREATE TABLE schema_version (
  version    INTEGER PRIMARY KEY,
  applied_at TEXT NOT NULL
);

CREATE TABLE meta (
  key   TEXT PRIMARY KEY,
  value TEXT NOT NULL
);

CREATE TABLE projects (
  id            TEXT PRIMARY KEY,          -- sha256(canonical root)
  root_path     TEXT NOT NULL,
  remote_url    TEXT,
  first_seen_at TEXT NOT NULL,
  last_seen_at  TEXT NOT NULL
);

CREATE TABLE approvals (
  id                       INTEGER PRIMARY KEY,
  project_id               TEXT NOT NULL REFERENCES projects(id),
  kind                     TEXT NOT NULL CHECK (kind IN ('EXACT','SEMANTIC')),
  semantic_ops             TEXT NOT NULL,   -- JSON array
  envelope                 TEXT NOT NULL,   -- JSON array of {type,scope,flags}
  targets                  TEXT,            -- JSON array (EXACT only)
  network                  TEXT NOT NULL,   -- JSON array
  engine_version           INTEGER NOT NULL,
  origin                   TEXT NOT NULL CHECK (origin IN ('claude_prompt','claude_rule','cli')),
  origin_ref               TEXT,
  created_from_event_id    INTEGER,
  created_from_raw_command TEXT NOT NULL,
  created_by_agent         TEXT NOT NULL,
  state                    TEXT NOT NULL CHECK (state IN ('ACTIVE','DISABLED','REVOKED')),
  note                     TEXT,
  created_at               TEXT NOT NULL,
  last_used_at             TEXT,
  use_count                INTEGER NOT NULL DEFAULT 0,
  disabled_at              TEXT,
  revoked_at               TEXT
);
CREATE INDEX idx_approvals_project_state ON approvals(project_id, state);

CREATE TABLE approval_conditions (
  approval_id INTEGER NOT NULL REFERENCES approvals(id),
  kind        TEXT NOT NULL,                -- 'fingerprint'
  key         TEXT NOT NULL,
  value       TEXT NOT NULL,
  PRIMARY KEY (approval_id, kind, key)
);

CREATE TABLE approval_events (
  id             INTEGER PRIMARY KEY,
  approval_id    INTEGER NOT NULL REFERENCES approvals(id),
  event_type     TEXT NOT NULL CHECK (event_type IN ('created','matched','not_matched','disabled','enabled','revoked')),
  audit_event_id INTEGER,
  at             TEXT NOT NULL,
  details        TEXT                       -- JSON
);
CREATE INDEX idx_approval_events_approval ON approval_events(approval_id, at);

CREATE TABLE audit_events (
  id                     INTEGER PRIMARY KEY,
  at                     TEXT NOT NULL,
  agent                  TEXT NOT NULL,
  agent_version          TEXT,
  session_id             TEXT,
  tool_use_id            TEXT,
  hook_event             TEXT,
  project_id             TEXT REFERENCES projects(id),
  cwd                    TEXT NOT NULL,
  tool                   TEXT NOT NULL,
  dialect                TEXT NOT NULL,
  raw_command            TEXT NOT NULL,
  resolved               TEXT NOT NULL,     -- JSON ResolvedAction
  resolution_status      TEXT NOT NULL,
  decision               TEXT NOT NULL,
  decision_class         TEXT NOT NULL,
  reason                 TEXT NOT NULL,
  hard_rule              TEXT,
  matched_approval_id    INTEGER,
  related_approval_ids   TEXT,              -- JSON array
  mismatch_report        TEXT,              -- JSON
  imported_approval_id   INTEGER,
  adapter_action         TEXT,
  adapter_context        TEXT,              -- JSON
  prompt_shown           INTEGER NOT NULL DEFAULT 0,
  permission_suggestions TEXT,              -- JSON verbatim
  execution_status       TEXT,
  execution_at           TEXT,
  response_summary       TEXT,
  engine_version         INTEGER NOT NULL,
  dry_run                INTEGER NOT NULL DEFAULT 0
);
CREATE INDEX idx_audit_at ON audit_events(at);
CREATE INDEX idx_audit_session_tool ON audit_events(session_id, tool_use_id);
CREATE INDEX idx_audit_project_at ON audit_events(project_id, at);

CREATE TABLE agent_rule_imports (
  id          INTEGER PRIMARY KEY,
  project_id  TEXT NOT NULL,
  agent       TEXT NOT NULL,
  rule_key    TEXT NOT NULL,
  raw_command TEXT NOT NULL,
  approval_id INTEGER NOT NULL REFERENCES approvals(id),
  imported_at TEXT NOT NULL,
  UNIQUE (project_id, agent, rule_key, raw_command)
);
