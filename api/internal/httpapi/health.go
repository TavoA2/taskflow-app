package httpapi

import (
	"context"
	"log/slog"
	"net/http"
	"sync/atomic"
	"time"
)

// Pinger checks a dependency; *pgxpool.Pool satisfies it.
type Pinger interface {
	Ping(ctx context.Context) error
}

// Readiness serves /readyz: 200 only when the database answers and the server is not shutting down.
// Unlike /healthz, a failure here means "send no traffic to me", not "restart me".
type Readiness struct {
	db           Pinger
	logger       *slog.Logger
	timeout      time.Duration
	shuttingDown atomic.Bool
	dbUp         atomic.Bool // last observed state, so only transitions are logged (probes run every few seconds)
}

func NewReadiness(db Pinger, logger *slog.Logger) *Readiness {
	r := &Readiness{db: db, logger: logger, timeout: time.Second}
	r.dbUp.Store(true)
	return r
}

// StartShutdown makes /readyz fail from now on, so traffic is moved away during the drain delay.
func (rd *Readiness) StartShutdown() {
	rd.shuttingDown.Store(true)
}

type readyBody struct {
	Status string            `json:"status"`
	Checks map[string]string `json:"checks,omitempty"`
}

func (rd *Readiness) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if rd.shuttingDown.Load() {
		writeJSON(w, rd.logger, http.StatusServiceUnavailable, readyBody{Status: "shutting_down"})
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), rd.timeout)
	defer cancel()
	err := rd.db.Ping(ctx)
	up := err == nil

	if was := rd.dbUp.Swap(up); was != up {
		if up {
			rd.logger.Info("database reachable, reporting ready")
		} else {
			rd.logger.Warn("database unreachable, reporting not ready", "err", err)
		}
	}
	if !up {
		// The error detail goes to the log only; the probe response stays generic.
		writeJSON(w, rd.logger, http.StatusServiceUnavailable, readyBody{Status: "unavailable", Checks: map[string]string{"database": "down"}})
		return
	}
	writeJSON(w, rd.logger, http.StatusOK, readyBody{Status: "ready", Checks: map[string]string{"database": "up"}})
}
