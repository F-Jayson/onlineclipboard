package extauth

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestVerifyParsesBlogUser(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var in map[string]string
		_ = json.NewDecoder(r.Body).Decode(&in)
		if in["username"] != "jayson" || in["password"] != "secret" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"user": map[string]any{"id": 42, "account": "jayson", "email": "a@b.com", "is_active": true},
		})
	}))
	t.Cleanup(srv.Close)
	user, err := New(srv.URL).Verify(context.Background(), "jayson", "secret")
	if err != nil {
		t.Fatal(err)
	}
	if user.Subject != "42" || user.Account != "jayson" || user.Email != "a@b.com" {
		t.Fatalf("%+v", user)
	}
	if _, err := New(srv.URL).Verify(context.Background(), "jayson", "nope"); err == nil {
		t.Fatal("expected auth error")
	}
}
