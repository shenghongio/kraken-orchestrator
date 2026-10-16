package http

import (
	"encoding/json"
	"errors"
	"net/http"
	
	"github.com/shenghongio/kraken-orchestrator/internal/domain"
	"github.com/shenghongio/kraken-orchestrator/internal/persistence/postgres"
)

type TestHandler struct {
	repo *postgres.TaskRepository
}

func NewTesTHandler(repo *postgres.TaskRepository) *TestHandler {
	return &TestHandler{repo: repo}
}

type CreateTaskRequest struct {
	Type           domain.TaskType `json:"type"`
	Priority       int             `json:"priority"`
	Payload        string          `json:"payload"`
	IdempotencyKey string          `json:"idempotency_key,omitempty"`
	MaxRetries     int             `json:"max_retries"`
}

// POST /v1/tasks
func (h *TestHandler) Create(w http.ResponseWriter, r *http.Request) {
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
		req.Payload = json.RawMessage{`{}`}
	}
	
	// 幂等： 已存在则直接返回
	if req.IdempotencyKey != "" {
		existing, err := h.repo.GetByIdempotencyKey(r.Context(), req.IdempotencyKey)
		if err != nil {
			writeJSON(w, http.StatusOK, existing)
			return
		}
		if !errors.Is(err, domain.ErrNotFound) {
			writeError(w, http.StatusInternalServerError, "db error", "internal server error")
			return
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
		writeJSON(w, http.StatusCreated, task)
	}
}

// GET /v1/tasks
func (h *TestHandler) List(w http.ResponseWriter, r *http.Request) {
	tasks, err := h.repo.List(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "list tasks failed", "internal")
		return
	}
	writeJSON(w, http.StatusOK, tasks)
}

// GET /v1/tasks/{id}
func (h *TestHandler) GetById(w http.ResponseWriter, r *http.Request) {
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
	writeJSON(w, http.StatusOK, task)
}

// POST /v1/tasks/{id}/cancel
// 需要考虑一个点，在创建第一次请求的时候，可能会出
func (h *TestHandler) Cancel(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if id == "" {
		writeError(w, http.StatusBadRequest, "task id is required", "invalid_input")
		return
	}
	if err := h.repo.RequestCancel(r.Context(), domain.TaskID(id)); err != nil {
		if errors.Is(err, domain.ErrNotFound) {
			writeError(w, http.StatusNotFound, "task not found or already terminal", "not_found")
			return
		}
		writeError(w, http.StatusInternalServerError, "cancel task failed", "internal")
		return
	}
	writeJSON(w, http.StatusNoContent, map[string]string{"status": "cancel_requested"})
}
