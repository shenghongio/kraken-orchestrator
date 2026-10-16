package executor

import (
	"context"
	"testing"
	
	"github.com/shenghongio/kraken-orchestrator/internal/domain"
)

type mockExecutor struct {
	t domain.TaskType
}

func (m *mockExecutor) Type() domain.TaskType            { return m.t }
func (m *mockExecutor) Validate(task *domain.Task) error { return nil }
func (m *mockExecutor) Execute(ctx context.Context, task *domain.Task) (*Result, error) {
	return &Result{Success: true}, nil
}

func TestRegistry(t *testing.T) {
	reg := NewRegistry()
	reg.Register(&mockExecutor{t: domain.TaskTypeDownload})
	ge, ok := reg.Get(domain.TaskTypeDownload)
	if !ok {
		t.Fatal("expected executor for download")
	}
	if ge.Type() != domain.TaskTypeDownload {
		t.Errorf("expected  download got: %s", ge.Type())
	}
	types := reg.Types()
	if len(types) != 1 {
		t.Errorf("expected 1 task type, got %d", len(types))
	}
}
