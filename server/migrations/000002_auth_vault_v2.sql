-- Password-wrapped vault envelopes, email registration, and external identity.

ALTER TABLE users
    ADD COLUMN email TEXT,
    ADD COLUMN email_verified_at TIMESTAMPTZ,
    ADD COLUMN auth_provider TEXT NOT NULL DEFAULT 'local',
    ADD COLUMN external_subject TEXT;

ALTER TABLE users
    ADD CONSTRAINT users_auth_provider_check CHECK (auth_provider IN ('local', 'external'));

CREATE UNIQUE INDEX users_external_subject_idx
    ON users (auth_provider, external_subject)
    WHERE external_subject IS NOT NULL;

CREATE UNIQUE INDEX users_local_email_idx
    ON users (lower(email))
    WHERE email IS NOT NULL AND auth_provider = 'local';

CREATE UNIQUE INDEX users_external_email_idx
    ON users (lower(email))
    WHERE email IS NOT NULL AND auth_provider = 'external';

ALTER TABLE vaults
    ADD COLUMN password_kdf TEXT,
    ADD COLUMN password_kdf_time INTEGER,
    ADD COLUMN password_kdf_memory INTEGER,
    ADD COLUMN password_kdf_parallelism INTEGER,
    ADD COLUMN password_kdf_salt BYTEA,
    ADD COLUMN password_wrap_salt BYTEA,
    ADD COLUMN password_wrap_nonce BYTEA,
    ADD COLUMN password_wrapped_key BYTEA;

ALTER TABLE vaults
    ADD CONSTRAINT vaults_password_wrap_all_or_nothing CHECK (
        (
            password_wrapped_key IS NULL
            AND password_kdf IS NULL
            AND password_kdf_time IS NULL
            AND password_kdf_memory IS NULL
            AND password_kdf_parallelism IS NULL
            AND password_kdf_salt IS NULL
            AND password_wrap_salt IS NULL
            AND password_wrap_nonce IS NULL
        )
        OR (
            password_kdf = 'argon2id'
            AND password_kdf_time BETWEEN 1 AND 10
            AND password_kdf_memory BETWEEN 8192 AND 262144
            AND password_kdf_parallelism BETWEEN 1 AND 4
            AND octet_length(password_kdf_salt) = 16
            AND octet_length(password_wrap_salt) = 32
            AND octet_length(password_wrap_nonce) = 12
            AND octet_length(password_wrapped_key) = 48
        )
    );

CREATE TABLE email_codes (
    email TEXT PRIMARY KEY,
    purpose TEXT NOT NULL CHECK (purpose IN ('register')),
    code_hash BYTEA NOT NULL CHECK (octet_length(code_hash) = 32),
    attempts INTEGER NOT NULL DEFAULT 0 CHECK (attempts >= 0 AND attempts <= 10),
    sent_at TIMESTAMPTZ NOT NULL,
    expires_at TIMESTAMPTZ NOT NULL
);
CREATE INDEX email_codes_expiry_idx ON email_codes (expires_at);
