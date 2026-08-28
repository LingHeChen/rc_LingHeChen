CREATE TABLE IF NOT EXISTS vendor_configs (
    id          UUID         PRIMARY KEY DEFAULT gen_random_uuid(),
    name        VARCHAR(100) UNIQUE NOT NULL,
    target_url  TEXT         NOT NULL,
    method      VARCHAR(10)  NOT NULL DEFAULT 'POST',
    headers     JSONB        NOT NULL DEFAULT '{}',
    created_at  TIMESTAMPTZ  NOT NULL DEFAULT NOW(),
    updated_at  TIMESTAMPTZ  NOT NULL DEFAULT NOW()
);
