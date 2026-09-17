package store

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/http"
	"strings"
	"time"
	"unicode"

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
	Email       string
	EmailCode   string
	DeviceID    string
	DeviceName  string
	Platform    string
}

func (s *Store) Register(ctx context.Context, in RegisterInput) (AuthResult, error) {
	switch s.Cfg.RegistrationMode {
	case "disabled":
		return AuthResult{}, apperr.New(http.StatusForbidden, apperr.RegistrationDisabled, "当前未开放注册。")
	case "external":
		msg := "请使用站点账号登录。"
		if s.Cfg.ExternalRegisterURL != "" {
			msg = "请先在 " + s.Cfg.ExternalRegisterURL + " 注册，再回到这里登录。"
		}
		return AuthResult{}, apperr.New(http.StatusForbidden, apperr.RegistrationDisabled, msg)
	}
	if s.Cfg.RegistrationMode == "invite" {
		if err := canon.Token(in.InviteToken); err != nil {
			return AuthResult{}, apperr.New(http.StatusBadRequest, apperr.InvalidRequest, "邀请码无效。")
		}
	}
	var email *string
	var verified *time.Time
	if s.Cfg.RegistrationMode == "email" {
		norm, err := canon.Email(in.Email)
		if err != nil {
			return AuthResult{}, apperr.New(http.StatusBadRequest, apperr.InvalidRequest, "请填写有效邮箱。")
		}
		if err := canon.EmailCode(in.EmailCode); err != nil {
			return AuthResult{}, apperr.New(http.StatusBadRequest, apperr.InvalidRequest, "邮箱验证码无效。")
		}
		email = &norm
	} else if strings.TrimSpace(in.Email) != "" {
		norm, err := canon.Email(in.Email)
		if err != nil {
			return AuthResult{}, apperr.New(http.StatusBadRequest, apperr.InvalidRequest, "请填写有效邮箱。")
		}
		email = &norm
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
	if s.Cfg.RegistrationMode == "email" {
		if err := consumeEmailCode(ctx, tx, *email, "register", in.EmailCode); err != nil {
			return AuthResult{}, err
		}
		now := time.Now().UTC()
		verified = &now
	}

	userID := uuid.NewString()
	_, err = tx.Exec(ctx, `
		INSERT INTO users(id, username, password_hash, email, email_verified_at, auth_provider)
		VALUES ($1,$2,$3,$4,$5,'local')`, userID, in.Username, hash, email, verified)
	if isUnique(err) {
		return AuthResult{}, apperr.New(http.StatusConflict, apperr.IdempotencyConflict, "用户名或邮箱已被使用。")
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

func (s *Store) RequestEmailCode(ctx context.Context, email string) error {
	if s.Cfg.RegistrationMode != "email" {
		return apperr.New(http.StatusForbidden, apperr.RegistrationDisabled, "当前未启用邮箱验证码注册。")
	}
	if s.Mailer == nil {
		return apperr.New(http.StatusServiceUnavailable, apperr.TemporarilyUnavailable, "邮件服务未配置。")
	}
	norm, err := canon.Email(email)
	if err != nil {
		return apperr.New(http.StatusBadRequest, apperr.InvalidRequest, "请填写有效邮箱。")
	}
	var exists bool
	if err := s.Pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM users WHERE auth_provider='local' AND lower(email)=lower($1))`, norm).Scan(&exists); err != nil {
		return err
	}
	if exists {
		return apperr.New(http.StatusConflict, apperr.IdempotencyConflict, "该邮箱已注册。")
	}
	var sentAt time.Time
	err = s.Pool.QueryRow(ctx, `SELECT sent_at FROM email_codes WHERE email=$1 AND purpose='register'`, norm).Scan(&sentAt)
	if err == nil && time.Since(sentAt) < time.Minute {
		return apperr.New(http.StatusTooManyRequests, apperr.RateLimited, "验证码发送过于频繁，请稍后再试。")
	} else if err != nil && err != pgx.ErrNoRows {
		return err
	}
	code, err := randomDigits(6)
	if err != nil {
		return err
	}
	hash := canon.EmailCodeHash(norm, "register", code)
	_, err = s.Pool.Exec(ctx, `
		INSERT INTO email_codes(email, purpose, code_hash, attempts, sent_at, expires_at)
		VALUES ($1,'register',$2,0,clock_timestamp(), clock_timestamp() + interval '15 minutes')
		ON CONFLICT (email) DO UPDATE
		SET purpose='register', code_hash=EXCLUDED.code_hash, attempts=0, sent_at=EXCLUDED.sent_at, expires_at=EXCLUDED.expires_at`,
		norm, hash)
	if err != nil {
		return err
	}
	if err := s.Mailer.SendCode(ctx, norm, code); err != nil {
		return apperr.New(http.StatusServiceUnavailable, apperr.TemporarilyUnavailable, "验证码发送失败。")
	}
	return nil
}

func consumeEmailCode(ctx context.Context, tx pgx.Tx, email, purpose, code string) error {
	var hash []byte
	var attempts int
	var expires time.Time
	err := tx.QueryRow(ctx, `
		SELECT code_hash, attempts, expires_at FROM email_codes
		WHERE email=$1 AND purpose=$2 FOR UPDATE`, email, purpose).Scan(&hash, &attempts, &expires)
	if err == pgx.ErrNoRows {
		return apperr.New(http.StatusBadRequest, apperr.InvalidRequest, "请先获取邮箱验证码。")
	}
	if err != nil {
		return err
	}
	if !expires.After(time.Now()) {
		_, _ = tx.Exec(ctx, `DELETE FROM email_codes WHERE email=$1 AND purpose=$2`, email, purpose)
		return apperr.New(http.StatusBadRequest, apperr.InvalidRequest, "验证码已过期。")
	}
	if attempts >= 5 {
		return apperr.New(http.StatusTooManyRequests, apperr.RateLimited, "验证码尝试次数过多。")
	}
	want := canon.EmailCodeHash(email, purpose, code)
	if !bytesEqual(hash, want) {
		_, _ = tx.Exec(ctx, `UPDATE email_codes SET attempts=attempts+1 WHERE email=$1 AND purpose=$2`, email, purpose)
		return apperr.New(http.StatusBadRequest, apperr.InvalidRequest, "邮箱验证码错误或已过期。")
	}
	_, err = tx.Exec(ctx, `DELETE FROM email_codes WHERE email=$1 AND purpose=$2`, email, purpose)
	return err
}

func bytesEqual(a, b []byte) bool {
	if len(a) != len(b) {
		return false
	}
	var v byte
	for i := range a {
		v |= a[i] ^ b[i]
	}
	return v == 0
}

func randomDigits(n int) (string, error) {
	const digits = "0123456789"
	buf := make([]byte, n)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	out := make([]byte, n)
	for i, b := range buf {
		out[i] = digits[int(b)%10]
	}
	return string(out), nil
}

type LoginInput struct {
	Username   string
	Password   string
	DeviceID   string
	DeviceName string
	Platform   string
}

func (s *Store) Login(ctx context.Context, in LoginInput) (AuthResult, error) {
	if s.Cfg.RegistrationMode == "external" {
		local, err := s.localUserExists(ctx, in.Username)
		if err != nil {
			return AuthResult{}, err
		}
		if local {
			return s.loginLocal(ctx, in)
		}
		return s.loginExternal(ctx, in)
	}
	return s.loginLocal(ctx, in)
}

func (s *Store) localUserExists(ctx context.Context, ident string) (bool, error) {
	ident = strings.TrimSpace(ident)
	var ok bool
	err := s.Pool.QueryRow(ctx, `
		SELECT EXISTS(
			SELECT 1 FROM users
			WHERE auth_provider='local'
			  AND (username=$1 OR (email IS NOT NULL AND lower(email)=lower($1)))
		)`, ident).Scan(&ok)
	return ok, err
}

func (s *Store) loginLocal(ctx context.Context, in LoginInput) (AuthResult, error) {
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return AuthResult{}, err
	}
	defer tx.Rollback(ctx)
	ident := strings.TrimSpace(in.Username)
	var userID, passwordHash, status, provider string
	err = tx.QueryRow(ctx, `SELECT id::text, password_hash, status, auth_provider FROM users WHERE username=$1`, ident).
		Scan(&userID, &passwordHash, &status, &provider)
	if err == pgx.ErrNoRows && strings.Contains(ident, "@") {
		err = tx.QueryRow(ctx, `SELECT id::text, password_hash, status, auth_provider FROM users WHERE auth_provider='local' AND lower(email)=lower($1)`, ident).
			Scan(&userID, &passwordHash, &status, &provider)
	}
	if err == pgx.ErrNoRows {
		return AuthResult{}, apperr.New(http.StatusUnauthorized, apperr.Unauthenticated, "用户名或密码不正确。")
	}
	if err != nil {
		return AuthResult{}, err
	}
	if provider != "local" {
		return AuthResult{}, apperr.New(http.StatusUnauthorized, apperr.Unauthenticated, "用户名或密码不正确。")
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

func (s *Store) loginExternal(ctx context.Context, in LoginInput) (AuthResult, error) {
	if s.External == nil {
		return AuthResult{}, apperr.New(http.StatusServiceUnavailable, apperr.TemporarilyUnavailable, "外部账号服务未配置。")
	}
	ext, err := s.External.Verify(ctx, in.Username, in.Password)
	if err != nil {
		return AuthResult{}, err
	}
	if ext.Subject == "" {
		return AuthResult{}, apperr.New(http.StatusUnauthorized, apperr.Unauthenticated, "用户名或密码不正确。")
	}
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return AuthResult{}, err
	}
	defer tx.Rollback(ctx)
	var userID, status string
	err = tx.QueryRow(ctx, `SELECT id::text, status FROM users WHERE auth_provider='external' AND external_subject=$1 FOR UPDATE`, ext.Subject).
		Scan(&userID, &status)
	switch {
	case err == pgx.ErrNoRows:
		userID = uuid.NewString()
		username, err := uniqueExternalUsername(ctx, tx, ext.Account, ext.Subject)
		if err != nil {
			return AuthResult{}, err
		}
		var email any
		if ext.Email != "" {
			if norm, err := canon.Email(ext.Email); err == nil {
				email = norm
			}
		}
		_, err = tx.Exec(ctx, `
			INSERT INTO users(id, username, password_hash, email, email_verified_at, auth_provider, external_subject)
			VALUES ($1,$2,'!',$3,CASE WHEN $3 IS NULL THEN NULL ELSE clock_timestamp() END,'external',$4)`,
			userID, username, email, ext.Subject)
		if isUnique(err) {
			return AuthResult{}, apperr.New(http.StatusConflict, apperr.IdempotencyConflict, "无法绑定外部账号。")
		}
		if err != nil {
			return AuthResult{}, err
		}
		if _, err := tx.Exec(ctx, `INSERT INTO user_sync_state(user_id) VALUES ($1)`, userID); err != nil {
			return AuthResult{}, err
		}
		status = "active"
	case err != nil:
		return AuthResult{}, err
	default:
		if ext.Email != "" {
			if norm, err := canon.Email(ext.Email); err == nil {
				_, _ = tx.Exec(ctx, `UPDATE users SET email=$1 WHERE id=$2 AND auth_provider='external'`, norm, userID)
			}
		}
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

func uniqueExternalUsername(ctx context.Context, tx pgx.Tx, account, subject string) (string, error) {
	base := sanitizeUsername(account)
	if base == "" {
		sum := sha256Hex(subject)
		base = "u_" + sum[:14]
	}
	name := base
	for i := 0; i < 8; i++ {
		var exists bool
		if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM users WHERE username=$1)`, name).Scan(&exists); err != nil {
			return "", err
		}
		if !exists {
			return name, nil
		}
		sum := sha256Hex(subject + ":" + fmt.Sprintf("%d", i))
		suffix := sum[:6]
		trim := 32 - 1 - len(suffix)
		if trim < 3 {
			trim = 3
		}
		if len(base) > trim {
			name = base[:trim] + "_" + suffix
		} else {
			name = base + "_" + suffix
		}
	}
	return "", apperr.New(http.StatusConflict, apperr.IdempotencyConflict, "无法绑定外部账号。")
}

