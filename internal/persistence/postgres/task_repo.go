package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/shenghongio/kraken-orchestrator/internal/domain"
)

type TaskRepository struct {
	pool     *pgxpool.Pool
	leaseTTL time.Duration
}

// ClaimResult 申领结果
type ClaimResult struct {
	Task       *domain.Task
	Attempt    int
	LeaseEpoch int64
}

func NewTaskRepository(pool *pgxpool.Pool, leaseTTL time.Duration) *TaskRepository {
	return &TaskRepository{pool: pool, leaseTTL: leaseTTL}
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

	// 空字符串的 idempotency_key 转成 nil，否则唯一索引会把多个 "" 视为冲突
	var idempotencyKey any
	if task.IdempotencyKey != "" {
		idempotencyKey = task.IdempotencyKey
	}

	if task.IdempotencyKey == "" {
		query := `INSERT INTO tasks (id,type,priority, status, payload,idempotency_key, max_retries,attempt,lease_epoch,cancel_requested,next_run_at,created_at,updated_at,version)VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14)`
		args := []any{task.ID, task.Type, task.Priority, task.Status, task.Payload, idempotencyKey, task.MaxRetries, task.Attempt, task.LeaseEpoch, task.CancelRequested, task.NextRunAt, task.CreatedAt, task.UpdatedAt, task.Version}
		_, err := r.pool.Exec(ctx, query, args...)
		return wrapSQLError("task.create", query, args, err)
	}

	// 有 idempotency_key: on conflict do nothing
	query := `INSERT INTO tasks (id,type,priority, status, payload,idempotency_key, max_retries,attempt,lease_epoch,cancel_requested,next_run_at,created_at,updated_at,version)VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14),
			  ON CONFLICT (idempotency_key) WHERE idempotency_key IS NOT NULL DO NOTHING`
	args := []any{
		task.ID, task.Type, task.Priority, task.Status, task.Payload, idempotencyKey, task.MaxRetries, task.Attempt, task.LeaseEpoch, task.CancelRequested,
		task.NextRunAt, task.CreatedAt, task.UpdatedAt, task.Version,
	}
	tag, err := r.pool.Exec(ctx, query, args...)
	if err != nil {
		return wrapSQLError("task.create", query, args, err)
	}
	if tag.RowsAffected() == 1 {
		return nil // 插入成功
	}
	// 冲突,已存在,查处已有任务会填
	existing, err := r.GetByIdempotencyKey(ctx, task.IdempotencyKey)
	if err != nil {
		return err
	}
	*task = *existing
	return nil
}

// GetByID 按ID查询
func (r *TaskRepository) GetByID(ctx context.Context, id domain.TaskID) (*domain.Task, error) {
	query := `SELECT id, type, priority, status, payload, idempotency_key, max_retries, attempt,current_agent_id,lease_epoch,cancel_requested,result,error,next_run_at,created_at,updated_at,version FROM tasks WHERE id = $1`
	return scanTask(r.pool.QueryRow(ctx, query, id), "task.get_by_id", query, []any{id})
}

// GetByIdempotencyKey 幂等查询
func (r *TaskRepository) GetByIdempotencyKey(ctx context.Context, idempotencyKey string) (*domain.Task, error) {
	query := `SELECT id,type,priority,status,payload,idempotency_key, max_retries,attempt,current_agent_id,lease_epoch, cancel_requested,result,error,next_run_at,created_at,updated_at,version FROM tasks WHERE idempotency_key = $1`
	return scanTask(r.pool.QueryRow(ctx, query, idempotencyKey), "task.get_by_idempotency_key", query, []any{idempotencyKey})
}

// List 简单列表，Phase 1 不加分页
func (r *TaskRepository) List(ctx context.Context) ([]*domain.Task, error) {
	query := `SELECT id, type, priority, status, payload, idempotency_key, max_retries, attempt,current_agent_id,lease_epoch,cancel_requested,result,error, next_run_at,created_at,updated_at,version FROM tasks ORDER BY created_at DESC`
	rows, err := r.pool.Query(ctx, query)
	if err != nil {
		return nil, wrapSQLError("task.list", query, nil, err)
	}
	defer rows.Close()
	var tasks []*domain.Task
	for rows.Next() {
		t, err := scanTask(rows, "task.list.scan", query, nil)
		if err != nil {
			return nil, err
		}
		tasks = append(tasks, t)
	}
	return tasks, rows.Err()
}

// RequestCancel 设置取消标记，只允许取消 pending/running 且尚未请求取消的任务
// 已取消或已终止的任务返回 ErrNotFound
func (r *TaskRepository) RequestCancel(ctx context.Context, id domain.TaskID) error {
	query := `UPDATE tasks SET cancel_requested = TRUE,updated_at = $2 WHERE id = $1 AND status IN ('pending','running') AND cancel_requested = FALSE`
	args := []any{id, time.Now().UTC()}
	tag, err := r.pool.Exec(ctx, query, args...)
	if err != nil {
		return wrapSQLError("task.cancel", query, args, err)
	}
	// RowsAffected == 0 说明任务不存在、已终止、或已请求取消
	if tag.RowsAffected() == 0 {
		return domain.ErrNotFound
	}
	return nil
}

