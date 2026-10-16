package postgres

import (
	"errors"
	
	"github.com/jackc/pgx/v5"
	"github.com/shenghongio/kraken-orchestrator/internal/domain"
)

func mapError(err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.ErrNotFound
	}
	return err
}
