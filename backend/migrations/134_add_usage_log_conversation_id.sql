ALTER TABLE usage_logs
    ADD COLUMN IF NOT EXISTS conversation_id VARCHAR(255);
