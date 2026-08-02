-- sites registry — the whole status schema (PRD §5). Version block 10000. Every
-- child table FKs to `site` with ON DELETE CASCADE, so deleting a site removes
-- its checks, rollups, crash groups, and events. `site` is the join point both
-- functional modules (monitoring, crash) key off; monitoring/crash/retention own
-- no tables of their own.
--
-- All timestamps are RFC3339 UTC text. Booleans are INTEGER 0/1. foreign_keys is
-- enabled per-connection via the DSN pragma (see platform/db).

-- +goose Up

-- +goose StatementBegin
CREATE TABLE site (
    id                 TEXT PRIMARY KEY,                          -- user-supplied slug
    name               TEXT NOT NULL,
    monitor_url        TEXT,                                      -- NULL = crash-only site
    monitor_enabled    INTEGER NOT NULL DEFAULT 1,                -- 0/1
    expected_status    INTEGER NOT NULL DEFAULT 200,
    crash_window_hours INTEGER NOT NULL DEFAULT 24,               -- drives orange
    ingest_key_hash    TEXT NOT NULL,                             -- SHA-256 hex of the ingest key
    cached_color       TEXT NOT NULL DEFAULT 'unknown',           -- red/orange/green/unknown
    last_checked_at    TEXT,                                      -- RFC3339
    last_ok            INTEGER,                                   -- last check result (0/1)
    fail_streak        INTEGER NOT NULL DEFAULT 0,                -- consecutive failed checks (red debounce)
    uptime_pct         REAL,                                      -- cached rolling uptime; NULL if disabled/unchecked
    created_at         TEXT NOT NULL
);
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TABLE check_result (
    id          INTEGER PRIMARY KEY AUTOINCREMENT,
    site_id     TEXT NOT NULL REFERENCES site(id) ON DELETE CASCADE,
    checked_at  TEXT NOT NULL,
    ok          INTEGER NOT NULL,
    status_code INTEGER,
    latency_ms  INTEGER,
    error       TEXT
);
-- +goose StatementEnd
-- +goose StatementBegin
CREATE INDEX idx_check_result_site_time ON check_result (site_id, checked_at DESC);
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TABLE check_rollup (
    site_id        TEXT NOT NULL REFERENCES site(id) ON DELETE CASCADE,
    day            TEXT NOT NULL,                                 -- UTC date YYYY-MM-DD
    ok_count       INTEGER NOT NULL,
    fail_count     INTEGER NOT NULL,
    latency_p50_ms INTEGER,
    latency_p95_ms INTEGER,
    PRIMARY KEY (site_id, day)
);
-- +goose StatementEnd
-- +goose StatementBegin
CREATE INDEX idx_check_rollup_site_day ON check_rollup (site_id, day DESC);
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TABLE crash_group (
    id          INTEGER PRIMARY KEY AUTOINCREMENT,
    site_id     TEXT NOT NULL REFERENCES site(id) ON DELETE CASCADE,
    fingerprint TEXT NOT NULL,
    title       TEXT NOT NULL,                                    -- derived from first event message
    level       TEXT NOT NULL,                                    -- highest level seen
    count       INTEGER NOT NULL DEFAULT 0,
    status      TEXT NOT NULL DEFAULT 'open',                     -- open/resolved/ignored
    first_seen  TEXT NOT NULL,
    last_seen   TEXT NOT NULL,
    UNIQUE (site_id, fingerprint)
);
-- +goose StatementEnd
-- +goose StatementBegin
CREATE INDEX idx_crash_group_site_lastseen ON crash_group (site_id, last_seen DESC);
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TABLE crash_event (
    id          INTEGER PRIMARY KEY AUTOINCREMENT,
    site_id     TEXT NOT NULL REFERENCES site(id) ON DELETE CASCADE,
    group_id    INTEGER NOT NULL REFERENCES crash_group(id) ON DELETE CASCADE,
    level       TEXT NOT NULL,
    message     TEXT NOT NULL,
    stack       TEXT,
    environment TEXT,
    release     TEXT,
    context     TEXT,                                             -- JSON blob
    occurred_at TEXT NOT NULL,                                    -- client-supplied or receipt time
    received_at TEXT NOT NULL                                     -- server receipt
);
-- +goose StatementEnd
-- +goose StatementBegin
CREATE INDEX idx_crash_event_site_time ON crash_event (site_id, occurred_at DESC);
-- +goose StatementEnd
-- +goose StatementBegin
CREATE INDEX idx_crash_event_group_time ON crash_event (group_id, occurred_at DESC);
-- +goose StatementEnd
-- +goose StatementBegin
CREATE INDEX idx_crash_event_received ON crash_event (received_at);
-- +goose StatementEnd

-- +goose Down

-- +goose StatementBegin
DROP TABLE IF EXISTS crash_event;
-- +goose StatementEnd
-- +goose StatementBegin
DROP TABLE IF EXISTS crash_group;
-- +goose StatementEnd
-- +goose StatementBegin
DROP TABLE IF EXISTS check_rollup;
-- +goose StatementEnd
-- +goose StatementBegin
DROP TABLE IF EXISTS check_result;
-- +goose StatementEnd
-- +goose StatementBegin
DROP TABLE IF EXISTS site;
-- +goose StatementEnd
