-- outbox_events: 事务性发件箱，与业务状态变更在同一事务内写入
-- 后台 dispatcher 异步扫描并发送到 Redis，发送成功后标记 published=true
CREATE TABLE IF NOT EXISTS outbox_events (
    id           BIGSERIAL PRIMARY KEY,
    event_type   TEXT        NOT NULL,
    task_id      TEXT        NOT NULL,
    payload      JSONB       NOT NULL DEFAULT '{}',
    published    BOOLEAN     NOT NULL DEFAULT FALSE,
    published_at TIMESTAMPTZ,
    created_at   TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

-- 索引：加速查询未发布的事件
CREATE INDEX IF NOT EXISTS idx_outbox_events_published ON outbox_events (published, created_at);