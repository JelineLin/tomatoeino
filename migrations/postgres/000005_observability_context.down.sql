BEGIN;

DROP INDEX IF EXISTS audit.security_events_request_id_idx;

ALTER TABLE account.account_deletion_jobs
    DROP COLUMN IF EXISTS request_id;

COMMIT;
