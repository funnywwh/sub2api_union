CREATE TABLE IF NOT EXISTS managed_node_api_keys (
    id BIGSERIAL PRIMARY KEY,
    name VARCHAR(100) NOT NULL,
    description TEXT NOT NULL DEFAULT '',
    key_hash VARCHAR(64) NOT NULL UNIQUE,
    key_prefix VARCHAR(24) NOT NULL,
    key_suffix VARCHAR(4) NOT NULL DEFAULT '',
    status VARCHAR(20) NOT NULL DEFAULT 'active',
    created_by BIGINT NULL REFERENCES users(id) ON DELETE SET NULL,
    revoked_by BIGINT NULL REFERENCES users(id) ON DELETE SET NULL,
    last_used_at TIMESTAMPTZ NULL,
    last_used_ip TEXT NOT NULL DEFAULT '',
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    revoked_at TIMESTAMPTZ NULL,
    CONSTRAINT chk_managed_node_api_keys_status CHECK (status IN ('active', 'revoked'))
);

CREATE INDEX IF NOT EXISTS idx_managed_node_api_keys_status_created_at
    ON managed_node_api_keys (status, created_at DESC, id DESC);

CREATE TABLE IF NOT EXISTS managed_node_api_key_audits (
    id BIGSERIAL PRIMARY KEY,
    managed_node_api_key_id BIGINT NOT NULL REFERENCES managed_node_api_keys(id) ON DELETE CASCADE,
    action VARCHAR(32) NOT NULL,
    operator_user_id BIGINT NULL REFERENCES users(id) ON DELETE SET NULL,
    operator_role VARCHAR(32) NOT NULL DEFAULT '',
    auth_method VARCHAR(32) NOT NULL DEFAULT '',
    detail JSONB NOT NULL DEFAULT '{}'::jsonb,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_managed_node_api_key_audits_key_created_at
    ON managed_node_api_key_audits (managed_node_api_key_id, created_at DESC, id DESC);
