// Package httpapi exposes an intentionally non-functional business API scaffold.
package httpapi

import (
	"crypto/rand"
	"encoding/json"
	"net/http"
)

func NewHandler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, map[string]string{"status": "ok", "stage": "skeleton"})
	})
	mux.HandleFunc("GET /readyz", func(w http.ResponseWriter, r *http.Request) {
		writeError(w, http.StatusServiceUnavailable, "NOT_READY", "Business services have not been implemented.")
	})
	mux.HandleFunc("GET /api/v1/server-info", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, map[string]any{
			"name": "OnlineClipboard", "version": "0.1.0-skeleton",
			"stage": "skeleton", "protocol_version": 1,
			"sync_available": false, "e2ee_available": false,
			"registration_mode": "disabled",
			"max_text_bytes":    65536, "max_request_bytes": 131072,
			"trash_retention_seconds": 604800,
		})
	})
	mux.HandleFunc("/api/v1/", func(w http.ResponseWriter, r *http.Request) {
		writeError(w, http.StatusNotImplemented, "NOT_IMPLEMENTED", "This endpoint is a design contract only; no data has been saved.")
	})
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		writeError(w, http.StatusNotFound, "NOT_FOUND", "Route not found.")
	})
	return mux
}

func writeError(w http.ResponseWriter, status int, code, message string) {
	writeJSON(w, status, map[string]any{"error": map[string]string{
		"code": code, "message": message, "request_id": rand.Text(),
	}})
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}
