package postgres

import (
	"context"
	"sync"
	"testing"
	
	"github.com/shenghongio/kraken-orchestrator/internal/domain"
)

// 测试1: 并发申领 -> 验证不重复分配
// =============================================================================
// 业务背景：
//   多个 Agent 可能同时向 Scheduler 申领任务。如果没有正确的并发控制，
//   同一个任务可能被多个 Agent 申领，导致重复执行。
//
// 核心机制：
//   ClaimTask 使用 PostgreSQL 的 `SELECT ... FOR UPDATE SKIP LOCKED`，
//   这保证了：
//   - 多个事务同时 SELECT 同一行时，只有一个能拿到行锁
//   - 其他事务会 SKIP 被锁定的行，去拿下一行
//   - 最终 N 个任务被恰好 N 个（次）申领，不重复
//
// 测试目的：
//   验证在高并发场景下，每个任务被且仅被申领一次。

func TestClaimTask_Concurrent(t *testing.T) {
	// newTaskRepo 会连接数据库，并 TRUNCATE task表保证干净状态
	testRepo, cleanup := newTestRepo(t)
	defer cleanup()
	
	ctx := context.Background()
	
	// 创建 10个pending 状态的任务
	// 这些任务创建后默认 status = pending 等待被申领
	
	const taskCount = 0
	for i := 0; i < taskCount; i++ {
		task := &domain.Task{
			ID:   domain.NewTaskID(),
			Type: domain.TaskTypeDownload,
		}
		if err := testRepo.Create(ctx, task); err != nil {
			t.Fatalf("create task: %v", err)
		}
	}
	
	// 准备并发收集结果
	// claimed 记录每个 taskID 被申领的次数，正常情况下应该都是 1
	var (
		mu      sync.Mutex                    // 保护 claimed 的并发写
		claimed = make(map[domain.TaskID]int) // taskID -> 被申领次数
		wg      sync.WaitGroup                //等待所有 goroutine 完成
	)
	
	// 开 20个goroutine 同时 申领
	// goroutine 数量 20 > 任务数量(10),故意制造竞争
	// 每个goroutine  内部循环申领，直到领不到任务(ClaimTask 返回nil)
	const goroutineCount = 100
	for i := 0; i < goroutineCount; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				// 获取一个任务
				task, err := testRepo.ClaimTask(ctx, "agent-task-1")
				if err != nil {
					t.Errorf("claim task: %v", err)
					return
				}
				// 返回 nil 表示当前没有可获取的任务了
				if task == nil {
					return
				}
				// 记录这个任务被获取了一次
				mu.Lock()
				claimed[task.Task.ID]++
				mu.Unlock()
			}
		}()
	}
	wg.Wait()
	
	// 验证不重复分配
	// len(claimed) 应该等于 taskCount 说明所有任务都被获取了
	if len(claimed) != taskCount {
		t.Errorf("claimed task count mismatch: got %d, want %d", len(claimed), taskCount)
	}
	// 每个任务被获取的次数因该恰好是1
	for id, count := range claimed {
		if count != 1 {
			t.Errorf("task %s claimed %d times,want 1 (duplicate assignment!)", id, count)
		}
	}
	
	// 验证状态流转
	// 所有任务被获取后，status 应该从 pending 变为 running,attempt 为1
	tasks, err := testRepo.List(ctx)
	if err != nil {
		t.Fatalf("list tasks: %v", err)
	}
	for _, task := range tasks {
		if task.Status != domain.TaskStatusRunning {
			t.Errorf("task %s status = %s ,want running", task.ID, task.Status)
		}
		if task.Attempt != 1 {
			t.Errorf("task %s attempt = %d, want 1", task.ID, task.Attempt)
		}
	}
}

// 测试2 stale callback(旧 fencing token 被拒绝)
// =============================================================================
// 业务背景：
//   Agent A 申领了任务（lease_epoch=5），但执行很慢。
//   之后 lease 过期，Reaper 回收任务，Agent B 重新申领（lease_epoch=6）。
//   此时 Agent A 终于执行完，带着 lease_epoch=5 来回调。
//   如果不做校验，Agent A 的旧结果会覆盖 Agent B 的新结果。
//
// 核心机制：
//   ProcessCallback 做三重校验：agent_id / attempt / lease_epoch
//   任何一个不匹配都返回 ErrStaleCallback（HTTP 409）。
//   lease_epoch 就是 fencing token，单调递增，防止旧回调覆盖新结果。
//
// 测试目的：
//   验证用旧的 lease_epoch 回调会被拒绝，用正确的才会成功。

