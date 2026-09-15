-- Design scaffold only. The current server does not apply this migration.
-- Apply once on an empty PostgreSQL 17 database using a future migration runner.
BEGIN;

CREATE TABLE users (
    id UUID PRIMARY KEY,
    username TEXT NOT NULL UNIQUE CHECK (username ~ '^[a-z0-9_]{3,32}$'),
    password_hash TEXT NOT NULL,
    status TEXT NOT NULL DEFAULT 'active' CHECK (status IN ('active', 'disabled')),
    created_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp()
);

CREATE TABLE devices (
    user_id UUID NOT NULL REFERENCES users(id),
    id UUID NOT NULL,
    name TEXT NOT NULL CHECK (char_length(name) BETWEEN 1 AND 64),
    platform TEXT NOT NULL CHECK (platform IN ('windows', 'android')),
    created_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp(),
    last_seen_at TIMESTAMPTZ,
    revoked_at TIMESTAMPTZ,
    PRIMARY KEY (user_id, id)
);

CREATE TABLE sessions (
    id UUID PRIMARY KEY,
    user_id UUID NOT NULL,
    device_id UUID NOT NULL,
    family_id UUID NOT NULL,
    access_hash BYTEA NOT NULL UNIQUE CHECK (octet_length(access_hash) = 32),
    refresh_hash BYTEA NOT NULL UNIQUE CHECK (octet_length(refresh_hash) = 32),
    expires_at TIMESTAMPTZ NOT NULL,
    refresh_expires_at TIMESTAMPTZ NOT NULL,
    revoked_at TIMESTAMPTZ,
    FOREIGN KEY (user_id, device_id) REFERENCES devices(user_id, id)
);
CREATE INDEX sessions_device_idx ON sessions(user_id, device_id);
CREATE INDEX sessions_family_idx ON sessions(family_id);

CREATE TABLE used_refresh_tokens (
    token_hash BYTEA PRIMARY KEY CHECK (octet_length(token_hash) = 32),
    family_id UUID NOT NULL,
    expires_at TIMESTAMPTZ NOT NULL
);

CREATE TABLE registration_invites (
    token_hash BYTEA PRIMARY KEY CHECK (octet_length(token_hash) = 32),
    expires_at TIMESTAMPTZ NOT NULL,
    consumed_at TIMESTAMPTZ
);

CREATE TABLE vaults (
    user_id UUID PRIMARY KEY REFERENCES users(id),
    id UUID NOT NULL,
    format_version SMALLINT NOT NULL CHECK (format_version = 1),
    key_epoch INTEGER NOT NULL CHECK (key_epoch = 1),
    wrap_salt BYTEA NOT NULL CHECK (octet_length(wrap_salt) = 32),
    wrap_nonce BYTEA NOT NULL CHECK (octet_length(wrap_nonce) = 12),
    wrapped_key BYTEA NOT NULL CHECK (octet_length(wrapped_key) = 48),
    created_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp(),
    UNIQUE (user_id, id, key_epoch)
);

CREATE TABLE user_sync_state (
    user_id UUID PRIMARY KEY REFERENCES users(id),
    next_seq BIGINT NOT NULL DEFAULT 0 CHECK (next_seq >= 0),
    min_available_seq BIGINT NOT NULL DEFAULT 1 CHECK (min_available_seq >= 1),
    ciphertext_bytes BIGINT NOT NULL DEFAULT 0 CHECK (ciphertext_bytes >= 0),
    item_count INTEGER NOT NULL DEFAULT 0 CHECK (item_count >= 0),
    CHECK (min_available_seq <= next_seq + 1)
);

-- This registry survives ciphertext deletion to prevent old outbox resurrection.
CREATE TABLE clip_ids (
    user_id UUID NOT NULL REFERENCES users(id),
    id UUID NOT NULL,
    request_hash BYTEA NOT NULL CHECK (octet_length(request_hash) = 32),
    created_seq BIGINT NOT NULL CHECK (created_seq > 0),
    created_at TIMESTAMPTZ NOT NULL,
    final_version INTEGER CHECK (final_version > 0),
    final_seq BIGINT CHECK (final_seq > 0),
    purged_at TIMESTAMPTZ,
    PRIMARY KEY (user_id, id),
    CHECK ((purged_at IS NULL AND final_version IS NULL AND final_seq IS NULL)
        OR (purged_at IS NOT NULL AND final_version IS NOT NULL AND final_seq IS NOT NULL))
);

