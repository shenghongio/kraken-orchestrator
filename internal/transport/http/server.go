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
	taskHandler := NewTask
}
