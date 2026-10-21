package main

import (
	"context"
	"embed"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"sort"
	"strings"
	"syscall"
	
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/shenghongio/kraken-orchestrator/internal/config"
	"github.com/shenghongio/kraken-orchestrator/internal/lease"
	"github.com/shenghongio/kraken-orchestrator/internal/observability"
	"github.com/shenghongio/kraken-orchestrator/internal/persistence/postgres"
	krakenhttp "github.com/shenghongio/kraken-orchestrator/internal/transport/http"
)

//go:embed migrations/*.sql
var migrationFS embed.FS

const defaultConfigPath = "./configs/scheduler.yaml"

func main() {
	if err := run(); err != nil {
		fmt.Fprintf(os.Stderr, "%v\n", err)
		os.Exit(1)
	}
}
func run() error {
	
	// 解析配置文件路径 flag > env > default
	configFile := flag.String("config", defaultConfigPath, "path to configuration file")
	flag.Parse()
	
	// 如果用户没有显示传 -config 但环境变量 KRAKEN_CONFIG 有值，则用环境变量
	if !isFlagPassed("config") {
		if envPath := os.Getenv("KRAKEN_CONFIG"); envPath != "" {
			*configFile = envPath
		}
	}
	// 加载配置
	loadSchedulerConfig, err := config.LoadSchedulerConfig(*configFile)
	if err != nil {
		return fmt.Errorf("error loading scheduler config: %w", err)
	}
	// 初始化logger
	logger := observability.InitLogger(loadSchedulerConfig.Log.Level, loadSchedulerConfig.Log.Format)
	logger.Info("scheduler started.....")
	logger.Info("scheduler config path", slog.String("log_level", loadSchedulerConfig.Log.Level), slog.String("log_format", loadSchedulerConfig.Log.Format), slog.String("addr", loadSchedulerConfig.HTTP.Addr))
	//  初始化上下文
	ctx, cancelFunc := context.WithCancel(context.Background())
	defer cancelFunc()
	
	// 初始化数据库连接池
	pool, err := postgres.NewPool(
		ctx,
		loadSchedulerConfig.DB.DSN,
		loadSchedulerConfig.DB.MaxOpenConns,
		loadSchedulerConfig.DB.MaxIdleConns,
		loadSchedulerConfig.DB.ConnMaxLifetime,
	)
	if err != nil {
		logger.Error("init db pool", slog.Any("error", err))
		return err
	}
	defer pool.Close()
	
	// 执行迁数据库迁移
	if err := runMigrations(ctx, pool); err != nil {
		logger.Error("run migrations failed", slog.Any("error", err))
		return err
	}
	
	// 启动HTTP Server
	server := krakenhttp.NewServer(logger, loadSchedulerConfig, pool)
	
	// 启动lease Reaper （后台回收过期租约）
	reaper := lease.NewReaper(pool, logger, loadSchedulerConfig.LeaseReaperInterval)
	go reaper.Run(ctx)
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
	
	// 先cancel 停止，reaper 等后台 goroutine
	cancelFunc()
	if err := server.Shutdown(); err != nil {
		logger.Error("shutdown http server", slog.Any("error", err))
	}
	logger.Info("scheduler stopped", slog.String("addr", loadSchedulerConfig.HTTP.Addr))
	return nil
}

// isFlagPasswd 判断指定flag 是否被用户显示设置
func isFlagPassed(name string) bool {
	found := false
	flag.Visit(func(f *flag.Flag) {
		if f.Name == name {
			found = true
		}
	})
	return found
}

// 迁移辅助函数
// 从embed.FS 读取migrations/001_init.sql文件内容
// 调用postgres.RunMigrations() 执行sql （建表）
// 从连接池获取一个DB连接
// runMigrations 按文件名顺序执行所有迁移文件
func runMigrations(ctx context.Context, pool *pgxpool.Pool) error {
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
		sqlBytes, err := migrationFS.ReadFile("migrations/" + entry.Name())
		if err != nil {
			
			return fmt.Errorf("read migrations failed: %w", err)
		}
		if err := postgres.RunMigrations(ctx, conn.Conn(), string(sqlBytes)); err != nil {
			return fmt.Errorf("run migrations %s: %w", entry.Name(), err)
		}
	}
	return nil
}
