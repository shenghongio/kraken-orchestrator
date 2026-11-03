package postgres

import (
	"context"
	"embed"
	"fmt"
	"strings"
	
	"github.com/jackc/pgx/v5/pgxpool"
)

//go:embed migrations/*.sql
var migrationsFS embed.FS
var migrationsFile string

// RunMigrations 执行Schema 迁移，直接读SQL文件执行
// 后续可换用 golang-migrate/presly/goose

func RunMigrations(ctx context.Context, conn *pgxpool.Pool) (string, error) {
	migrationsFile := []string{
		"001_init.sql",
		"002_outbox.sql",
		"003_unique_idempotency.sql",
	}
	for _, file := range migrationsFile {
		sqlBytes, err := migrationsFS.ReadFile("migrations/" + file)
		if err != nil {
			return "", fmt.Errorf("failed to read migration file %s: %w", file, err)
		}
		if _, err := conn.Exec(ctx, string(sqlBytes)); err != nil {
			return "", fmt.Errorf("failed to run migration %s: %w", file, err)
		}
	}
	return strings.Join(migrationsFile, ","), nil
}
