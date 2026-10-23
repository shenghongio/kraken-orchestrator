package main

import (
	"context"
	"embed"
	"fmt"
	"log/slog"
	"sort"
	"strings"
	"time"
	
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/shenghongio/kraken-orchestrator/internal/config"
	"github.com/shenghongio/kraken-orchestrator/internal/dispatch"
	"github.com/shenghongio/kraken-orchestrator/internal/persistence/postgres"
	redispkg "github.com/shenghongio/kraken-orchestrator/internal/persistence/redis"
)

//go:embed migrations/*.sql
var migrationFS embed.FS

func initDatabase(ctx context.Context, cfg *config.SchedulerConfig, logger *slog.Logger) (*pgxpool.Pool, error) {
	dbCtx, dbCancel := context.WithTimeout(ctx, 10*time.Second)
	defer dbCancel()
	logger.Debug("Initializing the database connection pool", slog.String("dsn", cfg.DB.DSN), slog.Int("max_open_conns", cfg.DB.MaxOpenConns), slog.Int("max_idle_conns", cfg.DB.MaxIdleConns),
		slog.Duration("conn_max_lifetime", cfg.DB.ConnMaxLifetime))
	pool, err := postgres.NewPool(dbCtx, cfg.DB.DSN, cfg.DB.MaxOpenConns, cfg.DB.MaxIdleConns, cfg.DB.ConnMaxLifetime)
	if err != nil {
		return nil, fmt.Errorf("initialization database connection pool error: %w", err)
	}
	
	logger.Info("Initialize database connection pool successfully")
	logger.Info("starting database migrations")
	if err := runMigrations(ctx, pool, logger); err != nil {
		pool.Close()
		return nil, err
	}
	logger.Info("database migrations completed successfully")
	return pool, nil
}

// 迁移辅助函数
// 从embed.FS 读取migrations/001_init.sql文件内容
// 调用postgres.RunMigrations() 执行sql （建表）
// 从连接池获取一个DB连接
// runMigrations 按文件名顺序执行所有迁移文件
func runMigrations(ctx context.Context, pool *pgxpool.Pool, logger *slog.Logger) error {
	entries, err := migrationFS.ReadDir("migrations")
	if err != nil {
		return fmt.Errorf("migrations directory: %w", err)
	}
	sort.Slice(entries, func(i, j int) bool {
		return entries[i].Name() < entries[j].Name()
	})
	
	conn, err := pool.Acquire(ctx)
	if err != nil {
		return fmt.Errorf("acquire connection: %w", err)
	}
	defer conn.Release()
	
	for _, entry := range entries {
		if !strings.HasSuffix(entry.Name(), ".sql") {
			continue
		}
		logger.Info("running migration", slog.String("file", entry.Name()))
		sqlBytes, err := migrationFS.ReadFile("migrations/" + entry.Name())
		if err != nil {
			return fmt.Errorf("read migrations failed: %w", err)
		}
		if err := postgres.RunMigrations(ctx, conn.Conn(), string(sqlBytes)); err != nil {
			return fmt.Errorf("run migrations %s: %w", entry.Name(), err)
		}
		logger.Info("migration applied", slog.String("file", entry.Name()))
	}
	return nil
}

/// initRedis 初始化 Redis 客户端。
// Redis 是通知通道而非状态存储，连接失败不阻塞启动，outbox 事件会在恢复后补发。
func initRedis(cfg *config.SchedulerConfig, logger *slog.Logger) *redispkg.Client {
	client := redispkg.NewRedisClient(cfg.Redis.Addr, cfg.Redis.Password, cfg.Redis.DB)
	logger.Info("initializing redis client", slog.String("addr", cfg.Redis.Addr))
	
	pingCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := client.Ping(pingCtx); err != nil {
		logger.Warn("redis ping failed, outbox events will be retried later", slog.Any("error", err))
	} else {
		logger.Info("redis connection established")
	}
	return client
}

// startOutboxDispatcher 启动 Outbox 分发器，后台扫描 outbox_events 并发布到 Redis
func startOutboxDispatcher(ctx context.Context, pool *pgxpool.Pool, redisClient *redispkg.Client, cfg *config.SchedulerConfig, logger *slog.Logger) {
	outboxRepo := postgres.NewOutboxRepository(pool)
	dispatcher := dispatch.NewOutboxDispatcher(outboxRepo, redisClient, logger, cfg.OutboxInterval)
	go dispatcher.Run(ctx)
	logger.Info("outbox dispatcher started", slog.Duration("interval", cfg.OutboxInterval))
}
