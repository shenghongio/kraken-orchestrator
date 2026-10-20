package redis

import (
	"context"

	"github.com/redis/go-redis/v9"
)

// Client 封装 redis 连接，目前仅用于 pub/sub 通知
type Client struct {
	rbd *redis.Client
}

func NewRedisClient(addr string) *Client {
	return &Client{
		rbd: redis.NewClient(&redis.Options{
			Addr: addr,
		}),
	}
}

// Publish 向指定 channel 发布消息
// Redis服务故障不应该影响任务状态，调用处需要处理错误
func (c *Client) Publish(ctx context.Context, channel string, payload []byte) error {
	return c.rbd.Publish(ctx, channel, payload).Err()
}

// Close 关闭redis 连接
func (c *Client) Close() error {
	return c.rbd.Close()
}
