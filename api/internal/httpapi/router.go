// Package httpapi wires HTTP routes and handlers.
package httpapi

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"

	"github.com/TavoA2/taskflow-app/api/internal/task"
)

// TaskStore is the persistence the handlers need. Defined here, where it is consumed,
// so tests can substitute an in-memory fake.
type TaskStore interface {
	List(ctx context.Context, limit, offset int) ([]task.Task, error)
	Get(ctx context.Context, id string) (task.Task, error)
	Create(ctx context.Context, in task.CreateInput) (task.Task, error)
	Update(ctx context.Context, id string, in task.UpdateInput) (task.Task, error)
	Delete(ctx context.Context, id string) error
}

// NewRouter returns the API's HTTP handler.
func NewRouter(logger *slog.Logger, store TaskStore) http.Handler {
	h := &taskHandlers{logger: logger, store: store}
	mux := http.NewServeMux()
	// Liveness: answers whether the process can serve HTTP at all. It deliberately does
	// not check the database: a database outage must not make Kubernetes restart every pod.
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, logger, http.StatusOK, map[string]string{"status": "ok"})
	})
	mux.HandleFunc("GET /api/v1/tasks", h.list)
	mux.HandleFunc("POST /api/v1/tasks", h.create)
	mux.HandleFunc("GET /api/v1/tasks/{id}", h.get)
	mux.HandleFunc("PATCH /api/v1/tasks/{id}", h.update)
	mux.HandleFunc("DELETE /api/v1/tasks/{id}", h.delete)
	return mux
}

func writeJSON(w http.ResponseWriter, logger *slog.Logger, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(body); err != nil {
		logger.Error("write response", "err", err)
	}
}
