package http

import (
	"context"
	"net/http"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

type HealthHandler struct {
	pool *pgxpool.Pool
}

func NewHealthHandler(pool *pgxpool.Pool) *HealthHandler {
	return &HealthHandler{pool: pool}
}

// Healthz  /healthz - 进程存活，不检查依赖
func (h *HealthHandler) Healthz(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

// Readyz /readyz - 就绪，检查DB连通
func (h *HealthHandler) Readyz(w http.ResponseWriter, r *http.Request) {
	withTimeout, cancelFunc := context.WithTimeout(r.Context(), time.Second*2)
	defer cancelFunc()
	if err := h.pool.Ping(withTimeout); err != nil {
		writeError(w, http.StatusServiceUnavailable, "db not ready", "db_unavailable")
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}
