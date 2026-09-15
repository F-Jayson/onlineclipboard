package httpapi_test

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"onlineclipboard/server/internal/canon"
	"onlineclipboard/server/internal/clipcrypto"
	"onlineclipboard/server/internal/config"
	"onlineclipboard/server/internal/httpapi"
	"onlineclipboard/server/internal/migrate"
	"onlineclipboard/server/internal/notify"
	"onlineclipboard/server/internal/store"
)

type harness struct {
	t      *testing.T
	pool   *pgxpool.Pool
	server *httptest.Server
	st     *store.Store
	cfg    config.Config
}

func setup(t *testing.T) *harness {
	t.Helper()
	url := os.Getenv("CLIP_TEST_DATABASE_URL")
	if url == "" {
		url = "postgres://onlineclipboard:onlineclipboard@127.0.0.1:5432/onlineclipboard?sslmode=disable"
	}
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	pool, err := pgxpool.New(ctx, url)
	if err != nil {
		t.Skipf("postgres unavailable: %v", err)
	}
	if err := pool.Ping(ctx); err != nil {
		t.Skipf("postgres unavailable: %v", err)
	}
	dbName := "oc_test_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	if _, err := pool.Exec(ctx, "CREATE DATABASE "+dbName); err != nil {
		t.Fatalf("create db: %v", err)
	}
	testURL := replaceDB(url, dbName)
	tp, err := pgxpool.New(ctx, testURL)
	if err != nil {
		t.Fatal(err)
	}
	if err := migrate.Apply(ctx, tp); err != nil {
		t.Fatal(err)
	}
	cfg := config.Config{
		HTTPAddr: "127.0.0.1:0", RegistrationMode: "invite",
		MaxTextBytes: 65536, MaxRequestBytes: 131072, MaxItems: 10000,
		MaxCiphertextBytes: 104857600, TrashRetentionSeconds: 604800, EventRetentionDays: 30,
	}
	hub := notify.New()
	st := store.New(tp, cfg, hub)
	srv := httptest.NewServer(httpapi.New(cfg, st, hub, true))
	t.Cleanup(func() {
		srv.Close()
		tp.Close()
		_, _ = pool.Exec(context.Background(), "DROP DATABASE IF EXISTS "+dbName+" WITH (FORCE)")
		pool.Close()
	})
	return &harness{t: t, pool: tp, server: srv, st: st, cfg: cfg}
}

func replaceDB(url, db string) string {
	if i := strings.LastIndex(url, "/"); i >= 0 {
		base := url[:i+1]
		rest := url[i+1:]
		if j := strings.Index(rest, "?"); j >= 0 {
			return base + db + rest[j:]
		}
		return base + db
	}
	return url
}

func (h *harness) invite() string {
	h.t.Helper()
	tok, _, err := h.st.CreateInvite(context.Background(), time.Hour)
	if err != nil {
		h.t.Fatal(err)
	}
	return tok
}

func (h *harness) post(path string, body any, token string, extra map[string]string) *http.Response {
	h.t.Helper()
	var rdr io.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		rdr = bytes.NewReader(b)
	}
	req, err := http.NewRequest(http.MethodPost, h.server.URL+path, rdr)
	if err != nil {
		h.t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	for k, v := range extra {
		req.Header.Set(k, v)
	}
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		h.t.Fatal(err)
	}
	return res
}

func (h *harness) do(method, path string, body any, token string, extra map[string]string) *http.Response {
	h.t.Helper()
	var rdr io.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		rdr = bytes.NewReader(b)
	}
	req, err := http.NewRequest(method, h.server.URL+path, rdr)
	if err != nil {
		h.t.Fatal(err)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	for k, v := range extra {
		req.Header.Set(k, v)
	}
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		h.t.Fatal(err)
	}
	return res
}

func decode[T any](t *testing.T, res *http.Response) T {
	t.Helper()
	defer res.Body.Close()
	var v T
	if err := json.NewDecoder(res.Body).Decode(&v); err != nil {
		t.Fatal(err)
	}
	return v
}

type authJSON struct {
	AccessToken      string `json:"access_token"`
	RefreshToken     string `json:"refresh_token"`
	UserID           string `json:"user_id"`
	DeviceID         string `json:"device_id"`
	VaultInitialized bool   `json:"vault_initialized"`
}

