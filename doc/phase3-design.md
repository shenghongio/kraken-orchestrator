# Phase 3 设计文档

## 概述

Phase 3 的目标是让整个系统在**单机 Docker Compose** 上跑通端到端流程：Scheduler、Agent、PostgreSQL、Redis 四个服务编排运行，验证任务能在容器重启和依赖临时故障下保持状态一致。

核心原则：

1. **单机优先**：先在一台 Docker host 上验证完整闭环，再考虑多机扩展
2. **配置外置**：所有连接信息（DSN、URL、端口）通过环境变量注入，不硬编码
3. **故障可恢复**：Postgres/Redis 临时不可用时，任务状态不丢失，服务恢复后自动继续

---

## 模块 1：Agent 入口程序（cmd/kraken-agent/main.go）

### 设计决策

#### 1.1 与 scheduler main.go 对称的结构

```go
func run() error {
    banner.Print("Kraken Agent")
    // 1. 解析 -config flag / KRAKEN_CONFIG 环境变量
    // 2. LoadAgentConfig
    // 3. InitLogger
    // 4. 初始化 SQLite（checkpoint + callback outbox）
    // 5. 初始化 executor.Registry（注册 noop executor）
    // 6. 初始化 http.Client（连接 scheduler）
    // 7. 构造 agent.Runtime 并启动
    // 8. 等待 SIGINT/SIGTERM，优雅关闭
}
```

**为什么**：和 scheduler 入口保持一致的模式（banner → config → logger → 依赖初始化 → 启动 → 优雅关闭），降低维护成本。

#### 1.2 SQLite 路径配置化

`AgentConfig.SQLitePath` 指向本地 checkpoint/outbox 数据库文件，通过环境变量 `KRAKEN_AGENT_SQLITE_PATH` 注入。容器内挂卷到这个路径，保证重启后数据不丢。

#### 1.3 noop Executor

Phase 3 不实现真实 Executor（那是 Phase 4），但 Agent 需要至少一个 Executor 才能跑通申领→执行→回调闭环。

noop Executor 行为：
- 接收一个可配置的 `duration`（从 payload 读取，默认 10s）
- `Execute` 里 `time.Sleep(duration)`，可被 context 取消
- 返回 success 或模拟失败（payload 里 `should_fail: true`）

**为什么**：
- 验证 claim → execute → callback 全链路
- 验证 context 取消（cancel 任务时能中断 sleep）
- 模拟长任务（duration 设大）验证 lease 续期和 reaper 行为

---

## 模块 2：CLI 客户端（cmd/krakenctl/main.go）

### 设计决策

#### 2.1 最少命令集

```bash
krakenctl task submit --file task.yaml    # 提交任务
krakenctl task get <task-id>              # 查询任务
krakenctl task list                       # 列出任务
krakenctl task cancel <task-id>           # 取消任务
krakenctl health                          # 健康检查
```

**为什么**：Phase 3 只需能验证任务生命周期，不需要复杂功能。后续 Phase 4/5 再扩展。

#### 2.2 Scheduler URL 配置

通过 `--url` flag 或 `KRAKEN_SCHEDULER_URL` 环境变量指定，默认 `http://localhost:8080`。

#### 2.3 输出格式

默认人类可读表格，`--json` 输出原始 JSON（便于脚本处理）。

---

## 模块 3：Dockerfile

### 设计决策

#### 3.1 多阶段构建

```dockerfile
# 构建阶段
FROM golang:1.27 AS builder
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -o /out/kraken-scheduler ./cmd/kraken-scheduler
RUN CGO_ENABLED=0 GOOS=linux go build -o /out/kraken-agent ./cmd/kraken-agent

# 运行阶段
FROM alpine:3.20
RUN apk add --no-cache ca-certificates tzdata
COPY --from=builder /out/kraken-scheduler /usr/local/bin/
COPY --from=builder /out/kraken-agent /usr/local/bin/
```

**为什么**：
- 多阶段构建减小最终镜像体积（只包含二进制和运行时依赖）
- `alpine` + `ca-certificates` 满足 HTTPS 需求
- `CGO_ENABLED=0` 静态编译，避免动态库依赖问题
- 两个二进制放同一个镜像，通过 `command` 区分启动哪个

#### 3.2 迁移文件 embed

scheduler main.go 已用 `//go:embed migrations/*.sql`，构建时自动打包，运行时无需额外挂载。

---

## 模块 4：Docker Compose 编排

### 设计决策

#### 4.1 四个服务

