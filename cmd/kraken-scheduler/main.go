package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	
	"github.com/shenghongio/kraken-orchestrator/internal/banner"
	"github.com/shenghongio/kraken-orchestrator/internal/lease"
	"github.com/shenghongio/kraken-orchestrator/internal/observability"
)

const defaultConfigPath = "./configs/scheduler.yaml"

func main() {
	if err := run(); err != nil {
		fmt.Fprintf(os.Stderr, "%v\n", err)
		os.Exit(1)
	}
}
func run() error {
	
	// 打印启动 banner
	banner.Print("Kraken Scheduler")
	
	// 配置文件初始化
	loadSchedulerConfig, configPath, err := loadConfig()
	if err != nil {
		return err
	}
	// 初始化logger
	logger := observability.InitLogger(loadSchedulerConfig.Log.Level, loadSchedulerConfig.Log.Format)
	logger.Info("scheduler config loaded", slog.String("config_path", configPath),
		slog.String("log_level", loadSchedulerConfig.Log.Level),
		slog.String("log_level", loadSchedulerConfig.Log.Level),
		slog.String("log_format", loadSchedulerConfig.Log.Format),
		slog.String("addr", loadSchedulerConfig.HTTP.Addr))
	
	//  初始化上下文
	ctx, cancelFunc := context.WithCancel(context.Background())
	defer cancelFunc()
	logger.Info("Initialization of the context successfully")
	
	// 初始化数据库连接池并执行迁移
	pool, err := initDatabase(ctx, loadSchedulerConfig, logger)
	if err != nil {
		logger.Error("database initialization failed", observability.ErrAttr(err))
		return err
	}
	defer pool.Close()
	
	// 初始化 Redis（通知通道，失败不阻塞启动）
	redisClient := initRedis(loadSchedulerConfig, logger)
	defer redisClient.Close()
	
	// 启动 Outbox 分发器
	startOutboxDispatcher(ctx, pool, redisClient, loadSchedulerConfig, logger)
	
	/// 启动 lease Reaper（后台回收过期租约）
	reaper := lease.NewReaper(pool, logger, loadSchedulerConfig.LeaseReaperInterval)
	logger.Debug("starting lease reaper", slog.Duration("interval", loadSchedulerConfig.LeaseReaperInterval))
	go reaper.Run(ctx)
	logger.Info("lease reaper started")
	
	// 启动 HTTP Server + 优雅关闭
	return runHTTPServer(ctx, cancelFunc, loadSchedulerConfig, logger, pool)
}
