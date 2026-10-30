-- Runtime-editable settings live in a single row so every replica reads the
-- same snapshot; version is the optimistic-lock token and the poll key.
-- 运行时可编辑配置存放为单行，保证多副本读取同一份快照；
-- version 既做乐观锁，也做轮询依据。
CREATE TABLE IF NOT EXISTS system_settings (
    id SMALLINT PRIMARY KEY CHECK (id = 1),
    payload JSONB NOT NULL,
    version BIGINT NOT NULL DEFAULT 1 CHECK (version > 0),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_by TEXT NOT NULL DEFAULT ''
);