```yaml
services:
  postgres:
    image: postgres:17-alpine
    volumes:
      - postgres_data:/var/lib/postgresql/data
    environment:
      POSTGRES_USER: kraken
      POSTGRES_PASSWORD: ${POSTGRES_PASSWORD}
      POSTGRES_DB: kraken
    healthcheck:
      test: ["CMD-SHELL", "pg_isready -U kraken"]
      interval: 5s
      timeout: 5s
      retries: 10

  redis:
    image: redis:7-alpine
    volumes:
      - redis_data:/data
    healthcheck:
      test: ["CMD", "redis-cli", "ping"]
      interval: 5s
      timeout: 3s
      retries: 10

  scheduler:
    build: .
    command: kraken-scheduler
    depends_on:
      postgres:
        condition: service_healthy
      redis:
        condition: service_healthy
    environment:
      KRAKEN_DB_DSN: postgres://kraken:${POSTGRES_PASSWORD}@postgres:5432/kraken?sslmode=disable
      KRAKEN_REDIS_ADDR: redis:6379
    ports:
      - "8080:8080"
    restart: unless-stopped

  agent:
    build: .
    command: kraken-agent
    depends_on:
      scheduler:
        condition: service_started
    environment:
      KRAKEN_SCHEDULER_URL: http://scheduler:8080
      KRAKEN_AGENT_ID: agent-1
      KRAKEN_AGENT_MAX_CONCURRENCY: 4
    volumes:
      - agent_data:/var/lib/kraken-agent
    restart: unless-stopped
```

#### 4.2 用 Compose 服务名做 DNS

容器间通信用服务名（`postgres`、`redis`、`scheduler`），不硬编码 `localhost`。这符合 AGENTS.md §4 的要求。

#### 4.3 健康检查 + depends_on

scheduler 依赖 postgres 和 redis 的健康检查通过后再启动，避免 scheduler 启动时数据库还没就绪导致连接失败。

#### 4.4 数据卷持久化

| 卷 | 挂载点 | 用途 |
|----|--------|------|
| `postgres_data` | `/var/lib/postgresql/data` | 任务状态持久化 |
| `redis_data` | `/data` | Redis 数据（可选，主要是通知） |
| `agent_data` | `/var/lib/kraken-agent` | Agent 的 SQLite checkpoint + callback outbox |

**为什么**：容器重启后数据不丢，验证任务可恢复性。

#### 4.5 restart: unless-stopped

容器异常退出自动重启，模拟进程崩溃场景。

---

## 模块 5：配置环境变量化

### 设计决策

#### 5.1 koanf 环境变量覆盖

`internal/config/config.go` 已支持 koanf，确保环境变量能覆盖配置文件值。约定环境变量前缀 `KRAKEN_`，用下划线分隔层级。

| 配置项 | 环境变量 |
|--------|----------|
| `db.dsn` | `KRAKEN_DB_DSN` |
| `db.max_open_conns` | `KRAKEN_DB_MAX_OPEN_CONNS` |
| `redis.addr` | `KRAKEN_REDIS_ADDR` |
| `http.addr` | `KRAKEN_HTTP_ADDR` |
| `log.level` | `KRAKEN_LOG_LEVEL` |
| `log.format` | `KRAKEN_LOG_FORMAT` |
| `scheduler.url` | `KRAKEN_SCHEDULER_URL` |
| `agent.id` | `KRAKEN_AGENT_ID` |
| `agent.max_concurrency` | `KRAKEN_AGENT_MAX_CONCURRENCY` |
| `agent.sqlite_path` | `KRAKEN_AGENT_SQLITE_PATH` |
| `lease_reaper.interval` | `KRAKEN_LEASE_REAPER_INTERVAL` |

#### 5.2 不提交 .env 文件

`.env` 含密码，加入 `.gitignore`，提供 `.env.example` 模板。

---

## 模块 6：故障注入验证

### 设计决策

#### 6.1 验证场景

| 场景 | 操作 | 预期结果 |
|------|------|----------|
| Scheduler 重启 | `docker compose restart scheduler` | Agent 继续执行任务，重启后能重新申领 |
| Postgres 短暂不可用 | `docker compose stop postgres` 30s 后 start | 任务状态不丢，scheduler 重连后继续工作 |
| Redis 不可用 | `docker compose stop redis` | 任务仍可正常申领/回调（outbox 事件积压），Redis 恢复后补发通知 |
| Agent 宕机 | `docker compose kill agent` | lease 过期后 reaper 回收任务，任务重新排队 |
| Agent 重启 | `docker compose restart agent` | Agent 从 SQLite checkpoint 恢复，对账 scheduler 状态 |

#### 6.2 验证方法

用 noop Executor 提交一个长任务（duration=120s），在执行过程中触发故障，观察日志和任务状态变化。

---

## 实施顺序

1. `internal/executor/noop.go` — noop Executor 实现
2. `cmd/kraken-agent/main.go` — Agent 入口程序
3. `cmd/krakenctl/main.go` — CLI 客户端
4. `Dockerfile` — 多阶段构建
5. `deployments/docker-compose.yml` — 四服务编排
6. `configs/agent.yaml` + `.env.example` — 配置文件
7. 故障注入验证
8. `krakenctl` 使用文档