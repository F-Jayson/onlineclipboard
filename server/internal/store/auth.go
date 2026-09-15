package store

import (
	"context"
	"net/http"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"onlineclipboard/server/internal/apperr"
	"onlineclipboard/server/internal/canon"
	"onlineclipboard/server/internal/passwd"
)

type RegisterInput struct {
	Username    string
	Password    string
	InviteToken string
	DeviceID    string
	DeviceName  string
	Platform    string
}

func (s *Store) Register(ctx context.Context, in RegisterInput) (AuthResult, error) {
	if s.Cfg.RegistrationMode == "disabled" {
		return AuthResult{}, apperr.New(http.StatusForbidden, apperr.RegistrationDisabled, "当前未开放注册。")
	}
	if s.Cfg.RegistrationMode == "invite" {
		if err := canon.Token(in.InviteToken); err != nil {
			return AuthResult{}, apperr.New(http.StatusBadRequest, apperr.InvalidRequest, "邀请码无效。")
		}
	}
	hash, err := passwd.Hash(in.Password)
	if err != nil {
		return AuthResult{}, err
	}
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return AuthResult{}, err
	}
	defer tx.Rollback(ctx)

	if s.Cfg.RegistrationMode == "invite" {
		inviteHash := canon.HashToken(in.InviteToken)
		tag, err := tx.Exec(ctx, `
			UPDATE registration_invites
			SET consumed_at=clock_timestamp()
			WHERE token_hash=$1 AND consumed_at IS NULL AND expires_at > clock_timestamp()`, inviteHash)
		if err != nil {
			return AuthResult{}, err
		}
		if tag.RowsAffected() != 1 {
			return AuthResult{}, apperr.New(http.StatusForbidden, apperr.RegistrationDisabled, "邀请码无效或已使用。")
		}
	}

	userID := uuid.NewString()
	_, err = tx.Exec(ctx, `INSERT INTO users(id, username, password_hash) VALUES ($1,$2,$3)`, userID, in.Username, hash)
	if isUnique(err) {
		return AuthResult{}, apperr.New(http.StatusConflict, apperr.IdempotencyConflict, "用户名已被使用。")
	}
	if err != nil {
		return AuthResult{}, err
	}
	if _, err := tx.Exec(ctx, `INSERT INTO user_sync_state(user_id) VALUES ($1)`, userID); err != nil {
		return AuthResult{}, err
	}
	if _, err := tx.Exec(ctx, `INSERT INTO devices(user_id, id, name, platform, last_seen_at) VALUES ($1,$2,$3,$4,clock_timestamp())`,
		userID, in.DeviceID, in.DeviceName, in.Platform); err != nil {
		return AuthResult{}, err
	}
	familyID := uuid.NewString()
	pair, _, err := issueSession(ctx, tx, userID, in.DeviceID, familyID)
	if err != nil {
		return AuthResult{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return AuthResult{}, err
	}
	return AuthResult{TokenPair: pair, UserID: userID, DeviceID: in.DeviceID, VaultInitialized: false}, nil
}

type LoginInput struct {
	Username   string
	Password   string
	DeviceID   string
	DeviceName string
	Platform   string
}

func (s *Store) Login(ctx context.Context, in LoginInput) (AuthResult, error) {
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return AuthResult{}, err
	}
	defer tx.Rollback(ctx)
	var userID, passwordHash, status string
	err = tx.QueryRow(ctx, `SELECT id::text, password_hash, status FROM users WHERE username=$1`, in.Username).Scan(&userID, &passwordHash, &status)
	if err == pgx.ErrNoRows {
		return AuthResult{}, apperr.New(http.StatusUnauthorized, apperr.Unauthenticated, "用户名或密码不正确。")
	}
	if err != nil {
		return AuthResult{}, err
	}
	ok, err := passwd.Verify(in.Password, passwordHash)
	if err != nil || !ok {
		return AuthResult{}, apperr.New(http.StatusUnauthorized, apperr.Unauthenticated, "用户名或密码不正确。")
	}
	if status != "active" {
		return AuthResult{}, apperr.New(http.StatusUnauthorized, apperr.Unauthenticated, "账号不可用。")
	}
	var existingUser, name, platform string
	var revoked pgtype.Timestamptz
	err = tx.QueryRow(ctx, `SELECT user_id::text, name, platform, revoked_at FROM devices WHERE user_id=$1 AND id=$2`, userID, in.DeviceID).
		Scan(&existingUser, &name, &platform, &revoked)
	switch {
	case err == pgx.ErrNoRows:
		if _, err := tx.Exec(ctx, `INSERT INTO devices(user_id, id, name, platform, last_seen_at) VALUES ($1,$2,$3,$4,clock_timestamp())`,
			userID, in.DeviceID, in.DeviceName, in.Platform); err != nil {
			return AuthResult{}, err
		}
	case err != nil:
		return AuthResult{}, err
	default:
		if revoked.Valid {
			return AuthResult{}, apperr.New(http.StatusForbidden, apperr.DeviceRevoked, "设备已被撤销，请作为新设备重新登记。")
		}
		if _, err := tx.Exec(ctx, `UPDATE devices SET last_seen_at=clock_timestamp() WHERE user_id=$1 AND id=$2`, userID, in.DeviceID); err != nil {
			return AuthResult{}, err
		}
	}
	familyID := uuid.NewString()
	pair, _, err := issueSession(ctx, tx, userID, in.DeviceID, familyID)
	if err != nil {
		return AuthResult{}, err
	}
	initialized, err := vaultInitialized(ctx, tx, userID)
	if err != nil {
		return AuthResult{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return AuthResult{}, err
	}
	return AuthResult{TokenPair: pair, UserID: userID, DeviceID: in.DeviceID, VaultInitialized: initialized}, nil
}

