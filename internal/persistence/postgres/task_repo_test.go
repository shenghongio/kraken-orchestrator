package postgres

import (
	"context"
	"errors"
	"os"
	"testing"
	"time"
	
	"github.com/shenghongio/kraken-orchestrator/internal/config"
	"github.com/shenghongio/kraken-orchestrator/internal/domain"
)

// 执行单元测试
// go test ./internal/domain/... ./internal/executor/... ./internal/config/... ./internal/observability/...

// 执行集成测试需要有数据库实例连接
// 建表
//   psql "postgres://kraken:kraken@localhost:5432/kraken?sslmode=disable" -f migrations/001_init.sql
// 跑集成测试
//  KRAKEN_TEST_DSN="postgres://kraken:kraken@localhost:5432/kraken?sslmode=disable" go test ./internal/persistence/... -v

func getTestDSN(t *testing.T) string {
	t.Helper()
	// 1, 环境变量
	if dsn := os.Getenv("KRAKEN_TEST_DSN"); dsn != "" {
		return dsn
	}
	k, err := config.Load("configs/scheduler.yaml")
	if err != nil {
		t.Skipf("skip: cannot load config: %v", err)
		return ""
	}
	dsn := k.String("db.dsn")
	if dsn == "" {
		t.Skipf("skip: db.dsn not set in configs/scheduler.yaml: %v", err)
	}
	return dsn
}
func newTestRepo(t *testing.T) (*TaskRepository, func()) {
	t.Helper()
	dsn := getTestDSN(t)
	background := context.Background()
	pool, err := NewPool(background, dsn, 5, 1, time.Minute)
	if err != nil {
		t.Fatalf("failed to connect to postgres database: %v", err)
	}
	
	// 清理测试数据
	_, _ = pool.Exec(background, "TRUNCATE tasks CASCADE")
	repository := NewTaskRepository(pool, time.Minute)
	cleanup := func() {
		_, _ = pool.Exec(background, "TRUNCATE tasks CASCADE")
		pool.Close()
	}
	return repository, cleanup
}
func TestTaskRepository_CreateAndGet(t *testing.T) {
	repo, cleanup := newTestRepo(t)
	defer cleanup()
	
	ctx := context.Background()
	task := &domain.Task{
		ID:         domain.NewTaskID(),
		Type:       domain.TaskTypeDownload,
		Priority:   domain.PriorityHigh,
		Payload:    []byte(`{"url": "http://example.com"}`),
		MaxRetries: 3,
	}
	if err := repo.Create(ctx, task); err != nil {
		t.Fatalf("failed to create task: %v", err)
	}
	got, err := repo.GetByID(ctx, task.ID)
	if err != nil {
		t.Fatalf("get bu id: %v", err)
	}
	if got.Type != task.Type {
		t.Errorf("type = %s,want %s", got.Type, task.Type)
	}
	if got.Status != domain.TaskStatusPending {
		t.Errorf("status = %s,want %s", got.Status, domain.TaskStatusPending)
	}
	if got.Version != 1 {
		t.Errorf("version = %d, want %d", got.Version, 1)
	}
}

func TestTaskRepository_idempotency(t *testing.T) {
	repo, clenaup := newTestRepo(t)
	defer clenaup()
	
	ctx := context.Background()
	key := "test-idempotency-key"
	task1 := &domain.Task{
		ID:             domain.NewTaskID(),
		Type:           domain.TaskTypeDownload,
		IdempotencyKey: key,
	}
	if err := repo.Create(ctx, task1); err != nil {
		t.Fatalf("create task: %v", err)
	}
	got, err := repo.GetByIdempotencyKey(ctx, key)
	if err != nil {
		t.Fatalf("get task by idempotency key: %v", err)
	}
	if got.ID != task1.ID {
		t.Errorf("id = %s, want %s", got.ID, task1.ID)
	}
}

func TestTaskRepository_NotFound(t *testing.T) {
	repo, cleanup := newTestRepo(t)
	defer cleanup()
	ctx := context.Background()
	_, err := repo.GetByID(ctx, "non-existent-id")
	if err == nil {
		t.Errorf("got nil, want error")
	}
	if !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("got %v, want %v", err, domain.ErrNotFound)
	}
	
}
func TestTaskRepository_List(t *testing.T) {
	repo, cleanup := newTestRepo(t)
	defer cleanup()
	ctx := context.Background()
	for i := 0; i < 10; i++ {
		task := &domain.Task{
			ID:   domain.NewTaskID(),
			Type: domain.TaskTypeDownload,
		}
		if err := repo.Create(ctx, task); err != nil {
			t.Fatalf("create task: %v", err)
		}
	}
	tasks, err := repo.List(ctx)
	if err != nil {
		t.Fatalf("list tasks: %v", err)
	}
	if len(tasks) != 10 {
		t.Errorf("list tasks count = %d, want %d", len(tasks), 10)
	}
}

func TestTaskRepository_RequestCancel(t *testing.T) {
	repo, cleanup := newTestRepo(t)
	defer cleanup()
	ctx := context.Background()
	task := &domain.Task{
		ID:   domain.NewTaskID(),
		Type: domain.TaskTypeDownload,
	}
	if err := repo.Create(ctx, task); err != nil {
		t.Fatalf("create task: %v", err)
	}
	if err := repo.RequestCancel(ctx, task.ID); err != nil {
		t.Fatalf("request cancel: %v", err)
	}
	got, err := repo.GetByID(ctx, task.ID)
	if err != nil {
		t.Fatalf("get task by id: %v", err)
	}
	if !got.CancelRequested {
		t.Errorf("cancel requested = %v, want %v", got.CancelRequested, true)
	}
}