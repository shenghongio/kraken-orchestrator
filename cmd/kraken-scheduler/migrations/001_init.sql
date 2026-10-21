-- 任务表
CREATE TABLE IF NOT EXISTS tasks (
    id              TEXT PRIMARY KEY,
    type            TEXT NOT NULL,
    priority        INTEGER NOT NULL DEFAULT 5,
    status          TEXT NOT NULL DEFAULT 'pending',
    payload         BYTEA NOT NULL DEFAULT '{}'::bytea,
    idempotency_key TEXT,

    max_retries     INTEGER NOT NULL DEFAULT 0,
    attempt         INTEGER NOT NULL DEFAULT 0,

    current_agent_id TEXT,
    lease_epoch     BIGINT NOT NULL DEFAULT 0,

    cancel_requested BOOLEAN NOT NULL DEFAULT FALSE,

    result          BYTEA,
    error           TEXT,

    next_run_at     TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    version         INTEGER NOT NULL DEFAULT 1
);

CREATE INDEX IF NOT EXISTS idx_tasks_status_next_run ON tasks(status, next_run_at);
CREATE INDEX IF NOT EXISTS idx_tasks_idempotency_key ON tasks(idempotency_key);
CREATE INDEX IF NOT EXISTS idx_tasks_current_agent ON tasks(current_agent_id) WHERE current_agent_id IS NOT NULL;

-- 任务尝试表
CREATE TABLE IF NOT EXISTS task_attempts (
    id            BIGSERIAL PRIMARY KEY,
    task_id       TEXT NOT NULL REFERENCES tasks(id) ON DELETE CASCADE,
    agent_id      TEXT NOT NULL,
    attempt       INTEGER NOT NULL,
    lease_epoch   BIGINT NOT NULL,
    status        TEXT NOT NULL,
    progress      BYTEA,
    result        BYTEA,
    error         TEXT,
    started_at    TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    ended_at      TIMESTAMPTZ
);

CREATE INDEX  IF NOT EXISTS idx_task_attempts_task ON task_attempts(task_id);
CREATE UNIQUE INDEX  IF NOT EXISTS idx_task_attempts_task_attempt ON task_attempts(task_id, attempt);

-- 任务租约表
CREATE TABLE IF NOT EXISTS task_leases (
    task_id       TEXT PRIMARY KEY REFERENCES tasks(id) ON DELETE CASCADE,
    agent_id      TEXT NOT NULL,
    attempt       INTEGER NOT NULL,
    lease_epoch   BIGINT NOT NULL,
    expires_at    TIMESTAMPTZ NOT NULL,
    renewed_at    TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_task_leases_expires ON task_leases(expires_at);

-- Agent 表
CREATE TABLE IF NOT EXISTS agents (
    id              TEXT PRIMARY KEY,
    status          TEXT NOT NULL DEFAULT 'offline',
    version         TEXT,
    capabilities    TEXT[] NOT NULL DEFAULT '{}',
    max_concurrency INTEGER NOT NULL DEFAULT 1,
    busy_slots      INTEGER NOT NULL DEFAULT 0,
    last_heartbeat  TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

-- 任务事件表（审计/日志）
CREATE TABLE IF NOT EXISTS task_events (
    id         BIGSERIAL PRIMARY KEY,
    task_id    TEXT NOT NULL REFERENCES tasks(id) ON DELETE CASCADE,
    event      TEXT NOT NULL,
    agent_id   TEXT,
    attempt    INTEGER,
    metadata   JSONB,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_task_events_task ON task_events(task_id);

-- 事务出箱表
CREATE TABLE IF NOT EXISTS outbox_events (
    id          BIGSERIAL PRIMARY KEY,
    event_type  TEXT NOT NULL,
    task_id     TEXT,
    payload     JSONB NOT NULL,
    published   BOOLEAN NOT NULL DEFAULT FALSE,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    published_at TIMESTAMPTZ
);

CREATE INDEX IF NOT EXISTS idx_outbox_pending ON outbox_events(published, created_at) WHERE published = FALSE;