BEGIN;

CREATE EXTENSION IF NOT EXISTS pgcrypto;

CREATE SCHEMA IF NOT EXISTS account;
CREATE SCHEMA IF NOT EXISTS audit;

CREATE TABLE account.users (
    id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    status      TEXT NOT NULL DEFAULT 'active'
                CHECK (status IN ('active', 'suspended', 'deleting', 'deleted')),
    display_name TEXT NOT NULL DEFAULT '',
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    deleted_at  TIMESTAMPTZ
);

CREATE TABLE account.parties (
    id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id     UUID NOT NULL UNIQUE REFERENCES account.users(id) ON DELETE CASCADE,
    party_type  TEXT NOT NULL DEFAULT 'individual'
                CHECK (party_type IN ('individual')),
    locale      TEXT NOT NULL DEFAULT 'zh-CN',
    timezone    TEXT NOT NULL DEFAULT 'Asia/Shanghai',
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE account.identities (
    id               UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id          UUID NOT NULL REFERENCES account.users(id) ON DELETE CASCADE,
    provider         TEXT NOT NULL CHECK (provider IN ('apple', 'email', 'legacy_token')),
    provider_subject TEXT NOT NULL,
    email            TEXT,
    email_verified   BOOLEAN NOT NULL DEFAULT false,
    created_at       TIMESTAMPTZ NOT NULL DEFAULT now(),
    last_login_at    TIMESTAMPTZ,
    UNIQUE (provider, provider_subject)
);
CREATE INDEX identities_user_id_idx ON account.identities(user_id);

CREATE TABLE account.sessions (
    id                 UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id            UUID NOT NULL REFERENCES account.users(id) ON DELETE CASCADE,
    refresh_token_hash BYTEA NOT NULL UNIQUE,
    device_id          TEXT NOT NULL,
    device_name        TEXT NOT NULL DEFAULT '',
    platform           TEXT NOT NULL DEFAULT '',
    expires_at         TIMESTAMPTZ NOT NULL,
    last_seen_at       TIMESTAMPTZ NOT NULL DEFAULT now(),
    revoked_at         TIMESTAMPTZ,
    created_at         TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX sessions_user_id_idx ON account.sessions(user_id);
CREATE INDEX sessions_active_expiry_idx ON account.sessions(expires_at)
    WHERE revoked_at IS NULL;

CREATE TABLE account.products (
    code         TEXT PRIMARY KEY,
    display_name TEXT NOT NULL,
    status       TEXT NOT NULL DEFAULT 'active'
                 CHECK (status IN ('active', 'disabled')),
    created_at   TIMESTAMPTZ NOT NULL DEFAULT now()
);

INSERT INTO account.products(code, display_name)
VALUES ('menu', 'Menu Agent'), ('english', 'English Coach')
ON CONFLICT (code) DO NOTHING;

CREATE TABLE account.login_challenges (
    id           UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    product_code TEXT NOT NULL REFERENCES account.products(code),
    nonce_hash   BYTEA NOT NULL,
    expires_at   TIMESTAMPTZ NOT NULL,
    used_at      TIMESTAMPTZ,
    created_at   TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX login_challenges_expiry_idx ON account.login_challenges(expires_at)
    WHERE used_at IS NULL;

CREATE TABLE account.product_memberships (
    user_id      UUID NOT NULL REFERENCES account.users(id) ON DELETE CASCADE,
    product_code TEXT NOT NULL REFERENCES account.products(code),
    status       TEXT NOT NULL DEFAULT 'active'
                 CHECK (status IN ('active', 'suspended', 'closed')),
    opened_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
    closed_at    TIMESTAMPTZ,
    PRIMARY KEY (user_id, product_code)
);

CREATE TABLE account.households (
    id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    name        TEXT NOT NULL DEFAULT '',
    created_by  UUID REFERENCES account.users(id) ON DELETE SET NULL,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE account.household_members (
    household_id UUID NOT NULL REFERENCES account.households(id) ON DELETE CASCADE,
    user_id       UUID NOT NULL REFERENCES account.users(id) ON DELETE CASCADE,
    role          TEXT NOT NULL CHECK (role IN ('owner', 'parent', 'member')),
    joined_at     TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (household_id, user_id)
);
CREATE INDEX household_members_user_id_idx ON account.household_members(user_id);

CREATE TABLE account.consents (
    id             UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id        UUID NOT NULL REFERENCES account.users(id) ON DELETE CASCADE,
    product_code   TEXT NOT NULL REFERENCES account.products(code),
    purpose_code   TEXT NOT NULL,
    policy_version TEXT NOT NULL,
    granted_at     TIMESTAMPTZ NOT NULL DEFAULT now(),
    revoked_at     TIMESTAMPTZ,
    UNIQUE (user_id, product_code, purpose_code, policy_version)
);
CREATE INDEX consents_user_product_idx ON account.consents(user_id, product_code);

CREATE TABLE account.account_deletion_jobs (
    id            UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id       UUID NOT NULL,
    status        TEXT NOT NULL DEFAULT 'pending'
                  CHECK (status IN ('pending', 'running', 'completed', 'failed')),
    requested_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    started_at    TIMESTAMPTZ,
    completed_at  TIMESTAMPTZ,
    error_message TEXT NOT NULL DEFAULT ''
);
COMMENT ON COLUMN account.account_deletion_jobs.user_id IS
    'Intentional UUID snapshot without a foreign key, so deletion audit survives hard deletion of account.users.';
CREATE UNIQUE INDEX account_deletion_jobs_active_user_idx
    ON account.account_deletion_jobs(user_id)
    WHERE status IN ('pending', 'running');

CREATE TABLE audit.security_events (
    id            BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    actor_user_id UUID REFERENCES account.users(id) ON DELETE SET NULL,
    event_type    TEXT NOT NULL,
    target_type   TEXT NOT NULL DEFAULT '',
    target_id     TEXT NOT NULL DEFAULT '',
    request_id    TEXT NOT NULL DEFAULT '',
    ip_hash       BYTEA,
    user_agent    TEXT NOT NULL DEFAULT '',
    details       JSONB NOT NULL DEFAULT '{}'::jsonb,
    created_at    TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX security_events_actor_time_idx
    ON audit.security_events(actor_user_id, created_at DESC);
CREATE INDEX security_events_type_time_idx
    ON audit.security_events(event_type, created_at DESC);

COMMIT;
