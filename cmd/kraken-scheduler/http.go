package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"syscall"
	"time"
	
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/shenghongio/kraken-orchestrator/internal/config"
	"github.com/shenghongio/kraken-orchestrator/internal/observability"
	krakenhttp "github.com/shenghongio/kraken-orchestrator/internal/transport/http"
)

// 优雅关闭 HTTP server 的超时时间
const httpShutdownTimeout = 10 * time.Second

// runHTTPServer 启动 HTTP server，监听信号和 server 错误，优雅关闭
func runHTTPServer(ctx context.Context, cancel context.CancelFunc, cfg *config.SchedulerConfig, logger *slog.Logger, pool *pgxpool.Pool) error {
	server := krakenhttp.NewServer(logger, cfg, pool)
	logger.Debug("http server created", slog.String("addr", cfg.HTTP.Addr))
	
	// 用 error channel 接收 server.Start() 的错误，避免端口占用等场景下 HTTP 起不来但进程假死
	errChan := make(chan error, 1)
	go func() {
		errChan <- server.Start()
	}()
	logger.Info("http server started", slog.String("addr", cfg.HTTP.Addr))
	
	// 等待 SIGINT/SIGTERM 或 HTTP server 异常退出
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)
	
	select {
	case sig := <-sigChan:
		logger.Info("received shutdown signal", slog.String("signal", sig.String()))
	case err := <-errChan:
		if err != nil {
			logger.Error("http server exited with error", observability.ErrAttr(err))
			cancel()
			return fmt.Errorf("http server failed: %w", err)
		}
		logger.Info("http server exited")
	}
	
	// 先优雅关闭 HTTP server（等在途请求完成），再 cancel 上下文
	logger.Info("shutting down http server", slog.Duration("timeout", httpShutdownTimeout))
	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), httpShutdownTimeout)
	defer shutdownCancel()
	if err := server.Shutdown(shutdownCtx); err != nil {
		logger.Error("http server shutdown error", slog.Any("error", err))
	}
	
	cancel()
	logger.Info("scheduler stopped", slog.String("addr", cfg.HTTP.Addr))
	return nil
}
