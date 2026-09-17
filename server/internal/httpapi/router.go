package httpapi

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/coder/websocket"

	"onlineclipboard/server/internal/apperr"
	"onlineclipboard/server/internal/canon"
	"onlineclipboard/server/internal/config"
	"onlineclipboard/server/internal/notify"
	"onlineclipboard/server/internal/store"
)

type Handler struct {
	cfg       config.Config
	store     *store.Store
	hub       *notify.Hub
	ready     bool
	argon     chan struct{}
	mu        sync.Mutex
	loginHits map[string][]time.Time
	ipHits    map[string][]time.Time
}

func New(cfg config.Config, st *store.Store, hub *notify.Hub, ready bool) http.Handler {
	h := &Handler{
		cfg: cfg, store: st, hub: hub, ready: ready,
		argon:     make(chan struct{}, 2),
		loginHits: map[string][]time.Time{},
		ipHits:    map[string][]time.Time{},
	}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", h.health)
	mux.HandleFunc("GET /readyz", h.readyz)
	mux.HandleFunc("GET /api/v1/server-info", h.serverInfo)
	mux.HandleFunc("POST /api/v1/auth/register", h.register)
	mux.HandleFunc("POST /api/v1/auth/email-code", h.emailCode)
	mux.HandleFunc("POST /api/v1/auth/login", h.login)
	mux.HandleFunc("POST /api/v1/auth/refresh", h.refresh)
	mux.HandleFunc("POST /api/v1/auth/logout", h.auth(h.logout))
	mux.HandleFunc("POST /api/v1/auth/password", h.auth(h.changePassword))
	mux.HandleFunc("GET /api/v1/devices", h.auth(h.listDevices))
	mux.HandleFunc("DELETE /api/v1/devices/{id}", h.auth(h.revokeDevice))
	mux.HandleFunc("GET /api/v1/vault", h.auth(h.getVault))
	mux.HandleFunc("PUT /api/v1/vault", h.auth(h.putVault))
	mux.HandleFunc("PUT /api/v1/vault/password-wrap", h.auth(h.putPasswordWrap))
	mux.HandleFunc("POST /api/v1/clips", h.auth(h.createClip))
	mux.HandleFunc("GET /api/v1/clips", h.auth(h.listClips))
	mux.HandleFunc("GET /api/v1/clips/{id}", h.auth(h.getClip))
	mux.HandleFunc("DELETE /api/v1/clips/{id}", h.auth(h.trashClip))
	mux.HandleFunc("POST /api/v1/clips/{id}/restore", h.auth(h.restoreClip))
	mux.HandleFunc("DELETE /api/v1/clips/{id}/permanent", h.auth(h.purgeClip))
	mux.HandleFunc("GET /api/v1/sync/changes", h.auth(h.changes))
	mux.HandleFunc("POST /api/v1/sync/snapshots", h.auth(h.beginSnapshot))
	mux.HandleFunc("GET /api/v1/sync/snapshots/{token}", h.auth(h.snapshotPage))
	mux.HandleFunc("GET /api/v1/sync/ws", h.auth(h.websocket))
	mux.HandleFunc("/api/v1/", func(w http.ResponseWriter, r *http.Request) {
		writeError(w, apperr.New(http.StatusNotFound, apperr.NotFound, "Route not found."))
	})
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		writeError(w, apperr.New(http.StatusNotFound, apperr.NotFound, "Route not found."))
	})
	return http.MaxBytesHandler(mux, int64(cfg.MaxRequestBytes))
}

