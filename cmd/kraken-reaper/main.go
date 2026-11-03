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
	"github.com/shenghongio/kraken-orchestrator/internal/lease"
)

const (
	serviceName     = "kraken-reaper"
	shutdownTimeout = 5 * time.Second
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintf(os.Stderr, "%v\n", err)
		os.Exit(1)
	}
}

func run() error {
	reaperConfig, reaperConfigPath, err := bootstrap.LoadConfig("")
	if err != nil {
		return fmt.Errorf("failed to load configuration: %w", err)
	}
	if err := bootstrap.ValidateConfig(reaperConfig); err != nil {
		return fmt.Errorf("failed to validate configuration: %w", err)
	}
	banner.Print(serviceName, reaperConfig)
	reaperLogger := bootstrap.InitLogger(reaperConfig)
	reaperLogger.Info("starting kraken-reaper",
		slog.String("config_path", reaperConfigPath),
		slog.String("log_level", reaperConfig.Log.Level),
		slog.Duration("lease_ttl", reaperConfig.LeaseTTL),
		slog.Duration("interval", reaperConfig.LeaseReaperInterval),
		slog.Duration("shutdown_timeout", shutdownTimeout),
	)
	
	ctx, cancelFunc := context.WithCancel(context.Background())
	defer cancelFunc()
	deps, err := bootstrap.InitDeps(ctx, reaperConfig, reaperLogger)
	if err != nil {
		return fmt.Errorf("failed to initialize dependencies: %w", err)
	}
	defer bootstrap.CloseDeps(deps)
	reaper := lease.NewReaper(deps.Pool, reaperLogger, reaperConfig.LeaseReaperInterval)
	errCh := make(chan error, 1)
	go func() {
		reaperLogger.Info("starting reaper")
		errCh <- reaper.Run(ctx)
	}()
	
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	select {
	case sig := <-sigCh:
		reaperLogger.Info("received shutdown signal", slog.String("signal", sig.String()))
	case err := <-errCh:
		if err != nil {
			reaperLogger.Error("lease reaper error", slog.Any("error", err))
			cancelFunc()
			return fmt.Errorf("lease reaper error: %w", err)
		}
		reaperLogger.Info("lease reaper shutdown")
	}
	cancelFunc()
	reaperLogger.Info("kraken-reaper shutting down", slog.Duration("shutdown_timeout", shutdownTimeout))
	return nil
}
