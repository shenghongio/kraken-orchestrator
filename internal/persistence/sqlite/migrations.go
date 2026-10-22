package sqlite

import (
	"database/sql"
	"fmt"
)

// RunMigrations 在打开的SQLite 连接上执行建表语句
func RunMigrations(db *sql.DB) error {
	_, err := db.Exec(`
	CREATE TABLE IF NOT EXISTS checkpoints (
			task_id     TEXT PRIMARY KEY,
			attempt     INTEGER NOT NULL,
			lease_epoch BIGINT NOT NULL,
			status      TEXT NOT NULL,
			payload     BLOB,
			checkpoint  BLOB,
			updated_at  DATETIME NOT NULL DEFAULT (datetime('now'))
		);

		CREATE TABLE IF NOT EXISTS callback_outbox (
			id          INTEGER PRIMARY KEY AUTOINCREMENT,
			task_id     TEXT NOT NULL,
			attempt     INTEGER NOT NULL,
			lease_epoch BIGINT NOT NULL,
			status      TEXT NOT NULL,
			result      BLOB,
			error       TEXT,
			created_at  DATETIME NOT NULL DEFAULT (datetime('now')),
			sent        INTEGER NOT NULL DEFAULT 0
		);

		CREATE INDEX IF NOT EXISTS idx_callback_outbox_sent ON callback_outbox(sent, created_at);
`)
	if err != nil {
		return fmt.Errorf("run sqlite migration failed error: %w", err)
	}
	return nil

}