func sanitizeUsername(account string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(strings.TrimSpace(account)) {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9', r == '_':
			b.WriteRune(r)
		case r == '.' || r == '-' || unicode.IsSpace(r):
			b.WriteByte('_')
		}
	}
	name := strings.Trim(b.String(), "_")
	for strings.Contains(name, "__") {
		name = strings.ReplaceAll(name, "__", "_")
	}
	if len(name) > 32 {
		name = name[:32]
	}
	if len(name) < 3 {
		return ""
	}
	return name
}

func sha256Hex(v string) string {
	sum := sha256.Sum256([]byte(v))
	return hex.EncodeToString(sum[:])
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
	var hash, provider string
	if err := tx.QueryRow(ctx, `SELECT password_hash, auth_provider FROM users WHERE id=$1 FOR UPDATE`, scope.UserID).Scan(&hash, &provider); err != nil {
		return err
	}
	if provider != "local" {
		return apperr.New(http.StatusForbidden, apperr.RegistrationDisabled, "请在账号中心修改密码。")
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
	var pwKDF *string
	var pwTime, pwMem, pwPar *int
	var pwKDFSalt, pwSalt, pwNonce, pwWrapped []byte
	err := s.Pool.QueryRow(ctx, `
		SELECT id::text, format_version, key_epoch, wrap_salt, wrap_nonce, wrapped_key,
		       password_kdf, password_kdf_time, password_kdf_memory, password_kdf_parallelism,
		       password_kdf_salt, password_wrap_salt, password_wrap_nonce, password_wrapped_key
		FROM vaults WHERE user_id=$1`, scope.UserID).Scan(
		&vaultID, &format, &epoch, &salt, &nonce, &wrapped,
		&pwKDF, &pwTime, &pwMem, &pwPar, &pwKDFSalt, &pwSalt, &pwNonce, &pwWrapped)
	if err == pgx.ErrNoRows {
		return VaultEnvelope{}, apperr.New(http.StatusNotFound, apperr.VaultNotInitialized, "保险库尚未初始化。")
	}
	if err != nil {
		return VaultEnvelope{}, err
	}
	env := VaultEnvelope{
		VaultID: vaultID, FormatVersion: format, KeyEpoch: epoch,
		WrapSalt: canon.Encode(salt), WrapNonce: canon.Encode(nonce), WrappedKey: canon.Encode(wrapped),
	}
	if len(pwWrapped) > 0 && pwKDF != nil && pwTime != nil && pwMem != nil && pwPar != nil {
		env.PasswordWrap = &PasswordWrap{
			KDF: *pwKDF, Time: *pwTime, Memory: *pwMem, Parallelism: *pwPar,
			KDFSalt: canon.Encode(pwKDFSalt), WrapSalt: canon.Encode(pwSalt),
			WrapNonce: canon.Encode(pwNonce), WrappedKey: canon.Encode(pwWrapped),
		}
	}
	return env, nil
}

func (s *Store) InitVault(ctx context.Context, scope Scope, env VaultEnvelope, salt, nonce, wrapped []byte, pw *PasswordWrapRaw) error {
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
	var kdf any
	var t, mem, par any
	var kdfSalt, pSalt, pNonce, pWrapped any
	if pw != nil {
		kdf, t, mem, par = pw.KDF, pw.Time, pw.Memory, pw.Parallelism
		kdfSalt, pSalt, pNonce, pWrapped = pw.KDFSalt, pw.WrapSalt, pw.WrapNonce, pw.WrappedKey
	}
	_, err = tx.Exec(ctx, `
		INSERT INTO vaults(user_id, id, format_version, key_epoch, wrap_salt, wrap_nonce, wrapped_key,
		                   password_kdf, password_kdf_time, password_kdf_memory, password_kdf_parallelism,
		                   password_kdf_salt, password_wrap_salt, password_wrap_nonce, password_wrapped_key)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15)`,
		scope.UserID, env.VaultID, env.FormatVersion, env.KeyEpoch, salt, nonce, wrapped,
		kdf, t, mem, par, kdfSalt, pSalt, pNonce, pWrapped)
	if isUnique(err) {
		return apperr.New(http.StatusPreconditionFailed, apperr.VaultExists, "保险库已存在，不能覆盖。")
	}
	if err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (s *Store) PutPasswordWrap(ctx context.Context, scope Scope, pw PasswordWrapRaw) error {
	tag, err := s.Pool.Exec(ctx, `
		UPDATE vaults
		SET password_kdf=$1, password_kdf_time=$2, password_kdf_memory=$3, password_kdf_parallelism=$4,
		    password_kdf_salt=$5, password_wrap_salt=$6, password_wrap_nonce=$7, password_wrapped_key=$8
		WHERE user_id=$9`,
		pw.KDF, pw.Time, pw.Memory, pw.Parallelism, pw.KDFSalt, pw.WrapSalt, pw.WrapNonce, pw.WrappedKey, scope.UserID)
	if err != nil {
		return err
	}
	if tag.RowsAffected() != 1 {
		return apperr.New(http.StatusNotFound, apperr.VaultNotInitialized, "保险库尚未初始化。")
	}
	return nil
}
