package postgres

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5"
)

// RunMigrations 执行Schema 迁移，直接读SQL文件执行
// 后续可换用 golang-migrate/presly/goose
func RunMigrations(ctx context.Context, conn *pgx.Conn, sql string) error {
	exec, err := conn.Exec(ctx, sql)
	if err != nil {
		return fmt.Errorf("run migrations: %q -> %w", sql, err)
	}
	_ = exec
	return nil
}
