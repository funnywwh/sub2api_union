ALTER TABLE usage_logs
    ADD COLUMN IF NOT EXISTS audio_duration_ms INTEGER,
    ADD COLUMN IF NOT EXISTS hourly_price DECIMAL(20,10);
