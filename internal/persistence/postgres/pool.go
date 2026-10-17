package postgres

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

func NewPool(ctx context.Context, dsn string, maxOpenConns, maxIdleConns int, connMaxLifetime time.Duration) (*pgxpool.Pool, error) {
	parseConfig, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		return nil, err
	}
	parseConfig.MaxConns = int32(maxOpenConns)
	parseConfig.MaxConnLifetime = connMaxLifetime
	parseConfig.MinIdleConns = int32(maxIdleConns)

	pool, err := pgxpool.NewWithConfig(ctx, parseConfig)
	if err != nil {
		return nil, err
	}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, err
	}
	return pool, nil
}
