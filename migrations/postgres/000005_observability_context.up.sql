BEGIN;

ALTER TABLE account.account_deletion_jobs
    ADD COLUMN request_id TEXT NOT NULL DEFAULT ''
        CHECK (char_length(request_id) <= 128);

CREATE INDEX security_events_request_id_idx
    ON audit.security_events(request_id, created_at DESC)
    WHERE request_id <> '';

COMMIT;