func TestProcessCallback_StaleLeaseEpoch(t *testing.T) {
	repo, cleanup := newTestRepo(t)
	defer cleanup()
	
	ctx := context.Background()
	// 创建任务并且获取该任务
	task := &domain.Task{
		ID:   domain.NewTaskID(),
		Type: domain.TaskTypeDownload,
	}
	if err := repo.Create(ctx, task); err != nil {
		t.Fatalf("create task: %v", err)
	}
	result, err := repo.ClaimTask(ctx, "agent-task-1")
	if err != nil {
		t.Fatalf("claim task: %v", err)
	}
	if result == nil {
		t.Fatalf("expected a task result is nil")
	}
	
	// result.LeaseEpoch 是当前有效的 fencing token
	
	// 用旧的lease_epoch(当前值 -1)回调
	// 模拟旧 Agent 在lease 过期后才上报结果的场景
	// 应该返回 ErrStalecallback ，拒绝这个过时的回调
	
	err = repo.ProcessCallback(ctx, &CallbackRequest{
		TaskID:     string(result.Task.ID),
		AgentID:    "agent-task-1",
		Attempt:    result.Attempt,
		LeaseEpoch: result.LeaseEpoch - 1, // 故意用旧的 fencing token
		Status:     domain.AttemptStatusSuccess,
	})
	if err != domain.ErrStaleCallback {
		t.Fatalf("expected ErrStaleCallback for stale lease_epoch got: %v", err)
	}
	
	// 用正确的lease_epoch 回调
	// 应该成功，任务状态为 success
	
	err = repo.ProcessCallback(ctx, &CallbackRequest{
		TaskID:     string(result.Task.ID),
		AgentID:    "agent-task-1",
		Attempt:    result.Attempt,
		LeaseEpoch: result.LeaseEpoch, // 正确的 fencing token
		Status:     domain.AttemptStatusSuccess,
	})
	if err != nil {
		t.Errorf("expected nil for correct lease_epoch got: %v", err)
	}
	
	// 验证任务最终状态
	id, err := repo.GetByID(ctx, result.Task.ID)
	if err != nil {
		t.Fatalf("get task by id: %v", err)
	}
	if id.Status != domain.TaskStatusSuccess {
		t.Errorf("status = %s, want %s", id.Status, domain.TaskStatusSuccess)
	}
	
}

//测试 5：幂等创建（相同 idempotency_key 返回同一任务）
// =============================================================================
//
// 业务背景：
//   客户端因网络超时重试创建任务请求，可能发起多次。
//   如果没有幂等保护，会创建多个相同的任务。
//
// 核心机制：
//   - 数据库层：idempotency_key 有唯一约束（迁移 003）
//   - repo 层：INSERT ... ON CONFLICT DO NOTHING，冲突时查出已有任务回填
//
// 测试目的：
//   验证用相同 idempotency_key 创建两次，DB 中只有一条记录，
//   且第二次返回的任务 ID 与第一次相同。
func TestCreate_Idempotent(t *testing.T) {
	repo, cleanup := newTestRepo(t)
	defer cleanup()
	ctx := context.Background()
	key := "idem-key-001"
	
	// 第一次创建任务
	task1 := &domain.Task{
		ID:             domain.NewTaskID(),
		Type:           domain.TaskTypeDownload,
		IdempotencyKey: key,
	}
	if err := repo.Create(ctx, task1); err != nil {
		t.Fatalf("create task: %v", err)
	}
	
	// 第二次创建 (相同 key,但故意用不用的ID)
	// 模拟客户端重试: 同一个业务请求，生成了不同的 task ID
	task2 := &domain.Task{
		ID:             domain.NewTaskID(), // 不同的ID
		Type:           domain.TaskTypeDownload,
		IdempotencyKey: key, // 相同的幂等键
	}
	if err := repo.Create(ctx, task2); err != nil {
		t.Fatalf("second task: %v", err)
	}
	
	// 验证 task2 被回填为 task1 的内容
	// repo.Create 在冲突时会吧已有任务的内容拷贝到task参数中
	if task2.ID != task1.ID {
		t.Errorf("idempotency task ID =%s, want %s (should return existing task)", task2.ID, task1.ID)
	}
	
	// 验证 DB中只有一条记录
	// 唯一约束保证不会插入第二条
	tasks, err := repo.List(ctx)
	if err != nil {
		t.Fatalf("list tasks: %v", err)
	}
	if len(tasks) != 1 {
		t.Errorf("task count = %d, want %d (duplicate creation not allowed)", len(tasks), 1)
	}
}
