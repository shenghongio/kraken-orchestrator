# Phase 2 设计文档

## 概述

Phase 2 的目标是构建完整的任务调度闭环：Agent 注册 → 任务申领 → 执行回调 → 租约回收。所有设计围绕三个核心原则：

1. **事务一致性**：状态变更必须在一个数据库事务内完成，不能出现中间状态
2. **Fencing Token**：用单调递增的 `lease_epoch` 防止旧 Agent 的回调覆盖新结果
3. **幂等性**：所有接口必须能安全重试，重试不产生副作用

---

## 模块 1：Agent 注册与心跳（已实现）

### 设计决策

#### 1.1 UpsertAgent 不更新 busy_slots 和 created_at

```sql
ON CONFLICT (id) DO UPDATE SET
    status = EXCLUDED.status,
    version = EXCLUDED.version,
    capabilities = EXCLUDED.capabilities,
    max_concurrency = EXCLUDED.max_concurrency,
    updated_at = EXCLUDED.updated_at
```

**为什么**：`busy_slots` 是运行时状态，只能由 heartbeat 接口更新。如果注册接口也能改，会导致 Agent 重启时误报空闲槽位，调度器据此分配任务后实际无法执行。`created_at` 是审计字段，不能被覆盖。

#### 1.2 Heartbeat 只更新 last_heartbeat 和 busy_slots

**为什么**：心跳的语义是"我还活着，当前负载是 X"。Agent 的版本、能力、并发数在注册后不应通过心跳变更（变更需要重新注册）。

#### 1.3 用 INSERT ... ON CONFLICT 而非先查后写

**为什么**：并发场景下，多个注册请求可能同时到达。先查后写存在 TOCTOU（Time-Of-Check to Time-Of-Use）竞态，会导致重复插入或丢失更新。`ON CONFLICT DO UPDATE` 是数据库原子操作，天然并发安全。

---

## 模块 2：原子申领 + Leases + Fencing Tokens（已实现）

### 设计决策

#### 2.1 FOR UPDATE SKIP LOCKED

```sql
SELECT id FROM tasks
WHERE status = 'pending' AND cancel_requested = FALSE AND next_run_at <= NOW()
ORDER BY priority DESC, created_at ASC
LIMIT 1
FOR UPDATE SKIP LOCKED
```

**为什么**：
- `FOR UPDATE`：对选中的行加排他锁，防止其他事务同时申领同一行
- `SKIP LOCKED`：跳过已被其他事务锁定的行，而不是等待。这让多个 Agent 可以并行申领，不会互相阻塞
- 没有 `SKIP LOCKED` 时，高并发下会大量事务排队等待锁，吞吐量骤降

#### 2.2 attempt 和 lease_epoch 用 SQL 自增

```sql
UPDATE tasks
SET attempt = attempt + 1, lease_epoch = lease_epoch + 1
WHERE id = $1
RETURNING attempt, lease_epoch
```

**为什么**：如果先 SELECT 出值，在 Go 里 +1 再 UPDATE，两个并发事务可能读到同一个值，导致 attempt 重复。SQL 层面的 `+ 1` 是原子的，配合行锁保证正确性。

#### 2.3 lease_epoch 作为 Fencing Token

**为什么**：考虑这个场景：
1. Agent A 申领任务（lease_epoch=1）
2. A 网络卡顿，lease 过期，Reaper 回收任务
3. Agent B 重新申领（lease_epoch=2）
4. A 网络恢复，回调成功

如果没有 fencing，A 的回调会覆盖 B 的结果。`lease_epoch` 单调递增，callback 时校验 `task.lease_epoch == req.lease_epoch`，A 的 epoch=1 ≠ task.lease_epoch=2，回调被拒绝。

#### 2.4 事务内完成所有写入

ClaimTask 在一个事务内完成：锁定任务 → 更新状态 → 插入 attempt → 插入 lease → 插入 event。

