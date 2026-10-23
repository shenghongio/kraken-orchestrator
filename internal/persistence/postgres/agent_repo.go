package postgres

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/shenghongio/kraken-orchestrator/internal/domain"
)

// AgentRepository 负责agent表的持久化操作
type AgentRepository struct {
	pool *pgxpool.Pool
}

func NewAgentRepository(pool *pgxpool.Pool) *AgentRepository {
	return &AgentRepository{pool: pool}
}

// UpsertAgent 注册新 Agent 或更新已有 Agent 的静态信息（version/capabilities/max_concurrency）。
// busy_slots 和 created_at 不会被此方法更新，busy_slots 只能由 Heartbeat 更新。
func (r *AgentRepository) UpsertAgent(ctx context.Context, agent *domain.Agent) error {
	utc := time.Now().UTC()
	if agent.CreatedAt.IsZero() {
		agent.CreatedAt = utc
	}
	agent.UpdatedAt = utc
	if agent.Status == "" {
		agent.Status = domain.AgentStatusOnline
	}

	const query = `
					INSERT INTO agents (id, status, version, capabilities, max_concurrency,busy_slots, last_heartbeat, created_at, updated_at) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
				    ON CONFLICT (id) DO UPDATE SET status = EXCLUDED.status,version = EXCLUDED.version,capabilities = EXCLUDED.capabilities,max_concurrency = EXCLUDED.max_concurrency,updated_at = EXCLUDED.updated_at`

	args := []any{
		agent.ID, agent.Status, agent.Version, agent.Capabilities,
		agent.MaxConcurrency, agent.BusySlots, utc, agent.CreatedAt, utc,
	}
	_, err := r.pool.Exec(ctx, query, args...)
	return wrapSQLError("agent.upsert", query, args, err)
}
func (r *AgentRepository) GetAgent(ctx context.Context, id string) (*domain.Agent, error) {
	const query = `
		SELECT id, status, version, capabilities, max_concurrency,busy_slots, last_heartbeat, created_at, updated_at FROM agents WHERE id = $1`

	return scanAgent(r.pool.QueryRow(ctx, query, id), "agent.get", query, []any{id})
}

func (r *AgentRepository) ListAgents(ctx context.Context) ([]*domain.Agent, error) {
	const query = `
		SELECT id, status, version, capabilities, max_concurrency, busy_slots, last_heartbeat, created_at, updated_at FROM agents ORDER BY created_at DESC`
	rows, err := r.pool.Query(ctx, query)
	if err != nil {
		return nil, wrapSQLError("agent.list", query, nil, err)
	}
	defer rows.Close()
	var agents []*domain.Agent
	for rows.Next() {
		a, err := scanAgent(rows, "agent.list", query, nil)
		if err != nil {
			return nil, err
		}
		agents = append(agents, a)
	}
	return agents, rows.Err()
}

// Heartbeat 更新 Agent 的最后心跳时间和当前忙碌槽位数
func (r *AgentRepository) Heartbeat(ctx context.Context, id string, busySlots int) error {
	newUtc := time.Now().UTC()
	const query = `UPDATE agents SET last_heartbeat = $2, busy_slots = $3, updated_at = $2 WHERE id = $1`
	args := []any{id, busySlots, newUtc, newUtc}
	exec, err := r.pool.Exec(ctx, query, args...)
	if err != nil {
		return wrapSQLError("agent.heartbeat", query, args, err)
	}
	if exec.RowsAffected() == 0 {
		return domain.ErrNotFound
	}
	return nil

}

func scanAgent(s rowScanner, operation, query string, args []any) (*domain.Agent, error) {
	var a domain.Agent
	err := s.Scan(
		&a.ID, &a.Status, &a.Version, &a.Capabilities, &a.MaxConcurrency,
		&a.BusySlots, &a.LastHeartbeat, &a.CreatedAt, &a.UpdatedAt,
	)
	if err != nil {
		return nil, wrapSQLError(operation, query, args, err)
	}
	return &a, nil
}
