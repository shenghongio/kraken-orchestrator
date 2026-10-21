# Kraken Scheduler HTTP API 文档

Base URL: `http://localhost:8080`（以配置为准）

所有请求和响应均使用 `application/json`。

---

## 目录

1. [健康检查](#1-健康检查)
2. [任务接口](#2-任务接口)
3. [Agent 接口](#3-agent-接口)
4. [回调接口](#4-回调接口)
5. [通用错误响应](#5-通用错误响应)
6. [端到端测试流程](#6-端到端测试流程)

---

## 1. 健康检查

### GET /healthz

存活检查，进程活着就返回 200。

**请求**：无参数

**响应 200**：
```
OK
```

---

### GET /readyz

就绪检查，数据库连接正常才返回 200。

**请求**：无参数

**响应 200**：
```
OK
```

---

## 2. 任务接口

### 2.1 创建任务 — POST /v1/tasks

创建一个新任务。支持通过 `idempotency_key` 实现幂等。

**请求 Body**：

| 字段 | 类型 | 必填 | 默认值 | 说明 |
|------|------|------|--------|------|
| `type` | string | 是 | - | 任务类型，如 `download` |
| `priority` | int | 否 | 5 | 优先级，数值越大优先级越高 |
| `payload` | object | 否 | `{}` | 任务负载，透传给 Agent |
| `idempotency_key` | string | 否 | - | 幂等键，相同 key 重复创建返回已有任务 |
| `max_retries` | int | 否 | 0 | 最大重试次数 |

**请求示例**：
```json
{
  "type": "download",
  "priority": 5,
  "payload": {
    "url": "https://example.com/file.zip",
    "target": "/tmp/file.zip"
  },
  "max_retries": 3
}
```

**响应 200**：
```json
{
  "id": "tsk_8f3a9c2e1b",
  "type": "download",
  "priority": 5,
  "status": "pending",
  "payload": { "url": "https://example.com/file.zip", "target": "/tmp/file.zip" },
  "max_retries": 3,
  "attempt": 0,
  "current_agent_id": "",
  "cancel_requested": false,
  "result": null,
  "error": "",
  "created_at": "2026-10-21T14:00:00Z",
  "updated_at": "2026-10-21T14:00:00Z"
}
```

**幂等行为**：如果 `idempotency_key` 已存在，返回已有的任务（状态为当前实际状态），不创建新任务。

---

### 2.2 任务列表 — GET /v1/tasks

获取所有任务列表。

**请求**：无参数

**响应 200**：任务数组
```json
[
  {
    "id": "tsk_8f3a9c2e1b",
    "type": "download",
    "priority": 5,
    "status": "pending",
    "payload": {},
    "max_retries": 3,
    "attempt": 0,
    "cancel_requested": false,
    "created_at": "...",
    "updated_at": "..."
  }
]
```

---

### 2.3 查询任务 — GET /v1/tasks/{id}

根据任务 ID 查询任务详情。

**路径参数**：

| 字段 | 类型 | 说明 |
|------|------|------|
| `id` | string | 任务 ID |

**响应 200**：任务详情（同 2.1 响应格式）

**响应 404**：
```json
{ "error": "task not found", "code": "not_found" }
```

---

### 2.4 取消任务 — POST /v1/tasks/{id}/cancel

请求取消任务。仅 `pending` 或 `running` 状态且未请求过取消的任务可取消。

**路径参数**：

| 字段 | 类型 | 说明 |
|------|------|------|
| `id` | string | 任务 ID |

**响应 200**：
```json
{ "status": "cancel_requested" }
```

**响应 404**：任务不存在、已终止、或已请求过取消
```json
{ "error": "task not found, already cancelled, or terminal", "code": "not_found" }
```

---

## 3. Agent 接口

### 3.1 注册 Agent — POST /v1/agent/register

注册或更新 Agent 静态信息。重复调用会 upsert（更新已有记录）。

**请求 Body**：

| 字段 | 类型 | 必填 | 说明 |
|------|------|------|------|
| `agent_id` | string | 是 | Agent 唯一标识 |
| `version` | string | 否 | Agent 版本号 |
| `capabilities` | []string | 否 | 支持的任务类型列表 |
| `max_concurrency` | int | 是 | 最大并发任务数，必须 > 0 |

**请求示例**：
```json
{
  "agent_id": "agent-001",
  "version": "1.0.0",
  "capabilities": ["download", "compress"],
  "max_concurrency": 4
}
```

**响应 200**：
```json
{
  "id": "agent-001",
  "status": "online",
  "version": "1.0.0",
  "capabilities": ["download", "compress"],
  "max_concurrency": 4,
  "busy_slots": 0,
  "last_heartbeat": "2026-10-21T14:00:00Z",
  "created_at": "...",
  "updated_at": "..."
}
```

---

### 3.2 心跳 — POST /v1/agent/heartbeat

Agent 定期上报心跳，更新 `last_heartbeat` 和 `busy_slots`。

**请求 Body**：

| 字段 | 类型 | 必填 | 说明 |
|------|------|------|------|
| `agent_id` | string | 是 | Agent ID |
| `busy_slots` | int | 是 | 当前正在执行的任务数，>= 0 |

**请求示例**：
```json
{
  "agent_id": "agent-001",
  "busy_slots": 2
}
```

**响应 200**：
```json
{ "status": "ok" }
```

**响应 404**：Agent 未注册
```json
{ "error": "agent not found", "code": "not_found" }
```

---

### 3.3 申领任务 — POST /v1/agent/tasks/claim

原子申领一个待执行任务。使用 `FOR UPDATE SKIP LOCKED` 保证并发安全。

**请求 Body**：

| 字段 | 类型 | 必填 | 说明 |
|------|------|------|------|
| `agent_id` | string | 是 | Agent ID |

**请求示例**：
```json
{ "agent_id": "agent-001" }
```

**响应 200（有任务）**：
```json
{
  "task_id": "tsk_8f3a9c2e1b",
  "type": "download",
  "attempt": 1,
  "lease_epoch": 1,
  "payload": { "url": "...", "target": "..." }
}
```

**响应字段说明**：

| 字段 | 类型 | 说明 |
|------|------|------|
| `task_id` | string | 任务 ID |
| `type` | string | 任务类型 |
| `attempt` | int | 当前尝试次数（从 1 开始） |
| `lease_epoch` | int64 | 租约纪元（fencing token），回调时必须原样带回 |
| `payload` | object | 任务负载 |

**响应 204（无任务）**：空 body，表示当前没有可申领的任务。

---

## 4. 回调接口

### POST /v1/callback

Agent 上报任务执行结果。必须携带申领时返回的 `attempt` 和 `lease_epoch`，用于 fencing 校验。

**请求 Body**：

| 字段 | 类型 | 必填 | 说明 |
|------|------|------|------|
| `task_id` | string | 是 | 任务 ID |
| `agent_id` | string | 是 | Agent ID |
| `attempt` | int | 是 | 申领时返回的 attempt |
| `lease_epoch` | int64 | 是 | 申领时返回的 lease_epoch（fencing token） |
| `status` | string | 是 | 执行结果：`success` / `failed` / `cancelled` |
| `result` | object | 否 | 执行结果数据 |
| `error` | string | 否 | 失败原因（status=failed 时填写） |

**请求示例（成功）**：
```json
{
  "task_id": "tsk_8f3a9c2e1b",
  "agent_id": "agent-001",
  "attempt": 1,
  "lease_epoch": 1,
  "status": "success",
  "result": { "downloaded_bytes": 1024 },
  "error": ""
}
```

**请求示例（失败）**：
```json
{
  "task_id": "tsk_8f3a9c2e1b",
  "agent_id": "agent-001",
  "attempt": 1,
  "lease_epoch": 1,
  "status": "failed",
  "result": null,
  "error": "connection timeout"
}
```

**响应 204**：回调成功处理（空 body）

**响应 409**：stale callback，lease_epoch 不匹配（旧回调被拒绝）
```json
{ "error": "stale callback", "code": "conflict" }
```

**响应 404**：任务不存在
```json
{ "error": "task not found", "code": "not_found" }
```

**响应 400**：参数缺失或 status 非法
```json
{ "error": "task_id, agent_id, attempt, lease_epoch are required", "code": "invalid_input" }
```

---

## 5. 通用错误响应

所有错误响应格式统一：

```json
{
  "error": "人类可读的错误描述",
  "code": "错误码"
}
```

**错误码列表**：

| code | HTTP 状态 | 说明 |
|------|-----------|------|
| `invalid_input` | 400 | 请求参数校验失败 |
| `not_found` | 404 | 资源不存在 |
| `conflict` | 409 | 冲突（如 stale callback） |
| `internal` | 500 | 服务器内部错误 |

---

## 6. 端到端测试流程

按顺序执行以下步骤验证完整流程：

| 步骤 | 接口 | 预期 |
|------|------|------|
| 1 | `GET /healthz` | 200 |
| 2 | `POST /v1/agent/register` | 200 |
| 3 | `POST /v1/agent/heartbeat` | 200 |
| 4 | `POST /v1/tasks` | 200，记下 `id` |
| 5 | `POST /v1/agent/tasks/claim` | 200，记下 `task_id`、`attempt`、`lease_epoch` |
| 6 | `GET /v1/tasks/{id}` | status = `running` |
| 7 | `POST /v1/callback`（status=success） | 204 |
| 8 | `GET /v1/tasks/{id}` | status = `success` |
| 9 | 查 DB `outbox_events` 表 | 存在 `event_type=task.success` 记录 |
