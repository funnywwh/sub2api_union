-- Add HappyHorse async video task mapping table.
CREATE TABLE IF NOT EXISTS happyhorse_tasks (
    id                   BIGSERIAL PRIMARY KEY,
    user_id              BIGINT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    api_key_id            BIGINT NOT NULL REFERENCES api_keys(id) ON DELETE CASCADE,
    account_id            BIGINT NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
    group_id              BIGINT REFERENCES groups(id) ON DELETE SET NULL,
    task_id               VARCHAR(128) NOT NULL UNIQUE,
    request_id            VARCHAR(128) NOT NULL DEFAULT '',
    model                 VARCHAR(100) NOT NULL,
    prompt                TEXT NOT NULL DEFAULT '',
    status                VARCHAR(32) NOT NULL DEFAULT 'submitted',
    result_urls           JSONB NOT NULL DEFAULT '[]'::jsonb,
    error_message         TEXT NOT NULL DEFAULT '',
    upstream_response     JSONB NOT NULL DEFAULT '{}'::jsonb,
    request_payload_hash       VARCHAR(128) NOT NULL DEFAULT '',
    usage_recorded             BOOLEAN NOT NULL DEFAULT FALSE,
    usage_recording_started_at TIMESTAMPTZ,
    usage_recorded_at          TIMESTAMPTZ,
    created_at                 TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at                 TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    completed_at               TIMESTAMPTZ
);

CREATE INDEX IF NOT EXISTS idx_happyhorse_tasks_user_created
    ON happyhorse_tasks(user_id, created_at DESC);

CREATE INDEX IF NOT EXISTS idx_happyhorse_tasks_api_key_created
    ON happyhorse_tasks(api_key_id, created_at DESC);

CREATE INDEX IF NOT EXISTS idx_happyhorse_tasks_account_status
    ON happyhorse_tasks(account_id, status);