func (h *harness) register(username, device string) authJSON {
	body := map[string]any{
		"username": username, "password": "correct-horse-battery",
		"invite_token": h.invite(),
		"device":       map[string]string{"id": device, "name": "dev", "platform": "windows"},
	}
	res := h.post("/api/v1/auth/register", body, "", nil)
	if res.StatusCode != 201 {
		b, _ := io.ReadAll(res.Body)
		res.Body.Close()
		h.t.Fatalf("register %d %s", res.StatusCode, b)
	}
	return decode[authJSON](h.t, res)
}

func TestHealthAndInfo(t *testing.T) {
	h := setup(t)
	res, err := http.Get(h.server.URL + "/healthz")
	if err != nil {
		t.Fatal(err)
	}
	got := decode[map[string]string](t, res)
	if got["stage"] != "ready" {
		t.Fatalf("%v", got)
	}
	res, _ = http.Get(h.server.URL + "/readyz")
	if res.StatusCode != 200 {
		t.Fatalf("ready %d", res.StatusCode)
	}
	res.Body.Close()
	res, _ = http.Get(h.server.URL + "/api/v1/server-info")
	info := decode[map[string]any](t, res)
	if info["sync_available"] != true {
		t.Fatalf("%v", info)
	}
}

func TestRegisterLoginVaultAndClip(t *testing.T) {
	h := setup(t)
	userID := "11111111-1111-4111-8111-111111111111"
	device := "55555555-5555-4555-8555-555555555555"
	vaultID := "22222222-2222-4222-8222-222222222222"
	auth := h.register("alice", device)

	raw, err := os.ReadFile("../../../contracts/crypto-v1-vectors.json")
	if err != nil {
		raw, err = os.ReadFile("../../../../contracts/crypto-v1-vectors.json")
	}
	if err != nil {
		t.Fatal(err)
	}
	var fix struct {
		CMKHex         string `json:"cmk_hex"`
		RecoveryKeyHex string `json:"recovery_key_hex"`
		Vault          struct {
			WrapSalt   string `json:"wrap_salt"`
			WrapNonce  string `json:"wrap_nonce"`
			WrappedKey string `json:"wrapped_key"`
		} `json:"vault_envelope"`
		Items []struct {
			Envelope map[string]any `json:"envelope"`
		} `json:"items"`
	}
	_ = json.Unmarshal(raw, &fix)
	_ = userID
	_ = clipcrypto.Encode
	_ = canon.Encode

	res := h.do(http.MethodPut, "/api/v1/vault", map[string]any{
		"vault_id": vaultID, "format_version": 1, "key_epoch": 1,
		"wrap_salt": fix.Vault.WrapSalt, "wrap_nonce": fix.Vault.WrapNonce, "wrapped_key": fix.Vault.WrappedKey,
	}, auth.AccessToken, map[string]string{"If-None-Match": "*"})
	if res.StatusCode != 201 {
		b, _ := io.ReadAll(res.Body)
		res.Body.Close()
		t.Fatalf("vault %d %s", res.StatusCode, b)
	}
	res.Body.Close()

	res = h.do(http.MethodPut, "/api/v1/vault", map[string]any{
		"vault_id": vaultID, "format_version": 1, "key_epoch": 1,
		"wrap_salt": fix.Vault.WrapSalt, "wrap_nonce": fix.Vault.WrapNonce, "wrapped_key": fix.Vault.WrappedKey,
	}, auth.AccessToken, map[string]string{"If-None-Match": "*"})
	if res.StatusCode != 412 {
		t.Fatalf("second vault %d", res.StatusCode)
	}
	res.Body.Close()

	env := fix.Items[0].Envelope
	res = h.post("/api/v1/clips", env, auth.AccessToken, nil)
	if res.StatusCode != 201 {
		b, _ := io.ReadAll(res.Body)
		res.Body.Close()
		t.Fatalf("create %d %s", res.StatusCode, b)
	}
	created := decode[map[string]any](t, res)
	res = h.post("/api/v1/clips", env, auth.AccessToken, nil)
	if res.StatusCode != 200 {
		t.Fatalf("replay %d", res.StatusCode)
	}
	replay := decode[map[string]any](t, res)
	if created["seq"] != replay["seq"] {
		t.Fatalf("idempotent seq %v %v", created, replay)
	}

	id := env["id"].(string)
	res = h.do(http.MethodDelete, "/api/v1/clips/"+id, nil, auth.AccessToken, map[string]string{
		"If-Match": `"1"`, "Idempotency-Key": uuid.NewString(),
	})
	if res.StatusCode != 200 {
		b, _ := io.ReadAll(res.Body)
		res.Body.Close()
		t.Fatalf("trash %d %s", res.StatusCode, b)
	}
	trash := decode[map[string]any](t, res)
	if trash["status"] != "trash" {
		t.Fatalf("%v", trash)
	}
	res = h.post("/api/v1/clips/"+id+"/restore", nil, auth.AccessToken, map[string]string{
		"If-Match": `"` + strconv.Itoa(int(trash["version"].(float64))) + `"`, "Idempotency-Key": uuid.NewString(),
	})
	if res.StatusCode != 200 {
		b, _ := io.ReadAll(res.Body)
		res.Body.Close()
		t.Fatalf("restore %d %s", res.StatusCode, b)
	}
	res.Body.Close()
}

