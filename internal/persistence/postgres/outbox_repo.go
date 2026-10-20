package postgres

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/shenghongio/kraken-orchestrator/internal/domain"
)

// OutboxEvent 表示一条待发送的通知事件
// 与业务状态变更在同一事务内写入，保证"状态变更"和"通知意图"的原子性
type OutboxEvent struct {
	ID          int64
	EventType   string
	TaskID      domain.TaskID
	Payload     []byte
	Published   bool
	CreatedAt   time.Time
	PublishedAt *time.Time
}

// OutboxRepository 负责outbox_events 表的操作
type OutboxRepository struct {
	pool *pgxpool.Pool
}

func NewOutboxRepository(pool *pgxpool.Pool) *OutboxRepository {
	return &OutboxRepository{pool: pool}
}

// Create 在指定事务那写入一条 outbox 事件
// 必须传入 tx,确保业务状态变更在同一事务中提交
func (repo *OutboxRepository) Create(ctx context.Context, tx pgx.Tx, eventType string, taskID domain.TaskID, payload []byte) error {
	_, err := tx.Exec(ctx, `INSERT INTO outbox_events (event_type, task_id, payload) VALUES ($1, $2, $3)`, eventType, taskID, payload)
	return err
}

// ListPending 查询未发布的事件，按创建时间升序
func (repo *OutboxRepository) ListPending(ctx context.Context, limit int) ([]*OutboxEvent, error) {
	rows, err := repo.pool.Query(ctx, `SELECT id, event_type, task_id, payload, published, created_at, published_at FROM outbox_events WHERE published = FALSE ORDER BY created_at ASC LIMIT $1`,
		limit)
	if err != nil {
		return nil, mapError(err)
	}
	defer rows.Close()

	var events []*OutboxEvent
	for rows.Next() {
		var event OutboxEvent
		if err := rows.Scan(&event.ID, &event.EventType, &event.TaskID, &event.Payload, &event.Published, &event.CreatedAt, &event.PublishedAt); err != nil {
			return nil, mapError(err)
		}
		events = append(events, &event)
	}
	return events, rows.Err()
}

// MarkPublished 标记事件已发布
func (repo *OutboxRepository) MarkPublished(ctx context.Context, id int64) error {
	now := time.Now().UTC()
	_, err := repo.pool.Exec(ctx, `UPDATE outbox_events SET published = TRUE, published_at = $2 WHERE id = $1`, id, now)
	return mapError(err)
}
