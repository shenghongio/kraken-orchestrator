package agent

import (
	"context"
	"errors"
	"log/slog"
	"sync"
	"time"
	
	"github.com/shenghongio/kraken-orchestrator/internal/config"
	"github.com/shenghongio/kraken-orchestrator/internal/domain"
	"github.com/shenghongio/kraken-orchestrator/internal/executor"
	"github.com/shenghongio/kraken-orchestrator/internal/persistence/sqlite"
	krakenhttp "github.com/shenghongio/kraken-orchestrator/internal/transport/http"
)

// Runtime 管理 Agent 的任务领取，执行，回调全生命周期
type Runtime struct {
	cfg          *config.AgentConfig              // Agent 配置
	logger       *slog.Logger                     // 结构化日志
	client       *krakenhttp.Client               // Scheduler HTTP 客户端
	registry     *executor.Registry               //Executor 注册表
	checkpoint   *sqlite.CheckpointRepository     // 本地 checkpoint 存储
	callbackRepo *sqlite.CallbackOutboxRepository // 本地回调 outbox
	
	// 并发控制,最多同时执行 maxConcurrency个任务  缓冲大小 = maxConcurrency 限制同时执行的任务数量
	slots     chan struct{}
	busySlots int        // 当前忙碌槽位数,(通过mu保护)
	mu        sync.Mutex // 保护 busySlots的并发访问
}

// NewRuntime 创建Agent Runtime
func RunRuntime(
	cfg *config.AgentConfig,
	logger *slog.Logger,
	client *krakenhttp.Client,
	registry *executor.Registry,
	checkpoint *sqlite.CheckpointRepository,
	callbackRepo *sqlite.CallbackOutboxRepository) *Runtime {
	return &Runtime{
		cfg:          cfg,
		logger:       logger,
		client:       client,
		registry:     registry,
		checkpoint:   checkpoint,
		callbackRepo: callbackRepo,
		slots:        make(chan struct{}, cfg.MaxConcurrency),
	}
}

// Run 启动 Agent 主循环，直到 ctx 取消
// 流程：注册 → 心跳 → flush 回调 → 申领执行循环
func (r *Runtime) Run(ctx context.Context) {
	r.logger.Info("agent runtime started", slog.String("agent_id", r.cfg.AgentID), slog.Int("max_concurrency", r.cfg.MaxConcurrency))
	
	// 注册 Agent
	capabilities := make([]string, 0, len(r.registry.Types()))
	for _, t := range r.registry.Types() {
		capabilities = append(capabilities, string(t))
	}
	if err := r.client.Register(ctx, &krakenhttp.RegisterAgentRequest{
		AgentID:        r.cfg.AgentID,
		Version:        "0.1.0",
		Capabilities:   capabilities,
		MaxConcurrency: r.cfg.MaxConcurrency,
	}); err != nil {
		r.logger.Error("register agent failed", slog.String("agent_id", r.cfg.AgentID), slog.Any("err", err))
		return
	}
	
	// 重启对账：处理上次奔溃时未完成的任务
	r.reconcile(ctx)
	
	// 启动心跳 goroutine （定期上报 busy_slots）
	go r.heartbeatLoop(ctx)
	
	// 启动 回调
	go r.flushCallbackLoop(ctx)
	
	// 主循环: 领取任务
	ticker := time.NewTicker(r.cfg.ClaimInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			r.logger.Info("agent runtime shutting down")
			return
		case <-ticker.C:
			r.claimAndExecute(ctx)
		}
	}
}

// claimAndExecute 尝试申领一个任务并执行
// 申领前先获取空闲槽，执行完释放槽
func (r *Runtime) claimAndExecute(ctx context.Context) {
	// 检查是否有空闲卡槽
	select {
	case r.slots <- struct{}{}:
	default:
		// 槽已满，跳过本次
		return
	}
	claimTaskRequest, err := r.client.Claim(ctx, &krakenhttp.ClaimTaskRequest{AgentID: r.cfg.AgentID})
	if err != nil {
		r.logger.Error("claim task failed", slog.String("agent_id", r.cfg.AgentID), slog.Any("err", err))
		<-r.slots // 释放槽位
		return
	}
	if claimTaskRequest == nil {
		// 无任务
		<-r.slots
		return
	}
	
	// 有任务，占用槽位并启动 goroutine 执行
	r.incBusySlots()
	go func() {
		defer func() {
			<-r.slots
			r.decBusySlots()
		}()
		r.executeTask(ctx, claimTaskRequest)
	}()
}