func (h *Handler) health(w http.ResponseWriter, r *http.Request) {
	stage := "skeleton"
	if h.ready {
		stage = "ready"
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok", "stage": stage})
}

func (h *Handler) readyz(w http.ResponseWriter, r *http.Request) {
	if !h.ready || h.store == nil {
		writeError(w, apperr.New(http.StatusServiceUnavailable, apperr.NotReady, "Business services have not been implemented."))
		return
	}
	if err := h.store.Ping(r.Context()); err != nil {
		writeError(w, apperr.New(http.StatusServiceUnavailable, apperr.NotReady, "Database is not reachable."))
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok", "stage": "ready"})
}

func (h *Handler) serverInfo(w http.ResponseWriter, r *http.Request) {
	stage := "skeleton"
	syncOn := false
	if h.ready {
		stage = "ready"
		syncOn = true
	}
	info := map[string]any{
		"name": "OnlineClipboard", "version": "0.1.0",
		"stage": stage, "protocol_version": 1,
		"sync_available": syncOn, "e2ee_available": syncOn,
		"registration_mode":       h.cfg.RegistrationMode,
		"email_verification":      h.cfg.EmailVerification(),
		"min_password_chars":      h.cfg.MinPasswordChars(),
		"password_wrap":           true,
		"max_text_bytes":          h.cfg.MaxTextBytes,
		"max_request_bytes":       h.cfg.MaxRequestBytes,
		"trash_retention_seconds": 604800,
	}
	if h.cfg.ExternalRegisterURL != "" {
		info["external_register_url"] = h.cfg.ExternalRegisterURL
	}
	writeJSON(w, http.StatusOK, info)
}

func (h *Handler) register(w http.ResponseWriter, r *http.Request) {
	if !h.gateIP(w, r, 5) {
		return
	}
	var body struct {
		Username    string `json:"username"`
		Password    string `json:"password"`
		InviteToken string `json:"invite_token"`
		Email       string `json:"email"`
		EmailCode   string `json:"email_code"`
		Device      struct {
			ID       string `json:"id"`
			Name     string `json:"name"`
			Platform string `json:"platform"`
		} `json:"device"`
	}
	if !decodeJSON(w, r, &body) {
		return
	}
	if err := canon.Username(body.Username); err != nil {
		writeError(w, apperr.New(http.StatusBadRequest, apperr.InvalidRequest, err.Error()))
		return
	}
	if err := canon.Password(body.Password); err != nil {
		writeError(w, apperr.New(http.StatusBadRequest, apperr.InvalidRequest, err.Error()))
		return
	}
	if err := canon.UUID(body.Device.ID); err != nil || canon.DeviceName(body.Device.Name) != nil || canon.Platform(body.Device.Platform) != nil {
		writeError(w, apperr.New(http.StatusBadRequest, apperr.InvalidRequest, "设备信息不正确。"))
		return
	}
	h.argon <- struct{}{}
	defer func() { <-h.argon }()
	result, err := h.store.Register(r.Context(), store.RegisterInput{
		Username: body.Username, Password: body.Password, InviteToken: body.InviteToken,
		Email: body.Email, EmailCode: body.EmailCode,
		DeviceID: body.Device.ID, DeviceName: body.Device.Name, Platform: body.Device.Platform,
	})
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, result)
}

func (h *Handler) login(w http.ResponseWriter, r *http.Request) {
	if !h.gateIP(w, r, 20) {
		return
	}
	var body struct {
		Username string `json:"username"`
		Password string `json:"password"`
		Device   struct {
			ID       string `json:"id"`
			Name     string `json:"name"`
			Platform string `json:"platform"`
		} `json:"device"`
	}
	if !decodeJSON(w, r, &body) {
		return
	}
	if !h.gateKey(w, "u:"+body.Username, 5) {
		return
	}
	if err := canon.LoginIdentifier(body.Username); err != nil || canon.LoginSecret(body.Password) != nil {
		writeError(w, apperr.New(http.StatusUnauthorized, apperr.Unauthenticated, "用户名或密码不正确。"))
		return
	}
	if h.cfg.RegistrationMode != "external" && canon.Password(body.Password) != nil {
		writeError(w, apperr.New(http.StatusUnauthorized, apperr.Unauthenticated, "用户名或密码不正确。"))
		return
	}
	if err := canon.UUID(body.Device.ID); err != nil || canon.DeviceName(body.Device.Name) != nil || canon.Platform(body.Device.Platform) != nil {
		writeError(w, apperr.New(http.StatusBadRequest, apperr.InvalidRequest, "设备信息不正确。"))
		return
	}
	h.argon <- struct{}{}
	defer func() { <-h.argon }()
	result, err := h.store.Login(r.Context(), store.LoginInput{
		Username: body.Username, Password: body.Password,
		DeviceID: body.Device.ID, DeviceName: body.Device.Name, Platform: body.Device.Platform,
	})
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, result)
}

func (h *Handler) refresh(w http.ResponseWriter, r *http.Request) {
	var body struct {
		RefreshToken string `json:"refresh_token"`
	}
	if !decodeJSON(w, r, &body) {
		return
	}
	pair, err := h.store.Refresh(r.Context(), body.RefreshToken)
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, pair)
}

type authed func(http.ResponseWriter, *http.Request, store.Scope)

