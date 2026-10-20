# Phase 2 测试验证与 Bug 修复记录

## 测试覆盖范围

| 测试文件 | 测试用例 | 验证场景 |
|----------|----------|----------|
| `task_claim_reaper_test.go` | TestClaimTask_Concurrent | 并发申领不重复分配 |
| `task_claim_reaper_test.go` | TestProcessCallback_StaleLeaseEpoch | fencing token 校验 |
| `task_claim_reaper_test.go` | TestCreate_Idempotent | 幂等创建 |
| `reaper_test.go` | TestReaper_ExpiredLEase | 过期租约回收（不可重试→failed） |
| `reaper_test.go` | TestReaper_ExpiredLeaseRetry | 过期租约回收（可重试→pending） |

## 运行方式

跨包集成测试共用同一数据库，并行执行会因 `TRUNCATE` 冲突导致数据污染，必须串行执行：

```bash
KRAKEN_TEST_DSN="postgres://user:pass@host:5432/kraken?sslmode=disable" \
  go test -p 1 ./internal/persistence/postgres/... ./internal/lease/... -v
```

## 发现并修复的 Bug

### Bug 1：task_attempts INSERT 语法错误

**文件**：`internal/persistence/postgres/task_repo.go:154`

**现象**：
```
ERROR: syntax error at or near "$5" (SQLSTATE 42601)
```

**根因**：`$5` 写在了 `VALUES` 括号外面。

**错误代码**：
```go
_, err = tx.Exec(ctx, `INSERT INTO task_attempts (task_id, agent_id, attempt, lease_epoch, status, started_at) VALUES
    ($1, $2, $3, $4, 'running'), $5`,
    taskID, agentID, attempt, leaseEpoch, now)
```

**修复**：把 `$5` 移到 `VALUES` 括号内：
```go
_, err = tx.Exec(ctx, `INSERT INTO task_attempts (task_id, agent_id, attempt, lease_epoch, status, started_at) VALUES
    ($1, $2, $3, $4, 'running', $5)`,
    taskID, agentID, attempt, leaseEpoch, now)
```

---

### Bug 2：ProcessCallback 查询 attempt 用错参数

**文件**：`internal/persistence/postgres/callback_repo.go:54`

**现象**：用正确的 lease_epoch 回调时返回 "not found"。

**根因**：`task_attempts` 查询的 WHERE 条件是 `task_id = $1 AND attempt = $2`，但传入的第一个参数是 `req.AgentID` 而非 `req.TaskID`。

**错误代码**：
```go
err = tx.QueryRow(ctx, `SELECT status FROM task_attempts WHERE task_id = $1 AND attempt = $2`,
    req.AgentID, req.Attempt).Scan(&attemptStatus)
```

**修复**：改为 `req.TaskID`：
```go
err = tx.QueryRow(ctx, `SELECT status FROM task_attempts WHERE task_id = $1 AND attempt = $2`,
    req.TaskID, req.Attempt).Scan(&attemptStatus)
```

---

### Bug 3：outbox INSERT 用字符串拼接而非参数占位符

**文件**：`internal/persistence/postgres/callback_repo.go:118`

**现象**：回调处理时 outbox 插入失败。

**根因**：`event_type` 被拼接到 SQL 字符串中，导致 `$1` 位置被替换成文本，SQL 语法错乱。

**错误代码**：
```go
_, err = tx.Exec(ctx, `INSERT INTO outbox_events (event_type, task_id, payload) VALUES ($1, $2, $3)`+"task."+string(req.Status),
    req.TaskID, payload)
```

**修复**：改用 `$1` 参数传递 event_type：
```go
_, err = tx.Exec(ctx, `INSERT INTO outbox_events (event_type, task_id, payload) VALUES ($1, $2, $3)`,
    "task."+string(req.Status), req.TaskID, payload)
```

---

### Bug 4：空字符串 idempotency_key 触发唯一约束冲突

**文件**：`internal/persistence/postgres/task_repo.go:43`

**现象**：批量创建不带 `IdempotencyKey` 的任务时报：
```
duplicate key value violates unique constraint "uq_tasks_idempotency_key"
```

**根因**：唯一索引只排除了 `NULL`（`WHERE idempotency_key IS NOT NULL`），但 Go 的空字符串 `""` 不是 NULL，多个空字符串互相冲突。

**修复**：在 `Create` 中将空字符串转成 `nil` 再传入：
```go
var idempotencyKey any
if task.IdempotencyKey != "" {
    idempotencyKey = task.IdempotencyKey
}
```

---

### Bug 5：scanTask 无法扫描 NULL 的 idempotency_key

**文件**：`internal/persistence/postgres/task_repo.go:231`

**现象**：修复 Bug 4 后，数据库存了 NULL，但 `scanTask` 用 `*string` 直接接收报错：
```
can't scan into dest[5] (col: idempotency_key): cannot scan NULL into *string
```

**根因**：`scanTask` 直接 `&t.IdempotencyKey` 扫描，而数据库列现在可能为 NULL。

**修复**：和 `currentAgentID`、`errMsg` 一样用 `*string` 中间变量：
```go
var idempotencyKey *string
// ... Scan(&idempotencyKey) ...
if idempotencyKey != nil {
    t.IdempotencyKey = *idempotencyKey
}
```

---

## 测试结果

所有测试通过（`go test -p 1` 串行执行）：

```
=== RUN   TestClaimTask_Concurrent
--- PASS: TestClaimTask_Concurrent (0.06s)
=== RUN   TestProcessCallback_StaleLeaseEpoch
--- PASS: TestProcessCallback_StaleLeaseEpoch (0.06s)
=== RUN   TestCreate_Idempotent
--- PASS: TestCreate_Idempotent (0.03s)
=== RUN   TestTaskRepository_CreateAndGet
--- PASS: TestTaskRepository_CreateAndGet (0.03s)
=== RUN   TestTaskRepository_idempotency
--- PASS: TestTaskRepository_idempotency (0.02s)
=== RUN   TestTaskRepository_NotFound
--- PASS: TestTaskRepository_NotFound (0.02s)
=== RUN   TestTaskRepository_List
--- PASS: TestTaskRepository_List (0.03s)
=== RUN   TestTaskRepository_RequestCancel
--- PASS: TestTaskRepository_RequestCancel (0.03s)
PASS
ok  	github.com/shenghongio/kraken-orchestrator/internal/persistence/postgres
=== RUN   TestReaper_ExpiredLEase
--- PASS: TestReaper_ExpiredLEase (0.03s)
=== RUN   TestReaper_ExpiredLeaseRetry
--- PASS: TestReaper_ExpiredLeaseRetry (0.03s)
PASS
ok  	github.com/shenghongio/kraken-orchestrator/internal/lease
```

## 注意事项

- 集成测试需要 PostgreSQL 中执行 001~003 迁移（特别是 `003_unique_idempotency.sql` 的部分唯一索引）
- `ON CONFLICT` 子句必须与唯一索引的 `WHERE` 条件一致，否则报 `no unique or exclusion constraint matching`
- 跨包并行测试会因共用同一数据库 `TRUNCATE` 冲突，必须用 `-p 1` 串行执行