-- Improve admin usage trace-search performance on large usage_logs tables.
DO $$
BEGIN
    BEGIN
        CREATE EXTENSION IF NOT EXISTS pg_trgm;
    EXCEPTION
        WHEN OTHERS THEN
            RAISE NOTICE 'pg_trgm extension not created: %', SQLERRM;
    END;

    IF EXISTS (SELECT 1 FROM pg_extension WHERE extname = 'pg_trgm') THEN
        EXECUTE 'CREATE INDEX IF NOT EXISTS idx_usage_logs_request_id_trgm
                 ON usage_logs USING gin (request_id gin_trgm_ops)';
        EXECUTE 'CREATE INDEX IF NOT EXISTS idx_usage_logs_conversation_id_trgm
                 ON usage_logs USING gin (conversation_id gin_trgm_ops)';
    ELSE
        RAISE NOTICE 'skip usage_logs trace trigram indexes because pg_trgm is unavailable';
    END IF;
END
$$;
