package store

import (
	"context"
	"net/http"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"onlineclipboard/server/internal/apperr"
	"onlineclipboard/server/internal/canon"
)

func (s *Store) Changes(ctx context.Context, scope Scope, epoch string, after int64, limit int) (ChangePage, error) {
	if limit <= 0 {
		limit = 100
	}
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return ChangePage{}, err
	}
	defer tx.Rollback(ctx)

	var currentEpoch string
	var high, minAvail int64
	if err := tx.QueryRow(ctx, `SELECT sync_epoch::text FROM server_state WHERE singleton`).Scan(&currentEpoch); err != nil {
		return ChangePage{}, err
	}
	if err := tx.QueryRow(ctx, `SELECT next_seq, min_available_seq FROM user_sync_state WHERE user_id=$1`, scope.UserID).
		Scan(&high, &minAvail); err != nil {
		return ChangePage{}, err
	}
	if epoch != currentEpoch || after > high {
		return ChangePage{}, apperr.WithDetails(http.StatusConflict, apperr.SyncResetRequired, "同步世代或游标无效，需要全量校准。", map[string]any{
			"sync_epoch": currentEpoch, "min_available_seq": canon.FormatSeq(minAvail),
		})
	}
	if after < minAvail-1 {
		return ChangePage{}, apperr.WithDetails(http.StatusGone, apperr.CursorExpired, "游标已过期，需要全量校准。", map[string]any{
			"sync_epoch": currentEpoch, "min_available_seq": canon.FormatSeq(minAvail),
		})
	}

	rows, err := tx.Query(ctx, `
		SELECT seq, clip_id::text, kind, version, occurred_at
		FROM sync_events
		WHERE user_id=$1 AND seq > $2 AND seq <= $3
		ORDER BY seq ASC
		LIMIT $4`, scope.UserID, after, high, limit)
	if err != nil {
		return ChangePage{}, err
	}
	defer rows.Close()
	events := []Change{}
	for rows.Next() {
		var e Change
		var seq int64
		if err := rows.Scan(&seq, &e.ClipID, &e.Kind, &e.Version, &e.OccurredAt); err != nil {
			return ChangePage{}, err
		}
		e.Seq = canon.FormatSeq(seq)
		e.OccurredAt = e.OccurredAt.UTC()
		events = append(events, e)
	}
	if err := rows.Err(); err != nil {
		return ChangePage{}, err
	}
	next := after
	if len(events) > 0 {
		n, _ := canon.Seq(events[len(events)-1].Seq)
		next = n
	}
	var serverTime time.Time
	if err := tx.QueryRow(ctx, `SELECT clock_timestamp()`).Scan(&serverTime); err != nil {
		return ChangePage{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return ChangePage{}, err
	}
	hasMore := next < high
	return ChangePage{
		SyncEpoch:     currentEpoch,
		Events:        events,
		NextSeq:       canon.FormatSeq(next),
		HighWatermark: canon.FormatSeq(high),
		HasMore:       hasMore,
		ServerTime:    serverTime.UTC(),
	}, nil
}

func (s *Store) BeginSnapshot(ctx context.Context, scope Scope) (Snapshot, error) {
	plain, hash, err := canon.RandomToken()
	if err != nil {
		return Snapshot{}, err
	}
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return Snapshot{}, err
	}
	defer tx.Rollback(ctx)
	var epoch string
	var high int64
	if err := tx.QueryRow(ctx, `SELECT sync_epoch::text FROM server_state WHERE singleton`).Scan(&epoch); err != nil {
		return Snapshot{}, err
	}
	if err := tx.QueryRow(ctx, `SELECT next_seq FROM user_sync_state WHERE user_id=$1`, scope.UserID).Scan(&high); err != nil {
		return Snapshot{}, err
	}
	var exp time.Time
	err = tx.QueryRow(ctx, `
		INSERT INTO snapshot_sessions(token_hash, user_id, device_id, base_seq, sync_epoch, expires_at)
		VALUES ($1,$2,$3,$4,$5::uuid, clock_timestamp() + interval '15 minutes')
		RETURNING expires_at`, hash, scope.UserID, scope.DeviceID, high, epoch).Scan(&exp)
	if err != nil {
		return Snapshot{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return Snapshot{}, err
	}
	return Snapshot{Token: plain, BaseSeq: canon.FormatSeq(high), SyncEpoch: epoch, ExpiresAt: exp.UTC()}, nil
}