func (h *Handler) auth(next authed) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		header := r.Header.Get("Authorization")
		if !strings.HasPrefix(header, "Bearer ") {
			writeError(w, apperr.New(http.StatusUnauthorized, apperr.Unauthenticated, "需要访问令牌。"))
			return
		}
		scope, err := h.store.LookupAccess(r.Context(), strings.TrimPrefix(header, "Bearer "))
		if err != nil {
			writeErr(w, err)
			return
		}
		next(w, r, scope)
	}
}

func (h *Handler) logout(w http.ResponseWriter, r *http.Request, scope store.Scope) {
	if err := h.store.Logout(r.Context(), scope); err != nil {
		writeErr(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *Handler) changePassword(w http.ResponseWriter, r *http.Request, scope store.Scope) {
	var body struct {
		Current string `json:"current_password"`
		New     string `json:"new_password"`
	}
	if !decodeJSON(w, r, &body) {
		return
	}
	if err := canon.Password(body.Current); err != nil || canon.Password(body.New) != nil {
		writeError(w, apperr.New(http.StatusBadRequest, apperr.InvalidRequest, "密码格式不正确。"))
		return
	}
	h.argon <- struct{}{}
	defer func() { <-h.argon }()
	if err := h.store.ChangePassword(r.Context(), scope, body.Current, body.New); err != nil {
		writeErr(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *Handler) listDevices(w http.ResponseWriter, r *http.Request, scope store.Scope) {
	items, err := h.store.ListDevices(r.Context(), scope)
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items})
}

func (h *Handler) revokeDevice(w http.ResponseWriter, r *http.Request, scope store.Scope) {
	id := r.PathValue("id")
	if err := canon.UUID(id); err != nil {
		writeError(w, apperr.New(http.StatusNotFound, apperr.NotFound, "设备不存在。"))
		return
	}
	if err := h.store.RevokeDevice(r.Context(), scope, id); err != nil {
		writeErr(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *Handler) getVault(w http.ResponseWriter, r *http.Request, scope store.Scope) {
	v, err := h.store.GetVault(r.Context(), scope)
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, v)
}

func (h *Handler) putVault(w http.ResponseWriter, r *http.Request, scope store.Scope) {
	if r.Header.Get("If-None-Match") != "*" {
		writeError(w, apperr.New(http.StatusPreconditionRequired, apperr.PreconditionRequired, "首次初始化必须携带 If-None-Match: *。"))
		return
	}
	var env store.VaultEnvelope
	if !decodeJSON(w, r, &env) {
		return
	}
	if err := canon.UUID(env.VaultID); err != nil || env.FormatVersion != 1 || env.KeyEpoch != 1 {
		writeError(w, apperr.New(http.StatusBadRequest, apperr.UnsupportedFormat, "保险库格式不受支持。"))
		return
	}
	salt, err := canon.Decode(env.WrapSalt, 32)
	if err != nil {
		writeError(w, apperr.New(http.StatusBadRequest, apperr.InvalidRequest, "wrap_salt 无效。"))
		return
	}
	nonce, err := canon.Decode(env.WrapNonce, 12)
	if err != nil {
		writeError(w, apperr.New(http.StatusBadRequest, apperr.InvalidRequest, "wrap_nonce 无效。"))
		return
	}
	wrapped, err := canon.Decode(env.WrappedKey, 48)
	if err != nil {
		writeError(w, apperr.New(http.StatusBadRequest, apperr.InvalidRequest, "wrapped_key 无效。"))
		return
	}
	pw, perr := parsePasswordWrap(env.PasswordWrap)
	if perr != nil {
		writeError(w, perr)
		return
	}
	if err := h.store.InitVault(r.Context(), scope, env, salt, nonce, wrapped, pw); err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, env)
}

func (h *Handler) emailCode(w http.ResponseWriter, r *http.Request) {
	if !h.gateIP(w, r, 5) {
		return
	}
	var body struct {
		Email string `json:"email"`
	}
	if !decodeJSON(w, r, &body) {
		return
	}
	if err := h.store.RequestEmailCode(r.Context(), body.Email); err != nil {
		writeErr(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *Handler) putPasswordWrap(w http.ResponseWriter, r *http.Request, scope store.Scope) {
	var wrap store.PasswordWrap
	if !decodeJSON(w, r, &wrap) {
		return
	}
	pw, perr := parsePasswordWrap(&wrap)
	if perr != nil {
		writeError(w, perr)
		return
	}
	if pw == nil {
		writeError(w, apperr.New(http.StatusBadRequest, apperr.InvalidRequest, "password wrap 无效。"))
		return
	}
	if err := h.store.PutPasswordWrap(r.Context(), scope, *pw); err != nil {
		writeErr(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func parsePasswordWrap(pw *store.PasswordWrap) (*store.PasswordWrapRaw, *apperr.Error) {
	if pw == nil {
		return nil, nil
	}
	if pw.KDF != clipcryptoKDF {
		return nil, apperr.New(http.StatusBadRequest, apperr.InvalidRequest, "password wrap kdf 无效。")
	}
	if pw.Time < 1 || pw.Time > 10 || pw.Memory < 8192 || pw.Memory > 262144 || pw.Parallelism < 1 || pw.Parallelism > 4 {
		return nil, apperr.New(http.StatusBadRequest, apperr.InvalidRequest, "password wrap 参数无效。")
	}
	kdfSalt, err := canon.Decode(pw.KDFSalt, 16)
	if err != nil {
		return nil, apperr.New(http.StatusBadRequest, apperr.InvalidRequest, "password kdf_salt 无效。")
	}
	salt, err := canon.Decode(pw.WrapSalt, 32)
	if err != nil {
		return nil, apperr.New(http.StatusBadRequest, apperr.InvalidRequest, "password wrap_salt 无效。")
	}
	nonce, err := canon.Decode(pw.WrapNonce, 12)
	if err != nil {
		return nil, apperr.New(http.StatusBadRequest, apperr.InvalidRequest, "password wrap_nonce 无效。")
	}
	wrapped, err := canon.Decode(pw.WrappedKey, 48)
	if err != nil {
		return nil, apperr.New(http.StatusBadRequest, apperr.InvalidRequest, "password wrapped_key 无效。")
	}
	return &store.PasswordWrapRaw{
		KDF: pw.KDF, Time: pw.Time, Memory: pw.Memory, Parallelism: pw.Parallelism,
		KDFSalt: kdfSalt, WrapSalt: salt, WrapNonce: nonce, WrappedKey: wrapped,
	}, nil
}

const clipcryptoKDF = "argon2id"

func (h *Handler) createClip(w http.ResponseWriter, r *http.Request, scope store.Scope) {
	var env store.Envelope
	if !decodeJSON(w, r, &env) {
		return
	}
	if err := validateEnvelope(env); err != nil {
		writeError(w, err)
		return
	}
	nonce, err := canon.Decode(env.Nonce, 12)
	if err != nil {
		writeError(w, apperr.New(http.StatusBadRequest, apperr.InvalidRequest, "nonce 无效。"))
		return
	}
	ciphertext, err := canon.Decode(env.Ciphertext, -1)
	if err != nil || len(ciphertext) < 17 || len(ciphertext) > 65552 {
		writeError(w, apperr.New(http.StatusBadRequest, apperr.InvalidRequest, "ciphertext 长度无效。"))
		return
	}
	hash := canon.CreateRequestHash(env.ID, env.VaultID, env.SourceDeviceID, env.FormatVersion, env.KeyEpoch, env.ContentType, nonce, ciphertext, env.DeliveryIntent)
	result, err := h.store.CreateClip(r.Context(), scope, env, nonce, ciphertext, hash)
	if err != nil {
		writeErr(w, err)
		return
	}
	status := http.StatusOK
	if result.Created {
		status = http.StatusCreated
	}
	writeJSON(w, status, result.Receipt)
}

func validateEnvelope(env store.Envelope) *apperr.Error {
	if canon.UUID(env.ID) != nil || canon.UUID(env.VaultID) != nil || canon.UUID(env.SourceDeviceID) != nil {
		return apperr.New(http.StatusBadRequest, apperr.InvalidRequest, "条目标识无效。")
	}
	if env.FormatVersion != 1 || env.KeyEpoch != 1 || env.ContentType != "text/plain" {
		return apperr.New(http.StatusBadRequest, apperr.UnsupportedFormat, "不支持的加密格式。")
	}
	if env.DeliveryIntent != "live" && env.DeliveryIntent != "history_only" {
		return apperr.New(http.StatusBadRequest, apperr.InvalidRequest, "delivery_intent 无效。")
	}
	return nil
}

func (h *Handler) listClips(w http.ResponseWriter, r *http.Request, scope store.Scope) {
	q := r.URL.Query()
	f := store.ListFilter{Status: q.Get("status"), SourceDeviceID: q.Get("source_device_id")}
	if f.Status == "" {
		f.Status = "active"
	}
	if f.Status != "active" && f.Status != "trash" {
		writeError(w, apperr.New(http.StatusBadRequest, apperr.InvalidRequest, "status 无效。"))
		return
	}
	if f.SourceDeviceID != "" && canon.UUID(f.SourceDeviceID) != nil {
		writeError(w, apperr.New(http.StatusBadRequest, apperr.InvalidRequest, "source_device_id 无效。"))
		return
	}
	if v := q.Get("since"); v != "" {
		t, err := time.Parse(time.RFC3339, v)
		if err != nil {
			writeError(w, apperr.New(http.StatusBadRequest, apperr.InvalidRequest, "since 无效。"))
			return
		}
		f.Since = &t
	}
	if v := q.Get("until"); v != "" {
		t, err := time.Parse(time.RFC3339, v)
		if err != nil {
			writeError(w, apperr.New(http.StatusBadRequest, apperr.InvalidRequest, "until 无效。"))
			return
		}
		f.Until = &t
	}
	limit, err := parseLimit(q.Get("limit"))
	if err != nil {
		writeErr(w, err)
		return
	}
	f.Limit = limit
	if tok := q.Get("page_token"); tok != "" {
		seq, id, err := store.ParsePageToken(tok)
		if err != nil {
			writeErr(w, err)
			return
		}
		f.AfterSeq = &seq
		f.AfterID = id
	}
	page, err := h.store.ListClips(r.Context(), scope, f)
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, page)
}

func (h *Handler) getClip(w http.ResponseWriter, r *http.Request, scope store.Scope) {
	id := r.PathValue("id")
	if err := canon.UUID(id); err != nil {
		writeError(w, apperr.New(http.StatusNotFound, apperr.NotFound, "条目不存在。"))
		return
	}
	clip, err := h.store.GetClip(r.Context(), scope, id)
	if err != nil {
		writeErr(w, err)
		return
	}
	w.Header().Set("ETag", `"`+strconv.Itoa(clip.Version)+`"`)
	writeJSON(w, http.StatusOK, clip)
}

func (h *Handler) trashClip(w http.ResponseWriter, r *http.Request, scope store.Scope) {
	h.mutate(w, r, scope, "trash")
}
func (h *Handler) restoreClip(w http.ResponseWriter, r *http.Request, scope store.Scope) {
	h.mutate(w, r, scope, "restore")
}
func (h *Handler) purgeClip(w http.ResponseWriter, r *http.Request, scope store.Scope) {
	h.mutate(w, r, scope, "purge")
}

func (h *Handler) mutate(w http.ResponseWriter, r *http.Request, scope store.Scope, kind string) {
	id := r.PathValue("id")
	if err := canon.UUID(id); err != nil {
		writeError(w, apperr.New(http.StatusNotFound, apperr.NotFound, "条目不存在。"))
		return
	}
	op := r.Header.Get("Idempotency-Key")
	if err := canon.UUID(op); err != nil {
		writeError(w, apperr.New(http.StatusBadRequest, apperr.InvalidRequest, "Idempotency-Key 必须是 UUID。"))
		return
	}
	match := r.Header.Get("If-Match")
	if match == "" {
		writeError(w, apperr.New(http.StatusPreconditionRequired, apperr.PreconditionRequired, "缺少 If-Match。"))
		return
	}
	version, err := canon.ParseIfMatch(match)
	if err != nil {
		writeError(w, apperr.New(http.StatusBadRequest, apperr.InvalidRequest, err.Error()))
		return
	}
	hash := canon.OperationRequestHash(kind, id, version)
	receipt, err := h.store.MutateClip(r.Context(), scope, id, op, kind, version, hash)
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, receipt)
}

func (h *Handler) changes(w http.ResponseWriter, r *http.Request, scope store.Scope) {
	epoch := r.URL.Query().Get("epoch")
	if err := canon.UUID(epoch); err != nil {
		writeError(w, apperr.New(http.StatusBadRequest, apperr.InvalidRequest, "epoch 无效。"))
		return
	}
	after, err := canon.Seq(r.URL.Query().Get("after"))
	if err != nil {
		writeError(w, apperr.New(http.StatusBadRequest, apperr.InvalidRequest, "after 无效。"))
		return
	}
	limit, err := parseLimit(r.URL.Query().Get("limit"))
	if err != nil {
		writeErr(w, err)
		return
	}
	page, err := h.store.Changes(r.Context(), scope, epoch, after, limit)
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, page)
}

func (h *Handler) beginSnapshot(w http.ResponseWriter, r *http.Request, scope store.Scope) {
	snap, err := h.store.BeginSnapshot(r.Context(), scope)
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, snap)
}

func (h *Handler) snapshotPage(w http.ResponseWriter, r *http.Request, scope store.Scope) {
	token := r.PathValue("token")
	limit, err := parseLimit(r.URL.Query().Get("limit"))
	if err != nil {
		writeErr(w, err)
		return
	}
	page, err := h.store.SnapshotPage(r.Context(), scope, token, r.URL.Query().Get("page_token"), limit)
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, page)
}

func (h *Handler) websocket(w http.ResponseWriter, r *http.Request, scope store.Scope) {
	conn, err := websocket.Accept(w, r, &websocket.AcceptOptions{InsecureSkipVerify: true})
	if err != nil {
		return
	}
	c := &notify.Conn{WS: conn, UserID: scope.UserID, DeviceID: scope.DeviceID, SessionID: scope.SessionID}
	h.hub.Add(c)
	defer func() {
		h.hub.Remove(c)
		_ = conn.Close(websocket.StatusNormalClosure, "")
	}()
	ctx := r.Context()
	go func() {
		ticker := time.NewTicker(30 * time.Second)
		defer ticker.Stop()
		authTick := time.NewTicker(60 * time.Second)
		defer authTick.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				pctx, cancel := context.WithTimeout(ctx, 60*time.Second)
				err := conn.Ping(pctx)
				cancel()
				if err != nil {
					_ = conn.Close(websocket.StatusPolicyViolation, "ping timeout")
					return
				}
			case <-authTick.C:
				if _, err := h.store.LookupAccess(ctx, strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")); err != nil {
					_ = conn.Close(websocket.StatusPolicyViolation, "session ended")
					return
				}
			}
		}
	}()
	for {
		_, _, err := conn.Read(ctx)
		if err != nil {
			return
		}
	}
}

