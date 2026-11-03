package bootstrap

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"time"
	
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/shenghongio/kraken-orchestrator/internal/config"
	"github.com/shenghongio/kraken-orchestrator/internal/observability"
	"github.com/shenghongio/kraken-orchestrator/internal/persistence/postgres"
	redispkg "github.com/shenghongio/kraken-orchestrator/internal/persistence/redis"
)

// 提供三个服务的统一初始化流程
// 配置加载 -> 配置校验 -> 日志初始化 -> 依赖初始化
// 所有服务 (kraken-api,kraken-reaper,kraken-outbox) 保持一致这种初始化格式,避免三个服务各自发展处不同的基础规范

// DefaultConfigPath 默认配置文件
const DefaultConfigPath = "./configs/api.example.yaml"

// getDefaultConfigPath 获取服务对应的默认配置文件
func getDefaultConfigPath(serviceName string) string {
	paths := map[string]string{
		"kraken-api":    "./configs/api.example.yaml",
		"kraken-reaper": "./configs/reaper.example.yaml",
		"kraken-outbox": "./configs/outbox.example.yaml",
	}
	if path, ok := paths[serviceName]; ok {
		return path
	}
	return DefaultConfigPath
}

// Deps 服务依赖，包含初始化后的资源
type Deps struct {
	// Logger 统一日志
	Logger *slog.Logger
	// Pool PostgreSQL 连接池, 使用 pgxpool.Pool 相比线程安全
	Pool *pgxpool.Pool
	// Redis 客户端
	Redis *redispkg.Client
}

// LoadConfig 加载配置文件
// serviceName: 服务名称，用于确定默认配置路径
// defaultPath: 如果不为空，则优先使用此路径
// 加载顺序为: 命令行 flag > 环境变量 > 默认值
func LoadConfig(serviceName, defaultPath string) (*config.SchedulerConfig, string, error) {
	// 如果没有指定配置文件，使用默认值
	if defaultPath == "" {
		defaultPath = getDefaultConfigPath(serviceName)
	}
	// 命令行flag 覆盖配置文件路径
	cfgPath := flag.String("config", defaultPath, "path to kraken-orchestrator config file")
	flag.Parse()
	
	// 环境变量优先级高于默认值，但低于命令行flag,允许通过环境变量指定配置文件
	if !isFlagPassed("config") {
		if envPath := os.Getenv("KRAKEN_SCHEDULER_CONFIG_PATH"); envPath != "" {
			*cfgPath = envPath
		}
	}
	// 使用koanf 加载配置
	koanf, err := config.Load(*cfgPath)
	if err != nil {
		return nil, *cfgPath, fmt.Errorf("load config from %s: %w", *cfgPath, err)
	}
	
	// 解析配置，未设置的字段使用零值或默认值
	cfg := &config.SchedulerConfig{}
	if err := koanf.Unmarshal("", cfg); err != nil {
		return nil, *cfgPath, fmt.Errorf("unmarshal config %w", err)
	}
	return cfg, *cfgPath, nil
}

// ValidateConfig  校验配置是否有效，每个服务可以根据需要添加额外的校验
func ValidateConfig(cfg *config.SchedulerConfig, serviceName string) error {
	// 通用校验 -> 数据库
	if cfg.DB.DSN == "" {
		return fmt.Errorf("db.dsn is required")
	}
	// API校验 服务需要HTTP地址用于监听请求 默认为8080
	if serviceName == "kraken-api" {
		if cfg.HTTP.Addr == "" {
			cfg.HTTP.Addr = ":8080"
		}
		if cfg.HTTP.ReadTimeout == 0 {
			cfg.HTTP.ReadTimeout = 10 * time.Second
		}
		if cfg.HTTP.WriteTimeout == 0 {
			cfg.HTTP.WriteTimeout = 10 * time.Second
		}
	}
	
	// 通用默认值校验
	if cfg.Log.Level == "" {
		cfg.Log.Level = "info"
	}
	if cfg.Log.Format == "" {
		cfg.Log.Format = "json"
	}
	
	// 数据库连接池默认值
	if cfg.DB.MaxOpenConns == 0 {
		cfg.DB.MaxOpenConns = 25
	}
	if cfg.DB.MaxIdleConns == 0 {
		cfg.DB.MaxIdleConns = 5
	}
	if cfg.DB.ConnMaxLifetime == 0 {
		cfg.DB.ConnMaxLifetime = 5 * time.Minute
	}
	
	// Lease 默认值
	if cfg.LeaseTTL == 0 {
		cfg.LeaseTTL = 60 * time.Second
	}
	if cfg.LeaseReaperInterval == 0 {
		cfg.LeaseReaperInterval = 5 * time.Minute
	}
	
	// Outbox 默认值
	if cfg.OutboxInterval == 0 {
		cfg.OutboxInterval = 5 * time.Minute
	}
	return nil
	
}