**为什么**：如果中途任何一步失败，整个事务回滚，不会出现"任务状态是 running 但没有 lease 记录"的不一致状态。

#### 2.5 没有任务时返回 nil, nil

**为什么**：没有可申领的任务不是错误，是正常情况。Handler 返回 204 No Content，Agent 收到后等待再重试。如果返回 error，会被误判为系统故障。

---

## 模块 3：回调验证与幂等处理（已实现）

### 设计决策

#### 3.1 三重校验：agent_id + attempt + lease_epoch

```go
if task.CurrentAgentID != req.AgentID { return ErrStaleCallback }
if task.Attempt != req.Attempt { return ErrStaleCallback }
if task.LeaseEpoch != req.LeaseEpoch { return ErrStaleCallback }
```

**为什么**：
- `agent_id`：确认是当前持有任务的 Agent
- `attempt`：确认是当前这一轮尝试（防止上一轮失败的回调覆盖本轮）
- `lease_epoch`：fencing 校验，防止旧 lease 的回调

三者必须同时匹配，缺一不可。

#### 3.2 幂等处理逻辑

```go
if attemptStatus != AttemptStatusRunning {
    if attemptStatus == req.Status {
        return nil  // 重复回调，幂等成功
    }
    return ErrStaleCallback  // 状态冲突
}
```

**为什么**：Agent 发送回调后可能因网络超时没收到响应，会重试。如果 attempt 已经是终态且状态一致，说明回调已处理，直接返回成功。如果状态不一致（比如上次回调是 success，这次是 failed），说明出现了冲突，拒绝。

#### 3.3 重试决策：attempt <= max_retries

```go
if task.Attempt <= task.MaxRetries {
    newStatus = TaskStatusPending
    nextRunAt = now + nextBackoff(task.Attempt)
} else {
    newStatus = TaskStatusFailed
}
```

**为什么**：`attempt` 在 claim 时已经 +1，所以第一次执行时 attempt=1。如果 max_retries=3，那么 attempt=1,2,3 都可以重试，第 4 次（attempt=4）就失败。总共执行 4 次（1 次初始 + 3 次重试），符合预期。

#### 3.4 指数退避 + Jitter

```go
d := base * (1 << attempt)
jitter := rand(d/2)
return d/2 + jitter
```

**为什么**：
- 指数退避：失败越多次，等越久，避免无效重试浪费资源
- Jitter：如果所有失败任务在同一时刻重试，会造成重试风暴。加随机抖动让重试时间分散

---

## 模块 4：Lease Reaper（已实现）

### 设计决策

#### 4.1 扫描过期 lease 而非主动检测

**为什么**：主动检测需要维护定时器，复杂且易错。扫描方式简单可靠，间隔 5 秒扫描一次，最多延迟 5 秒回收，可接受。

#### 4.2 回收前的 fencing 校验

```go
if taskLeaseEpoch != leaseEpoch {
    // 任务已被重新申领，只删旧 lease
    DELETE FROM task_leases WHERE task_id = $1 AND lease_epoch = $2
    return
}
```

**为什么**：Reaper 扫描到过期 lease 时，任务可能已经被其他 Agent 重新申领（lease_epoch 变了）。如果不校验就回收，会把新 Agent 的任务错误地置为 pending。

#### 4.3 LIMIT 100 分批处理

**为什么**：如果大量 lease 同时过期（比如调度器宕机后重启），一次性处理会长时间持锁，阻塞 claim 和 callback。分批处理每次最多 100 个，平衡了回收速度和锁持有时间。

#### 4.4 过期 lease 默认重试而非直接失败

**为什么**：lease 过期通常是 Agent 宕机或网络抖动，任务本身可能没失败。优先重试，只有超过 max_retries 才标记失败，减少误判。

---

## 模块 5：事务性 Outbox + Redis 通知（已实现）

### 核心设计

#### 5.1 Outbox 模式