func (s *Store) Refresh(ctx context.Context, refreshToken string) (TokenPair, error) {
	if err := canon.Token(refreshToken); err != nil {
		return TokenPair{}, apperr.New(http.StatusUnauthorized, apperr.Unauthenticated, "刷新令牌无效。")
	}
	hash := canon.HashToken(refreshToken)
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return TokenPair{}, err
	}
	defer tx.Rollback(ctx)

	var familyID string
	err = tx.QueryRow(ctx, `SELECT family_id::text FROM used_refresh_tokens WHERE token_hash=$1`, hash).Scan(&familyID)
	if err == nil {
		_, _ = tx.Exec(ctx, `UPDATE sessions SET revoked_at=clock_timestamp() WHERE family_id=$1 AND revoked_at IS NULL`, familyID)
		_ = tx.Commit(ctx)
		return TokenPair{}, apperr.New(http.StatusUnauthorized, apperr.Unauthenticated, "刷新令牌已被使用，请重新登录。")
	}
	if err != pgx.ErrNoRows {
		return TokenPair{}, err
	}

	var sessionID, userID, deviceID string
	var refreshExp, revoked, deviceRevoked pgtype.Timestamptz
	var status string
	err = tx.QueryRow(ctx, `
		SELECT s.id::text, s.user_id::text, s.device_id::text, s.family_id::text,
		       s.refresh_expires_at, s.revoked_at, d.revoked_at, u.status
		FROM sessions s
		JOIN devices d ON d.user_id=s.user_id AND d.id=s.device_id
		JOIN users u ON u.id=s.user_id
		WHERE s.refresh_hash=$1
		FOR UPDATE OF s`, hash).Scan(&sessionID, &userID, &deviceID, &familyID, &refreshExp, &revoked, &deviceRevoked, &status)
	if err == pgx.ErrNoRows {
		return TokenPair{}, apperr.New(http.StatusUnauthorized, apperr.Unauthenticated, "刷新令牌无效。")
	}
	if err != nil {
		return TokenPair{}, err
	}
	if status != "active" || revoked.Valid {
		return TokenPair{}, apperr.New(http.StatusUnauthorized, apperr.Unauthenticated, "会话不可用。")
	}
	if deviceRevoked.Valid {
		return TokenPair{}, apperr.New(http.StatusForbidden, apperr.DeviceRevoked, "设备已被撤销。")
	}
	if !refreshExp.Valid || !refreshExp.Time.After(time.Now()) {
		return TokenPair{}, apperr.New(http.StatusUnauthorized, apperr.Unauthenticated, "刷新令牌已过期。")
	}

	access, accessHash, err := canon.RandomToken()
	if err != nil {
		return TokenPair{}, err
	}
	newRefresh, newRefreshHash, err := canon.RandomToken()
	if err != nil {
		return TokenPair{}, err
	}
	if _, err := tx.Exec(ctx, `INSERT INTO used_refresh_tokens(token_hash, family_id, expires_at) VALUES ($1,$2,$3)`,
		hash, familyID, refreshExp.Time); err != nil {
		return TokenPair{}, err
	}
	var newExp time.Time
	err = tx.QueryRow(ctx, `
		UPDATE sessions
		SET access_hash=$1, refresh_hash=$2, expires_at=clock_timestamp() + interval '15 minutes'
		WHERE id=$3
		RETURNING refresh_expires_at`, accessHash, newRefreshHash, sessionID).Scan(&newExp)
	if err != nil {
		return TokenPair{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return TokenPair{}, err
	}
	return TokenPair{
		AccessToken:      access,
		RefreshToken:     newRefresh,
		TokenType:        "Bearer",
		ExpiresIn:        900,
		RefreshExpiresAt: newExp.UTC(),
	}, nil
}

func (s *Store) Logout(ctx context.Context, scope Scope) error {
	_, err := s.Pool.Exec(ctx, `UPDATE sessions SET revoked_at=clock_timestamp() WHERE id=$1 AND revoked_at IS NULL`, scope.SessionID)
	return err
}

