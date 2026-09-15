package store

import (
	"context"
	"log/slog"
	"time"

	"github.com/jackc/pgx/v5"
)

func (s *Store) RunJobs(ctx context.Context) {
	ticker := time.NewTicker(time.Minute)
	defer ticker.Stop()
	s.tick(ctx)
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			s.tick(ctx)
		}
	}
}

func (s *Store) tick(ctx context.Context) {
	if err := s.PurgeExpired(ctx); err != nil {
		slog.Error("purge expired trash", "error", err)
	}
	if err := s.RetainEvents(ctx); err != nil {
		slog.Error("retain events", "error", err)
	}
	if err := s.CleanupAux(ctx); err != nil {
		slog.Error("cleanup auxiliary rows", "error", err)
	}
}

func (s *Store) PurgeExpired(ctx context.Context) error {
	for {
		rows, err := s.Pool.Query(ctx, `
			SELECT DISTINCT user_id::text
			FROM clips
			WHERE status='trash' AND expires_at <= clock_timestamp()
			LIMIT 50`)
		if err != nil {
			return err
		}
		var users []string
		for rows.Next() {
			var id string
			if err := rows.Scan(&id); err != nil {
				rows.Close()
				return err
			}
			users = append(users, id)
		}
		rows.Close()
		if len(users) == 0 {
			return nil
		}
		for _, userID := range users {
			n, err := s.purgeUserExpired(ctx, userID)
			if err != nil {
				return err
			}
			if n > 0 {
				s.notify(ctx, userID)
			}
		}
		if len(users) < 50 {
			return nil
		}
	}
}

func (s *Store) purgeUserExpired(ctx context.Context, userID string) (int, error) {
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback(ctx)
	if _, err := tx.Exec(ctx, `SELECT next_seq FROM user_sync_state WHERE user_id=$1 FOR UPDATE`, userID); err != nil {
		return 0, err
	}
	rows, err := tx.Query(ctx, `
		SELECT id::text FROM clips
		WHERE user_id=$1 AND status='trash' AND expires_at <= clock_timestamp()
		ORDER BY expires_at, id
		FOR UPDATE
		LIMIT 100`, userID)
	if err != nil {
		return 0, err
	}
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return 0, err
		}
		ids = append(ids, id)
	}
	rows.Close()
	n := 0
	for _, id := range ids {
		clip, err := s.scanClip(ctx, tx, userID, id)
		if err == pgx.ErrNoRows {
			continue
		}
		if err != nil {
			return n, err
		}
		var stillExpired bool
		if err := tx.QueryRow(ctx, `SELECT status='trash' AND expires_at <= clock_timestamp() FROM clips WHERE user_id=$1 AND id=$2`,
			userID, id).Scan(&stillExpired); err != nil {
			return n, err
		}
		if !stillExpired {
			continue
		}
		if _, err := s.purgeLocked(ctx, tx, Scope{UserID: userID}, clip); err != nil {
			return n, err
		}
		n++
	}
	if err := tx.Commit(ctx); err != nil {
		return 0, err
	}
	return n, nil
}

func (s *Store) RetainEvents(ctx context.Context) error {
	days := s.Cfg.EventRetentionDays
	if days < 1 {
		days = 30
	}
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	if _, err := tx.Exec(ctx, `DELETE FROM sync_events WHERE occurred_at < clock_timestamp() - make_interval(days => $1)`, days); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `
		UPDATE user_sync_state u
		SET min_available_seq = COALESCE(
			(SELECT MIN(seq) FROM sync_events e WHERE e.user_id=u.user_id),
			u.next_seq + 1
		)`); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (s *Store) CleanupAux(ctx context.Context) error {
	if _, err := s.Pool.Exec(ctx, `DELETE FROM operation_receipts WHERE expires_at <= clock_timestamp()`); err != nil {
		return err
	}
	if _, err := s.Pool.Exec(ctx, `DELETE FROM snapshot_sessions WHERE expires_at <= clock_timestamp()`); err != nil {
		return err
	}
	if _, err := s.Pool.Exec(ctx, `DELETE FROM used_refresh_tokens WHERE expires_at <= clock_timestamp()`); err != nil {
		return err
	}
	return nil
}