// InitLogger 初始化日志 自动脱敏敏感字段
func InitLogger(cfg *config.SchedulerConfig) *slog.Logger {
	return observability.InitLogger(cfg.Log.Level, cfg.Log.Format)
}

// InitDeps 初始化依赖 [PostgreSQL + Redis]
// PostgreSQL：权威数据源，连接失败则服务启动失败
// Redis：通知通道，连接失败不阻塞启动（Outbox 事件在恢复后补发
func InitDeps(ctx context.Context, cfg *config.SchedulerConfig, logger *slog.Logger) (*Deps, error) {
	deps := &Deps{
		Logger: logger,
	}
	
	// 初始化 PostgreSQL 连接池,连接失败意味着服务无法正常工作，必须返回错误
	logger.Info("initializing postgres connection pool",
		slog.String("dsn", observability.Redact("dsn", cfg.DB.DSN)),
		slog.Int("max_open_conns", cfg.DB.MaxOpenConns),
		slog.Int("max_idle_conns", cfg.DB.MaxIdleConns),
		slog.Duration("conn_max_lifetime", cfg.DB.ConnMaxLifetime),
	)
	var err error
	deps.Pool, err = postgres.NewPool(
		ctx,
		cfg.DB.DSN,
		cfg.DB.MaxOpenConns,
		cfg.DB.MaxIdleConns,
		cfg.DB.ConnMaxLifetime,
	)
	if err != nil {
		return nil, fmt.Errorf("init postgres connection pool: %w", err)
	}
	logger.Info("initializing redis connection pool")
	
	// 初始化 redis, 设计决策：Outbox 事件在 Redis 恢复后会自动补发
	if cfg.Redis.Addr != "" {
		deps.Redis = redispkg.NewRedisClient(cfg.Redis.Addr, cfg.Redis.Password, cfg.Redis.DB)
		
		// ping检查 redis 连通性,单失败值记录警告,不阻塞启动
		withTimeout, cancelFunc := context.WithTimeout(ctx, 5*time.Second)
		defer cancelFunc()
		if err := deps.Redis.Ping(withTimeout); err != nil {
			logger.Warn("redis unavailable, outbox events will be buffered and retried after redis recovers",
				slog.String("addr", cfg.Redis.Addr),
				slog.Any("error", err),
				slog.String("note", "redis is notification channel only, postgres is authoritative for task state"))
		} else {
			logger.Info("redis connection pool successfully initialized", slog.String("addr", cfg.Redis.Addr))
		}
	}
	return deps, nil
}

// CloseDeps 关闭依赖顺序于初始化顺序相反，保证资源正确释放
func CloseDeps(deps *Deps) {
	// 先关闭 Redis 在关闭 PostgreSQL
	if deps.Redis != nil {
		deps.Redis.Close()
	}
	if deps.Pool != nil {
		deps.Pool.Close()
	}
}

// ShutdownTimeout 优雅关闭超时时间。统一生命周期规范，优雅关闭最多等待超时此时间
const ShutdownTimeout = 10 * time.Second

// isFlagPassed 判断指定 flag 是否被用户显示指定，和判断命令行 flag是否覆盖了默认值或环境变量
func isFlagPassed(name string) bool {
	found := false
	flag.Visit(func(f *flag.Flag) {
		if f.Name == name {
			found = true
		}
	})
	return found
}
