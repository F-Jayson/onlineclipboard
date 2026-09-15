package migrate

import (
	"context"
	"crypto/sha256"
	"fmt"
	"io/fs"
	"log/slog"
	"sort"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"onlineclipboard/server/migrations"
)

const advisoryLock = 0x4F434C50 // "OCLP"

func Apply(ctx context.Context, pool *pgxpool.Pool) error {
	conn, err := pool.Acquire(ctx)
	if err != nil {
		return err
	}
	defer conn.Release()
	if _, err := conn.Exec(ctx, "SELECT pg_advisory_lock($1)", advisoryLock); err != nil {
		return fmt.Errorf("migration lock: %w", err)
	}
	defer func() {
		_, _ = conn.Exec(context.Background(), "SELECT pg_advisory_unlock($1)", advisoryLock)
	}()
	if _, err := conn.Exec(ctx, `
		CREATE TABLE IF NOT EXISTS schema_migrations (
			version TEXT PRIMARY KEY,
			checksum TEXT NOT NULL,
			applied_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp()
		)`); err != nil {
		return err
	}
	entries, err := fs.ReadDir(migrations.FS, ".")
	if err != nil {
		return err
	}
	var names []string
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), ".sql") {
			names = append(names, e.Name())
		}
	}
	sort.Strings(names)
	for _, name := range names {
		body, err := fs.ReadFile(migrations.FS, name)
		if err != nil {
			return err
		}
		sum := fmt.Sprintf("%x", sha256.Sum256(body))
		var existing string
		err = conn.QueryRow(ctx, "SELECT checksum FROM schema_migrations WHERE version=$1", name).Scan(&existing)
		if err == nil {
			if existing != sum {
				return fmt.Errorf("migration %s checksum mismatch; refusing to start", name)
			}
			continue
		}
		if err != pgx.ErrNoRows {
			return err
		}
		tx, err := conn.Begin(ctx)
		if err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, string(body)); err != nil {
			_ = tx.Rollback(ctx)
			return fmt.Errorf("apply %s: %w", name, err)
		}
		if _, err := tx.Exec(ctx, "INSERT INTO schema_migrations(version, checksum) VALUES ($1,$2)", name, sum); err != nil {
			_ = tx.Rollback(ctx)
			return err
		}
		if err := tx.Commit(ctx); err != nil {
			return err
		}
		slog.Info("applied migration", "version", name)
	}
	if err := ensureServerState(ctx, conn); err != nil {
		return err
	}
	return nil
}

func ensureServerState(ctx context.Context, conn *pgxpool.Conn) error {
	var n int
	if err := conn.QueryRow(ctx, "SELECT COUNT(*) FROM server_state").Scan(&n); err != nil {
		return err
	}
	if n > 0 {
		return nil
	}
	_, err := conn.Exec(ctx, `
		INSERT INTO server_state(singleton, server_id, sync_epoch)
		VALUES (TRUE, gen_random_uuid(), gen_random_uuid())
		ON CONFLICT DO NOTHING`)
	return err
}

func CurrentVersion(ctx context.Context, pool *pgxpool.Pool) (string, error) {
	var version string
	err := pool.QueryRow(ctx, "SELECT version FROM schema_migrations ORDER BY version DESC LIMIT 1").Scan(&version)
	if err == pgx.ErrNoRows {
		return "", nil
	}
	return version, err
}
