package sqlite

import (
	"context"
	"database/sql"
	
	"github.com/shenghongio/kraken-orchestrator/internal/domain"
)

// CallbackOutbox 本地回调待发送对列
// 执行完成后先写入outbox ，在发送HTTP回调，保证不丢失
type CallbackOutbox struct {
	ID         int64
	TaskID     domain.TaskID
	Attempt    int
	LeaseEpoch int64
	Status     string
	Result     []byte
	Error      string
	Sent       bool
}

// CallbackOutboxRepository 负责 callback_outbox 表的操作
type CallbackOutboxRepository struct {
	db *sql.DB
}

func NewCallbackOutboxRepository(db *sql.DB) *CallbackOutboxRepository {
	return &CallbackOutboxRepository{db: db}
}

// Enqueue 写入一条待发送的回调
func (r *CallbackOutboxRepository) Enqueue(ctx context.Context, cb *CallbackOutbox) error {
	_, err := r.db.ExecContext(ctx, `INSERT INTO callback_outbox (task_id, attempt, lease_epoch, status, result, error) VALUES (?, ?, ?, ?, ?, ?)`,
		cb.TaskID, cb.Attempt, cb.LeaseEpoch, cb.Status, cb.Result, cb.Error)
	return err
}

// ListPending 列出未房的回调，
func (r *CallbackOutboxRepository) ListPending(ctx context.Context) ([]*CallbackOutbox, error) {
	queryContext, err := r.db.QueryContext(ctx, `SELECT id,task_id, attempt, lease_epoch, status, result, error,sent FROM callback_outbox WHERE sent = 0 ORDER BY created_at ASC`)
	if err != nil {
		return nil, err
	}
	defer queryContext.Close()
	
	var items []*CallbackOutbox
	for queryContext.Next() {
		var cb CallbackOutbox
		if err := queryContext.Scan(&cb.ID, &cb.TaskID, &cb.Attempt, &cb.LeaseEpoch, &cb.Status, &cb.Result, &cb.Error, &cb.Sent); err != nil {
			return nil, err
		}
		items = append(items, &cb)
	}
	return items, queryContext.Err()
}

// MarkSent 标记回调已成功发送
func (r *CallbackOutboxRepository) MarkSent(ctx context.Context, id int64) error {
	_, err := r.db.ExecContext(ctx, `UPDATE callback_outbox SET sent = 1 WHERE id = ?`, id)
	return err
}
