PRAGMA journal_mode=WAL;
PRAGMA foreign_keys=ON;
PRAGMA busy_timeout=5000;
PRAGMA synchronous=FULL;

CREATE TABLE IF NOT EXISTS schema_migrations (
  version INTEGER PRIMARY KEY,
  applied_at TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS settings (
  key TEXT PRIMARY KEY,
  value TEXT NOT NULL,
  revision INTEGER NOT NULL DEFAULT 1
);

CREATE TABLE IF NOT EXISTS admin_users (
  id TEXT PRIMARY KEY,
  username TEXT NOT NULL UNIQUE,
  password_hash TEXT NOT NULL,
  role TEXT NOT NULL,
  created_at TEXT NOT NULL,
  last_login_at TEXT
);

CREATE TABLE IF NOT EXISTS admin_sessions (
  id TEXT PRIMARY KEY,
  user_id TEXT NOT NULL,
  token_hash TEXT NOT NULL UNIQUE,
  csrf TEXT NOT NULL,
  expires_at TEXT NOT NULL,
  created_at TEXT NOT NULL,
  last_seen_at TEXT NOT NULL,
  recent_auth_until TEXT,
  FOREIGN KEY(user_id) REFERENCES admin_users(id)
);

CREATE TABLE IF NOT EXISTS enrollment_codes (
  id TEXT PRIMARY KEY,
  code_hash TEXT NOT NULL UNIQUE,
  created_by TEXT NOT NULL,
  created_at TEXT NOT NULL,
  expires_at TEXT NOT NULL,
  consumed_at TEXT,
  consumed_by TEXT
);

CREATE TABLE IF NOT EXISTS agents (
  id TEXT PRIMARY KEY,
  display_name TEXT,
  hostname TEXT,
  os TEXT,
  arch TEXT,
  credential_hash TEXT,
  credential_id TEXT,
  revoked INTEGER NOT NULL DEFAULT 0,
  archived INTEGER NOT NULL DEFAULT 0,
  pinned INTEGER NOT NULL DEFAULT 0,
  hidden INTEGER NOT NULL DEFAULT 0,
  desired_revision INTEGER NOT NULL DEFAULT 1,
  desired_hash TEXT,
  desired_config TEXT,
  applied_revision INTEGER NOT NULL DEFAULT 0,
  applied_hash TEXT,
  last_seen_at TEXT,
  last_live_at TEXT,
  session_id TEXT,
  last_seq INTEGER NOT NULL DEFAULT 0,
  worker_version TEXT,
  worker_digest TEXT,
  service_host_version TEXT,
  service_host_digest TEXT,
  managed_ready INTEGER NOT NULL DEFAULT 0,
  capabilities TEXT,
  addresses TEXT,
  endpoint_generation INTEGER NOT NULL DEFAULT 1,
  group_name TEXT,
  tags TEXT,
  created_at TEXT NOT NULL,
  conflict INTEGER NOT NULL DEFAULT 0
);

CREATE TABLE IF NOT EXISTS agent_sessions (
  id TEXT PRIMARY KEY,
  agent_id TEXT NOT NULL,
  started_at TEXT NOT NULL,
  ended_at TEXT,
  FOREIGN KEY(agent_id) REFERENCES agents(id)
);

CREATE TABLE IF NOT EXISTS services (
  id TEXT PRIMARY KEY,
  agent_id TEXT NOT NULL,
  display_name TEXT,
  url TEXT,
  dial_target TEXT,
  host_header TEXT,
  tls_server_name TEXT,
  process_name TEXT,
  source TEXT,
  speaks_http INTEGER NOT NULL DEFAULT 0,
  speaks_tls INTEGER NOT NULL DEFAULT 0,
  pinned INTEGER NOT NULL DEFAULT 0,
  hidden INTEGER NOT NULL DEFAULT 0,
  paused INTEGER NOT NULL DEFAULT 0,
  ignored INTEGER NOT NULL DEFAULT 0,
  first_seen_at TEXT NOT NULL,
  last_seen_at TEXT,
  last_discovered_at TEXT,
  archived INTEGER NOT NULL DEFAULT 0,
  UNIQUE(agent_id, dial_target, host_header),
  FOREIGN KEY(agent_id) REFERENCES agents(id)
);

CREATE TABLE IF NOT EXISTS check_definitions (
  id TEXT PRIMARY KEY,
  service_id TEXT NOT NULL,
  agent_id TEXT NOT NULL,
  kind TEXT NOT NULL,
  spec TEXT NOT NULL,
  revision INTEGER NOT NULL DEFAULT 1,
  FOREIGN KEY(service_id) REFERENCES services(id)
);

CREATE TABLE IF NOT EXISTS config_revisions (
  agent_id TEXT NOT NULL,
  revision INTEGER NOT NULL,
  hash TEXT NOT NULL,
  body TEXT NOT NULL,
  created_at TEXT NOT NULL,
  actor TEXT,
  PRIMARY KEY(agent_id, revision)
);

CREATE TABLE IF NOT EXISTS host_samples (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  agent_id TEXT NOT NULL,
  observed_at TEXT NOT NULL,
  received_at TEXT NOT NULL,
  seq INTEGER,
  session_id TEXT,
  cpu_pct REAL,
  ram_used INTEGER,
  ram_avail INTEGER,
  ram_total INTEGER,
  ping_mean_ms REAL,
  ping_loss REAL,
  payload TEXT NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_host_samples_agent_time ON host_samples(agent_id, observed_at);

CREATE TABLE IF NOT EXISTS service_observations (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  agent_id TEXT NOT NULL,
  service_id TEXT NOT NULL,
  check_id TEXT,
  observed_at TEXT NOT NULL,
  received_at TEXT NOT NULL,
  vantage TEXT,
  transport TEXT,
  http_status INTEGER,
  latency_ms REAL,
  app_result TEXT,
  quality TEXT,
  payload TEXT
);
CREATE INDEX IF NOT EXISTS idx_svc_obs_svc_time ON service_observations(service_id, observed_at);
CREATE INDEX IF NOT EXISTS idx_svc_obs_agent_time ON service_observations(agent_id, observed_at);

CREATE TABLE IF NOT EXISTS current_states (
  entity_type TEXT NOT NULL,
  entity_id TEXT NOT NULL,
  state TEXT NOT NULL,
  reason TEXT,
  since TEXT NOT NULL,
  updated_at TEXT NOT NULL,
  PRIMARY KEY(entity_type, entity_id)
);

CREATE TABLE IF NOT EXISTS state_events (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  entity_type TEXT NOT NULL,
  entity_id TEXT NOT NULL,
  from_state TEXT,
  to_state TEXT NOT NULL,
  reason TEXT,
  at TEXT NOT NULL,
  rule_version INTEGER
);
CREATE INDEX IF NOT EXISTS idx_state_events_entity ON state_events(entity_id, at);

CREATE TABLE IF NOT EXISTS metric_buckets (
  entity_id TEXT NOT NULL,
  metric TEXT NOT NULL,
  step_seconds INTEGER NOT NULL,
  bucket_start TEXT NOT NULL,
  count INTEGER NOT NULL,
  missing INTEGER NOT NULL DEFAULT 0,
  min_val REAL,
  max_val REAL,
  sum_val REAL,
  min_at TEXT,
  max_at TEXT,
  PRIMARY KEY(entity_id, metric, step_seconds, bucket_start)
);

CREATE TABLE IF NOT EXISTS dashboard_preferences (
  id TEXT PRIMARY KEY,
  body TEXT NOT NULL,
  revision INTEGER NOT NULL DEFAULT 1
);

CREATE TABLE IF NOT EXISTS operations (
  id TEXT PRIMARY KEY,
  action TEXT NOT NULL,
  status TEXT NOT NULL,
  revision INTEGER NOT NULL DEFAULT 1,
  client_request_key TEXT NOT NULL,
  actor_id TEXT NOT NULL,
  params TEXT NOT NULL,
  created_at TEXT NOT NULL,
  deadline TEXT,
  summary TEXT,
  parent_id TEXT,
  request_hash TEXT NOT NULL,
  UNIQUE(actor_id, client_request_key)
);

CREATE TABLE IF NOT EXISTS operation_targets (
  operation_id TEXT NOT NULL,
  agent_id TEXT NOT NULL,
  status TEXT NOT NULL,
  stage TEXT NOT NULL,
  message TEXT,
  error_code TEXT,
  retryable INTEGER NOT NULL DEFAULT 0,
  evidence TEXT,
  job_id TEXT,
  updated_at TEXT NOT NULL,
  PRIMARY KEY(operation_id, agent_id),
  FOREIGN KEY(operation_id) REFERENCES operations(id)
);

CREATE TABLE IF NOT EXISTS agent_jobs (
  job_id TEXT PRIMARY KEY,
  operation_id TEXT NOT NULL,
  agent_id TEXT NOT NULL,
  action TEXT NOT NULL,
  envelope TEXT NOT NULL,
  status TEXT NOT NULL,
  created_at TEXT NOT NULL,
  deadline TEXT NOT NULL,
  delivered_at TEXT,
  result TEXT
);
CREATE INDEX IF NOT EXISTS idx_agent_jobs_agent ON agent_jobs(agent_id, status);

CREATE TABLE IF NOT EXISTS controller_migrations (
  id TEXT PRIMARY KEY,
  operation_id TEXT,
  controller_id TEXT NOT NULL,
  current_url TEXT NOT NULL,
  candidate_url TEXT NOT NULL,
  generation INTEGER NOT NULL,
  mode TEXT NOT NULL,
  payload_hash TEXT NOT NULL,
  trust_pem TEXT,
  expires_at TEXT NOT NULL,
  arm_fallback INTEGER NOT NULL DEFAULT 0,
  created_at TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS migration_targets (
  plan_id TEXT NOT NULL,
  agent_id TEXT NOT NULL,
  state TEXT NOT NULL,
  reason TEXT,
  updated_at TEXT NOT NULL,
  PRIMARY KEY(plan_id, agent_id)
);

CREATE TABLE IF NOT EXISTS rule_versions (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  name TEXT NOT NULL,
  body TEXT NOT NULL,
  effective_from TEXT NOT NULL,
  effective_to TEXT
);

CREATE TABLE IF NOT EXISTS incidents (
  id TEXT PRIMARY KEY,
  entity_type TEXT NOT NULL,
  entity_id TEXT NOT NULL,
  metric TEXT,
  severity TEXT NOT NULL,
  status TEXT NOT NULL,
  opened_at TEXT NOT NULL,
  confirmed_at TEXT,
  resolved_at TEXT,
  acked_at TEXT,
  acked_by TEXT,
  rule_version INTEGER,
  reason TEXT,
  maintenance INTEGER NOT NULL DEFAULT 0
);
CREATE INDEX IF NOT EXISTS idx_incidents_open ON incidents(status, opened_at);

CREATE TABLE IF NOT EXISTS maintenance_windows (
  id TEXT PRIMARY KEY,
  entity_type TEXT,
  entity_id TEXT,
  purpose TEXT,
  start_at TEXT NOT NULL,
  end_at TEXT NOT NULL,
  created_by TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS audit_events (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  at TEXT NOT NULL,
  actor TEXT NOT NULL,
  action TEXT NOT NULL,
  entity TEXT,
  detail TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS event_log (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  ts TEXT NOT NULL,
  type TEXT NOT NULL,
  entity TEXT,
  entity_id TEXT,
  revision INTEGER,
  payload TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS check_secrets (
  id TEXT PRIMARY KEY,
  name TEXT NOT NULL,
  header_name TEXT NOT NULL,
  version INTEGER NOT NULL,
  ciphertext BLOB NOT NULL,
  nonce BLOB NOT NULL,
  agent_id TEXT NOT NULL,
  check_id TEXT,
  created_at TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS releases (
  id TEXT PRIMARY KEY,
  version TEXT NOT NULL,
  digest TEXT NOT NULL UNIQUE,
  notes TEXT,
  metadata TEXT NOT NULL,
  imported_at TEXT NOT NULL,
  trust_ok INTEGER NOT NULL DEFAULT 0,
  platforms TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS release_artifacts (
  release_id TEXT NOT NULL,
  os TEXT NOT NULL,
  arch TEXT NOT NULL,
  name TEXT NOT NULL,
  sha256 TEXT NOT NULL,
  length INTEGER NOT NULL,
  path TEXT NOT NULL,
  PRIMARY KEY(release_id, os, arch, name)
);

CREATE TABLE IF NOT EXISTS rollouts (
  id TEXT PRIMARY KEY,
  release_id TEXT NOT NULL,
  operation_id TEXT,
  policy TEXT NOT NULL,
  status TEXT NOT NULL,
  created_at TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS backups (
  id TEXT PRIMARY KEY,
  path TEXT NOT NULL,
  sha256 TEXT NOT NULL,
  size INTEGER NOT NULL,
  created_at TEXT NOT NULL,
  verified INTEGER NOT NULL DEFAULT 0
);

-- Durable, per-session deduplication of accepted telemetry envelopes.
CREATE TABLE IF NOT EXISTS ingest_receipts (
 agent_id TEXT NOT NULL, session_id TEXT NOT NULL, seq INTEGER NOT NULL,
 observed_at TEXT NOT NULL, received_at TEXT NOT NULL, payload_hash TEXT NOT NULL,
 has_host INTEGER NOT NULL DEFAULT 0, has_discovery INTEGER NOT NULL DEFAULT 0,
 PRIMARY KEY(agent_id,session_id,seq)
);
CREATE INDEX IF NOT EXISTS idx_ingest_received ON ingest_receipts(received_at);

CREATE TABLE IF NOT EXISTS agent_credential_overlap (
  agent_id TEXT PRIMARY KEY,
  pending_hash TEXT NOT NULL,
  job_id TEXT NOT NULL,
  expires_at TEXT NOT NULL,
  created_at TEXT NOT NULL,
  FOREIGN KEY(agent_id) REFERENCES agents(id)
);

CREATE TABLE IF NOT EXISTS incident_streaks (
  entity_type TEXT NOT NULL,
  entity_id TEXT NOT NULL,
  metric TEXT NOT NULL,
  last_at TEXT NOT NULL,
  bad_since TEXT NOT NULL,
  good_since TEXT NOT NULL,
  bad_count INTEGER NOT NULL,
  good_count INTEGER NOT NULL,
  PRIMARY KEY(entity_type,entity_id,metric)
);

CREATE TABLE IF NOT EXISTS service_discovery (
 service_id TEXT PRIMARY KEY REFERENCES services(id) ON DELETE CASCADE,
 payload TEXT NOT NULL, updated_at TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS agent_discovery(agent_id TEXT PRIMARY KEY REFERENCES agents(id) ON DELETE CASCADE,payload TEXT NOT NULL);

CREATE TABLE IF NOT EXISTS agent_candidates (
 id TEXT PRIMARY KEY,
 credential_hash TEXT NOT NULL,
 fingerprint TEXT NOT NULL,
 hostname TEXT NOT NULL,
 display_name TEXT NOT NULL,
 os TEXT NOT NULL,
 arch TEXT NOT NULL,
 worker_version TEXT NOT NULL,
 state TEXT NOT NULL CHECK(state IN ('pending','approved','rejected')),
 source_ip TEXT NOT NULL,
 created_at TEXT NOT NULL,
 last_seen_at TEXT NOT NULL,
 expires_at TEXT NOT NULL,
 decided_by TEXT,
 decided_at TEXT
);
CREATE INDEX IF NOT EXISTS agent_candidates_expiry ON agent_candidates(expires_at);

-- Independent cancellation preserves the original maintenance interval.
CREATE TABLE IF NOT EXISTS maintenance_cancellations (
 window_id TEXT PRIMARY KEY REFERENCES maintenance_windows(id),
 cancelled_at TEXT NOT NULL,
 cancelled_by TEXT NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_maintenance_time ON maintenance_windows(end_at,start_at);
CREATE INDEX IF NOT EXISTS idx_host_rule_version ON rule_versions(name,effective_from,id);
CREATE INDEX IF NOT EXISTS idx_host_retention ON host_samples(observed_at);
CREATE INDEX IF NOT EXISTS idx_service_retention ON service_observations(observed_at);
CREATE INDEX IF NOT EXISTS idx_event_retention ON event_log(ts);

CREATE INDEX IF NOT EXISTS idx_receipt_retention ON ingest_receipts(received_at);
CREATE INDEX IF NOT EXISTS idx_session_retention ON admin_sessions(expires_at);

-- Acknowledgement follows the attention generation, not execution revisions.
CREATE TABLE IF NOT EXISTS operation_attention (
  operation_id TEXT PRIMARY KEY REFERENCES operations(id) ON DELETE CASCADE,
  generation INTEGER NOT NULL DEFAULT 1,
  fingerprint TEXT NOT NULL DEFAULT '',
  required INTEGER NOT NULL DEFAULT 0 CHECK(required IN (0,1)),
  acknowledged_generation INTEGER NOT NULL DEFAULT 0,
  acked_at TEXT,
  acked_by TEXT
);
CREATE INDEX IF NOT EXISTS idx_operations_created_id ON operations(created_at DESC,id DESC);
CREATE INDEX IF NOT EXISTS idx_operation_attention_required ON operation_attention(required,acknowledged_generation,generation);

-- Versioned immutable release catalogue. Legacy mutable entries remain visible
-- but are not eligible for new rollout commands.
CREATE TABLE IF NOT EXISTS release_publications (
 release_id TEXT PRIMARY KEY REFERENCES releases(id),
 object_digest TEXT NOT NULL UNIQUE,
 file_inventory TEXT NOT NULL,
 expires_at TEXT NOT NULL,
 format_version INTEGER NOT NULL CHECK(format_version=1)
);
CREATE TABLE IF NOT EXISTS release_trust (
 singleton INTEGER PRIMARY KEY CHECK(singleton=1),
 revision INTEGER NOT NULL CHECK(revision>0),
 root_json BLOB NOT NULL,
 versions_json TEXT NOT NULL
);
