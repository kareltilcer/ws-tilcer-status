-- notify module — email notifications. Version block 30000, after the platform
-- sessions (02xxx), the sites registry schema (10xxx) and feedback (20xxx); goose
-- orders migrations globally by the numeric filename prefix.
--
-- The shape is an OUTBOX: a producer (crash ingest, feedback submit, the poller)
-- writes a notify_event row inside the transaction that caused it, and a worker
-- turns pending rows into one notify_digest — rendered once, stored, and sent
-- after the commit. Nothing here is ever written while a mail provider is being
-- talked to.
--
-- Every FK to site is ON DELETE CASCADE: a deleted site takes its unsent events,
-- its mute and its downtime memory with it.
--
-- All timestamps are timeutil.Layout (fixed-width RFC3339 UTC) so string
-- comparison is a valid time order — the digest window, the hourly cap and the
-- retry schedule all compare strings.

-- +goose Up

-- +goose StatementBegin
CREATE TABLE notify_settings (
    id          INTEGER PRIMARY KEY CHECK (id = 1),           -- one row, seeded below
    enabled     INTEGER NOT NULL DEFAULT 0,                   -- master switch; off until Karel turns it on
    recipients  TEXT NOT NULL DEFAULT '[]',                   -- JSON array of plain addresses, <= 5
    on_crash    INTEGER NOT NULL DEFAULT 1,                   -- new crash groups and regressions
    on_feedback INTEGER NOT NULL DEFAULT 1,                   -- new reports
    on_downtime INTEGER NOT NULL DEFAULT 1,                   -- down and back up
    updated_at  TEXT
);
-- +goose StatementEnd
-- +goose StatementBegin
INSERT INTO notify_settings (id) VALUES (1);
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TABLE notify_site_mute (
    site_id    TEXT PRIMARY KEY REFERENCES site(id) ON DELETE CASCADE,
    created_at TEXT NOT NULL
);
-- +goose StatementEnd

-- notify's own memory of an outage. ⚠ Not site.cached_color: that is reset to
-- 'unknown' when a site's URL changes and left 'red' when monitoring is switched
-- off, and a "back up" email read off it would announce recoveries that did not
-- happen. A row exists only while the site is down.
-- +goose StatementBegin
CREATE TABLE notify_site_state (
    site_id    TEXT PRIMARY KEY REFERENCES site(id) ON DELETE CASCADE,
    down_since TEXT NOT NULL,
    announced  INTEGER NOT NULL                                -- a "down" email was queued; only then is "back up" news
);
-- +goose StatementEnd

-- Whether a crash group is due an email. A MISSING row means armed and never
-- announced: the group's first qualifying event (error or fatal, in production)
-- announces it as new; a regression reopen re-arms it, and its next qualifying
-- event announces it as back.
-- +goose StatementBegin
CREATE TABLE notify_crash_state (
    group_id  INTEGER PRIMARY KEY REFERENCES crash_group(id) ON DELETE CASCADE,
    armed     INTEGER NOT NULL,
    announced INTEGER NOT NULL
);
-- +goose StatementEnd

-- One email. Everything the provider receives is stored here and never
-- re-rendered: a retry must send the identical request, because the provider's
-- idempotency key refuses the same key with a different body.
-- +goose StatementBegin
CREATE TABLE notify_digest (
    id                  INTEGER PRIMARY KEY AUTOINCREMENT,
    created_at          TEXT NOT NULL,
    sender              TEXT NOT NULL,
    recipients          TEXT NOT NULL,                        -- JSON array, frozen at render time
    subject             TEXT NOT NULL,
    body_text           TEXT NOT NULL,
    body_html           TEXT NOT NULL,
    event_count         INTEGER NOT NULL,
    idempotency_key     TEXT NOT NULL UNIQUE,                 -- "status-digest-" + random UUID
    state               TEXT NOT NULL DEFAULT 'pending',      -- pending/sent/failed
    attempts            INTEGER NOT NULL DEFAULT 0,
    next_attempt_at     TEXT NOT NULL,
    last_attempt_at     TEXT,
    last_error          TEXT,
    sent_at             TEXT,
    provider_message_id TEXT
);
-- +goose StatementEnd
-- +goose StatementBegin
CREATE INDEX idx_notify_digest_due ON notify_digest (state, next_attempt_at);
-- +goose StatementEnd
-- +goose StatementBegin
CREATE INDEX idx_notify_digest_created ON notify_digest (created_at);
-- +goose StatementEnd

-- The outbox. ⚠ No UNIQUE constraint: this table is written inside feedback's
-- insert transaction, whose retry loop reads any UNIQUE failure as a ref
-- collision.
-- +goose StatementBegin
CREATE TABLE notify_event (
    id         INTEGER PRIMARY KEY AUTOINCREMENT,
    site_id    TEXT NOT NULL REFERENCES site(id) ON DELETE CASCADE,
    kind       TEXT NOT NULL,                                 -- crash_new/crash_regression/feedback/site_down/site_recovered
    ref        TEXT NOT NULL,                                 -- crash group id, report ref, or '' for downtime
    payload    TEXT NOT NULL,                                 -- JSON snapshot; excerpts only, never a stack or a reporter's context
    created_at TEXT NOT NULL,
    digest_id  INTEGER REFERENCES notify_digest(id) ON DELETE CASCADE
);
-- +goose StatementEnd
-- +goose StatementBegin
CREATE INDEX idx_notify_event_unassigned ON notify_event (created_at) WHERE digest_id IS NULL;
-- +goose StatementEnd
-- +goose StatementBegin
CREATE INDEX idx_notify_event_digest ON notify_event (digest_id);
-- +goose StatementEnd

-- Upgrade seed. A group that has ALREADY had a qualifying event is old news:
-- record it as announced and disarmed, so switching notifications on does not
-- mail the history of every open group. A group only ever seen outside
-- production keeps no row — it has never been news, and stays armed.
-- +goose StatementBegin
INSERT INTO notify_crash_state (group_id, armed, announced)
SELECT g.id, 0, 1
  FROM crash_group g
 WHERE EXISTS (
       SELECT 1 FROM crash_event e
        WHERE e.group_id = g.id
          AND e.level IN ('error', 'fatal')
          AND (e.environment IS NULL
               OR trim(e.environment) = ''
               OR lower(trim(e.environment)) IN ('prod', 'production')));
-- +goose StatementEnd

-- +goose Down

-- +goose StatementBegin
DROP TABLE IF EXISTS notify_event;
-- +goose StatementEnd
-- +goose StatementBegin
DROP TABLE IF EXISTS notify_digest;
-- +goose StatementEnd
-- +goose StatementBegin
DROP TABLE IF EXISTS notify_crash_state;
-- +goose StatementEnd
-- +goose StatementBegin
DROP TABLE IF EXISTS notify_site_state;
-- +goose StatementEnd
-- +goose StatementBegin
DROP TABLE IF EXISTS notify_site_mute;
-- +goose StatementEnd
-- +goose StatementBegin
DROP TABLE IF EXISTS notify_settings;
-- +goose StatementEnd