// ClaimTask 原子申领任务： 在一个事务内锁定任务，递增attempt/lease_epoch 写入attempts 和leases
func (r *TaskRepository) ClaimTask(ctx context.Context, agentID string) (*ClaimResult, error) {
	tx, err := r.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.ReadCommitted})
	if err != nil {
		return nil, wrapSQLError("task.claim.begin_tx", "", nil, err)
	}
	defer tx.Rollback(ctx)

	var taskID domain.TaskID
	selectQuery := `SELECT id FROM tasks WHERE status = 'pending' AND cancel_requested = FALSE AND next_run_at <= NOW()
						ORDER BY priority DESC, created_at ASC LIMIT 1 FOR UPDATE SKIP LOCKED`
	err = tx.QueryRow(ctx, selectQuery).Scan(&taskID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, nil
		}
		return nil, wrapSQLError("task.claim.select_pending", selectQuery, nil, err)
	}
	now := time.Now().UTC()
	expiresAt := now.Add(r.leaseTTL)
	var attempt int
	var leaseEpoch int64

	updateTaskQuery := `UPDATE tasks SET status = 'running', attempt = attempt + 1, lease_epoch = lease_epoch + 1, current_agent_id = $2,
						updated_at = $3 WHERE id = $1 RETURNING attempt, lease_epoch`
	updateTaskArgs := []any{taskID, agentID, now}
	err = tx.QueryRow(ctx, updateTaskQuery, updateTaskArgs...).Scan(&attempt, &leaseEpoch)
	if err != nil {
		return nil, wrapSQLError("task.claim.update_task", updateTaskQuery, updateTaskArgs, err)
	}

	insertAttemptQuery := `INSERT INTO task_attempts (task_id, agent_id, attempt, lease_epoch, status, started_at) VALUES ($1, $2, $3, $4, 'running',$5)`
	insertAttemptArgs := []any{taskID, agentID, attempt, leaseEpoch, now}
	_, err = tx.Exec(ctx, insertAttemptQuery, insertAttemptArgs...)
	if err != nil {
		return nil, wrapSQLError("task.claim.insert_attempt", insertAttemptQuery, insertAttemptArgs, err)
	}

	insertLeaseQuery := `INSERT INTO task_leases (task_id, agent_id, attempt, lease_epoch, expires_at, renewed_at) VALUES ($1, $2, $3, $4, $5, $6)
						  ON CONFLICT (task_id) DO UPDATE SET agent_id = EXCLUDED.agent_id, attempt = EXCLUDED.attempt,lease_epoch = EXCLUDED.lease_epoch,
                          expires_at = EXCLUDED.expires_at,renewed_at = EXCLUDED.renewed_at`
	insertLeaseArgs := []any{taskID, agentID, attempt, leaseEpoch, expiresAt, now}

	_, err = tx.Exec(ctx, insertLeaseQuery, insertLeaseArgs...)
	if err != nil {
		return nil, wrapSQLError("task.claim.insert_lease", insertLeaseQuery, insertLeaseArgs, err)
	}

	insertEventQuery := `INSERT INTO task_events (task_id, event, agent_id, attempt)VALUES ($1, 'claimed', $2, $3)`
	insertEventArgs := []any{taskID, agentID, attempt}
	_, err = tx.Exec(ctx, insertEventQuery, insertEventArgs...)
	if err != nil {
		return nil, wrapSQLError("task.claim.insert_event", insertEventQuery, insertEventArgs, err)
	}

	// 写入outbox事件，与任务状态变更在同一个事务内
	payload, _ := json.Marshal(map[string]any{
		"attempt":     attempt,
		"lease_epoch": leaseEpoch,
		"agent_id":    agentID,
	})
	insertOutboxQuery := `INSERT INTO outbox_events (event_type, task_id, payload) VALUES ('task.claimed', $1, $2)`
	insertOutboxArgs := []any{taskID, payload}
	_, err = tx.Exec(ctx, insertOutboxQuery, insertOutboxArgs...)
	if err != nil {
		return nil, wrapSQLError("task.claim.insert_outbox", insertOutboxQuery, insertOutboxArgs, err)
	}

	if err := tx.Commit(ctx); err != nil {
		return nil, wrapSQLError("task.claim.commit", "", nil, err)
	}
	byID, err := r.GetByID(ctx, taskID)
	if err != nil {
		return nil, err
	}
	return &ClaimResult{
		Task:       byID,
		Attempt:    attempt,
		LeaseEpoch: leaseEpoch,
	}, nil
}

// RenewLease 续期租约，lease 过期或不匹配返回 ErrLeaseExpired
func (r *TaskRepository) RenewLease(ctx context.Context, taskID domain.TaskID, agentID string, attempt int, leaseEpoch int64) error {
	now := time.Now().UTC()
	expiresAt := now.Add(r.leaseTTL)
	query := `UPDATE task_leases SET expires_at = $5, renewed_at = $6 WHERE task_id = $1 AND agent_id = $2 AND attempt = $3 AND lease_epoch = $4 AND expires_at > NOW()`
	args := []any{taskID, agentID, attempt, leaseEpoch, expiresAt, now}
	tag, err := r.pool.Exec(ctx, query, args)
	if err != nil {
		return wrapSQLError("task.renew_lease", query, args, err)
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

func scanTask(s rowScanner, operation, query string, args []any) (*domain.Task, error) {
	var t domain.Task
	var currentAgentID, errMsg, idempotencyKey *string
	var result []byte
	var nextRunAt time.Time

	err := s.Scan(
		&t.ID, &t.Type, &t.Priority, &t.Status, &t.Payload, &idempotencyKey,
		&t.MaxRetries, &t.Attempt, &currentAgentID, &t.LeaseEpoch,
		&t.CancelRequested, &result, &errMsg,
		&nextRunAt, &t.CreatedAt, &t.UpdatedAt, &t.Version,
	)
	if err != nil {
		return nil, wrapSQLError(operation, query, args, err)
	}
	if currentAgentID != nil {
		t.CurrentAgentID = *currentAgentID
	}
	if idempotencyKey != nil {
		t.IdempotencyKey = *idempotencyKey
	}
	t.Result = result
	if errMsg != nil {
		t.Error = *errMsg
	}
	t.NextRunAt = nextRunAt
	return &t, nil
}
