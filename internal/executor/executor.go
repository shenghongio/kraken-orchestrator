package executor

import (
	"context"

	"github.com/shenghongio/kraken-orchestrator/internal/domain"
)

type Result struct {
	Success   bool
	Retryable bool
	Message   string
	Output    []byte
}
type Progress struct {
	Percent    float64
	Message    string
	Checkpoint []byte
}

type Executor interface {
	Type() domain.TaskType
	Validate(task *domain.Task) error
	Execute(ctx context.Context, task *domain.Task) (*Result, error)
}

type Registry struct {
	executors map[domain.TaskType]Executor
}

func NewRegistry() *Registry {
	return &Registry{
		executors: make(map[domain.TaskType]Executor),
	}
}

func (r *Registry) Get(t domain.TaskType) (Executor, bool) {
	executor, ok := r.executors[t]
	return executor, ok
}
func (r *Registry) Register(executor Executor) {
	r.executors[executor.Type()] = executor
}
func (r *Registry) Types() []domain.TaskType {
	types := make([]domain.TaskType, 0, len(r.executors))
	for t := range r.executors {
		types = append(types, t)
	}
	return types
}
