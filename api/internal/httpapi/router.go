// Package httpapi wires HTTP routes, handlers and middleware.
package httpapi

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"

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

type Deps struct {
	Logger    *slog.Logger
	Store     TaskStore
	Readiness *Readiness
	Metrics   *Metrics
	// Gatherer is what /metrics exposes: the HTTP metrics plus runtime and DB pool collectors.
	Gatherer prometheus.Gatherer
}

// NewRouter returns the API's HTTP handler.
//
// /healthz, /readyz and /metrics live outside /api/, so the frontend reverse proxy (which
// forwards only /api/) never exposes them publicly; in-cluster probes and Prometheus reach them directly.
func NewRouter(d Deps) http.Handler {
	h := &taskHandlers{logger: d.Logger, store: d.Store}
	mux := http.NewServeMux()
	// Liveness: answers whether the process can serve HTTP at all. It deliberately does
	// not check the database: a database outage must not make Kubernetes restart every pod.
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, d.Logger, http.StatusOK, map[string]string{"status": "ok"})
	})
	mux.Handle("GET /readyz", d.Readiness)
	mux.Handle("GET /metrics", promhttp.HandlerFor(d.Gatherer, promhttp.HandlerOpts{
		ErrorLog: slog.NewLogLogger(d.Logger.Handler(), slog.LevelError),
	}))
	mux.HandleFunc("GET /api/v1/tasks", h.list)
	mux.HandleFunc("POST /api/v1/tasks", h.create)
	mux.HandleFunc("GET /api/v1/tasks/{id}", h.get)
	mux.HandleFunc("PATCH /api/v1/tasks/{id}", h.update)
	mux.HandleFunc("DELETE /api/v1/tasks/{id}", h.delete)

	// observe is outermost so it also records the 500s that recoverer writes after a panic.
	return observe(recoverer(mux, d.Logger), d.Logger, d.Metrics)
}

func writeJSON(w http.ResponseWriter, logger *slog.Logger, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(body); err != nil {
		logger.Error("write response", "err", err)
	}
}