CREATE TABLE clips (
    user_id UUID NOT NULL,
    id UUID NOT NULL,
    source_device_id UUID NOT NULL,
    vault_id UUID NOT NULL,
    key_epoch INTEGER NOT NULL CHECK (key_epoch = 1),
    format_version SMALLINT NOT NULL CHECK (format_version = 1),
    content_type TEXT NOT NULL CHECK (content_type = 'text/plain'),
    nonce BYTEA NOT NULL CHECK (octet_length(nonce) = 12),
    ciphertext BYTEA NOT NULL CHECK (octet_length(ciphertext) BETWEEN 17 AND 65552),
    delivery_intent TEXT NOT NULL CHECK (delivery_intent IN ('live', 'history_only')),
    status TEXT NOT NULL CHECK (status IN ('active', 'trash')),
    version INTEGER NOT NULL DEFAULT 1 CHECK (version > 0),
    created_seq BIGINT NOT NULL CHECK (created_seq > 0),
    last_seq BIGINT NOT NULL CHECK (last_seq >= created_seq),
    created_at TIMESTAMPTZ NOT NULL,
    deleted_at TIMESTAMPTZ,
    expires_at TIMESTAMPTZ,
    PRIMARY KEY (user_id, id),
    FOREIGN KEY (user_id, id) REFERENCES clip_ids(user_id, id),
    FOREIGN KEY (user_id, source_device_id) REFERENCES devices(user_id, id),
    FOREIGN KEY (user_id, vault_id, key_epoch) REFERENCES vaults(user_id, id, key_epoch),
    CHECK ((status = 'active' AND deleted_at IS NULL AND expires_at IS NULL)
        OR (status = 'trash' AND deleted_at IS NOT NULL AND expires_at IS NOT NULL
            AND expires_at = deleted_at + INTERVAL '168 hours'))
);
CREATE INDEX clips_history_idx ON clips(user_id, status, created_seq DESC, id);
CREATE INDEX clips_expiry_idx ON clips(expires_at, user_id) WHERE status = 'trash';

CREATE TABLE sync_events (
    user_id UUID NOT NULL REFERENCES users(id),
    seq BIGINT NOT NULL CHECK (seq > 0),
    clip_id UUID NOT NULL,
    kind TEXT NOT NULL CHECK (kind IN ('clip.created', 'clip.trashed', 'clip.restored', 'clip.purged')),
    version INTEGER NOT NULL CHECK (version > 0),
    occurred_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp(),
    PRIMARY KEY (user_id, seq),
    FOREIGN KEY (user_id, clip_id) REFERENCES clip_ids(user_id, id)
);
CREATE INDEX sync_events_retention_idx ON sync_events(occurred_at);

CREATE TABLE operation_receipts (
    user_id UUID NOT NULL REFERENCES users(id),
    operation_id UUID NOT NULL,
    request_hash BYTEA NOT NULL CHECK (octet_length(request_hash) = 32),
    response_status INTEGER NOT NULL CHECK (response_status BETWEEN 200 AND 599),
    response_body JSONB NOT NULL,
    expires_at TIMESTAMPTZ NOT NULL,
    PRIMARY KEY (user_id, operation_id)
);
CREATE INDEX operation_receipts_expiry_idx ON operation_receipts(expires_at);

CREATE TABLE snapshot_sessions (
    token_hash BYTEA PRIMARY KEY CHECK (octet_length(token_hash) = 32),
    user_id UUID NOT NULL,
    device_id UUID NOT NULL,
    base_seq BIGINT NOT NULL CHECK (base_seq >= 0),
    sync_epoch UUID NOT NULL,
    expires_at TIMESTAMPTZ NOT NULL,
    FOREIGN KEY (user_id, device_id) REFERENCES devices(user_id, id)
);

CREATE TABLE server_state (
    singleton BOOLEAN PRIMARY KEY DEFAULT TRUE CHECK (singleton),
    server_id UUID NOT NULL,
    sync_epoch UUID NOT NULL,
    installed_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp()
);
-- Installation code must insert cryptographically random server_id/sync_epoch.
-- Do not seed production identities, passwords or recovery keys in migrations.
COMMIT;
