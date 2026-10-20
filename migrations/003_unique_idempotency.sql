-- 为 idempotency_key 添加唯一约束，保证幂等键不重复
-- NULL 值不参与唯一约束，允许多个 NULL 存在
CREATE UNIQUE INDEX IF NOT EXISTS uq_tasks_idempotency_key ON tasks(idempotency_key) WHERE idempotency_key IS NOT NULL;