package postgres

import (
	"context"
	"encoding/json"
	"math/rand"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/shenghongio/kraken-orchestrator/internal/domain"
)

// CallbackRequest 回调请求，由agent 执行完成后上报
type CallbackRequest struct {
	TaskID     string
	AgentID    string
	Attempt    int
	LeaseEpoch int64
	Status     domain.AttemptStatus
	Result     []byte
	Error      string
}

// ProcessCallback 在一个事务内完成回调的三重校验（agent_id/attempt/lease_epoch）、
// 幂等处理、状态流转、租约释放和事件记录。
// 任何校验不匹配都返回 ErrStaleCallback（HTTP 409），防止旧 Agent 的回调覆盖新结果。
func (r *TaskRepository) ProcessCallback(ctx context.Context, req *CallbackRequest) error {
	tx, err := r.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.ReadCommitted})
	if err != nil {
		return wrapSQLError("callback.begin_tx", "", nil, err)
	}
	defer tx.Rollback(ctx)

	// 锁定任务行
	selectQuery := `SELECT id, type, priority, status, payload, idempotency_key,max_retries, attempt, current_agent_id, lease_epoch,cancel_requested, result, error,next_run_at, created_at,
					updated_at, version FROM tasks WHERE id = $1 FOR UPDATE`
	task, err := scanTask(tx.QueryRow(ctx, selectQuery, req.TaskID), "callback.select_task", selectQuery, []any{req.TaskID})
	if err != nil {
		return err
	}

	// 校验归属 agent_id /attempt / lease_epoch 必须全部匹配
	if task.CurrentAgentID != req.AgentID {
		return domain.ErrStaleCallback
	}
	if task.Attempt != req.Attempt {
		return domain.ErrStaleCallback
	}
	if task.LeaseEpoch != req.LeaseEpoch {
		return domain.ErrStaleCallback
	}

	// 幂等： 读取当前 attempt记录，判断是否已是终态
	var attemptStatus domain.AttemptStatus
	attemptQuery := `SELECT status FROM task_attempts WHERE task_id = $1 AND attempt = $2`
	attemptArgs := []any{req.TaskID, req.Attempt}
	err = tx.QueryRow(ctx, attemptQuery, attemptArgs...).Scan(&attemptStatus)
	if err != nil {
		return wrapSQLError("callback.get_attempt", attemptQuery, attemptArgs, err)
	}
	if attemptStatus != domain.AttemptStatusRunning {
		if attemptStatus == req.Status {
			return nil
		}
		return domain.ErrStaleCallback
	}

	// 决定 task新状态
	now := time.Now().UTC()
	var newStatus domain.TaskStatus
	var nextRunAt time.Time

	switch req.Status {
	case domain.AttemptStatusSuccess:
		newStatus = domain.TaskStatusSuccess
	case domain.AttemptStatusCancelled:
		newStatus = domain.TaskStatusCancelled
	case domain.AttemptStatusFailed:
		if task.Attempt <= task.MaxRetries {
			newStatus = domain.TaskStatusPending
			nextRunAt = now.Add(nextBackoff(task.Attempt))
		} else {
			newStatus = domain.TaskStatusFailed
		}
	default:
		return domain.ErrInvalidStatusTransition
	}

	// 更新任务
	updateQuery := `UPDATE tasks SET status = $2, result = $3, error = $4,current_agent_id = NULL, next_run_at = $5, updated_at = $6 WHERE id = $1`
	updateArgs := []any{req.TaskID, newStatus, req.Result, req.Error, nextRunAt, now}
	_, err = tx.Exec(ctx, updateQuery, updateArgs...)
	if err != nil {
		return wrapSQLError("callback.update_task", updateQuery, updateArgs, err)
	}
	// 更新 attempt 终态
	updateAttemptQuery := `UPDATE task_attempts SET status = $2, result = $3, error = $4, ended_at = $5 WHERE task_id = $1 AND attempt = $6`
	updateAttemptArgs := []any{req.TaskID, req.Status, req.Result, req.Error, now, req.Attempt}
	_, err = tx.Exec(ctx, updateAttemptQuery, updateAttemptArgs...)
	if err != nil {
		return wrapSQLError("callback.update_attempt", updateAttemptQuery, updateAttemptArgs, err)
	}

	// 删除租约
	deleteLeaseQuery := "DELETE FROM task_leases WHERE task_id = $1"
	_, err = tx.Exec(ctx, deleteLeaseQuery, req.TaskID)
	if err != nil {
		return wrapSQLError("callback.delete_lease", deleteLeaseQuery, []any{req.TaskID}, err)
	}

	// 记录事件
	eventQuery := `INSERT INTO task_events (task_id, event, agent_id, attempt)VALUES ($1, $2, $3, $4)`
	eventArgs := []any{req.TaskID, string(req.Status), req.AgentID, req.Attempt}
	_, err = tx.Exec(ctx, eventQuery, eventArgs...)
	if err != nil {
		return wrapSQLError("callback.insert_event", eventQuery, eventArgs, err)
	}

	//写入 outbox事件
	payload, _ := json.Marshal(map[string]any{
		"status":  req.Status,
		"attempt": req.Attempt,
		"error":   req.Error,
	})
	outboxQuery := `INSERT INTO outbox_events (event_type, task_id, payload) VALUES ($1, $2, $3)`
	outboxArgs := []any{"task." + string(req.Status), req.TaskID, payload}
	_, err = tx.Exec(ctx, outboxQuery, outboxArgs...)
	if err != nil {
		return wrapSQLError("callback.insert_outbox", outboxQuery, outboxArgs, err)
	}
	return wrapSQLError("callback.commit", "", nil, tx.Commit(ctx))
}

// nextBackoff 指数退避 + jitter 上限 10 分钟
func nextBackoff(attempt int) time.Duration {
	base := 5 * time.Second
	max := 10 * time.Minute
	d := base * time.Duration(1<<uint(attempt))
	if d > max {
		d = max
	}
	jitter := time.Duration(rand.Int63n(int64(d)/2 + 1))
	return d/2 + jitter
}
