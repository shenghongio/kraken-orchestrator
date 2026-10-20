package dispatch

import (
	"context"
	"encoding/json"
	"log/slog"
	"time"

	"github.com/shenghongio/kraken-orchestrator/internal/persistence/postgres"
	redispkg "github.com/shenghongio/kraken-orchestrator/internal/persistence/redis"
)

// OutboxDispatcher 定期扫描 outbox_events表，将未发布的事件发送到Redis
// Redis不可用时不删除事件，下次重试，保证通知不会丢失
type OutboxDispatcher struct {
	outboxRepo *postgres.OutboxRepository
	redis      *redispkg.Client
	logger     *slog.Logger
	interval   time.Duration
}

func NewOutboxDispatcher(outboxRepo *postgres.OutboxRepository, redis *redispkg.Client, logger *slog.Logger, interval time.Duration) *OutboxDispatcher {
	return &OutboxDispatcher{
		outboxRepo: outboxRepo,
		redis:      redis,
		logger:     logger,
		interval:   interval,
	}
}

// Run 开始循环分发，直到ctx取消
func (d *OutboxDispatcher) Run(ctx context.Context) {
	ticker := time.NewTicker(d.interval)
	defer ticker.Stop()
	d.logger.Info("outbox dispatcher started", slog.Duration("interval", d.interval))
	for {
		select {
		case <-ctx.Done():
			d.logger.Info("outbox dispatcher shutting down")
			return
		case <-ticker.C:
			if err := d.dispatch(ctx); err != nil {
				d.logger.Error("outbox dispatcher failed", slog.Any("error", err))
			}

		}
	}
}

// dispatch 扫描未发布的事件并发送到Redis
func (d *OutboxDispatcher) dispatch(ctx context.Context) error {
	events, err := d.outboxRepo.ListPending(ctx, 100)
	if err != nil {
		return err
	}
	for _, event := range events {
		// 构造通知消息
		msg := map[string]any{
			"event_type": event.EventType,
			"task_id":    event.TaskID,
			// json.RawMessage 底层是 []byte，event.Payload 也是 []byte。用 {} 是复合字面量（构造切片），结果变成了 [][]byte（一个元素为 []byte 的切片），类型不匹配。
			"payload": json.RawMessage(event.Payload),
		}
		data, err := json.Marshal(msg)
		if err != nil {
			d.logger.Error("marshal outbox event faild", slog.Int64("event_id", event.ID), slog.Any("error", err))
			continue
		}

		// 发送到 Redis ,channel 名为 kraken:events
		if err := d.redis.Publish(ctx, "kraken:events", data); err != nil {
			// redis服务连接异常时只记录日志，不标记已发布，下次重试
			d.logger.Error("publish to redis failed , will retry", slog.Int64("event_id", event.ID), slog.Any("error", err))
			continue
		}

		// 发送成功后标记为已发布
		if err := d.outboxRepo.MarkPublished(ctx, event.ID); err != nil {
			d.logger.Error("mark outbox published failed", slog.Int64("event_id", event.ID), slog.Any("error", err))
		}
	}
	return nil
}
