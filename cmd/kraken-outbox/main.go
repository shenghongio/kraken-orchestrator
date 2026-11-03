package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"syscall"
	"time"
	
	"github.com/shenghongio/kraken-orchestrator/internal/banner"
	"github.com/shenghongio/kraken-orchestrator/internal/bootstrap"
	"github.com/shenghongio/kraken-orchestrator/internal/dispatch"
	"github.com/shenghongio/kraken-orchestrator/internal/persistence/postgres"
)

const (
	serviceName     = "kraken-outbox"
	shutdownTimeout = 5 * time.Second
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintf(os.Stderr, "%v\n", err)
		os.Exit(1)
	}
}

func run() error {
	cfg, configPath, err := bootstrap.LoadConfig(serviceName, "")
	if err != nil {
		return fmt.Errorf("failed to load configuration: %w", err)
	}
	
	if err := bootstrap.ValidateConfig(cfg, serviceName); err != nil {
		return fmt.Errorf("failed to validate configuration: %w", err)
	}
	banner.Print(serviceName, cfg)
	logger := bootstrap.InitLogger(cfg)
	logger.Info("starting", slog.String("service", serviceName),
		slog.String("config_path", configPath),
		slog.String("log_level", cfg.Log.Level),
		slog.Duration("outbox_interval", cfg.OutboxInterval),
		slog.Duration("shutdown_timeout", shutdownTimeout),
	)
	
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	
	deps, err := bootstrap.InitDeps(ctx, cfg, logger, serviceName)
	if err != nil {
		return fmt.Errorf("init dependencies: %w", err)
	}
	defer bootstrap.CloseDeps(deps)
	
	outboxRepo := postgres.NewOutboxRepository(deps.Pool)
	dispatcher := dispatch.NewOutboxDispatcher(outboxRepo, deps.Redis, logger, cfg.OutboxInterval)
	
	errCh := make(chan error, 1)
	go func() {
		logger.Info("outbox dispatcher started")
		errCh <- dispatcher.Run(ctx)
	}()
	
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)
	
	select {
	case sig := <-sigChan:
		logger.Info("received shutdown signal", slog.String("signal", sig.String()))
	case err := <-errCh:
		if err != nil {
			logger.Error("outbox dispatcher error", slog.Any("error", err))
			cancel()
			return fmt.Errorf("outbox dispatcher error: %w", err)
		}
		logger.Info("outbox dispatcher stopped")
	}
	
	cancel()
	logger.Info("stopped", slog.String("service", serviceName), slog.Duration("shutdown_timeout", shutdownTimeout))
	return nil
}
