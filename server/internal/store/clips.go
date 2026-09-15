package store

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"strconv"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"onlineclipboard/server/internal/apperr"
	"onlineclipboard/server/internal/canon"
)

type CreateResult struct {
	Receipt MutationReceipt
	Created bool
}

func (s *Store) CreateClip(ctx context.Context, scope Scope, env Envelope, nonce, ciphertext []byte, reqHash []byte) (CreateResult, error) {
	if env.SourceDeviceID != scope.DeviceID {
		return CreateResult{}, apperr.New(http.StatusBadRequest, apperr.InvalidRequest, "source_device_id 必须匹配当前设备。")
	}
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return CreateResult{}, err
	}
	defer tx.Rollback(ctx)

	if _, err := tx.Exec(ctx, `SELECT next_seq FROM user_sync_state WHERE user_id=$1 FOR UPDATE`, scope.UserID); err != nil {
		return CreateResult{}, err
	}

	var vaultID string
	err = tx.QueryRow(ctx, `SELECT id::text FROM vaults WHERE user_id=$1 AND id=$2 AND key_epoch=$3`,
		scope.UserID, env.VaultID, env.KeyEpoch).Scan(&vaultID)
	if err == pgx.ErrNoRows {
		return CreateResult{}, apperr.New(http.StatusBadRequest, apperr.InvalidRequest, "保险库不存在或不匹配。")
	}
	if err != nil {
		return CreateResult{}, err
	}

	var revoked pgtype.Timestamptz
	err = tx.QueryRow(ctx, `SELECT revoked_at FROM devices WHERE user_id=$1 AND id=$2`, scope.UserID, scope.DeviceID).Scan(&revoked)
	if err != nil || revoked.Valid {
		return CreateResult{}, apperr.New(http.StatusForbidden, apperr.DeviceRevoked, "设备已被撤销。")
	}

	var existingHash []byte
	var createdSeq int64
	var createdAt time.Time
	err = tx.QueryRow(ctx, `SELECT request_hash, created_seq, created_at FROM clip_ids WHERE user_id=$1 AND id=$2`,
		scope.UserID, env.ID).Scan(&existingHash, &createdSeq, &createdAt)
	if err == nil {
		if !bytes.Equal(existingHash, reqHash) {
			return CreateResult{}, apperr.New(http.StatusConflict, apperr.IdempotencyConflict, "同一条目 ID 已使用不同信封。")
		}
		if err := tx.Commit(ctx); err != nil {
			return CreateResult{}, err
		}
		return CreateResult{Receipt: MutationReceipt{
			ID: env.ID, Status: "active", Version: 1, Seq: canon.FormatSeq(createdSeq), CreatedAt: utcPtr(createdAt),
		}}, nil
	}
	if err != pgx.ErrNoRows {
		return CreateResult{}, err
	}

	var itemCount int
	var cipherBytes int64
	if err := tx.QueryRow(ctx, `SELECT item_count, ciphertext_bytes FROM user_sync_state WHERE user_id=$1`, scope.UserID).
		Scan(&itemCount, &cipherBytes); err != nil {
		return CreateResult{}, err
	}
	if itemCount+1 > s.Cfg.MaxItems || cipherBytes+int64(len(ciphertext)) > s.Cfg.MaxCiphertextBytes {
		return CreateResult{}, apperr.New(http.StatusConflict, apperr.QuotaExceeded, "已达到账号容量上限。")
	}

	var seq int64
	if err := tx.QueryRow(ctx, `UPDATE user_sync_state SET next_seq=next_seq+1, item_count=item_count+1, ciphertext_bytes=ciphertext_bytes+$2
		WHERE user_id=$1 RETURNING next_seq`, scope.UserID, len(ciphertext)).Scan(&seq); err != nil {
		return CreateResult{}, err
	}
	var created time.Time
	if err := tx.QueryRow(ctx, `SELECT clock_timestamp()`).Scan(&created); err != nil {
		return CreateResult{}, err
	}
	if _, err := tx.Exec(ctx, `INSERT INTO clip_ids(user_id, id, request_hash, created_seq, created_at) VALUES ($1,$2,$3,$4,$5)`,
		scope.UserID, env.ID, reqHash, seq, created); err != nil {
		return CreateResult{}, err
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO clips(user_id, id, source_device_id, vault_id, key_epoch, format_version, content_type,
			nonce, ciphertext, delivery_intent, status, version, created_seq, last_seq, created_at)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,'active',1,$11,$11,$12)`,
		scope.UserID, env.ID, env.SourceDeviceID, env.VaultID, env.KeyEpoch, env.FormatVersion, env.ContentType,
		nonce, ciphertext, env.DeliveryIntent, seq, created); err != nil {
		return CreateResult{}, err
	}
	if _, err := tx.Exec(ctx, `INSERT INTO sync_events(user_id, seq, clip_id, kind, version, occurred_at) VALUES ($1,$2,$3,'clip.created',1,$4)`,
		scope.UserID, seq, env.ID, created); err != nil {
		return CreateResult{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return CreateResult{}, err
	}
	s.notify(ctx, scope.UserID)
	return CreateResult{Created: true, Receipt: MutationReceipt{
		ID: env.ID, Status: "active", Version: 1, Seq: canon.FormatSeq(seq), CreatedAt: utcPtr(created),
	}}, nil
}

func utcPtr(t time.Time) *time.Time {
	v := t.UTC()
	return &v
}

func (s *Store) GetClip(ctx context.Context, scope Scope, clipID string) (Clip, error) {
	clip, err := s.scanClip(ctx, s.Pool, scope.UserID, clipID)
	if err == nil {
		if clip.Status == "trash" && clip.ExpiresAt != nil {
			var expired bool
			if err := s.Pool.QueryRow(ctx, `SELECT clock_timestamp() >= $1`, *clip.ExpiresAt).Scan(&expired); err != nil {
				return Clip{}, err
			}
			if expired {
				return Clip{}, apperr.WithDetails(http.StatusGone, apperr.TrashExpired, "回收站条目已到期。", map[string]any{
					"id": clipID, "status": "trash", "version": clip.Version, "seq": clip.LastSeq, "expires_at": clip.ExpiresAt.UTC().Format(time.RFC3339Nano),
				})
			}
		}
		return clip, nil
	}
	if err != pgx.ErrNoRows {
		return Clip{}, err
	}
	var finalVersion int
	var finalSeq int64
	var purged pgtype.Timestamptz
	err = s.Pool.QueryRow(ctx, `SELECT final_version, final_seq, purged_at FROM clip_ids WHERE user_id=$1 AND id=$2`,
		scope.UserID, clipID).Scan(&finalVersion, &finalSeq, &purged)
	if err == pgx.ErrNoRows {
		return Clip{}, apperr.New(http.StatusNotFound, apperr.NotFound, "条目不存在。")
	}
	if err != nil {
		return Clip{}, err
	}
	if purged.Valid {
		return Clip{}, apperr.WithDetails(http.StatusGone, apperr.ClipPurged, "条目已永久删除。", map[string]any{
			"id": clipID, "status": "purged", "version": finalVersion, "seq": canon.FormatSeq(finalSeq),
		})
	}
	return Clip{}, apperr.New(http.StatusNotFound, apperr.NotFound, "条目不存在。")
}

type querier interface {
	QueryRow(context.Context, string, ...any) pgx.Row
	Query(context.Context, string, ...any) (pgx.Rows, error)
	Exec(context.Context, string, ...any) (interface {
		RowsAffected() int64
	}, error)
}

func (s *Store) scanClip(ctx context.Context, q interface {
	QueryRow(context.Context, string, ...any) pgx.Row
}, userID, clipID string) (Clip, error) {
	var c Clip
	var nonce, ciphertext []byte
	var createdSeq, lastSeq int64
	var deleted, expires pgtype.Timestamptz
	err := q.QueryRow(ctx, `
		SELECT id::text, vault_id::text, source_device_id::text, format_version, key_epoch, content_type,
		       nonce, ciphertext, delivery_intent, status, version, created_seq, last_seq, created_at, deleted_at, expires_at
		FROM clips WHERE user_id=$1 AND id=$2`, userID, clipID).Scan(
		&c.ID, &c.VaultID, &c.SourceDeviceID, &c.FormatVersion, &c.KeyEpoch, &c.ContentType,
		&nonce, &ciphertext, &c.DeliveryIntent, &c.Status, &c.Version, &createdSeq, &lastSeq, &c.CreatedAt, &deleted, &expires)
	if err != nil {
		return Clip{}, err
	}
	c.Nonce = canon.Encode(nonce)
	c.Ciphertext = canon.Encode(ciphertext)
	c.CreatedSeq = canon.FormatSeq(createdSeq)
	c.LastSeq = canon.FormatSeq(lastSeq)
	c.CreatedAt = c.CreatedAt.UTC()
	c.DeletedAt = ptrTime(deleted)
	c.ExpiresAt = ptrTime(expires)
	return c, nil
}

type ListFilter struct {
	Status         string
	SourceDeviceID string
	Since          *time.Time
	Until          *time.Time
	AfterSeq       *int64
	AfterID        string
	Limit          int
}

func (s *Store) ListClips(ctx context.Context, scope Scope, f ListFilter) (ClipPage, error) {
	if f.Limit <= 0 {
		f.Limit = 100
	}
	rows, err := s.Pool.Query(ctx, `
		SELECT id::text, vault_id::text, source_device_id::text, format_version, key_epoch, content_type,
		       nonce, ciphertext, delivery_intent, status, version, created_seq, last_seq, created_at, deleted_at, expires_at
		FROM clips
		WHERE user_id=$1 AND status=$2
		  AND ($3::uuid IS NULL OR source_device_id=$3)
		  AND ($4::timestamptz IS NULL OR created_at >= $4)
		  AND ($5::timestamptz IS NULL OR created_at < $5)
		  AND (status <> 'trash' OR expires_at > clock_timestamp())
		  AND ($6::bigint IS NULL OR (created_seq, id) < ($6, $7::uuid))
		ORDER BY created_seq DESC, id DESC
		LIMIT $8`,
		scope.UserID, f.Status, nilUUID(f.SourceDeviceID), f.Since, f.Until, f.AfterSeq, nilUUID(f.AfterID), f.Limit+1)
	if err != nil {
		return ClipPage{}, err
	}
	defer rows.Close()
	var items []Clip
	for rows.Next() {
		c, err := scanClipRow(rows)
		if err != nil {
			return ClipPage{}, err
		}
		items = append(items, c)
	}
	if err := rows.Err(); err != nil {
		return ClipPage{}, err
	}
	var next *string
	if len(items) > f.Limit {
		items = items[:f.Limit]
		last := items[len(items)-1]
		tok := last.CreatedSeq + "|" + last.ID
		next = &tok
	}
	if items == nil {
		items = []Clip{}
	}
	return ClipPage{Items: items, NextPageToken: next, ServerTime: time.Now().UTC()}, nil
}

type clipRow interface {
	Scan(dest ...any) error
}

func scanClipRow(row clipRow) (Clip, error) {
	var c Clip
	var nonce, ciphertext []byte
	var createdSeq, lastSeq int64
	var deleted, expires pgtype.Timestamptz
	err := row.Scan(&c.ID, &c.VaultID, &c.SourceDeviceID, &c.FormatVersion, &c.KeyEpoch, &c.ContentType,
		&nonce, &ciphertext, &c.DeliveryIntent, &c.Status, &c.Version, &createdSeq, &lastSeq, &c.CreatedAt, &deleted, &expires)
	if err != nil {
		return Clip{}, err
	}
	c.Nonce = canon.Encode(nonce)
	c.Ciphertext = canon.Encode(ciphertext)
	c.CreatedSeq = canon.FormatSeq(createdSeq)
	c.LastSeq = canon.FormatSeq(lastSeq)
	c.CreatedAt = c.CreatedAt.UTC()
	c.DeletedAt = ptrTime(deleted)
	c.ExpiresAt = ptrTime(expires)
	return c, nil
}

func nilUUID(v string) any {
	if v == "" {
		return nil
	}
	return v
}

func (s *Store) MutateClip(ctx context.Context, scope Scope, clipID, operationID, kind string, expectedVersion int, reqHash []byte) (MutationReceipt, error) {
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return MutationReceipt{}, err
	}
	defer tx.Rollback(ctx)

	if _, err := tx.Exec(ctx, `SELECT next_seq FROM user_sync_state WHERE user_id=$1 FOR UPDATE`, scope.UserID); err != nil {
		return MutationReceipt{}, err
	}

	var storedHash []byte
	var status int
	var body []byte
	err = tx.QueryRow(ctx, `SELECT request_hash, response_status, response_body FROM operation_receipts
		WHERE user_id=$1 AND operation_id=$2 AND expires_at > clock_timestamp()`, scope.UserID, operationID).
		Scan(&storedHash, &status, &body)
	if err == nil {
		if !bytes.Equal(storedHash, reqHash) {
			return MutationReceipt{}, apperr.New(http.StatusConflict, apperr.IdempotencyConflict, "幂等键对应不同请求。")
		}
		var receipt MutationReceipt
		if err := json.Unmarshal(body, &receipt); err != nil {
			return MutationReceipt{}, err
		}
		if err := tx.Commit(ctx); err != nil {
			return MutationReceipt{}, err
		}
		return receipt, nil
	}
	if err != pgx.ErrNoRows {
		return MutationReceipt{}, err
	}

	receipt, err := s.applyMutation(ctx, tx, scope, clipID, kind, expectedVersion)
	if err != nil {
		return MutationReceipt{}, err
	}
	if _, err := tx.Exec(ctx, `INSERT INTO operation_receipts(user_id, operation_id, request_hash, response_status, response_body, expires_at)
		VALUES ($1,$2,$3,200,$4, clock_timestamp() + interval '30 days')`,
		scope.UserID, operationID, reqHash, marshalReceipt(receipt)); err != nil {
		return MutationReceipt{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return MutationReceipt{}, err
	}
	s.notify(ctx, scope.UserID)
	return receipt, nil
}

func (s *Store) applyMutation(ctx context.Context, tx pgx.Tx, scope Scope, clipID, kind string, expectedVersion int) (MutationReceipt, error) {
	clip, err := s.scanClip(ctx, tx, scope.UserID, clipID)
	if err == pgx.ErrNoRows {
		var finalVersion *int
		var finalSeq *int64
		var purged pgtype.Timestamptz
		err2 := tx.QueryRow(ctx, `SELECT final_version, final_seq, purged_at FROM clip_ids WHERE user_id=$1 AND id=$2`,
			scope.UserID, clipID).Scan(&finalVersion, &finalSeq, &purged)
		if err2 == pgx.ErrNoRows {
			return MutationReceipt{}, apperr.New(http.StatusNotFound, apperr.NotFound, "条目不存在。")
		}
		if err2 != nil {
			return MutationReceipt{}, err2
		}
		if purged.Valid && finalVersion != nil && finalSeq != nil {
			return MutationReceipt{}, apperr.WithDetails(http.StatusGone, apperr.ClipPurged, "条目已永久删除。", map[string]any{
				"id": clipID, "status": "purged", "version": *finalVersion, "seq": canon.FormatSeq(*finalSeq),
			})
		}
		return MutationReceipt{}, apperr.New(http.StatusNotFound, apperr.NotFound, "条目不存在。")
	}
	if err != nil {
		return MutationReceipt{}, err
	}
	if clip.Version != expectedVersion {
		return MutationReceipt{}, apperr.WithDetails(http.StatusPreconditionFailed, apperr.VersionConflict, "条目状态已改变，请刷新后重试。", map[string]any{
			"id": clipID, "status": clip.Status, "version": clip.Version, "seq": clip.LastSeq,
		})
	}

	switch kind {
	case "trash":
		if clip.Status != "active" {
			return MutationReceipt{}, apperr.WithDetails(http.StatusPreconditionFailed, apperr.VersionConflict, "条目状态已改变，请刷新后重试。", map[string]any{
				"id": clipID, "status": clip.Status, "version": clip.Version, "seq": clip.LastSeq,
			})
		}
		return s.bump(ctx, tx, scope, clip, "trash", "clip.trashed", true)
	case "restore":
		if clip.Status != "trash" {
			return MutationReceipt{}, apperr.WithDetails(http.StatusPreconditionFailed, apperr.VersionConflict, "条目状态已改变，请刷新后重试。", map[string]any{
				"id": clipID, "status": clip.Status, "version": clip.Version, "seq": clip.LastSeq,
			})
		}
		var expired bool
		if err := tx.QueryRow(ctx, `SELECT clock_timestamp() >= expires_at FROM clips WHERE user_id=$1 AND id=$2`, scope.UserID, clip.ID).Scan(&expired); err != nil {
			return MutationReceipt{}, err
		}
		if expired {
			return MutationReceipt{}, apperr.WithDetails(http.StatusGone, apperr.TrashExpired, "回收站条目已到期。", map[string]any{
				"id": clipID, "status": "trash", "version": clip.Version, "seq": clip.LastSeq, "expires_at": clip.ExpiresAt,
			})
		}
		return s.bump(ctx, tx, scope, clip, "active", "clip.restored", false)
	case "purge":
		if clip.Status != "trash" {
			return MutationReceipt{}, apperr.WithDetails(http.StatusPreconditionFailed, apperr.VersionConflict, "条目状态已改变，请刷新后重试。", map[string]any{
				"id": clipID, "status": clip.Status, "version": clip.Version, "seq": clip.LastSeq,
			})
		}
		return s.purgeLocked(ctx, tx, scope, clip)
	default:
		return MutationReceipt{}, apperr.New(http.StatusBadRequest, apperr.InvalidRequest, "未知操作。")
	}
}

func (s *Store) bump(ctx context.Context, tx pgx.Tx, scope Scope, clip Clip, status, eventKind string, trash bool) (MutationReceipt, error) {
	var seq int64
	if err := tx.QueryRow(ctx, `UPDATE user_sync_state SET next_seq=next_seq+1 WHERE user_id=$1 RETURNING next_seq`, scope.UserID).Scan(&seq); err != nil {
		return MutationReceipt{}, err
	}
	version := clip.Version + 1
	var deleted, expires *time.Time
	if trash {
		err := tx.QueryRow(ctx, `
			UPDATE clips SET status='trash', version=$3, last_seq=$4,
				deleted_at=clock_timestamp(), expires_at=clock_timestamp() + interval '168 hours'
			WHERE user_id=$1 AND id=$2
			RETURNING deleted_at, expires_at`, scope.UserID, clip.ID, version, seq).Scan(&deleted, &expires)
		if err != nil {
			return MutationReceipt{}, err
		}
	} else {
		if _, err := tx.Exec(ctx, `
			UPDATE clips SET status='active', version=$3, last_seq=$4, deleted_at=NULL, expires_at=NULL
			WHERE user_id=$1 AND id=$2`, scope.UserID, clip.ID, version, seq); err != nil {
			return MutationReceipt{}, err
		}
	}
	if _, err := tx.Exec(ctx, `INSERT INTO sync_events(user_id, seq, clip_id, kind, version) VALUES ($1,$2,$3,$4,$5)`,
		scope.UserID, seq, clip.ID, eventKind, version); err != nil {
		return MutationReceipt{}, err
	}
	receipt := MutationReceipt{ID: clip.ID, Status: status, Version: version, Seq: canon.FormatSeq(seq)}
	if deleted != nil {
		receipt.DeletedAt = utcPtr(*deleted)
	}
	if expires != nil {
		receipt.ExpiresAt = utcPtr(*expires)
	}
	return receipt, nil
}

func (s *Store) purgeLocked(ctx context.Context, tx pgx.Tx, scope Scope, clip Clip) (MutationReceipt, error) {
	var cipherLen int
	if err := tx.QueryRow(ctx, `SELECT octet_length(ciphertext) FROM clips WHERE user_id=$1 AND id=$2`, scope.UserID, clip.ID).Scan(&cipherLen); err != nil {
		return MutationReceipt{}, err
	}
	var seq int64
	if err := tx.QueryRow(ctx, `UPDATE user_sync_state
		SET next_seq=next_seq+1, item_count=item_count-1, ciphertext_bytes=ciphertext_bytes-$2
		WHERE user_id=$1 RETURNING next_seq`, scope.UserID, cipherLen).Scan(&seq); err != nil {
		return MutationReceipt{}, err
	}
	version := clip.Version + 1
	if _, err := tx.Exec(ctx, `DELETE FROM clips WHERE user_id=$1 AND id=$2`, scope.UserID, clip.ID); err != nil {
		return MutationReceipt{}, err
	}
	if _, err := tx.Exec(ctx, `UPDATE clip_ids SET final_version=$3, final_seq=$4, purged_at=clock_timestamp() WHERE user_id=$1 AND id=$2`,
		scope.UserID, clip.ID, version, seq); err != nil {
		return MutationReceipt{}, err
	}
	if _, err := tx.Exec(ctx, `INSERT INTO sync_events(user_id, seq, clip_id, kind, version) VALUES ($1,$2,$3,'clip.purged',$4)`,
		scope.UserID, seq, clip.ID, version); err != nil {
		return MutationReceipt{}, err
	}
	return MutationReceipt{ID: clip.ID, Status: "purged", Version: version, Seq: canon.FormatSeq(seq)}, nil
}

func ParsePageToken(token string) (seq int64, id string, err error) {
	seqS, id, ok := splitOnce(token, "|")
	if !ok {
		return 0, "", apperr.New(http.StatusBadRequest, apperr.InvalidRequest, "page_token 无效。")
	}
	seq, err = strconv.ParseInt(seqS, 10, 64)
	if err != nil {
		return 0, "", apperr.New(http.StatusBadRequest, apperr.InvalidRequest, "page_token 无效。")
	}
	if err := canon.UUID(id); err != nil {
		return 0, "", apperr.New(http.StatusBadRequest, apperr.InvalidRequest, "page_token 无效。")
	}
	return seq, id, nil
}

func splitOnce(s, sep string) (string, string, bool) {
	i := indexOf(s, sep)
	if i < 0 {
		return "", "", false
	}
	return s[:i], s[i+len(sep):], true
}

func indexOf(s, sep string) int {
	for i := 0; i+len(sep) <= len(s); i++ {
		if s[i:i+len(sep)] == sep {
			return i
		}
	}
	return -1
}
