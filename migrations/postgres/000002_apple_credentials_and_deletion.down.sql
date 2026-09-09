BEGIN;

ALTER TABLE account.login_challenges
    DROP CONSTRAINT IF EXISTS login_challenges_nonce_hash_length;
ALTER TABLE account.sessions
    DROP CONSTRAINT IF EXISTS sessions_refresh_token_hash_length;

DROP INDEX IF EXISTS account.account_deletion_jobs_ready_idx;

ALTER TABLE account.account_deletion_jobs
    DROP COLUMN IF EXISTS locked_until,
    DROP COLUMN IF EXISTS next_attempt_at,
    DROP COLUMN IF EXISTS attempt_count;

DROP TABLE IF EXISTS account.apple_credentials;

COMMIT;
