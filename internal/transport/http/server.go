package http

import (
	"log/slog"
	"net/http"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/shenghongio/kraken-orchestrator/internal/config"
	"github.com/shenghongio/kraken-orchestrator/internal/persistence/postgres"
)

type Server struct {
	logger     *slog.Logger
	cfg        *config.SchedulerConfig
	pool       *pgxpool.Pool
	httpServer *http.Server
}

func NewServer(logger *slog.Logger, cfg *config.SchedulerConfig, pool *pgxpool.Pool) *Server {
	return &Server{logger: logger, cfg: cfg, pool: pool}
}
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()

	// Handler 路由
	taskRepo := postgres.NewTaskRepository(s.pool, s.cfg.LeaseTTL)
	taskHandler := NewTaskHandler(taskRepo)
	healthHandler := NewHealthHandler(s.pool)
	agentRepository := postgres.NewAgentRepository(s.pool)
	newAgentHandler := NewAgentHandler(agentRepository, taskRepo, s.logger)
	callbackHandler := NewCallbackHandler(taskRepo, s.logger)

	// 拼接 http 请求url
	mux.HandleFunc("GET /healthz", healthHandler.Healthz)
	mux.HandleFunc("GET /readyz", healthHandler.Readyz)
	mux.HandleFunc("POST /v1/tasks", taskHandler.Create)
	mux.HandleFunc("GET /v1/tasks", taskHandler.List)
	mux.HandleFunc("GET /v1/tasks/{id}", taskHandler.GetById)
	mux.HandleFunc("POST /v1/tasks/{id}/cancel", taskHandler.Cancel)

	mux.HandleFunc("POST /v1/agent/register", newAgentHandler.Register)
	mux.HandleFunc("POST /v1/agent/heartbeat", newAgentHandler.Heartbeat)
	mux.HandleFunc("POST /v1/agent/tasks/claim", newAgentHandler.Claim)
	mux.HandleFunc("POST /v1/callback", callbackHandler.Callback)

	var handler http.Handler = mux

	// 每次请求时嵌入调用 中间件顺序: RequestID → RequestLogger → Recover → AccessLog
	// RequestLogger 必须在 RequestID 之后：因为 RequestLogger 要从 context 取 request_id 来构建请求级 logger。
	// RequestLogger 必须在 Recover 和 AccessLog 之前：这样 panic 日志和访问日志都能通过 context 拿到带 request_id 的 logger。
	// 完整顺序：RequestID → RequestLogger → Recover → AccessLog → mux。
	handler = RequestID(handler)
	handler = RequestLogger(s.logger)(handler)
	handler = Recover(s.logger)(handler)
	handler = AccessLog(s.logger)(handler)
	return handler
}

func (s *Server) Start() error {
	s.httpServer = &http.Server{
		Addr:         s.cfg.HTTP.Addr,
		Handler:      s.Handler(),
		ReadTimeout:  s.cfg.HTTP.ReadTimeout,
		WriteTimeout: s.cfg.HTTP.WriteTimeout,
	}
	s.logger.Info("http server starting", slog.String("addr", s.cfg.HTTP.Addr))
	return s.httpServer.ListenAndServe()
}
func (s *Server) Shutdown() error {
	if s.httpServer == nil {
		return nil
	}

	return s.httpServer.Close()
}
