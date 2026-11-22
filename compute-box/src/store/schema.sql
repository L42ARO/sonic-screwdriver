CREATE TABLE IF NOT EXISTS users (
    id         INTEGER PRIMARY KEY,
    name       TEXT NOT NULL UNIQUE,
    created_at TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS projects (
    id         INTEGER PRIMARY KEY,
    user_id    INTEGER NOT NULL REFERENCES users(id),
    name       TEXT NOT NULL,
    created_at TEXT NOT NULL,
    UNIQUE (user_id, name)
);

-- One row per operation; `definition` is the JSON document callers sync.
CREATE TABLE IF NOT EXISTS operations (
    id         INTEGER PRIMARY KEY,
    project_id INTEGER NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
    name       TEXT NOT NULL,
    definition TEXT NOT NULL,
    updated_at TEXT NOT NULL,
    UNIQUE (project_id, name)
);

CREATE TABLE IF NOT EXISTS machines (
    id         INTEGER PRIMARY KEY,
    user_id    INTEGER NOT NULL REFERENCES users(id),
    name       TEXT NOT NULL,
    screen     TEXT,
    last_seen  TEXT,
    UNIQUE (user_id, name)
);

-- Project relevance and machine installation details are independent configurations.
CREATE TABLE IF NOT EXISTS project_programs (
    project_id INTEGER NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
    name TEXT NOT NULL,
    definition TEXT NOT NULL,
    PRIMARY KEY (project_id, name)
);
CREATE TABLE IF NOT EXISTS machine_programs (
    machine_id INTEGER NOT NULL REFERENCES machines(id) ON DELETE CASCADE,
    name TEXT NOT NULL,
    definition TEXT NOT NULL,
    PRIMARY KEY (machine_id, name)
);

-- `definition` is a snapshot of the operation the job ran with.
CREATE TABLE IF NOT EXISTS jobs (
    id              TEXT PRIMARY KEY,
    user_id         INTEGER NOT NULL REFERENCES users(id),
    project         TEXT NOT NULL,
    operation       TEXT NOT NULL,
    machine         TEXT NOT NULL,
    input           TEXT NOT NULL,
    definition      TEXT NOT NULL,
    status          TEXT NOT NULL,
    output          TEXT,
    error_code      TEXT,
    error_message   TEXT,
    outcome_unknown INTEGER NOT NULL DEFAULT 0,
    idempotency_key TEXT,
    model           TEXT,
    created_at      TEXT NOT NULL,
    started_at      TEXT,
    finished_at     TEXT
);
CREATE UNIQUE INDEX IF NOT EXISTS jobs_idempotency ON jobs (user_id, idempotency_key)
    WHERE idempotency_key IS NOT NULL;
CREATE INDEX IF NOT EXISTS jobs_machine ON jobs (user_id, machine, created_at);

CREATE TABLE IF NOT EXISTS job_steps (
    job_id      TEXT NOT NULL REFERENCES jobs(id),
    n           INTEGER NOT NULL,
    description TEXT NOT NULL,
    action      TEXT NOT NULL,
    ok          INTEGER NOT NULL,
    error       TEXT,
    screenshot  TEXT,
    note        TEXT,
    created_at  TEXT NOT NULL,
    PRIMARY KEY (job_id, n)
);

-- Every model call, with tokens and cost. cost_usd uses the agreed pricing function;
-- cost_conservative_usd also charges cache writes at the write premium.
CREATE TABLE IF NOT EXISTS model_usage (
    id                    INTEGER PRIMARY KEY,
    job_id                TEXT,
    model                 TEXT NOT NULL,
    input_tokens          INTEGER NOT NULL,
    cached_tokens         INTEGER NOT NULL,
    cache_write_tokens    INTEGER NOT NULL,
    output_tokens         INTEGER NOT NULL,
    reasoning_tokens      INTEGER NOT NULL,
    cost_usd              REAL NOT NULL,
    cost_conservative_usd REAL NOT NULL,
    created_at            TEXT NOT NULL
);
CREATE INDEX IF NOT EXISTS model_usage_job ON model_usage (job_id);
