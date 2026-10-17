package postgres

import (
	"context"
	"time"
	
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/shenghongio/kraken-orchestrator/internal/domain"
)

type AgentRepoository struct {
	pool *pgxpool.Pool
}

func NewAgentRepository(pool *pgxpool.Pool) *AgentRepoository {
	return &AgentRepoository{pool: pool}
}

func (r *AgentRepoository) UpsertAgent(ctx context.Context, agent *domain.Agent) error {
	utc := time.Now().UTC()
	if agent.CreatedAt.IsZero() {
		agent.CreatedAt = utc
	}
	agent.UpdatedAt = utc
	if agent.Status == "" {
		agent.Status = domain.AgentStatusOnline
	}
	_, err := r.pool.Exec(ctx, `
		INSERT INTO agents (id, status, version, capabilities, max_concurrency,busy_slots, last_heartbeat, created_at, updated_at) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
		ON CONFLICT (id) DO UPDATE SET status = EXCLUDED.status,version = EXCLUDED.version,capabilities = EXCLUDED.capabilities,max_concurrency = EXCLUDED.max_concurrency,updated_at = EXCLUDED.updated_at
	`,
		agent.ID, agent.Status, agent.Version, agent.Capabilities,
		agent.MaxConcurrency, agent.BusySlots, utc, agent.CreatedAt, utc,
	)
	return mapError(err)
}
func (r *AgentRepoository) GetAgent(ctx context.Context, id string) (*domain.Agent, error) {
	return scanAgent(r.pool.QueryRow(ctx, `
		SELECT id, status, version, capabilities, max_concurrency,busy_slots, last_heartbeat, created_at, updated_at FROM agents WHERE id = $1`, id))
}

func (r *AgentRepoository) ListAgents(ctx context.Context) ([]*domain.Agent, error) {
	rows, err := r.pool.Query(ctx, `
		SELECT id, status, version, capabilities, max_concurrency, busy_slots, last_heartbeat, created_at, updated_at FROM agents ORDER BY created_at DESC`)
	if err != nil {
		return nil, mapError(err)
	}
	defer rows.Close()
	var agents []*domain.Agent
	for rows.Next() {
		a, err := scanAgent(rows)
		if err != nil {
			return nil, err
		}
		agents = append(agents, a)
	}
	return agents, rows.Err()
}

func (r *AgentRepoository) Heartbeat(ctx context.Context, id string, busySlots int) error {
	newUtc := time.Now().UTC()
	exec, err := r.pool.Exec(ctx, `UPDATE agents SET last_heartbeat = $2, busy_slots = $3, updated_at = $2 WHERE id = $1`, id, newUtc, busySlots)
	if err != nil {
		return mapError(err)
	}
	if exec.RowsAffected() == 0 {
		return domain.ErrNotFound
	}
	return nil
	
}

func scanAgent(s rowScanner) (*domain.Agent, error) {
	var a domain.Agent
	err := s.Scan(
		&a.ID, &a.Status, &a.Version, &a.Capabilities, &a.MaxConcurrency,
		&a.BusySlots, &a.LastHeartbeat, &a.CreatedAt, &a.UpdatedAt,
	)
	if err != nil {
		return nil, mapError(err)
	}
	return &a, nil
}
