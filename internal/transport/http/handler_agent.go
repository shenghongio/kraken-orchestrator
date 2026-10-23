package http

import (
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"time"

	"github.com/shenghongio/kraken-orchestrator/internal/domain"
	"github.com/shenghongio/kraken-orchestrator/internal/observability"
	"github.com/shenghongio/kraken-orchestrator/internal/persistence/postgres"
)

// AgentHandler 处理Agent 相关的 HTTP 请求： 注册，心跳，申领任务
type AgentHandler struct {
	repo     *postgres.AgentRepository
	logger   *slog.Logger
	taskRepo *postgres.TaskRepository
}

func NewAgentHandler(repo *postgres.AgentRepository, taskRepo *postgres.TaskRepository, logger *slog.Logger) *AgentHandler {
	return &AgentHandler{repo: repo, taskRepo: taskRepo, logger: logger}
}

type RegisterAgentRequest struct {
	AgentID        string   `json:"agent_id"`
	Version        string   `json:"version"`
	Capabilities   []string `json:"capabilities"`
	MaxConcurrency int      `json:"max_concurrency"`
}

type ClaimTaskRequest struct {
	AgentID string `json:"agent_id"`
}

type ClaimTaskResponse struct {
	TaskID     string          `json:"task_id"`
	Type       string          `json:"type"`
	Attempt    int             `json:"attempt"`
	LeaseEpoch int64           `json:"lease_epoch"`
	Payload    json.RawMessage `json:"payload"`
}
type HeartbeatRequest struct {
	AgentID   string `json:"agent_id"`
	BusySlots int    `json:"busy_slots"`
}

// Register POST /v1/agent/register
// Agent 注册或更新静态信息
func (h *AgentHandler) Register(w http.ResponseWriter, r *http.Request) {
	logger := LoggerFromContext(r.Context())
	var req RegisterAgentRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		logger.Debug("register agent: invalid request body", observability.ErrAttr(err))
		writeError(w, http.StatusBadRequest, "invalid request body", "invalid_input")
		return
	}
	if req.AgentID == "" {
		writeError(w, http.StatusBadRequest, "agent_id is required", "invalid_input")
		return
	}
	if req.MaxConcurrency <= 0 {
		writeError(w, http.StatusBadRequest, "max_concurrency must be positive", "invalid_input")
		return
	}
	logger.Debug("register agent", slog.String("agent_id", req.AgentID), slog.String("version", req.Version), slog.Int("max_concurrency", req.MaxConcurrency), slog.Any("capabilities", req.Capabilities))
	agent := &domain.Agent{
		ID:             req.AgentID,
		Status:         domain.AgentStatusOnline,
		Version:        req.Version,
		Capabilities:   req.Capabilities,
		MaxConcurrency: req.MaxConcurrency,
		LastHeartbeat:  time.Now().UTC(),
	}
	if err := h.repo.UpsertAgent(r.Context(), agent); err != nil {
		logger.Debug("register agent failed", slog.String("agent_id", req.AgentID), observability.ErrAttr(err))
		writeError(w, http.StatusInternalServerError, "register agent failed", "internal")
		return
	}
	writeJSON(w, http.StatusOK, agent)
}

// Heartbeat POST /v1/agent/heartbeat
// 原子申领一个执行任务，无任务时返回204 code
func (h *AgentHandler) Heartbeat(w http.ResponseWriter, r *http.Request) {
	logger := LoggerFromContext(r.Context())
	var req HeartbeatRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		logger.Debug("heartbeat: invalid request body", observability.ErrAttr(err))
		writeError(w, http.StatusBadRequest, "invalid request body", "invalid_input")
		return
	}
	if req.AgentID == "" {
		writeError(w, http.StatusBadRequest, "agent_id is required", "invalid_input")
		return
	}
	if req.BusySlots < 0 {
		writeError(w, http.StatusBadRequest, "busy_slots must be non-negative", "invalid_input")
		return
	}
	logger.Debug("heartbeat agent", slog.String("agent_id", req.AgentID), slog.Int("busy_slots", req.BusySlots))
	if err := h.repo.Heartbeat(r.Context(), req.AgentID, req.BusySlots); err != nil {
		if errors.Is(err, domain.ErrNotFound) {
			writeError(w, http.StatusNotFound, "agent not found", "not_found")
			return
		}
		logger.Error("heartbeat agent failed", slog.String("agent_id", req.AgentID), slog.Int("busy_slots", req.BusySlots), observability.ErrAttr(err))
		writeError(w, http.StatusInternalServerError, "heartbeat failed", "internal")
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

// Claim POST /v1/agent/tasks/claim
func (h *AgentHandler) Claim(w http.ResponseWriter, r *http.Request) {
	logger := LoggerFromContext(r.Context())
	var req ClaimTaskRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		logger.Debug("claim task: invalid request body", observability.ErrAttr(err))
		writeError(w, http.StatusBadRequest, "invalid request body", "invalid_input")
		return
	}
	if req.AgentID == "" {
		writeError(w, http.StatusBadRequest, "agent_id is required", "invalid_input")
		return
	}
	logger.Debug("claim task", slog.String("agent_id", req.AgentID))
	result, err := h.taskRepo.ClaimTask(r.Context(), req.AgentID)
	if err != nil {
		logger.Error("claim task failed", slog.String("agent_id", req.AgentID), observability.ErrAttr(err))
		writeError(w, http.StatusInternalServerError, "claim task failed", "internal")
		return
	}
	if result == nil {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	logger.Debug("claim task success", slog.String("agent_id", req.AgentID), slog.String("task_id", string(result.Task.ID)), slog.Int("attempt", result.Attempt), slog.Int64("lease_epoch", result.LeaseEpoch))
	writeJSON(w, http.StatusOK, ClaimTaskResponse{
		TaskID:     string(result.Task.ID),
		Type:       string(result.Task.Type),
		Attempt:    result.Attempt,
		LeaseEpoch: result.LeaseEpoch,
		Payload:    result.Task.Payload,
	})
}