func TestAccountIsolation(t *testing.T) {
	h := setup(t)
	a := h.register("alice", uuid.NewString())
	b := h.register("bob", uuid.NewString())
	res := h.do(http.MethodGet, "/api/v1/devices", nil, b.AccessToken, nil)
	page := decode[map[string]any](t, res)
	items := page["items"].([]any)
	if len(items) != 1 {
		t.Fatalf("bob devices %v", page)
	}
	res = h.do(http.MethodDelete, "/api/v1/devices/"+a.DeviceID, nil, b.AccessToken, nil)
	if res.StatusCode != 404 {
		t.Fatalf("cross revoke %d", res.StatusCode)
	}
	res.Body.Close()
}

func b64n(n int) string {
	b := make([]byte, n)
	_, _ = rand.Read(b)
	return canon.Encode(b)
}

func (h *harness) initVault(auth authJSON) string {
	h.t.Helper()
	vaultID := uuid.NewString()
	res := h.do(http.MethodPut, "/api/v1/vault", map[string]any{
		"vault_id": vaultID, "format_version": 1, "key_epoch": 1,
		"wrap_salt": b64n(32), "wrap_nonce": b64n(12), "wrapped_key": b64n(48),
	}, auth.AccessToken, map[string]string{"If-None-Match": "*"})
	if res.StatusCode != 201 {
		b, _ := io.ReadAll(res.Body)
		res.Body.Close()
		h.t.Fatalf("vault %d %s", res.StatusCode, b)
	}
	res.Body.Close()
	return vaultID
}

func (h *harness) createDummy(auth authJSON, vaultID string) (string, map[string]any) {
	h.t.Helper()
	id := uuid.NewString()
	env := map[string]any{
		"id": id, "vault_id": vaultID, "source_device_id": auth.DeviceID,
		"format_version": 1, "key_epoch": 1, "content_type": "text/plain",
		"nonce": b64n(12), "ciphertext": b64n(32), "delivery_intent": "history_only",
	}
	res := h.post("/api/v1/clips", env, auth.AccessToken, nil)
	if res.StatusCode != 201 {
		b, _ := io.ReadAll(res.Body)
		res.Body.Close()
		h.t.Fatalf("create %d %s", res.StatusCode, b)
	}
	receipt := decode[map[string]any](h.t, res)
	return id, receipt
}

func TestRefreshReuseRevokesFamily(t *testing.T) {
	h := setup(t)
	auth := h.register("alice", uuid.NewString())
	res := h.post("/api/v1/auth/refresh", map[string]any{"refresh_token": auth.RefreshToken}, "", nil)
	if res.StatusCode != 200 {
		t.Fatalf("refresh %d", res.StatusCode)
	}
	next := decode[authJSON](t, res)
	res = h.post("/api/v1/auth/refresh", map[string]any{"refresh_token": auth.RefreshToken}, "", nil)
	if res.StatusCode != 401 {
		t.Fatalf("reuse %d", res.StatusCode)
	}
	res.Body.Close()
	res = h.post("/api/v1/auth/refresh", map[string]any{"refresh_token": next.RefreshToken}, "", nil)
	if res.StatusCode != 401 {
		t.Fatalf("family after reuse %d", res.StatusCode)
	}
	res.Body.Close()
}

