-- platform core — Mode B session store. Version block 02000. status owns its own
-- session: the browser carries NO bearer token; each request is authorized from
-- this session, and roles are refreshed periodically via auth /internal/token/mint.
--
-- Only the SHA-256 hash of the session cookie token is stored — never the token
-- itself. `roles` is a JSON array cached from auth; `roles_refreshed_at` drives
-- the re-mint threshold. A revoked or expired row denies access.

-- +goose Up

-- +goose StatementBegin
CREATE TABLE sessions (
    id                 TEXT PRIMARY KEY,
    user_id            TEXT NOT NULL,                              -- auth user id (sub)
    token_hash         TEXT NOT NULL UNIQUE,                       -- SHA-256 of the cookie token
    email              TEXT NOT NULL,
    display_name       TEXT,
    roles              TEXT NOT NULL DEFAULT '[]',                 -- JSON array, cached from auth
    roles_refreshed_at TEXT NOT NULL,                             -- RFC3339 UTC
    user_agent         TEXT,
    ip                 TEXT,
    created_at         TEXT NOT NULL,
    last_seen_at       TEXT NOT NULL,
    expires_at         TEXT NOT NULL,                             -- sliding window
    revoked_at         TEXT                                       -- non-NULL once revoked
);
-- +goose StatementEnd
-- +goose StatementBegin
CREATE INDEX idx_sessions_user ON sessions (user_id);
-- +goose StatementEnd

-- +goose Down

-- +goose StatementBegin
DROP TABLE IF EXISTS sessions;
-- +goose StatementEnd
