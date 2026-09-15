package store

import (
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	"onlineclipboard/server/internal/apperr"
	"onlineclipboard/server/internal/canon"
	"onlineclipboard/server/internal/config"
	"onlineclipboard/server/internal/notify"
	"onlineclipboard/server/internal/passwd"
)

type Store struct {
	Pool *pgxpool.Pool
	Cfg  config.Config
	Hub  *notify.Hub
}

func New(pool *pgxpool.Pool, cfg config.Config, hub *notify.Hub) *Store {
	return &Store{Pool: pool, Cfg: cfg, Hub: hub}
}

func (s *Store) Ping(ctx context.Context) error {
	return s.Pool.Ping(ctx)
}

func (s *Store) State(ctx context.Context) (ServerState, error) {
	var st ServerState
	err := s.Pool.QueryRow(ctx, "SELECT server_id::text, sync_epoch::text FROM server_state WHERE singleton").Scan(&st.ServerID, &st.SyncEpoch)
	return st, err
}

func (s *Store) notify(ctx context.Context, userID string) {
	if s.Hub == nil {
		return
	}
	var epoch string
	var hw int64
	_ = s.Pool.QueryRow(ctx, `SELECT ss.sync_epoch::text, u.next_seq
		FROM server_state ss, user_sync_state u WHERE ss.singleton AND u.user_id=$1`, userID).Scan(&epoch, &hw)
	s.Hub.Broadcast(userID, epoch, canon.FormatSeq(hw))
}

func isUnique(err error) bool {
	var pg *pgconn.PgError
	return errors.As(err, &pg) && pg.Code == "23505"
}

func (s *Store) CreateInvite(ctx context.Context, ttl time.Duration) (string, time.Time, error) {
	plain, hash, err := canon.RandomToken()
	if err != nil {
		return "", time.Time{}, err
	}
	exp := time.Now().UTC().Add(ttl)
	_, err = s.Pool.Exec(ctx, `INSERT INTO registration_invites(token_hash, expires_at) VALUES ($1,$2)`, hash, exp)
	return plain, exp, err
}

func (s *Store) ResetPassword(ctx context.Context, username, password string) error {
	hash, err := passwd.Hash(password)
	if err != nil {
		return err
	}
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	var userID string
	err = tx.QueryRow(ctx, `SELECT id::text FROM users WHERE username=$1 FOR UPDATE`, username).Scan(&userID)
	if err == pgx.ErrNoRows {
		return apperr.New(http.StatusNotFound, apperr.NotFound, "账号不存在。")
	}
	if err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `UPDATE users SET password_hash=$1 WHERE id=$2`, hash, userID); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `UPDATE sessions SET revoked_at=clock_timestamp() WHERE user_id=$1 AND revoked_at IS NULL`, userID); err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return err
	}
	if s.Hub != nil {
		s.Hub.CloseUser(userID)
	}
	return nil
}

func (s *Store) RotateSyncEpoch(ctx context.Context) (string, error) {
	var epoch string
	err := s.Pool.QueryRow(ctx, `UPDATE server_state SET sync_epoch=gen_random_uuid() WHERE singleton RETURNING sync_epoch::text`).Scan(&epoch)
	if err != nil {
		return "", err
	}
	if _, err := s.Pool.Exec(ctx, `UPDATE sessions SET revoked_at=clock_timestamp() WHERE revoked_at IS NULL`); err != nil {
		return "", err
	}
	if _, err := s.Pool.Exec(ctx, `DELETE FROM snapshot_sessions`); err != nil {
		return "", err
	}
	return epoch, nil
}

func (s *Store) LookupAccess(ctx context.Context, accessToken string) (Scope, error) {
	if err := canon.Token(accessToken); err != nil {
		return Scope{}, apperr.New(http.StatusUnauthorized, apperr.Unauthenticated, "访问令牌无效。")
	}
	hash := canon.HashToken(accessToken)
	var scope Scope
	var accessExp, revoked, deviceRevoked pgtype.Timestamptz
	var status string
	err := s.Pool.QueryRow(ctx, `
		SELECT s.id::text, s.user_id::text, s.device_id::text, s.family_id::text,
		       s.expires_at, s.revoked_at, d.revoked_at, u.status
		FROM sessions s
		JOIN devices d ON d.user_id=s.user_id AND d.id=s.device_id
		JOIN users u ON u.id=s.user_id
		WHERE s.access_hash=$1`, hash).Scan(
		&scope.SessionID, &scope.UserID, &scope.DeviceID, &scope.FamilyID,
		&accessExp, &revoked, &deviceRevoked, &status)
	if err == pgx.ErrNoRows {
		return Scope{}, apperr.New(http.StatusUnauthorized, apperr.Unauthenticated, "访问令牌无效。")
	}
	if err != nil {
		return Scope{}, err
	}
	if status != "active" {
		return Scope{}, apperr.New(http.StatusUnauthorized, apperr.Unauthenticated, "账号不可用。")
	}
	if deviceRevoked.Valid {
		return Scope{}, apperr.New(http.StatusForbidden, apperr.DeviceRevoked, "设备已被撤销。")
	}
	if revoked.Valid {
		return Scope{}, apperr.New(http.StatusUnauthorized, apperr.Unauthenticated, "会话已撤销。")
	}
	if !accessExp.Valid || !accessExp.Time.After(time.Now()) {
		return Scope{}, apperr.New(http.StatusUnauthorized, apperr.TokenExpired, "访问令牌已过期。")
	}
	_, _ = s.Pool.Exec(ctx, `UPDATE devices SET last_seen_at=clock_timestamp() WHERE user_id=$1 AND id=$2`, scope.UserID, scope.DeviceID)
	return scope, nil
}

func issueSession(ctx context.Context, tx pgx.Tx, userID, deviceID, familyID string) (TokenPair, string, error) {
	access, accessHash, err := canon.RandomToken()
	if err != nil {
		return TokenPair{}, "", err
	}
	refresh, refreshHash, err := canon.RandomToken()
	if err != nil {
		return TokenPair{}, "", err
	}
	sessionID := ""
	var refreshExp time.Time
	err = tx.QueryRow(ctx, `
		INSERT INTO sessions(id, user_id, device_id, family_id, access_hash, refresh_hash, expires_at, refresh_expires_at)
		VALUES (gen_random_uuid(), $1, $2, $3, $4, $5, clock_timestamp() + interval '15 minutes', clock_timestamp() + interval '30 days')
		RETURNING id::text, refresh_expires_at`, userID, deviceID, familyID, accessHash, refreshHash).Scan(&sessionID, &refreshExp)
	if err != nil {
		return TokenPair{}, "", err
	}
	return TokenPair{
		AccessToken:      access,
		RefreshToken:     refresh,
		TokenType:        "Bearer",
		ExpiresIn:        900,
		RefreshExpiresAt: refreshExp.UTC(),
	}, sessionID, nil
}

func vaultInitialized(ctx context.Context, q interface {
	QueryRow(context.Context, string, ...any) pgx.Row
}, userID string) (bool, error) {
	var ok bool
	err := q.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM vaults WHERE user_id=$1)`, userID).Scan(&ok)
	return ok, err
}
