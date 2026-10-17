package postgres

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/shenghongio/kraken-orchestrator/internal/domain"
)

type TaskRepository struct {
	pool *pgxpool.Pool
}

func NewTaskRepository(pool *pgxpool.Pool) *TaskRepository {
	return &TaskRepository{pool: pool}
}

// Create 插入新的任务，事物内幂等，如果 idempotency_key 已经存在，返回已存在的任务
func (r *TaskRepository) Create(ctx context.Context, task *domain.Task) error {
	utc := time.Now().UTC()
	task.CreatedAt = utc
	task.UpdatedAt = utc
	task.Status = domain.TaskStatusPending
	task.Version = 1

	// 业务层不关心 payload 时也能正常插入
	if len(task.Payload) == 0 {
		task.Payload = []byte("{}")
	}

	_, err := r.pool.Exec(ctx, `
		INSERT INTO tasks (
			id,type,priority, status, payload,idempotency_key,
			max_retries,attempt,lease_epoch,cancel_requested,
			next_run_at,created_at,updated_at,version
			)VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14)
       `,
		task.ID, task.Type, task.Priority, task.Status, task.Payload, task.IdempotencyKey,
		task.MaxRetries, task.Attempt, task.LeaseEpoch, task.CancelRequested,
		task.NextRunAt, task.CreatedAt, task.UpdatedAt, task.Version,
	)
	return mapError(err)
}

// GetByID 按ID查询
func (r *TaskRepository) GetByID(ctx context.Context, id domain.TaskID) (*domain.Task, error) {
	return scanTask(r.pool.QueryRow(ctx, `
		SELECT id, type, priority, status, payload, idempotency_key,
		max_retries, attempt,current_agent_id,lease_epoch,
		cancel_requested,result,error,
		next_run_at,created_at,updated_at,version
		FROM tasks WHERE id = $1`, id))
}

// GetByIdempotencyKey 幂等查询
func (r *TaskRepository) GetByIdempotencyKey(ctx context.Context, idempotencyKey string) (*domain.Task, error) {
	return scanTask(r.pool.QueryRow(ctx, `
			SELECT id,type,priority,status,payload,idempotency_key,
			max_retries,attempt,current_agent_id,lease_epoch,
			cancel_requested,result,error,
			next_run_at,created_at,updated_at,version
			FROM tasks WHERE idempotency_key = $1`, idempotencyKey))
}

// List 简单列表，Phase 1 不加分页
func (r *TaskRepository) List(ctx context.Context) ([]*domain.Task, error) {
	rows, err := r.pool.Query(ctx, `
			SELECT id, type, priority, status, payload, idempotency_key,
			max_retries, attempt,current_agent_id,lease_epoch,
			cancel_requested,result,error,
			next_run_at,created_at,updated_at,version
			FROM tasks ORDER BY created_at DESC`)
	if err != nil {
		return nil, mapError(err)
	}
	defer rows.Close()
	var tasks []*domain.Task
	for rows.Next() {
		t, err := scanTask(rows)
		if err != nil {
			return nil, err
		}
		tasks = append(tasks, t)
	}
	return tasks, rows.Err()
}

// RequestCancel 设置取消标记，乐观锁更新
func (r *TaskRepository) RequestCancel(ctx context.Context, id domain.TaskID) error {
	tag, err := r.pool.Exec(ctx, `UPDATE tasks SET cancel_requested = TRUE,updated_at = $2 WHERE id = $1 AND status IN ('pending','running')`, id, time.Now().UTC())
	if err != nil {
		return mapError(err)
	}
	if tag.RowsAffected() == 0 {
		return domain.ErrNotFound
	}
	return nil
}

// scanTask 同时支持pgx.Row 和 pgx.Rows
type rowScanner interface {
	Scan(dest ...any) error
}

func scanTask(s rowScanner) (*domain.Task, error) {
	var t domain.Task
	var currentAgentID, errMsg *string
	var result []byte
	var nextRunAt time.Time

	err := s.Scan(
		&t.ID, &t.Type, &t.Priority, &t.Status, &t.Payload, &t.IdempotencyKey,
		&t.MaxRetries, &t.Attempt, &currentAgentID, &t.LeaseEpoch,
		&t.CancelRequested, &result, &errMsg,
		&nextRunAt, &t.CreatedAt, &t.UpdatedAt, &t.Version,
	)
	if err != nil {
		return nil, mapError(err)
	}
	if currentAgentID != nil {
		t.CurrentAgentID = *currentAgentID
	}
	t.Result = result
	if errMsg != nil {
		t.Error = *errMsg
	}
	t.NextRunAt = nextRunAt
	return &t, nil
}
