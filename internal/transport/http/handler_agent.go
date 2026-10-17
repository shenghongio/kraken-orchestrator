package http

import (
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"time"
	
	"github.com/shenghongio/kraken-orchestrator/internal/domain"
	"github.com/shenghongio/kraken-orchestrator/internal/persistence/postgres"
)

type AgentHandler struct {
	repo     *postgres.AgentRepoository
	logger   *slog.Logger
	taskRepo *postgres.TaskRepository
}

func NewAgentHandler(repo *postgres.AgentRepoository, taskRepo *postgres.TaskRepository, logger *slog.Logger) *AgentHandler {
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
func (h *AgentHandler) Register(w http.ResponseWriter, r *http.Request) {
	var req RegisterAgentRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
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
	agent := &domain.Agent{
		ID:             req.AgentID,
		Status:         domain.AgentStatusOnline,
		Version:        req.Version,
		Capabilities:   req.Capabilities,
		MaxConcurrency: req.MaxConcurrency,
		LastHeartbeat:  time.Now().UTC(),
	}
	if err := h.repo.UpsertAgent(r.Context(), agent); err != nil {
		writeError(w, http.StatusInternalServerError, "register agent failed", "internal")
		return
	}
	writeJSON(w, http.StatusOK, agent)
}

// Heartbeat POST /v1/agent/heartbeat
func (h *AgentHandler) Heartbeat(w http.ResponseWriter, r *http.Request) {
	var req HeartbeatRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
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
	if err := h.repo.Heartbeat(r.Context(), req.AgentID, req.BusySlots); err != nil {
		if errors.Is(err, domain.ErrNotFound) {
			writeError(w, http.StatusNotFound, "agent not found", "not_found")
			return
		}
		h.logger.Debug("heartbeat agent failed", slog.String("agent_id", req.AgentID), slog.Any("error", err))
		writeError(w, http.StatusInternalServerError, "heartbeat failed", "internal")
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

// Claim POST /v1/agent/tasks/claim
func (h *AgentHandler) Claim(w http.ResponseWriter, r *http.Request) {
	var req ClaimTaskRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body", "invalid_input")
		return
	}
	if req.AgentID == "" {
		writeError(w, http.StatusBadRequest, "agent_id is required", "invalid_input")
		return
	}
	result, err := h.taskRepo.ClaimTask(r.Context(), req.AgentID)
	if err != nil {
		h.logger.Error("claim task failed", slog.String("agent_id", req.AgentID), slog.String("error", err.Error()))
		writeError(w, http.StatusInternalServerError, "claim task failed", "internal")
		return
	}
	if result == nil {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	writeJSON(w, http.StatusOK, ClaimTaskResponse{
		TaskID:     string(result.Task.ID),
		Type:       string(result.Task.Type),
		Attempt:    result.Attempt,
		LeaseEpoch: result.LeaseEpoch,
		Payload:    result.Task.Payload,
	})
}
