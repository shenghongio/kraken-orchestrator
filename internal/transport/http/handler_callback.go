package http

import (
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"

	"github.com/shenghongio/kraken-orchestrator/internal/domain"
	"github.com/shenghongio/kraken-orchestrator/internal/observability"
	"github.com/shenghongio/kraken-orchestrator/internal/persistence/postgres"
)

// CallbackHandler 处理 Agent 的执行结果回调
type CallbackHandler struct {
	taskRepo *postgres.TaskRepository
	logger   *slog.Logger
}

func NewCallbackHandler(taskRepo *postgres.TaskRepository, logger *slog.Logger) *CallbackHandler {
	return &CallbackHandler{taskRepo: taskRepo, logger: logger}
}

type CallbackRequest struct {
	TaskID     string          `json:"task_id"`
	AgentID    string          `json:"agent_id"`
	Attempt    int             `json:"attempt"`
	LeaseEpoch int64           `json:"lease_epoch"`
	Status     string          `json:"status"`
	Result     json.RawMessage `json:"result"`
	Error      string          `json:"error"`
}

// Callback  POST /v1/callback
// Agent 上报任务执行结果(success/filed/cancelled)
func (handler *CallbackHandler) Callback(w http.ResponseWriter, r *http.Request) {
	logger := LoggerFromContext(r.Context())
	var req CallbackRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		logger.Debug("callback: invalid request body", observability.ErrAttr(err))
		writeError(w, http.StatusBadRequest, "invalid request body", "invalid_input")
		return
	}
	if req.TaskID == "" || req.AgentID == "" || req.Attempt == 0 || req.LeaseEpoch == 0 {
		writeError(w, http.StatusBadRequest, "task_id, agent_id, attempt, lease_epoch are required", "invalid_input")
		return
	}
	status := domain.AttemptStatus(req.Status)
	if status != domain.AttemptStatusSuccess && status != domain.AttemptStatusFailed && status != domain.AttemptStatusCancelled {
		logger.Debug("callback: invalid status", slog.String("status", req.Status))
		writeError(w, http.StatusBadRequest, "status must be one of: success,failed cancelled", "invalid_input")
		return
	}

	var result []byte
	if len(req.Result) > 0 {
		result = []byte(req.Result)
	}
	logger.Debug("callback received",
		slog.String("task_id", req.TaskID), slog.String("agent_id", req.AgentID), slog.Int("attempt", req.Attempt),
		slog.Int64("lease_epoch", req.LeaseEpoch), slog.String("status", req.Status))
	err := handler.taskRepo.ProcessCallback(r.Context(), &postgres.CallbackRequest{
		TaskID:     req.TaskID,
		AgentID:    req.AgentID,
		Attempt:    req.Attempt,
		LeaseEpoch: req.LeaseEpoch,
		Status:     status,
		Result:     result,
		Error:      req.Error,
	})
	if err != nil {
		switch {
		case errors.Is(err, domain.ErrNotFound):
			writeError(w, http.StatusNotFound, "task not found", "not_found")
		case errors.Is(err, domain.ErrStaleCallback):
			writeError(w, http.StatusConflict, "stale callback", "conflict")
		case errors.Is(err, domain.ErrInvalidStatusTransition):
			writeError(w, http.StatusBadRequest, "invalid status", "invalid_input")
		default:
			logger.Error("callback: process callback",
				slog.String("task_id", req.TaskID), slog.String("agent_id", req.AgentID), slog.Int("attempt", req.Attempt),
				slog.Int64("lease_epoch", req.LeaseEpoch), slog.String("status", req.Status), observability.ErrAttr(err))
		}
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