func (s *Store) ChangePassword(ctx context.Context, scope Scope, current, next string) error {
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	var hash string
	if err := tx.QueryRow(ctx, `SELECT password_hash FROM users WHERE id=$1 FOR UPDATE`, scope.UserID).Scan(&hash); err != nil {
		return err
	}
	ok, err := passwd.Verify(current, hash)
	if err != nil || !ok {
		return apperr.New(http.StatusUnauthorized, apperr.Unauthenticated, "当前密码不正确。")
	}
	newHash, err := passwd.Hash(next)
	if err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `UPDATE users SET password_hash=$1 WHERE id=$2`, newHash, scope.UserID); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `UPDATE sessions SET revoked_at=clock_timestamp() WHERE user_id=$1 AND revoked_at IS NULL`, scope.UserID); err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return err
	}
	if s.Hub != nil {
		s.Hub.CloseUser(scope.UserID)
	}
	return nil
}

func (s *Store) ListDevices(ctx context.Context, scope Scope) ([]Device, error) {
	rows, err := s.Pool.Query(ctx, `
		SELECT id::text, name, platform, created_at, last_seen_at, revoked_at
		FROM devices WHERE user_id=$1 ORDER BY created_at`, scope.UserID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Device
	for rows.Next() {
		var d Device
		var last, revoked pgtype.Timestamptz
		if err := rows.Scan(&d.ID, &d.Name, &d.Platform, &d.CreatedAt, &last, &revoked); err != nil {
			return nil, err
		}
		d.CreatedAt = d.CreatedAt.UTC()
		d.LastSeenAt = ptrTime(last)
		d.RevokedAt = ptrTime(revoked)
		out = append(out, d)
	}
	if out == nil {
		out = []Device{}
	}
	return out, rows.Err()
}

func (s *Store) RevokeDevice(ctx context.Context, scope Scope, deviceID string) error {
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	tag, err := tx.Exec(ctx, `
		UPDATE devices SET revoked_at=clock_timestamp()
		WHERE user_id=$1 AND id=$2 AND revoked_at IS NULL`, scope.UserID, deviceID)
	if err != nil {
		return err
	}
	if tag.RowsAffected() != 1 {
		var exists bool
		_ = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM devices WHERE user_id=$1 AND id=$2)`, scope.UserID, deviceID).Scan(&exists)
		if !exists {
			return apperr.New(http.StatusNotFound, apperr.NotFound, "设备不存在。")
		}
	}
	if _, err := tx.Exec(ctx, `UPDATE sessions SET revoked_at=clock_timestamp() WHERE user_id=$1 AND device_id=$2 AND revoked_at IS NULL`,
		scope.UserID, deviceID); err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return err
	}
	if s.Hub != nil {
		s.Hub.CloseDevice(scope.UserID, deviceID)
	}
	return nil
}

func (s *Store) GetVault(ctx context.Context, scope Scope) (VaultEnvelope, error) {
	var vaultID string
	var format, epoch int
	var salt, nonce, wrapped []byte
	err := s.Pool.QueryRow(ctx, `
		SELECT id::text, format_version, key_epoch, wrap_salt, wrap_nonce, wrapped_key
		FROM vaults WHERE user_id=$1`, scope.UserID).Scan(&vaultID, &format, &epoch, &salt, &nonce, &wrapped)
	if err == pgx.ErrNoRows {
		return VaultEnvelope{}, apperr.New(http.StatusNotFound, apperr.VaultNotInitialized, "保险库尚未初始化。")
	}
	if err != nil {
		return VaultEnvelope{}, err
	}
	return VaultEnvelope{
		VaultID: vaultID, FormatVersion: format, KeyEpoch: epoch,
		WrapSalt: canon.Encode(salt), WrapNonce: canon.Encode(nonce), WrappedKey: canon.Encode(wrapped),
	}, nil
}

func (s *Store) InitVault(ctx context.Context, scope Scope, env VaultEnvelope, salt, nonce, wrapped []byte) error {
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	var exists bool
	if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM vaults WHERE user_id=$1)`, scope.UserID).Scan(&exists); err != nil {
		return err
	}
	if exists {
		return apperr.New(http.StatusPreconditionFailed, apperr.VaultExists, "保险库已存在，不能覆盖。")
	}
	_, err = tx.Exec(ctx, `
		INSERT INTO vaults(user_id, id, format_version, key_epoch, wrap_salt, wrap_nonce, wrapped_key)
		VALUES ($1,$2,$3,$4,$5,$6,$7)`,
		scope.UserID, env.VaultID, env.FormatVersion, env.KeyEpoch, salt, nonce, wrapped)
	if isUnique(err) {
		return apperr.New(http.StatusPreconditionFailed, apperr.VaultExists, "保险库已存在，不能覆盖。")
	}
	if err != nil {
		return err
	}
	return tx.Commit(ctx)
}
