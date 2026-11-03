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
	"github.com/shenghongio/kraken-orchestrator/internal/transport/http"
)

// 功能职责：
// - 提供 HTTP API（任务管理、Agent 协议、健康检查）
// - 不包含 Reaper 和 Outbox 后台任务（这两个是独立服务）
//
// 启动流程（Phase 1 统一规范）：
// 1. Load Config     -> 加载配置文件
// 2. Validate Config -> 校验配置有效性
// 3. Init Logger     -> 初始化日志（slog + 敏感信息脱敏）
// 4. Init Deps       -> 初始化 PostgreSQL + Redis 依赖
// 5. Build Server    -> 构建 HTTP Server（路由 + 中间件）
// 6. Start Server    -> 启动 HTTP Server
// 7. Wait Signal     -> 等待 SIGTERM/SIGINT
// 8. Graceful Shutdown -> 优雅关闭（停止接受新请求，等待在途请求完成）
//
// HTTP API 端点：
// - GET  /healthz           -> 健康检查（进程存活）
// - GET  /readyz            -> 就绪检查（数据库可用）
// - POST /v1/tasks          -> 创建任务（幂等）
// - GET  /v1/tasks          -> 列出任务
// - GET  /v1/tasks/{id}     -> 获取单个任务
// - POST /v1/tasks/{id}/cancel -> 请求取消任务
// - POST /v1/agents/register   -> Agent 注册
// - POST /v1/agents/heartbeat  -> Agent 心跳
// - POST /v1/tasks/claim       -> Agent 申领任务
// - POST /v1/tasks/callback    -> Agent 回调

const (
	// serviceName 服务名称，用于和日志banner
	serviceName = "kraken-api"
	// 优雅关闭超时，等待回收的HTTP请求完成的最长时间
	shutdownTimeout = 10 * time.Second
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintf(os.Stderr, "%v\n", err)
		os.Exit(1)
	}
}

// run 服务入口
func run() error {
	
	// 加载配置
	// 配置加载顺序：默认值 -> YAML 文件 -> 环境变量 -> 命令行 flag
	// 环境变量格式：KRAKEN_HTTP__ADDR -> http.addr
	// 命令行覆盖：./kraken-api -config /path/to/config.yaml
	apiConfig, apiConfigPath, err := bootstrap.LoadConfig(serviceName, "")
	if err != nil {
		return fmt.Errorf("file load to configuration: %w", err)
	}
	
	// 校验配置，如果配置校验失败，不允许启动服务
	if err := bootstrap.ValidateConfig(apiConfig, serviceName); err != nil {
		return fmt.Errorf("bootstrap.ValidateConfig: %w", err)
	}
	// 打印启动 banner
	banner.Print(serviceName, apiConfig)
	// 初始化日志
	apiLogger := bootstrap.InitLogger(apiConfig)
	apiLogger.Info("Starting kraken-api  server",
		slog.String("config_path", apiConfigPath),
		slog.String("log_level", apiConfig.Log.Level),
		slog.String("log_format", apiConfig.Log.Format),
	)
	
	// 初始化依赖
	// PostgreSQL：权威数据源，失败则启动失败
	// Redis：通知通道，失败不阻塞启动
	ctx, cancelFunc := context.WithCancel(context.Background())
	defer cancelFunc()
	deps, err := bootstrap.InitDeps(ctx, apiConfig, apiLogger, serviceName)
	if err != nil {
		return fmt.Errorf("init dependencies: %w", err)
	}
	// 延迟关闭依赖，取保服务推出时释放资源
	defer bootstrap.CloseDeps(deps)
	
	// HTTP Server 包含：
	// - 路由注册（tasks、agents、health）
	// - 中间件链（按执行顺序）：
	//   1. RequestID   -> 为每个请求生成唯一 ID
	//   2. RequestLogger -> 请求级日志（附带 request_id、method、path）
	//   3. Recover     -> 捕获下游 panic，防止服务器崩溃
	//   4. AccessLog   -> 记录访问日志（耗时、状态码）
	
	server := http.NewServer(apiLogger, apiConfig, deps.Pool)
	// HTTP Server 在独立 goroutine 中运行
	// 使用 error channel 接收 server 错误，避免端口占用等场景下假死
	errCh := make(chan error, 1)
	go func() {
		apiLogger.Info("Starting http server", slog.String("name", serviceName), slog.String("address", apiConfig.HTTP.Addr))
		errCh <- server.Start()
	}()
	
	// 等待信号或者 Server 错误
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)
	select {
	case sig := <-sigChan:
		// 收到终止信号，记录日志并开始关闭流程
		apiLogger.Info("received shutdown signal", slog.String("signal", sig.String()))
	case err := <-errCh:
		// http server 异常退出
		if err != nil && err != http.ErrServerClosed {
			apiLogger.Error("http server error", slog.Any("error", err))
			cancelFunc()
			return fmt.Errorf("http server error: %w", err)
		}
		apiLogger.Info("http server shutdown")
	}
	
	// 优雅关闭
	apiLogger.Info("Shutting down http server", slog.Duration("shutdown_timeout", shutdownTimeout))
	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), shutdownTimeout)
	defer shutdownCancel()
	if err := server.Shutdown(shutdownCtx); err != nil {
		apiLogger.Error("http server error", slog.Any("error", err))
	}
	cancelFunc()
	apiLogger.Info("Shutting down graceful kraken-api", slog.String("address", apiConfig.HTTP.Addr))
	return nil
}
