package http

import (
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"github.com/shenghongio/kraken-orchestrator/internal/domain"
	"github.com/shenghongio/kraken-orchestrator/internal/persistence/postgres"
)

type TaskHandler struct {
	repo *postgres.TaskRepository
}

func NewTaskHandler(repo *postgres.TaskRepository) *TaskHandler {
	return &TaskHandler{repo: repo}
}

type CreateTaskRequest struct {
	Type           domain.TaskType `json:"type"`
	Priority       int             `json:"priority"`
	Payload        json.RawMessage `json:"payload"`
	IdempotencyKey string          `json:"idempotency_key,omitempty"`
	MaxRetries     int             `json:"max_retries"`
}

// TaskResponse 是对外暴露的任务 DTO，隔离内部 domain 模型
type TaskResponse struct {
	ID              string          `json:"id"`
	Type            string          `json:"type"`
	Priority        int             `json:"priority"`
	Status          string          `json:"status"`
	Payload         json.RawMessage `json:"payload"`
	IdempotencyKey  string          `json:"idempotency_key,omitempty"`
	MaxRetries      int             `json:"max_retries"`
	Attempt         int             `json:"attempt"`
	CurrentAgentID  string          `json:"current_agent_id,omitempty"`
	CancelRequested bool            `json:"cancel_requested"`
	Result          json.RawMessage `json:"result,omitempty"`
	Error           string          `json:"error,omitempty"`
	CreatedAt       string          `json:"created_at"`
	UpdatedAt       string          `json:"updated_at"`
}

// toTaskResponse 将内部 domain.Task 转换为对外 DTO
func toTaskResponse(t *domain.Task) TaskResponse {
	var result json.RawMessage
	if len(t.Result) > 0 {
		result = t.Result
	}
	return TaskResponse{
		ID:              string(t.ID),
		Type:            string(t.Type),
		Priority:        int(t.Priority),
		Status:          string(t.Status),
		Payload:         json.RawMessage(t.Payload),
		IdempotencyKey:  t.IdempotencyKey,
		MaxRetries:      t.MaxRetries,
		Attempt:         t.Attempt,
		CurrentAgentID:  t.CurrentAgentID,
		CancelRequested: t.CancelRequested,
		Result:          result,
		Error:           t.Error,
		CreatedAt:       t.CreatedAt.Format(time.RFC3339),
		UpdatedAt:       t.UpdatedAt.Format(time.RFC3339),
	}
}

// Create POST /v1/tasks
// 幂等由 repo 层保证（ON CONFLICT DO NOTHING），handler 无需先查再插
func (h *TaskHandler) Create(w http.ResponseWriter, r *http.Request) {
	var req CreateTaskRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body", "invalid_input")
		return
	}
	if req.Type == "" {
		writeError(w, http.StatusBadRequest, "type is required", "invalid_input")
		return
	}
	if len(req.Payload) == 0 {
		req.Payload = json.RawMessage(`{}`)
	}

	task := &domain.Task{
		ID:             domain.NewTaskID(),
		Type:           req.Type,
		Priority:       domain.TaskPriority(req.Priority),
		Payload:        []byte(req.Payload),
		IdempotencyKey: req.IdempotencyKey,
		MaxRetries:     req.MaxRetries,
	}
	if err := h.repo.Create(r.Context(), task); err != nil {
		writeError(w, http.StatusInternalServerError, "create task failed", "internal")
		return
	}
	// 新任务或幂等命中的已有任务，统一返回 200
	writeJSON(w, http.StatusOK, toTaskResponse(task))
}

// List GET /v1/tasks
func (h *TaskHandler) List(w http.ResponseWriter, r *http.Request) {
	tasks, err := h.repo.List(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "list tasks failed", "internal")
		return
	}
	// 转换为 DTO 列表
	resp := make([]TaskResponse, 0, len(tasks))
	for _, t := range tasks {
		resp = append(resp, toTaskResponse(t))
	}
	writeJSON(w, http.StatusOK, resp)
}

// GetById GET /v1/tasks/{id}
func (h *TaskHandler) GetById(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if id == "" {
		writeError(w, http.StatusBadRequest, "task id is required", "invalid_input")
		return
	}
	task, err := h.repo.GetByID(r.Context(), domain.TaskID(id))
	if err != nil {
		if errors.Is(err, domain.ErrNotFound) {
			writeError(w, http.StatusNotFound, "task not found", "not_found")
			return
		}
		writeError(w, http.StatusInternalServerError, "get task failed", "internal")
		return
	}
	writeJSON(w, http.StatusOK, toTaskResponse(task))
}

// Cancel POST /v1/tasks/{id}/cancel
func (h *TaskHandler) Cancel(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if id == "" {
		writeError(w, http.StatusBadRequest, "task id is required", "invalid_input")
		return
	}
	if err := h.repo.RequestCancel(r.Context(), domain.TaskID(id)); err != nil {
		if errors.Is(err, domain.ErrNotFound) {
			// 任务不存在、已终止、或已请求取消
			writeError(w, http.StatusNotFound, "task not found, already cancelled, or terminal", "not_found")
			return
		}
		writeError(w, http.StatusInternalServerError, "cancel task failed", "internal")
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "cancel_requested"})
}
