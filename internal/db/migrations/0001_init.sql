-- Initial schema. Times are Unix milliseconds.

CREATE TABLE settings (
    key   TEXT PRIMARY KEY,
    value TEXT NOT NULL
);

CREATE TABLE proxies (
    id           INTEGER PRIMARY KEY,
    name         TEXT    NOT NULL,
    type         TEXT    NOT NULL CHECK (type IN ('http', 'https', 'socks5')),
    host         TEXT    NOT NULL,
    port         INTEGER NOT NULL CHECK (port BETWEEN 1 AND 65535),
    username     TEXT    NOT NULL DEFAULT '',
    password_enc TEXT    NOT NULL DEFAULT '',
    enabled      INTEGER NOT NULL DEFAULT 1,
    created_at   INTEGER NOT NULL
);
CREATE UNIQUE INDEX proxies_identity ON proxies (type, host, port, username);

CREATE TABLE providers (
    id              INTEGER PRIMARY KEY,
    name            TEXT    NOT NULL UNIQUE,
    type            TEXT    NOT NULL CHECK (type IN ('openai', 'anthropic', 'gemini')),
    base_url        TEXT    NOT NULL,
    headers         TEXT    NOT NULL DEFAULT '{}',
    timeout_seconds INTEGER NOT NULL DEFAULT 600,
    enabled         INTEGER NOT NULL DEFAULT 1,
    proxy_id        INTEGER REFERENCES proxies (id) ON DELETE SET NULL,
    key_strategy    TEXT    NOT NULL DEFAULT 'round_robin'
                    CHECK (key_strategy IN ('round_robin', 'random', 'least_recently_used')),
    created_at      INTEGER NOT NULL
);

CREATE TABLE api_keys (
    id             INTEGER PRIMARY KEY,
    provider_id    INTEGER NOT NULL REFERENCES providers (id) ON DELETE CASCADE,
    label          TEXT    NOT NULL DEFAULT '',
    secret_enc     TEXT    NOT NULL,
    secret_hash    TEXT    NOT NULL,
    secret_hint    TEXT    NOT NULL,
    enabled        INTEGER NOT NULL DEFAULT 1,
    proxy_mode     TEXT    NOT NULL DEFAULT 'inherit' CHECK (proxy_mode IN ('inherit', 'direct', 'specific')),
    proxy_id       INTEGER REFERENCES proxies (id) ON DELETE SET NULL,
    status         TEXT    NOT NULL DEFAULT 'active' CHECK (status IN ('active', 'cooling_down', 'disabled')),
    cooldown_until INTEGER NOT NULL DEFAULT 0,
    last_error     TEXT    NOT NULL DEFAULT '',
    last_used_at   INTEGER NOT NULL DEFAULT 0,
    success_count  INTEGER NOT NULL DEFAULT 0,
    failure_count  INTEGER NOT NULL DEFAULT 0,
    created_at     INTEGER NOT NULL,
    UNIQUE (provider_id, secret_hash)
);
CREATE INDEX api_keys_proxy ON api_keys (proxy_id);

CREATE TABLE models (
    id                      INTEGER PRIMARY KEY,
    name                    TEXT    NOT NULL UNIQUE,
    type                    TEXT    NOT NULL CHECK (type IN ('chat', 'embedding', 'image', 'video', 'tts', 'asr')),
    strategy                TEXT    NOT NULL DEFAULT 'priority'
                            CHECK (strategy IN ('priority', 'round_robin', 'weighted', 'random')),
    max_total_attempts      INTEGER NOT NULL DEFAULT 3,
    max_keys_per_target     INTEGER NOT NULL DEFAULT 2,
    retry_on                TEXT    NOT NULL DEFAULT '429,5xx,timeout,connection',
    attempt_timeout_seconds INTEGER NOT NULL DEFAULT 120,
    created_at              INTEGER NOT NULL
);

CREATE TABLE model_targets (
    id             INTEGER PRIMARY KEY,
    model_id       INTEGER NOT NULL REFERENCES models (id) ON DELETE CASCADE,
    position       INTEGER NOT NULL,
    provider_id    INTEGER NOT NULL REFERENCES providers (id) ON DELETE CASCADE,
    upstream_model TEXT    NOT NULL,
    weight         INTEGER NOT NULL DEFAULT 1,
    enabled        INTEGER NOT NULL DEFAULT 1
);
CREATE INDEX model_targets_model ON model_targets (model_id, position);
CREATE INDEX model_targets_provider ON model_targets (provider_id);

CREATE TABLE gateway_keys (
    id          INTEGER PRIMARY KEY,
    name        TEXT    NOT NULL,
    secret_hash TEXT    NOT NULL UNIQUE,
    secret_hint TEXT    NOT NULL,
    enabled     INTEGER NOT NULL DEFAULT 1,
    created_at  INTEGER NOT NULL
);

CREATE TABLE sessions (
    token_hash TEXT PRIMARY KEY,
    created_at INTEGER NOT NULL,
    expires_at INTEGER NOT NULL
);

CREATE TABLE request_logs (
    id              INTEGER PRIMARY KEY,
    created_at      INTEGER NOT NULL,
    endpoint        TEXT    NOT NULL,
    model           TEXT    NOT NULL,
    model_type      TEXT    NOT NULL DEFAULT '',
    provider_id     INTEGER,
    provider_name   TEXT    NOT NULL DEFAULT '',
    key_id          INTEGER,
    key_hint        TEXT    NOT NULL DEFAULT '',
    upstream_model  TEXT    NOT NULL DEFAULT '',
    status_code     INTEGER NOT NULL,
    latency_ms      INTEGER NOT NULL,
    stream          INTEGER NOT NULL DEFAULT 0,
    input_tokens    INTEGER,
    output_tokens   INTEGER,
    attempts        INTEGER NOT NULL DEFAULT 0,
    error           TEXT    NOT NULL DEFAULT '',
    attempt_details TEXT    NOT NULL DEFAULT '[]',
    request_body    TEXT,
    response_body   TEXT
);
CREATE INDEX request_logs_created ON request_logs (created_at);
CREATE INDEX request_logs_model ON request_logs (model, id);
CREATE INDEX request_logs_provider ON request_logs (provider_id, id);

CREATE TABLE video_jobs (
    id          TEXT PRIMARY KEY,
    model       TEXT    NOT NULL,
    provider_id INTEGER NOT NULL REFERENCES providers (id) ON DELETE CASCADE,
    key_id      INTEGER NOT NULL REFERENCES api_keys (id) ON DELETE CASCADE,
    created_at  INTEGER NOT NULL
);
