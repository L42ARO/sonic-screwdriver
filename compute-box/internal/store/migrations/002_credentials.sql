-- Per-machine tokens, API keys, dashboard sessions, one-time sign-in links, and usage
-- attributed to its owner. Only SHA-256 hashes of credentials are stored.

ALTER TABLE machines ADD COLUMN token_hash TEXT;
ALTER TABLE machines ADD COLUMN token_prefix TEXT;
ALTER TABLE machines ADD COLUMN token_created_at TEXT;
CREATE UNIQUE INDEX machines_token ON machines (token_hash) WHERE token_hash IS NOT NULL;

CREATE TABLE api_keys (
    id           INTEGER PRIMARY KEY,
    user_id      INTEGER NOT NULL REFERENCES users(id),
    name         TEXT NOT NULL,
    prefix       TEXT NOT NULL,
    hash         TEXT NOT NULL UNIQUE,
    created_at   TEXT NOT NULL,
    last_used_at TEXT,
    revoked_at   TEXT
);
CREATE INDEX api_keys_user ON api_keys (user_id);

CREATE TABLE sessions (
    hash       TEXT PRIMARY KEY,
    user_id    INTEGER NOT NULL REFERENCES users(id),
    created_at TEXT NOT NULL,
    expires_at TEXT NOT NULL
);

CREATE TABLE login_tokens (
    hash       TEXT PRIMARY KEY,
    user_id    INTEGER NOT NULL REFERENCES users(id),
    created_at TEXT NOT NULL,
    expires_at TEXT NOT NULL,
    used_at    TEXT
);

ALTER TABLE model_usage ADD COLUMN user_id INTEGER REFERENCES users(id);
UPDATE model_usage SET user_id = COALESCE(
    (SELECT j.user_id FROM jobs j WHERE j.id = model_usage.job_id),
    (SELECT id FROM users WHERE name = 'default'));
CREATE INDEX model_usage_user ON model_usage (user_id);
