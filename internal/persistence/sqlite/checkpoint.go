package sqlite

import (
	"context"
	"database/sql"
	"time"

	"github.com/shenghongio/kraken-orchestrator/internal/domain"
)

// CheckpointStatus 本地任务状态
type CheckpointStatus string

const (
	CheckpointStatusInRunning   CheckpointStatus = "running"
	CheckpointStatusInCompleted CheckpointStatus = "completed"
	CheckpointStatusInAbandoned CheckpointStatus = "abandoned"
)

// Checkpoint 表示agent 本地持久化的任务执行状态
type Checkpoint struct {
	TaskID     domain.TaskID
	Attempt    int
	LeaseEpoch int64
	Status     CheckpointStatus
	Payload    []byte
	Checkpoint []byte
	UpdatedAt  time.Time
}

// CheckpointRepository 负责 checkpoints 表的操作
type CheckpointRepository struct {
	db *sql.DB
}

func NewCheckpointRepository(db *sql.DB) *CheckpointRepository {
	return &CheckpointRepository{db: db}
}

// Save  写入或更新 checkpoint 表，执行开始前必须调用该方法,检查
func (r *CheckpointRepository) Save(ctx context.Context, cp *Checkpoint) error {
	_, err := r.db.ExecContext(ctx, `
		INSERT INTO checkpoints (task_id, attempt, lease_epoch, status, payload,checkpoint,updated_at) VALUES (?,?,?,?,?,?,?) ON  CONFLICT(task_id) DO UPDATE SET
		attempt = excluded.attempt,
		lease_epoch = excluded.lease_epoch,
		status = excluded.status,
		payload = excluded.payload,
		checkpoint = excluded.checkpoint,
		updated_at = excluded.updated_at`, cp.TaskID, cp.Attempt, cp.LeaseEpoch, cp.Status, cp.Payload, cp.Checkpoint, time.Now().UTC())
	return err
}

// Get 按task_id 查询 checkpoint表 不存在返回 (nil,nil)
func (r *CheckpointRepository) Get(ctx context.Context, taskID domain.TaskID) (*Checkpoint, error) {
	var cp Checkpoint
	err := r.db.QueryRowContext(ctx, `SELECT task_id,attempt,lease_epoch, status, payload, checkpoint, updated_at FROM checkpoints WHERE task_id = ?`, taskID).Scan(&cp.TaskID, &cp.Attempt, &cp.LeaseEpoch, &cp.Status, &cp.Payload, &cp.Checkpoint, &cp.UpdatedAt)
	if err != nil {
		return nil, err
	}
	return &cp, nil
}

// ListRunning  列出所有status=running 的checkpoint 用于重启对账
func (r *CheckpointRepository) ListRunning(ctx context.Context) ([]*Checkpoint, error) {
	row, err := r.db.QueryContext(ctx, `SELET task_id,attempt,lease_epoch,status,payload, checkpoint,updated_at FROM checkpoint WHERE status = 'running'`)
	if err != nil {
		return nil, err
	}

	defer func(row *sql.Rows) {
		err := row.Close()
		if err != nil {

		}
	}(row)

	var cps []*Checkpoint
	for row.Next() {
		var cp Checkpoint
		if err := row.Scan(&cp.TaskID, &cp.Attempt, &cp.LeaseEpoch, &cp.Status, &cp.Payload, &cp.Checkpoint, &cp.UpdatedAt); err != nil {
			return nil, err
		}
		cps = append(cps, &cp)
	}
	return cps, row.Err()
}

// Delete 删除 checkpoint （任务完全结束后清理）
func (r *CheckpointRepository) Delete(ctx context.Context, taskID domain.TaskID) error {
	_, err := r.db.ExecContext(ctx, `DELETE FROM checkpoint WHERE task_id = ?`, taskID)
	return err

}
