package agent

import (
	"context"
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
	cfg          *config.AgentConfig
	logger       *slog.Logger
	client       *krakenhttp.Client
	registry     *executor.Registry
	checkpoint   *sqlite.CheckpointRepository
	callbackRepo *sqlite.CallbackOutboxRepository
	
	// 并发控制,最多同时执行 maxConcurrency个任务
	slots     chan struct{}
	busySlots int
	mu        sync.Mutex
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

// Run 启动 Agent 主循环直到ctx取消
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
	// 启动心跳 goroutine
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

// claimAndExecute 尝试领取一个任务并执行
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
		<-r.slots
		return
	}
	if claimTaskRequest == nil {
		// 无任务
		<-r.slots
		return
	}
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

// reportResult 将执行结果写入 callback ouotbox 并尝试发送
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
	
	// 立即尝试发送
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

func (r *Runtime) incBusySlots() {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.busySlots++
}
func (r *Runtime) decBusySlots() {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.busySlots--
}
func (r *Runtime) busySlotCount() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.busySlots
}