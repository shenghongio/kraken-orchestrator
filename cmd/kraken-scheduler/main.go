package main

import (
	"context"
	"embed"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"syscall"
	
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/shenghongio/kraken-orchestrator/internal/config"
	"github.com/shenghongio/kraken-orchestrator/internal/observability"
	"github.com/shenghongio/kraken-orchestrator/internal/persistence/postgres"
	krakenhttp "github.com/shenghongio/kraken-orchestrator/internal/transport/http"
)

//go:embed migrations/*.sql
var migrationFS embed.FS

func main() {
	getenvConfigFile := os.Getenv("KRAKEN_CONFIG")
	loadSchedulerConfig, err := config.LoadSchedulerConfig(getenvConfigFile)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error loading scheduler config: %v\n", err)
		os.Exit(1)
	}
	
	logger := observability.InitLogger(loadSchedulerConfig.Log.Level, loadSchedulerConfig.Log.Format)
	logger.Info("scheduler started", slog.String("addr", loadSchedulerConfig.HTTP.Addr), slog.String("db_dsn", observability.Redact("dsn", loadSchedulerConfig.DB.DSN)))
	ctx, cancelFunc := context.WithCancel(context.Background())
	defer cancelFunc()
	pool, err := postgres.NewPool(
		ctx,
		loadSchedulerConfig.DB.DSN,
		loadSchedulerConfig.DB.MaxOpenConns,
		loadSchedulerConfig.DB.MaxIdleConns,
		loadSchedulerConfig.DB.ConnMaxLifetime,
	)
	if err != nil {
		logger.Error("init db pool", slog.Any("error", err))
		os.Exit(1)
	}
	defer pool.Close()
	// 执行迁移
	if err := runMigrations(ctx, pool); err != nil {
		logger.Error("run migrations failed", slog.Any("error", err))
		os.Exit(1)
	}
	
	server := krakenhttp.NewServer(logger, loadSchedulerConfig.HTTP, pool)
	go func() {
		if err := server.Start(); err != nil {
			logger.Error("start http server", slog.Any("error", err))
		}
	}()
	
	// 优雅关闭: 等待 SIGINT/SIGTERM
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)
	sig := <-sigChan
	logger.Info("shutdown down", slog.String("signal", sig.String()))
	
	if err := server.Shutdown(); err != nil {
		logger.Error("shutdown http server", slog.Any("error", err))
	}
	logger.Info("scheduler stopped", slog.String("addr", loadSchedulerConfig.HTTP.Addr))
}

// 迁移辅助函数

// 从embed.FS 读取migrations/001_init.sql文件内容
// 从连接池获取一个DB连接
// 调用postgres.RunMigrations() 执行sql （建表）
func runMigrations(ctx context.Context, pool *pgxpool.Pool) error {
	sqlBytes, err := migrationFS.ReadFile("migrations/001_init.sql")
	if err != nil {
		return fmt.Errorf("read migrations failed: %w", err)
	}
	conn, err := pool.Acquire(ctx)
	if err != nil {
		return fmt.Errorf("acquire connection failed: %w", err)
	}
	defer conn.Release()
	return postgres.RunMigrations(ctx, conn.Conn(), string(sqlBytes))
}
