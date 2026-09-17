package extauth

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"onlineclipboard/server/internal/apperr"
)

type User struct {
	Subject string
	Account string
	Email   string
}

type Verifier interface {
	Verify(ctx context.Context, identifier, password string) (User, error)
}

type HTTPVerifier struct {
	URL    string
	Client *http.Client
}

func New(url string) *HTTPVerifier {
	return &HTTPVerifier{
		URL: url,
		Client: &http.Client{
			Timeout: 12 * time.Second,
		},
	}
}

func (v *HTTPVerifier) Verify(ctx context.Context, identifier, password string) (User, error) {
	ident := strings.TrimSpace(identifier)
	payload := map[string]string{
		"username": ident,
		"account":  ident,
		"password": password,
	}
	if strings.Contains(ident, "@") {
		payload["email"] = ident
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		return User{}, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, v.URL, bytes.NewReader(raw))
	if err != nil {
		return User{}, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	res, err := v.Client.Do(req)
	if err != nil {
		return User{}, apperr.New(http.StatusServiceUnavailable, apperr.TemporarilyUnavailable, "外部账号服务暂时不可用。")
	}
	defer res.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(res.Body, 1<<20))
	if res.StatusCode == http.StatusUnauthorized || res.StatusCode == http.StatusForbidden {
		return User{}, apperr.New(http.StatusUnauthorized, apperr.Unauthenticated, "用户名或密码不正确。")
	}
	if res.StatusCode < 200 || res.StatusCode >= 300 {
		return User{}, apperr.New(http.StatusServiceUnavailable, apperr.TemporarilyUnavailable, "外部账号服务暂时不可用。")
	}
	user, err := parseUser(body)
	if err != nil {
		return User{}, apperr.New(http.StatusUnauthorized, apperr.Unauthenticated, "用户名或密码不正确。")
	}
	return user, nil
}

func parseUser(body []byte) (User, error) {
	var envelope struct {
		User json.RawMessage `json:"user"`
		ID   json.RawMessage `json:"id"`
	}
	if err := json.Unmarshal(body, &envelope); err != nil {
		return User{}, err
	}
	src := envelope.User
	if len(src) == 0 {
		src = body
	}
	var rec struct {
		ID       any    `json:"id"`
		Account  string `json:"account"`
		Username string `json:"username"`
		Email    string `json:"email"`
		IsActive *bool  `json:"is_active"`
		Active   *bool  `json:"isActive"`
	}
	if err := json.Unmarshal(src, &rec); err != nil {
		return User{}, err
	}
	subject := stringifyID(rec.ID)
	if subject == "" {
		return User{}, fmt.Errorf("missing user id")
	}
	if rec.IsActive != nil && !*rec.IsActive {
		return User{}, fmt.Errorf("inactive")
	}
	if rec.Active != nil && !*rec.Active {
		return User{}, fmt.Errorf("inactive")
	}
	account := strings.TrimSpace(rec.Account)
	if account == "" {
		account = strings.TrimSpace(rec.Username)
	}
	email := strings.ToLower(strings.TrimSpace(rec.Email))
	return User{Subject: subject, Account: account, Email: email}, nil
}

func stringifyID(v any) string {
	switch t := v.(type) {
	case nil:
		return ""
	case string:
		return strings.TrimSpace(t)
	case float64:
		if t != float64(int64(t)) {
			return strings.TrimSpace(fmt.Sprintf("%v", t))
		}
		return fmt.Sprintf("%d", int64(t))
	case json.Number:
		return t.String()
	default:
		return strings.TrimSpace(fmt.Sprintf("%v", t))
	}
}