func (s *Store) SnapshotPage(ctx context.Context, scope Scope, token, afterID string, limit int) (SnapshotPage, error) {
	if err := canon.Token(token); err != nil {
		return SnapshotPage{}, apperr.New(http.StatusGone, apperr.SnapshotExpired, "扫描会话无效或已过期。")
	}
	if limit <= 0 {
		limit = 100
	}
	hash := canon.HashToken(token)
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return SnapshotPage{}, err
	}
	defer tx.Rollback(ctx)

	var userID, deviceID, epoch string
	var base int64
	var exp pgtype.Timestamptz
	err = tx.QueryRow(ctx, `SELECT user_id::text, device_id::text, base_seq, sync_epoch::text, expires_at
		FROM snapshot_sessions WHERE token_hash=$1`, hash).Scan(&userID, &deviceID, &base, &epoch, &exp)
	if err == pgx.ErrNoRows {
		return SnapshotPage{}, apperr.New(http.StatusGone, apperr.SnapshotExpired, "扫描会话无效或已过期。")
	}
	if err != nil {
		return SnapshotPage{}, err
	}
	if userID != scope.UserID || deviceID != scope.DeviceID {
		return SnapshotPage{}, apperr.New(http.StatusNotFound, apperr.NotFound, "扫描会话不存在。")
	}
	var currentEpoch string
	if err := tx.QueryRow(ctx, `SELECT sync_epoch::text FROM server_state WHERE singleton`).Scan(&currentEpoch); err != nil {
		return SnapshotPage{}, err
	}
	if currentEpoch != epoch {
		return SnapshotPage{}, apperr.WithDetails(http.StatusConflict, apperr.SyncResetRequired, "同步世代已更换，需要重新扫描。", map[string]any{
			"sync_epoch": currentEpoch,
		})
	}
	var expired bool
	if err := tx.QueryRow(ctx, `SELECT clock_timestamp() >= $1`, exp.Time).Scan(&expired); err != nil {
		return SnapshotPage{}, err
	}
	if expired {
		return SnapshotPage{}, apperr.New(http.StatusGone, apperr.SnapshotExpired, "扫描会话已过期。")
	}

	after := "00000000-0000-0000-0000-000000000000"
	if afterID != "" {
		if err := canon.UUID(afterID); err != nil {
			return SnapshotPage{}, apperr.New(http.StatusBadRequest, apperr.InvalidRequest, "page_token 无效。")
		}
		after = afterID
	}
	rows, err := tx.Query(ctx, `
		SELECT id::text, vault_id::text, source_device_id::text, format_version, key_epoch, content_type,
		       nonce, ciphertext, delivery_intent, status, version, created_seq, last_seq, created_at, deleted_at, expires_at
		FROM clips
		WHERE user_id=$1 AND created_seq <= $2 AND id > $3::uuid
		  AND (status='active' OR (status='trash' AND expires_at > clock_timestamp()))
		ORDER BY id ASC
		LIMIT $4`, scope.UserID, base, after, limit+1)
	if err != nil {
		return SnapshotPage{}, err
	}
	defer rows.Close()
	var items []Clip
	for rows.Next() {
		c, err := scanClipRow(rows)
		if err != nil {
			return SnapshotPage{}, err
		}
		items = append(items, c)
	}
	if err := rows.Err(); err != nil {
		return SnapshotPage{}, err
	}
	var next *string
	if len(items) > limit {
		items = items[:limit]
		id := items[len(items)-1].ID
		next = &id
	}
	if items == nil {
		items = []Clip{}
	}
	var serverTime time.Time
	if err := tx.QueryRow(ctx, `SELECT clock_timestamp()`).Scan(&serverTime); err != nil {
		return SnapshotPage{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return SnapshotPage{}, err
	}
	return SnapshotPage{
		BaseSeq: canon.FormatSeq(base), SyncEpoch: epoch, Items: items,
		NextPageToken: next, ServerTime: serverTime.UTC(),
	}, nil
}