func parseLimit(v string) (int, error) {
	if v == "" {
		return 100, nil
	}
	n, err := strconv.Atoi(v)
	if err != nil || n < 1 || n > 200 {
		return 0, apperr.New(http.StatusBadRequest, apperr.InvalidRequest, "limit 必须是 1-200。")
	}
	return n, nil
}

func (h *Handler) gateIP(w http.ResponseWriter, r *http.Request, n int) bool {
	return h.gateKey(w, "ip:"+r.RemoteAddr, n)
}

func (h *Handler) gateKey(w http.ResponseWriter, key string, n int) bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	now := time.Now()
	cut := now.Add(-time.Minute)
	list := h.loginHits[key]
	var kept []time.Time
	for _, t := range list {
		if t.After(cut) {
			kept = append(kept, t)
		}
	}
	if len(kept) >= n {
		w.Header().Set("Retry-After", "60")
		writeError(w, apperr.New(http.StatusTooManyRequests, apperr.RateLimited, "请求过于频繁。"))
		return false
	}
	h.loginHits[key] = append(kept, now)
	return true
}

func decodeJSON(w http.ResponseWriter, r *http.Request, dest any) bool {
	dec := json.NewDecoder(io.LimitReader(r.Body, 131072))
	dec.DisallowUnknownFields()
	if err := dec.Decode(dest); err != nil {
		var maxErr *http.MaxBytesError
		if errors.As(err, &maxErr) {
			writeError(w, apperr.New(http.StatusRequestEntityTooLarge, apperr.PayloadTooLarge, "请求体过大。"))
			return false
		}
		writeError(w, apperr.New(http.StatusBadRequest, apperr.InvalidRequest, "JSON 无效。"))
		return false
	}
	return true
}

func writeErr(w http.ResponseWriter, err error) {
	var ae *apperr.Error
	if errors.As(err, &ae) {
		writeError(w, ae)
		return
	}
	slog.Error("unhandled error", "error", err)
	writeError(w, apperr.New(http.StatusServiceUnavailable, apperr.TemporarilyUnavailable, "服务暂时不可用。"))
}

func writeError(w http.ResponseWriter, e *apperr.Error) {
	payload := map[string]any{"error": map[string]any{
		"code": e.Code, "message": e.Message, "request_id": rand.Text(),
	}}
	if e.Details != nil {
		payload["error"].(map[string]any)["details"] = e.Details
	}
	writeJSON(w, e.Status, payload)
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}
