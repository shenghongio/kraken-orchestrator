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
	cfg        config.HTTPConfig
	pool       *pgxpool.Pool
	httpServer *http.Server
}

func NewServer(logger *slog.Logger, cfg config.HTTPConfig, pool *pgxpool.Pool) *Server {
	return &Server{logger: logger, cfg: cfg, pool: pool}
}
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	taskRepo := postgres.NewTaskRepository(s.pool)
	taskHandler := NewTaskHandler(taskRepo)
	healthHandler := NewHealthHandler(s.pool)

	// 拼接 http 请求url
	mux.HandleFunc("GET /healthz", healthHandler.Healthz)
	mux.HandleFunc("GET /readyz", healthHandler.Readyz)
	mux.HandleFunc("POST /v1/tasks", taskHandler.Create)
	mux.HandleFunc("GET /v1/tasks", taskHandler.List)
	mux.HandleFunc("GET /v1/tasks/{id}", taskHandler.GetById)
	mux.HandleFunc("POST /v1/tasks/{id}/cancel", taskHandler.Cancel)

	var handler http.Handler = mux

	// 每次请求时嵌入调用
	handler = RequestID(handler)
	handler = Recover(s.logger)(handler)
	handler = AccessLog(s.logger)(handler)
	return handler
}

func (s *Server) Start() error {
	s.httpServer = &http.Server{
		Addr:         s.cfg.Addr,
		Handler:      s.Handler(),
		ReadTimeout:  s.cfg.ReadTimeout,
		WriteTimeout: s.cfg.WriteTimeout,
	}
	s.logger.Info("http server starting", slog.String("addr", s.cfg.Addr))
	return s.httpServer.ListenAndServe()
}
func (s *Server) Shutdown() error {
	if s.httpServer == nil {
		return nil
	}
	return s.httpServer.Close()
}