在业务事务中同时写入 outbox 事件表：

```go
// 在 ClaimTask / ProcessCallback / Reaper 的事务内
INSERT INTO outbox_events (event_type, task_id, payload) VALUES (...)
```

**为什么**：如果先更新任务状态再发 Redis 通知，Redis 可能失败导致通知丢失。如果先发通知再更新状态，通知可能指向不存在的状态。Outbox 模式把"状态变更"和"通知意图"放在同一个事务里，保证原子性。后台 dispatcher 异步扫描 outbox 发送通知，发送成功后标记已发送。

#### 5.2 Redis 失败不影响任务状态

**为什么**：Redis 是通知通道，不是状态存储。任务状态以 DB 为准，Redis 不可用时任务仍可通过 DB 轮询被发现。这符合 AGENTS.md 的要求："Redis 故障不能导致任务状态不一致"。

---

## 模块 6：Agent 侧（已实现）

### 核心设计

#### 6.1 SQLite Checkpoint + Callback Outbox

Agent 本地用 SQLite 存储：
- `checkpoints`：正在执行的任务状态，重启后可恢复
- `callback_outbox`：待发送的回调，保证不丢

**为什么**：Agent 是故障多发点（进程崩溃、机器宕机）。如果只在内存中保存任务状态，崩溃后任务执行进度丢失，scheduler 只能等 lease 过期重试。有了本地 checkpoint，可以恢复执行；有了 callback outbox，回调不会因网络问题丢失。

#### 6.2 重启对账以 Scheduler 为准

**为什么**：Agent 本地状态可能过期（比如 lease 已被 Reaper 回收，任务已被其他 Agent 申领）。重启时必须向 scheduler 查询任务当前状态，不能盲目用本地状态继续执行。

#### 6.3 Lease Renewal

Agent 定期（leaseTTL / 3）调用 RenewLease 续期。

**为什么**：长时间任务（超过 leaseTTL）需要续期，否则会被 Reaper 误回收。每 TTL/3 续期一次，留两次重试余量。

---

## 错误码与 HTTP 状态映射

| 错误 | HTTP 状态 | 含义 |
|------|-----------|------|
| ErrNotFound | 404 | 资源不存在 |
| ErrStaleCallback | 409 | 回调已过期（fencing 校验失败） |
| ErrInvalidStatusTransition | 400 | 状态转换非法 |
| ErrLeaseExpired | 409 | 租约已过期 |
| 其他 | 500 | 内部错误 |

---

## 事务隔离级别

所有写事务使用 `ReadCommitted`：

```go
tx, err := r.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.ReadCommitted})
```

**为什么**：`ReadCommitted` 配合行级锁（`FOR UPDATE`）已经足够保证正确性，不需要 `Serializable`（性能差，容易死锁）。`FOR UPDATE` 保证同一行不会被并发修改，这是我们的核心需求。

---

## 模块 7：日志增强（已实现）

目标是在 debug 时显示尽可能全面的信息，同时保证敏感数据不泄露。增强四个方面：debug 级别输出、错误调用栈、HTTP 请求分组、SQL 错误上下文。

### 7.1 敏感字段自动脱敏

`observability.NewLogger` 创建 handler 时注册 `ReplaceAttr` 回调，递归遍历所有 attr，对命中的敏感 key 自动替换为 `"[REDACTED]"`。

脱敏字段列表：`password`、`token`、`dsn`、`authorization`、`secret`、`api_key`、`credential`。

**为什么**：slog 的 `ReplaceAttr` 是结构化日志的统一出口，在一处拦截即可覆盖所有日志输出，避免每个调用点手动脱敏遗漏。对 map 类型的值递归脱敏（`RedactMap`），保证嵌套结构里的敏感字段也被处理。

### 7.2 错误日志附带调用栈

新增 `observability.ErrAttr(err)`，返回 `slog.Group("error", message, stack)`：

