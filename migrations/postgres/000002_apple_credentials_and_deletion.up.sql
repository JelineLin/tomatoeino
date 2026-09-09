BEGIN;

CREATE TABLE account.apple_credentials (
    id                       UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    identity_id              UUID NOT NULL REFERENCES account.identities(id) ON DELETE CASCADE,
    client_id                TEXT NOT NULL,
    refresh_token_hash       BYTEA NOT NULL,
    refresh_token_ciphertext BYTEA NOT NULL,
    refresh_token_nonce      BYTEA NOT NULL,
    created_at               TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at               TIMESTAMPTZ NOT NULL DEFAULT now(),
    CHECK (octet_length(refresh_token_hash) = 32),
    CHECK (octet_length(refresh_token_ciphertext) > 16),
    CHECK (octet_length(refresh_token_nonce) = 12),
    UNIQUE (identity_id, client_id, refresh_token_hash)
);
CREATE INDEX apple_credentials_identity_idx ON account.apple_credentials(identity_id);

ALTER TABLE account.sessions
    ADD CONSTRAINT sessions_refresh_token_hash_length CHECK (octet_length(refresh_token_hash) = 32);
ALTER TABLE account.login_challenges
    ADD CONSTRAINT login_challenges_nonce_hash_length CHECK (octet_length(nonce_hash) = 32);

ALTER TABLE account.account_deletion_jobs
    ADD COLUMN attempt_count INTEGER NOT NULL DEFAULT 0 CHECK (attempt_count >= 0),
    ADD COLUMN next_attempt_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    ADD COLUMN locked_until TIMESTAMPTZ;

CREATE INDEX account_deletion_jobs_ready_idx
    ON account.account_deletion_jobs(next_attempt_at, requested_at)
    WHERE status IN ('pending', 'failed', 'running');

COMMIT;
