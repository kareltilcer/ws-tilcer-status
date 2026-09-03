-- feedback module — the four tables of PRD §V3-5. Version block 20000, after the
-- platform sessions (02xxx) and the sites registry schema (10xxx); goose orders
-- migrations globally by the numeric filename prefix.
--
-- This is the first module that owns a migration block: `sites` carrying the
-- schema of a third functional module it does not otherwise know about stopped
-- being defensible at four tables (V3-D01). `feedback` adds NO column to `site`
-- (V3-D02) — its per-site configuration is its own table.
--
-- Every FK is REFERENCES site(id) ON DELETE CASCADE. ⚠ The cascade removes rows
-- and cannot remove objects from R2; FR-22 collects the object keys inside the
-- deleting transaction and issues the R2 deletes after it commits.
--
-- All timestamps are timeutil.Layout (fixed-width RFC3339 UTC) so string
-- comparison is a valid time order — the inbox cursor and the sweep's age
-- threshold both rely on it.

-- +goose Up

-- +goose StatementBegin
CREATE TABLE feedback_report (
    id             INTEGER PRIMARY KEY AUTOINCREMENT,
    ref            TEXT NOT NULL UNIQUE,                      -- "R-" + 4 Crockford base32 chars
    site_id        TEXT NOT NULL REFERENCES site(id) ON DELETE CASCADE,
    kind           TEXT NOT NULL DEFAULT 'bug',               -- bug/idea/other
    message        TEXT NOT NULL,                             -- <= 4000 chars, never rendered as HTML
    state          TEXT NOT NULL DEFAULT 'new',               -- new/open/resolved/declined
    reporter_label TEXT,                                      -- untrusted display string from the host app
    page_url       TEXT,
    referrer       TEXT,
    user_agent     TEXT,
    viewport       TEXT,
    locale         TEXT,
    app_release    TEXT,
    console_tail   TEXT,                                      -- JSON array; only when the site opted in
    last_error     TEXT,                                      -- same opt-in
    internal_note  TEXT,                                      -- admin-only; no route can show it to a reporter
    ip_hash        TEXT,                                      -- SHA-256 of client IP + IP_HASH_SALT; the IP is never stored
    created_at     TEXT NOT NULL,
    updated_at     TEXT NOT NULL,
    resolved_at    TEXT
);
-- +goose StatementEnd
-- +goose StatementBegin
CREATE INDEX idx_feedback_report_site_time ON feedback_report (site_id, created_at DESC);
-- +goose StatementEnd
-- +goose StatementBegin
CREATE INDEX idx_feedback_report_state_time ON feedback_report (state, created_at DESC);
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TABLE feedback_attachment (
    id           INTEGER PRIMARY KEY AUTOINCREMENT,
    report_id    INTEGER NOT NULL REFERENCES feedback_report(id) ON DELETE CASCADE,
    object_key   TEXT NOT NULL UNIQUE,                        -- feedback/{site_id}/{ref}/{n}-{rand}.{ext}
    content_type TEXT NOT NULL,                               -- from the signed allow-list
    byte_size    INTEGER NOT NULL,                            -- declared at init, overwritten with what HEAD reports at claim
    state        TEXT NOT NULL DEFAULT 'pending',             -- pending/stored/missing
    created_at   TEXT NOT NULL,
    claimed_at   TEXT
);
-- +goose StatementEnd
-- +goose StatementBegin
CREATE INDEX idx_feedback_attachment_report ON feedback_attachment (report_id);
-- +goose StatementEnd
-- +goose StatementBegin
CREATE INDEX idx_feedback_attachment_state_time ON feedback_attachment (state, created_at);
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TABLE feedback_site_config (
    site_id           TEXT PRIMARY KEY REFERENCES site(id) ON DELETE CASCADE,
    enabled           INTEGER NOT NULL DEFAULT 0,             -- the kill switch; off until deliberately turned on
    widget_key_hash   TEXT NOT NULL,                          -- SHA-256 hex, constant-time compared, plaintext shown once
    widget_key_set_at TEXT NOT NULL,
    console_capture   INTEGER NOT NULL DEFAULT 0,             -- opt-in (PRD §V3-8)
    created_at        TEXT NOT NULL,
    updated_at        TEXT NOT NULL
);
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TABLE feedback_ticket (
    id         TEXT PRIMARY KEY,                              -- opaque id; the token carries an HMAC over it
    site_id    TEXT NOT NULL REFERENCES site(id) ON DELETE CASCADE,
    issued_at  TEXT NOT NULL,
    expires_at TEXT NOT NULL
);
-- +goose StatementEnd
-- +goose StatementBegin
CREATE INDEX idx_feedback_ticket_expires ON feedback_ticket (expires_at);
-- +goose StatementEnd

-- +goose Down

-- +goose StatementBegin
DROP TABLE IF EXISTS feedback_ticket;
-- +goose StatementEnd
-- +goose StatementBegin
DROP TABLE IF EXISTS feedback_site_config;
-- +goose StatementEnd
-- +goose StatementBegin
DROP TABLE IF EXISTS feedback_attachment;
-- +goose StatementEnd
-- +goose StatementBegin
DROP TABLE IF EXISTS feedback_report;
-- +goose StatementEnd