// executeTask 执行单个任务的完整流程
// 流程：写 checkpoint → 查 executor → 执行 → 上报结果
func (r *Runtime) executeTask(ctx context.Context, claim *krakenhttp.ClaimTaskResponse) {
	taskID := domain.TaskID(claim.TaskID)
	r.logger.Info("executing task", slog.String("task_id", string(taskID)), slog.Int("attempt", claim.Attempt), slog.Int64("lease_epoch", claim.LeaseEpoch))
	
	// 先写 checkpoint表，保证重启可以恢复
	if err := r.checkpoint.Save(ctx, &sqlite.Checkpoint{
		TaskID:     taskID,
		Attempt:    claim.Attempt,
		LeaseEpoch: claim.LeaseEpoch,
		Status:     sqlite.CheckpointStatusInRunning,
		Payload:    []byte(claim.Payload),
	}); err != nil {
		r.logger.Error("save checkpoint failed", slog.String("task_id", string(taskID)), slog.Any("err", err))
		return
	}
	
	// 构造 domain.Task 给 executor 使用
	task := &domain.Task{
		ID:      taskID,
		Type:    domain.TaskType(claim.Type),
		Payload: claim.Payload,
		Attempt: claim.Attempt,
	}
	
	// 查找 executor
	exec, ok := r.registry.Get(task.Type)
	if !ok {
		r.logger.Error("no executor for task type", slog.String("type", string(task.Type)))
		r.reportResult(ctx, taskID, claim, "failed", nil, "no executor for task type")
		return
	}
	
	// 执行任务（待可取消 context）
	execCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	
	result, err := exec.Execute(execCtx, task)
	if err != nil {
		r.logger.Error("executor failed", slog.String("task_id", string(taskID)), slog.Any("err", err))
		r.reportResult(ctx, taskID, claim, "failed", nil, err.Error())
		return
	}
	
	// 跟进执行结果上报
	status := "success"
	errMsg := ""
	if !result.Success {
		status = "failed"
		errMsg = result.Message
	}
	r.reportResult(ctx, taskID, claim, status, result.Output, errMsg)
}

// reportResult 将执行结果写入 callback outbox 并尝试发送
// 先持久化到 SQLite，再发 HTTP，保证回调不丢
func (r *Runtime) reportResult(ctx context.Context, taskID domain.TaskID, claim *krakenhttp.ClaimTaskResponse, status string, result []byte, errMsg string) {
	// 写callback outbox （持久化,保证不丢失）
	if err := r.callbackRepo.Enqueue(ctx, &sqlite.CallbackOutbox{
		TaskID:     taskID,
		Attempt:    claim.Attempt,
		LeaseEpoch: claim.LeaseEpoch,
		Status:     status,
		Result:     result,
		Error:      errMsg,
	}); err != nil {
		r.logger.Error("enqueue callback failed", slog.String("task_id", string(taskID)), slog.Int("attempt", claim.Attempt), slog.Any("err", err))
		return
	}
	
	//  2. 立即尝试发送（可能因网络失败，下次 flush 会重试）
	r.flushCallbacks(ctx)
	
	// 更新 checkpoint 状态
	if err := r.checkpoint.Save(ctx, &sqlite.Checkpoint{
		TaskID:     taskID,
		Attempt:    claim.Attempt,
		LeaseEpoch: claim.LeaseEpoch,
		Status:     sqlite.CheckpointStatusInCompleted,
	}); err != nil {
		r.logger.Error("enqueue callback failed", slog.String("task_id", string(taskID)), slog.Int("attempt", claim.Attempt), slog.Any("err", err))
	}
}

