// Package httpapi wires HTTP routes and handlers.
package httpapi

import (
	"encoding/json"
	"log/slog"
	"net/http"
)

// NewRouter returns the API's HTTP handler.
func NewRouter(logger *slog.Logger) http.Handler {
	mux := http.NewServeMux()
	// Liveness: answers whether the process can serve HTTP at all. It deliberately does
	// not check the database: a database outage must not make Kubernetes restart every pod.
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, logger, http.StatusOK, map[string]string{"status": "ok"})
	})
	return mux
}

func writeJSON(w http.ResponseWriter, logger *slog.Logger, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(body); err != nil {
		logger.Error("write response", "err", err)
	}
}
