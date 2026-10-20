package lease

import (
	"context"
	"log/slog"
	"math/rand"
	"time"
	
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/shenghongio/kraken-orchestrator/internal/domain"
)

// Reaper 定期扫描过期的 task_leases ，将任务回收为 pending（可重试）或 failed (超限)
type Reaper struct {
	pool     *pgxpool.Pool
	logger   *slog.Logger
	interval time.Duration
}

// NewReaper Run 启动租约收割循环，知道ctx 取消
func NewReaper(pool *pgxpool.Pool, logger *slog.Logger, interval time.Duration) *Reaper {
	return &Reaper{pool: pool, logger: logger, interval: interval}
}

// Run 指定收回循环，每隔 interval 扫描一次，直道 ctx 取消
func (r *Reaper) Run(ctx context.Context) {
	ticher := time.NewTicker(r.interval)
	defer ticher.Stop()
	
	r.logger.Info("lease reaper started", slog.Duration("interval", r.interval))
	for {
		select {
		case <-ctx.Done():
			r.logger.Info("lease reaper stopped")
			return
		case <-ticher.C:
			if err := r.sweep(ctx); err != nil {
				r.logger.Error("lease reaper sweep failed", slog.Any("error", err))
			}
		}
	}
	
}

// sweep 扫描过期租约并回收，最多100个租约，逐个回收
func (r *Reaper) sweep(ctx context.Context) error {
	rows, err := r.pool.Query(ctx, `SELECT task_id, agent_id, attempt, lease_epoch FROM task_leases WHERE expires_at < NOW() ORDER BY expires_at ASC LIMIT 100`)
	if err != nil {
		return err
	}
	defer rows.Close()
	type expiredLease struct {
		taskID     domain.TaskID
		agentID    string
		attempt    int
		leaseEpoch int64
	}
	var expired []expiredLease
	for rows.Next() {
		var l expiredLease
		if err := rows.Scan(&l.taskID, &l.agentID, &l.attempt, &l.leaseEpoch); err != nil {
			return err
		}
		expired = append(expired, l)
	}
	if err := rows.Err(); err != nil {
		return err
	}
	if len(expired) == 0 {
		return nil
	}
	for _, l := range expired {
		if err := r.reclaim(ctx, l.taskID, l.agentID, l.attempt, l.leaseEpoch); err != nil {
			r.logger.Error("reclaim lease failed", slog.String("task_id", string(l.taskID)), slog.String("agent_id", l.agentID), slog.Any("error", err))
		}
	}
	return nil
}

// reclaim 回收单个过期租约,回收前会做fencing校验，避免误回收已被重新申领的任务
func (r *Reaper) reclaim(ctx context.Context, taskID domain.TaskID, agentID string, attempt int, leaseEpoch int64) error {
	tx, err := r.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.ReadCommitted})
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	
	// 锁定任务行
	var status domain.TaskStatus
	
	var taskAttempt, maxRetries int
	var taskLeaseEpoch int64
	err = tx.QueryRow(ctx, `SELECT status, attempt, max_retries, lease_epoch FROM tasks WHERE id = $1 FOR UPDATE`,
		taskID).Scan(&status, &taskAttempt, &maxRetries, &taskLeaseEpoch)
	if err != nil {
		return err
	}
	
	// fencing 校验: 如果lease_epoch 不匹配，说明任务已被重新申领，只需删除旧lease
	if taskLeaseEpoch != leaseEpoch {
		_, err := tx.Exec(ctx, `DELETE FROM task_leases WHERE task_id = $1 AND lease_epoch = $2`, taskID, leaseEpoch)
		if err != nil {
			return err
		}
		return tx.Commit(ctx)
	}
	
	// 只有running 状态的任务才需要回收
	if status != domain.TaskStatusRunning {
		_, err := tx.Exec(ctx, `DELETE FROM task_leases WHERE task_id = $1`, taskID)
		if err != nil {
			return err
		}
		return tx.Commit(ctx)
	}
	now := time.Now().UTC()
	var newStatus domain.TaskStatus
	var nextRunAt time.Time
	if taskAttempt <= maxRetries {
		newStatus = domain.TaskStatusPending
		nextRunAt = now.Add(nextBackoff(taskAttempt))
	} else {
		newStatus = domain.TaskStatusFailed
	}
	_, err = tx.Exec(ctx, `UPDATE tasks SET status = $2, current_agent_id = NULL, next_run_at = $3, updated_at = $4 WHERE id = $1`,
		taskID, newStatus, nextRunAt, now)
	if err != nil {
		return err
	}
	_, err = tx.Exec(ctx, `UPDATE task_attempts SET status = 'failed', error = 'lease expired', ended_at = $3 WHERE task_id = $1 AND attempt = $2`,
		taskID, attempt, now)
	if err != nil {
		return err
	}
	_, err = tx.Exec(ctx, `DELETE FROM task_leases WHERE task_id = $1`, taskID)
	if err != nil {
		return err
	}
	_, err = tx.Exec(ctx, `INSERT INTO task_events (task_id, event, agent_id, attempt) VALUES ($1, 'lease_expired', $2, $3)`, taskID, agentID, attempt)
	
	if err != nil {
		return err
	}
	// 写入outbox事件
	_, err = tx.Exec(ctx, `  INSERT INTO outbox_events (event_type, task_id, payload) VALUES ('task.lease_expired', $1, '{}')`, taskID)
	if err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// nextBackoff 与 callback_repo.go 保持一致
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