func TestTrashExpiryRestoreAndPurge(t *testing.T) {
	h := setup(t)
	auth := h.register("alice", uuid.NewString())
	vault := h.initVault(auth)
	id, _ := h.createDummy(auth, vault)
	res := h.do(http.MethodDelete, "/api/v1/clips/"+id, nil, auth.AccessToken, map[string]string{
		"If-Match": `"1"`, "Idempotency-Key": uuid.NewString(),
	})
	if res.StatusCode != 200 {
		b, _ := io.ReadAll(res.Body)
		res.Body.Close()
		t.Fatalf("trash %d %s", res.StatusCode, b)
	}
	trash := decode[map[string]any](t, res)
	if _, err := h.pool.Exec(context.Background(), `
		UPDATE clips
		SET deleted_at = clock_timestamp() - interval '168 hours' - interval '1 second',
		    expires_at = clock_timestamp() - interval '1 second'
		WHERE id=$1`, id); err != nil {
		t.Fatal(err)
	}
	res = h.post("/api/v1/clips/"+id+"/restore", nil, auth.AccessToken, map[string]string{
		"If-Match": `"` + strconv.Itoa(int(trash["version"].(float64))) + `"`, "Idempotency-Key": uuid.NewString(),
	})
	if res.StatusCode != 410 {
		b, _ := io.ReadAll(res.Body)
		res.Body.Close()
		t.Fatalf("expired restore %d %s", res.StatusCode, b)
	}
	res.Body.Close()
	if err := h.st.PurgeExpired(context.Background()); err != nil {
		t.Fatal(err)
	}
	res = h.do(http.MethodGet, "/api/v1/clips/"+id, nil, auth.AccessToken, nil)
	if res.StatusCode != 410 {
		t.Fatalf("purged get %d", res.StatusCode)
	}
	res.Body.Close()
	bob := h.register("bob", uuid.NewString())
	res = h.do(http.MethodGet, "/api/v1/clips/"+id, nil, bob.AccessToken, nil)
	if res.StatusCode != 404 {
		t.Fatalf("bob saw alice clip %d", res.StatusCode)
	}
	res.Body.Close()
}

func TestCursorExpiredRequiresSnapshot(t *testing.T) {
	h := setup(t)
	auth := h.register("alice", uuid.NewString())
	res := h.do(http.MethodGet, "/api/v1/sync/changes?epoch=00000000-0000-0000-0000-000000000000&after=0", nil, auth.AccessToken, nil)
	if res.StatusCode != 409 {
		t.Fatalf("bad epoch %d", res.StatusCode)
	}
	body := decode[map[string]any](t, res)
	errObj := body["error"].(map[string]any)
	if errObj["code"] != "SYNC_RESET_REQUIRED" {
		t.Fatalf("%v", body)
	}
	if _, err := h.pool.Exec(context.Background(), `UPDATE user_sync_state SET next_seq=100, min_available_seq=50 WHERE user_id=$1`, auth.UserID); err != nil {
		t.Fatal(err)
	}
	st, err := h.st.State(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	res = h.do(http.MethodGet, "/api/v1/sync/changes?epoch="+st.SyncEpoch+"&after=0", nil, auth.AccessToken, nil)
	if res.StatusCode != 410 {
		t.Fatalf("cursor expired %d", res.StatusCode)
	}
	got := decode[map[string]any](t, res)
	code := got["error"].(map[string]any)["code"]
	if code != "CURSOR_EXPIRED" {
		t.Fatalf("%v", got)
	}
}

func TestDeviceRevokeBlocksAccess(t *testing.T) {
	h := setup(t)
	auth := h.register("alice", uuid.NewString())
	res := h.do(http.MethodDelete, "/api/v1/devices/"+auth.DeviceID, nil, auth.AccessToken, nil)
	if res.StatusCode != 204 {
		t.Fatalf("self revoke %d", res.StatusCode)
	}
	res.Body.Close()
	res = h.do(http.MethodGet, "/api/v1/devices", nil, auth.AccessToken, nil)
	if res.StatusCode != 401 && res.StatusCode != 403 {
		t.Fatalf("revoked session %d", res.StatusCode)
	}
	res.Body.Close()
}