```go
func ErrAttr(err error) slog.Attr {
    return slog.Group("error",
        slog.String("message", err.Error()),
        slog.String("stack", Stack()),
    )
}
```

`Stack()` 用 `runtime.Callers` 抓取调用栈，跳帧 3 层跳过 observability 包自身，从业务代码帧开始显示。

**为什么**：
- 统一错误输出结构，所有 error 日志都是 `error.message` + `error.stack`，便于检索和聚合
- 调用栈帮助定位错误来源，尤其是 repo 层深处返回的错误
- 跳帧避免日志里全是 `runtime.Callers` / `observability.Stack` 这些无关帧

### 7.3 HTTP 请求日志分组

中间件链顺序：`RequestID → RequestLogger → Recover → AccessLog → mux`。

`RequestLogger` 从 context 取 `request_id`，构建请求级 logger（附带 `request_id`/`method`/`path`/`ip`），再存入 context。handler 和中间件通过 `LoggerFromContext(ctx)` 获取，同一次请求的所有日志自动携带 `request_id`。

**为什么**：
- 不需要每个日志调用手动传 `request_id`，避免遗漏
- `Recover` 里的 panic 日志、`AccessLog` 的访问日志、handler 的业务日志都自动关联
- `LoggerFromContext` fallback 到 `slog.Default()`，防止 context 无 logger 时 nil panic（如单元测试直接调 handler）

### 7.4 SQL 错误封装

新增 `SQLError` 结构体包装 `pgx.PgError`，保留 `errors.Is`/`As` 语义链：

```go
type SQLError struct {
    Operation string   // 业务操作名，如 "task.claim.update_task"
    Query     string   // 截断后的 SQL
    Args      string   // 参数摘要（类型+长度，不含真实值）
    SQLState  string
    SQLCode   string
    Detail    string
    Routine   string
    Cause     error
}
```

`wrapSQLError(operation, query, args, err)` 统一包装：
- `pgx.ErrNoRows` → `domain.ErrNotFound`（保持原语义）
- 其他错误提取 `PgError` 字段（SQLState/Code/Detail/Routine）
- `truncateQuery` 截断超长 SQL（默认 500 字符）
- `formatArgs` 只输出参数类型和长度，不输出真实值

**为什么**：
- 出问题时能从日志直接看到是哪个操作、哪条 SQL、哪些参数，不用翻代码
- 参数只输出类型和长度（如 `$1=string(8) $2=bytes(1024)`），避免 payload 中的敏感数据泄露
- `Unwrap()` 返回原始错误，`errors.Is(err, domain.ErrNotFound)` 等判断完全兼容
- 事务步骤（BeginTx/Commit）无 SQL，传空 query 和 nil args，仅用 operation 标识

### 7.5 scanTask/scanAgent 签名变更

```go
func scanTask(s rowScanner, operation, query string, args []any) (*domain.Task, error)
```

扫描错误在内部直接用 `wrapSQLError` 包装，删除了原来的 `scanTaskWrap` 额外包装层。

**为什么**：原来 `scanTask` 返回原始错误，调用方再用 `scanTaskWrap` 包一层，两层包装冗余且容易遗漏。直接让 `scanTask` 接收 operation/query/args，内部包装，调用方只需传入对应上下文即可。

### 7.6 不包装的场景

以下情况保持原样，不使用 `wrapSQLError`：

- `RowsAffected() == 0` 返回 `domain.ErrNotFound`（业务判断，不是 SQL 错误）
- 业务错误如 `domain.ErrStaleCallback`、`domain.ErrInvalidStatusTransition`（校验失败，正常分支）
- `ClaimTask` 中 `select pending task` 的 `pgx.ErrNoRows` 返回 `nil, nil`（无任务可申领，正常情况）
- `rows.Err()` 连接级错误

**为什么**：这些不是数据库执行错误，包装后反而会丢失业务语义或产生误导。