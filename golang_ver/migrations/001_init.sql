CREATE TABLE IF NOT EXISTS notification_jobs (
    id              UUID         PRIMARY KEY,
    idempotency_key VARCHAR(255) UNIQUE NOT NULL,
    target_url      TEXT         NOT NULL,
    method          VARCHAR(10)  NOT NULL DEFAULT 'POST',
    headers         JSONB        NOT NULL DEFAULT '{}',
    body            BYTEA,
    status          VARCHAR(20)  NOT NULL DEFAULT 'pending',
    attempts        INT          NOT NULL DEFAULT 0,
    max_attempts    INT          NOT NULL DEFAULT 10,
    next_retry_at   TIMESTAMPTZ  NOT NULL DEFAULT NOW(),
    last_error      TEXT,
    created_at      TIMESTAMPTZ  NOT NULL DEFAULT NOW(),
    updated_at      TIMESTAMPTZ  NOT NULL DEFAULT NOW()
);

-- Only index pending jobs to keep it lean
CREATE INDEX IF NOT EXISTS idx_jobs_pending_retry
    ON notification_jobs (next_retry_at)
    WHERE status = 'pending';
