# Kraken Orchestrator

> **分布式任务编排平台**：基于 Go 构建，面向长时间运行、可恢复的任务，支持 Scheduler / Agent 分离架构。

[English](README.md) · [架构设计](docs/architecture/overview.md) · [路线图](ROADMAP.md) · [变更记录](CHANGELOG.md)

## 项目状态

**工程状态：架构基线与实施计划。** 本文档描述目标架构和验收要求，不代表所有能力已经实现。只有经过代码、测试、部署和运行行为验证的功能，才可以标记为已实现。详见 [能力状态表](README.md#capability-status) 和 [ROADMAP.md](ROADMAP.md)。

## 项目目标

Kraken 面向可能持续数小时、数天甚至数周的任务，例如文件下载、rclone 数据同步、日志采集和 AI 处理。首要目标是可靠执行、故障恢复、明确的任务状态转换，以及从单机 Docker 部署逐步扩展到多机器 Agent。

第一阶段采用单机 Docker Compose：一个 Scheduler、一个 Agent、PostgreSQL，以及可选 Redis。后续目标规模约为 10–50 个 Agent。当前不提前拆分大量微服务。

## 命名规范

| 项目 | 统一名称 |
|---|---|
| 仓库 | `kraken-orchestrator` |
| 调度进程 | `kraken-scheduler` |
| 执行进程 | `kraken-agent` |
| CLI | `krakenctl` |
| Compose 服务 | `scheduler`、`agent`、`postgres`、`redis` |

入口文件：

```text
cmd/kraken-scheduler/main.go
cmd/kraken-agent/main.go
cmd/krakenctl/main.go
```

Go module 路径必须使用仓库真实远程地址对应的模块路径，不得编造 Git 托管域名。

## 核心架构约束

- **PostgreSQL**：任务、执行尝试、租约、事件和事务 Outbox 的权威存储。
- **Redis**：可选通知、缓存或唤醒组件，不是任务持久化的唯一位置。
- **eBPF**：可选节点/进程运行时观测，采集结果只能作为辅助信号，不能单独决定任务成功或失败。
- **client-go**：可选 Kubernetes 资源发现与监听。Pod 状态不等同于 Agent 业务状态。
- **etcd / Kubernetes Lease**：可选协调明确的单实例控制循环，不替代 PostgreSQL 任务领取和 Fencing 校验。

## 工程原则

1. 业务逻辑放在 `internal/`；`cmd/*/main.go` 只负责配置、依赖装配、启动和退出。
2. 状态转换显式定义，拒绝非法转换。
3. 按**至少一次投递**设计，通过幂等和 fencing 保护副作用；不承诺 exactly-once。
4. 外部副作用得到确认前，先持久化必要状态。
5. 所有 I/O 和数据库操作设置超时；使用有界队列和 goroutine 池。
6. 传播取消信号，脱敏秘密信息，校验外部输入，并优雅释放资源。
7. 有意义的行为变更必须附带测试和文档更新。
8. 不得静默修改 API 契约、状态机语义、迁移行为或配置名称。

详细设计见 `docs/`，里程碑见 `ROADMAP.md`。当前部署目标是单机 Docker Compose；单机是单一故障域，容器重启策略不等于机器级高可用。生产使用前必须具备持久化卷、健康检查、重连、密钥管理和备份恢复流程。
