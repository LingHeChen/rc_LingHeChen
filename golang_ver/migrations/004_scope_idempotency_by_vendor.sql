-- Upgrade existing installations from globally unique idempotency keys to
-- vendor-scoped keys. Existing rows predate vendor snapshots and retain an
-- empty vendor name; new jobs always populate vendor_name.
ALTER TABLE notification_jobs
    ADD COLUMN IF NOT EXISTS vendor_name VARCHAR(100) NOT NULL DEFAULT '';

ALTER TABLE notification_jobs
    DROP CONSTRAINT IF EXISTS notification_jobs_idempotency_key_key;

CREATE UNIQUE INDEX IF NOT EXISTS idx_jobs_vendor_idempotency
    ON notification_jobs (vendor_name, idempotency_key);
