package lease

import (
	"context"
	"io"
	"log/slog"
	"os"
	"testing"
	"time"
	
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/shenghongio/kraken-orchestrator/internal/domain"
	"github.com/shenghongio/kraken-orchestrator/internal/persistence/postgres"
)

// getTestPool 返回测试用的 pgxpool，没有 DSN 则 skip
func getTestPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	dsn := os.Getenv("KRAKEN_TEST_DSN")
	if dsn == "" {
		t.Skip("KRAKEN_TEST_DSN not set, skipping integration test")
	}
	ctx := context.Background()
	pool, err := postgres.NewPool(ctx, dsn, 5, 1, time.Minute)
	if err != nil {
		t.Fatalf("connect to postgres: %v", err)
	}
	// 清理所有相关表
	_, _ = pool.Exec(ctx, `TRUNCATE tasks CASCADE`)
	return pool
}

//测试3：Reaper 过期租约回收（不可重试 → failed）
// =============================================================================
//
// 业务背景：
//   Agent 申领任务后崩溃，lease 一直不续期，最终过期。
//   Reaper 定期扫描过期 lease，回收任务：
//   - 如果 attempt <= max_retries → 设为 pending，等待重试
//   - 如果 attempt > max_retries → 设为 failed，不再重试
//
// 核心机制：
//   Reaper 回收前也做 fencing 校验（lease_epoch 不匹配说明已被重新申领，
//   只需删除旧 lease 即可，不能改动任务状态）。
//
// 测试目的：
//   验证过期 lease 被正确回收，且根据 max_retries 决定最终状态

func TestReaper_ExpiredLEase(t *testing.T) {
	pool := getTestPool(t)
	defer pool.Close()
	
	ctx := context.Background()
	
	// 用postgres.TaskRepository 创建并且获取任务
	taskRepo := postgres.NewTaskRepository(pool, time.Minute)
	task := &domain.Task{
		ID:         domain.NewTaskID(),
		Type:       domain.TaskTypeDownload,
		MaxRetries: 0,
	}
	if err := taskRepo.Create(ctx, task); err != nil {
		t.Fatalf("create task: %v", err)
	}
	claimTask, err := taskRepo.ClaimTask(ctx, "agent-1")
	if err != nil {
		t.Fatalf("claim task: %v", err)
	}
	if claimTask == nil {
		t.Fatal("claim task is nil")
	}
	
	// 手动让 lease 过期 (expires_at 设为 1 小时前)
	_, err = pool.Exec(ctx, `UPDATE task_leases SET expires_at = NOW() - INTERVAL '1 hour' WHERE task_id = $1`, claimTask.Task.ID)
	if err != nil {
		t.Fatalf("expire lease: %v", err)
	}
	// 构造 Reaper 并执行一次 sweep
	// sweep 是 unexported，测试在同包内可直接调用
	reaper := NewReaper(pool, slog.New(slog.NewTextHandler(io.Discard, nil)), time.Minute)
	if err := reaper.sweep(ctx); err != nil {
		t.Fatalf("sweep: %v", err)
	}
	
	// 验证：attempt=1 > max_retries=0，应标为 failed
	got, err := taskRepo.GetByID(ctx, claimTask.Task.ID)
	if err != nil {
		t.Fatalf("get task: %v", err)
	}
	if got.Status != domain.TaskStatusFailed {
		t.Errorf("status = %s, want failed (attempt %d > max_retries %d)",
			got.Status, got.Attempt, got.MaxRetries)
	}
	
	// 验证：lease 已被删除
	var count int
	if err := pool.QueryRow(ctx,
		`SELECT COUNT(*) FROM task_leases WHERE task_id = $1`, claimTask.Task.ID).Scan(&count); err != nil {
		t.Fatalf("count leases: %v", err)
	}
	if count != 0 {
		t.Errorf("lease count = %d, want 0", count)
	}
	
}

// TestReaper_ExpiredLeaseRetry 验证可重试任务过期后回到 pending
// max_retries=3，attempt=1 <= 3，应回收为 pending 等待重试。
func TestReaper_ExpiredLeaseRetry(t *testing.T) {
	pool := getTestPool(t)
	defer pool.Close()
	
	ctx := context.Background()
	
	taskRepo := postgres.NewTaskRepository(pool, time.Minute)
	task := &domain.Task{
		ID:         domain.NewTaskID(),
		Type:       domain.TaskTypeDownload,
		MaxRetries: 3,
	}
	if err := taskRepo.Create(ctx, task); err != nil {
		t.Fatalf("create task: %v", err)
	}
	result, err := taskRepo.ClaimTask(ctx, "agent-1")
	if err != nil {
		t.Fatalf("claim task: %v", err)
	}
	if result == nil {
		t.Fatal("expected a task, got nil")
	}
	
	// 让 lease 过期
	_, err = pool.Exec(ctx,
		`UPDATE task_leases SET expires_at = NOW() - INTERVAL '1 hour' WHERE task_id = $1`,
		result.Task.ID)
	if err != nil {
		t.Fatalf("expire lease: %v", err)
	}
	
	reaper := NewReaper(pool, slog.New(slog.NewTextHandler(io.Discard, nil)), time.Minute)
	if err := reaper.sweep(ctx); err != nil {
		t.Fatalf("sweep: %v", err)
	}
	
	// 验证：attempt=1 <= max_retries=3，应回到 pending
	got, err := taskRepo.GetByID(ctx, result.Task.ID)
	if err != nil {
		t.Fatalf("get task: %v", err)
	}
	if got.Status != domain.TaskStatusPending {
		t.Errorf("status = %s, want pending (attempt %d <= max_retries %d)",
			got.Status, got.Attempt, got.MaxRetries)
	}
}