// flushCallbackLoop 定期 flush 回调 outbox
// 即使 reportResult 时发送失败，这里也会重试
func (r *Runtime) flushCallbackLoop(ctx context.Context) {
	ticker := time.NewTicker(10 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			r.flushCallbacks(ctx)
		}
	}
}

// flushCallbacks 扫描为未发送的回调并发送
func (r *Runtime) flushCallbacks(ctx context.Context) {
	listPending, err := r.callbackRepo.ListPending(ctx)
	if err != nil {
		r.logger.Error("list pending callbacks  failed", slog.String("err", err.Error()))
		return
	}
	
	for _, callback := range listPending {
		err := r.client.SendCallback(ctx, &krakenhttp.CallbackRequest{
			TaskID:     string(callback.TaskID),
			AgentID:    r.cfg.AgentID,
			Attempt:    callback.Attempt,
			LeaseEpoch: callback.LeaseEpoch,
			Status:     callback.Status,
			Result:     callback.Result,
			Error:      callback.Error,
		})
		if err != nil {
			r.logger.Error("send callback failed", slog.String("task_id", string(callback.TaskID)), slog.String("err", err.Error()))
			continue
		}
		if err := r.callbackRepo.MarkSent(ctx, callback.ID); err != nil {
			r.logger.Error("mark callback sent failed", slog.String("task_id", string(callback.TaskID)), slog.Any("err", err))
		}
	}
}

// heartbeatLoop 定期发送心跳
func (r *Runtime) heartbeatLoop(ctx context.Context) {
	ticker := time.NewTicker(r.cfg.HeartbeatInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if err := r.client.Heartbeat(ctx, &krakenhttp.HeartbeatRequest{
				AgentID:   r.cfg.AgentID,
				BusySlots: r.busySlots,
			}); err != nil {
				r.logger.Error("heartbeat failed", slog.String("agent_id", r.cfg.AgentID), slog.Any("err", err))
			}
		}
	}
}

// incBusySlots 忙碌槽位数 +1
func (r *Runtime) incBusySlots() {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.busySlots++
}

// decBusySlots 忙碌槽位数 -1
func (r *Runtime) decBusySlots() {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.busySlots--
}

// busySlotCount 读取当前忙碌槽位数
func (r *Runtime) busySlotCount() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.busySlots
}

// reconcile 重启对账，处理上次奔溃时未完成的任务
// 策略: 主动上报 failed 让 Scheduler 根据重试策略决定重新分配或标记失败
func (r *Runtime) reconcile(ctx context.Context) {
	cps, err := r.checkpoint.ListRunning(ctx)
	if err != nil {
		r.logger.Error("list running checkpoints failed", slog.Any("err", err))
		return
	}
	if len(cps) == 0 {
		return
	}
	
	r.logger.Info("reconciling unfinished tasks", slog.Int("count", len(cps)))
	for _, cp := range cps {
		// 主动上报 failed ，Scheduler 会更加重试策略处理
		err := r.client.SendCallback(ctx, &krakenhttp.CallbackRequest{
			TaskID:     string(cp.TaskID),
			AgentID:    r.cfg.AgentID,
			Attempt:    cp.Attempt,
			LeaseEpoch: cp.LeaseEpoch,
			Status:     "failed",
			Error:      "agent restarted",
		})
		if err != nil {
			// 409 表示任务已被其他 agent 重新分配，本地checkpoint 无意义，删除
			if errors.Is(err, domain.ErrStaleCallback) {
				r.logger.Error("task already reassigned deleting checkpoint", slog.String("task_id", string(cp.TaskID)), slog.Any("err", err))
				_ = r.checkpoint.Delete(ctx, cp.TaskID)
				continue
			}
			// 网络错误，保留 checkpoint 下次重试
			r.logger.With("reconcile callback failed,will retry", slog.String("task_id", string(cp.TaskID)), slog.Any("err", err))
			continue
			
		}
		// 上报成功 删除 checkpoint
		if err := r.checkpoint.Delete(ctx, cp.TaskID); err != nil {
			r.logger.Error("delete checkpoint failed", slog.String("task_id", string(cp.TaskID)), slog.Any("err", err))
		}
	}
}
