CREATE TABLE IF NOT EXISTS system_state (
    id INTEGER PRIMARY KEY CHECK (id = 1),
    enabled BOOLEAN NOT NULL DEFAULT false
);

CREATE TABLE IF NOT EXISTS cursors (
    filename TEXT PRIMARY KEY,
    last_offset INTEGER NOT NULL DEFAULT 0,
    updated_at DATETIME DEFAULT CURRENT_TIMESTAMP
);

CREATE TABLE IF NOT EXISTS batches (
    acceptance_id TEXT PRIMARY KEY,
    source_id TEXT NOT NULL,
    batch_id TEXT NOT NULL,
    filename TEXT NOT NULL,
    generation TEXT NOT NULL,
    payload_fingerprint TEXT NOT NULL,
    records_json TEXT NOT NULL,
    accepted_at DATETIME DEFAULT CURRENT_TIMESTAMP,
    UNIQUE(source_id, batch_id)
);

CREATE TABLE IF NOT EXISTS runs (
    id TEXT PRIMARY KEY,
    batch_acceptance_id TEXT NOT NULL,
    program_id TEXT NOT NULL,
    status TEXT NOT NULL CHECK(status IN ('queued', 'running', 'completed', 'failed', 'interrupted')),
    log_file_path TEXT,
    output_file_path TEXT,
    created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
    FOREIGN KEY(batch_acceptance_id) REFERENCES batches(acceptance_id)
);

CREATE TABLE IF NOT EXISTS capture_cursors (
 filename TEXT PRIMARY KEY,
 identity TEXT NOT NULL,
 generation TEXT NOT NULL,
 offset INTEGER NOT NULL,
 prefix_hash TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS processor_checkpoint_resets (
 id TEXT PRIMARY KEY, cursors_json TEXT NOT NULL, legacy_json TEXT NOT NULL,
 created_at DATETIME DEFAULT CURRENT_TIMESTAMP
);

CREATE TABLE IF NOT EXISTS processor_programs (
 program_id TEXT PRIMARY KEY, version TEXT NOT NULL, parallel_limit INTEGER NOT NULL,
 retry_safe INTEGER NOT NULL, export_results INTEGER NOT NULL
);
CREATE TABLE IF NOT EXISTS processor_run_specs (
 run_id TEXT PRIMARY KEY, version TEXT NOT NULL, parallel_limit INTEGER NOT NULL,
 retry_safe INTEGER NOT NULL, export_results INTEGER NOT NULL
);
CREATE TABLE IF NOT EXISTS processor_attempts (
 id TEXT PRIMARY KEY, run_id TEXT NOT NULL, worker_id TEXT NOT NULL,
 version TEXT NOT NULL, token TEXT NOT NULL, phase TEXT NOT NULL,
 lease_until INTEGER NOT NULL, created_at INTEGER NOT NULL, started_at INTEGER,
 finished_at INTEGER, completion_json TEXT, error TEXT NOT NULL DEFAULT ''
);
CREATE INDEX IF NOT EXISTS processor_attempts_run ON processor_attempts(run_id);
CREATE INDEX IF NOT EXISTS processor_attempts_phase ON processor_attempts(phase,lease_until);
CREATE TABLE IF NOT EXISTS processor_blocks (run_id TEXT PRIMARY KEY, reason TEXT NOT NULL);
CREATE TABLE IF NOT EXISTS processor_batch_workers(acceptance_id TEXT PRIMARY KEY,worker_id TEXT NOT NULL);
CREATE TABLE IF NOT EXISTS processor_retry_windows (run_id TEXT PRIMARY KEY, baseline INTEGER NOT NULL);

CREATE TABLE IF NOT EXISTS processor_version_settings(version TEXT PRIMARY KEY, settings TEXT NOT NULL);
CREATE TABLE IF NOT EXISTS processor_run_ancestry(run_id TEXT PRIMARY KEY, ancestry TEXT NOT NULL);
CREATE TABLE IF NOT EXISTS processor_publications(attempt_id TEXT NOT NULL,name TEXT NOT NULL,generation TEXT NOT NULL,sequence INTEGER NOT NULL,fingerprint TEXT NOT NULL,PRIMARY KEY(attempt_id,name,generation,sequence));
CREATE TABLE IF NOT EXISTS processor_output_lines(id INTEGER PRIMARY KEY AUTOINCREMENT,name TEXT NOT NULL,line TEXT NOT NULL,run_id TEXT NOT NULL,rendered INTEGER NOT NULL DEFAULT 0,UNIQUE(name,line));
CREATE TABLE IF NOT EXISTS processor_dispatch_order(program_id TEXT NOT NULL,filename TEXT NOT NULL,last_started INTEGER NOT NULL,PRIMARY KEY(program_id,filename));
CREATE TABLE IF NOT EXISTS processor_migrations(name TEXT PRIMARY KEY, applied_at INTEGER NOT NULL);

CREATE TABLE IF NOT EXISTS version_check_templates (
 id TEXT PRIMARY KEY, name TEXT NOT NULL COLLATE NOCASE UNIQUE,
 checks TEXT NOT NULL, revision INTEGER NOT NULL DEFAULT 1,
 created_at TEXT NOT NULL, updated_at TEXT NOT NULL
);
CREATE TABLE IF NOT EXISTS version_template_seeds (key TEXT PRIMARY KEY);

CREATE TABLE IF NOT EXISTS processor_program_priorities (
 program_id TEXT PRIMARY KEY, configured_rank INTEGER NOT NULL DEFAULT 1 CHECK(configured_rank BETWEEN 1 AND 5),
 override_rank INTEGER CHECK(override_rank BETWEEN 1 AND 5), revision INTEGER NOT NULL DEFAULT 1
);
CREATE TABLE IF NOT EXISTS processor_priority_turn (
 id INTEGER PRIMARY KEY CHECK(id=1), program_id TEXT NOT NULL DEFAULT '', remaining INTEGER NOT NULL DEFAULT 0
);
INSERT OR IGNORE INTO processor_priority_turn(id) VALUES(1);
CREATE TABLE IF NOT EXISTS processor_priority_commits (attempt_id TEXT PRIMARY KEY);

CREATE TABLE IF NOT EXISTS processor_output_destinations (
 output_id INTEGER PRIMARY KEY, drive INTEGER NOT NULL, version TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS processor_templates (
 id TEXT PRIMARY KEY, name TEXT NOT NULL COLLATE NOCASE UNIQUE,
 definition TEXT NOT NULL, revision INTEGER NOT NULL,
 created_at TEXT NOT NULL, updated_at TEXT NOT NULL
);
CREATE TABLE IF NOT EXISTS processor_template_seeds (id TEXT PRIMARY KEY);

-- Dashboard publication counts must not scan every stored output per run.
CREATE INDEX IF NOT EXISTS processor_output_run_rendered ON processor_output_lines(run_id,rendered);
