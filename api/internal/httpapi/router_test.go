package httpapi

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/prometheus/client_golang/prometheus"
)

type pingerFunc func(ctx context.Context) error

func (f pingerFunc) Ping(ctx context.Context) error { return f(ctx) }

var discardLogger = slog.New(slog.NewTextHandler(io.Discard, nil))

// testDeps returns router dependencies with a healthy fake database and a fresh metrics registry.
func testDeps(store TaskStore) Deps {
	reg := prometheus.NewRegistry()
	return Deps{
		Logger:    discardLogger,
		Store:     store,
		Readiness: NewReadiness(pingerFunc(func(context.Context) error { return nil }), discardLogger),
		Metrics:   NewMetrics(reg),
		Gatherer:  reg,
	}
}

func newTestServer(t *testing.T, d Deps) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(NewRouter(d))
	t.Cleanup(srv.Close)
	return srv
}

func TestHealthz(t *testing.T) {
	srv := newTestServer(t, testDeps(newFakeStore()))

	resp, err := http.Get(srv.URL + "/healthz")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)

	if resp.StatusCode != http.StatusOK {
		t.Errorf("status = %d, want 200", resp.StatusCode)
	}
	if ct := resp.Header.Get("Content-Type"); ct != "application/json" {
		t.Errorf("Content-Type = %q, want application/json", ct)
	}
	if got := strings.TrimSpace(string(body)); got != `{"status":"ok"}` {
		t.Errorf("body = %s", got)
	}
}

func TestHealthzRejectsOtherMethods(t *testing.T) {
	srv := newTestServer(t, testDeps(newFakeStore()))

	resp, err := http.Post(srv.URL+"/healthz", "application/json", nil)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()

	if resp.StatusCode != http.StatusMethodNotAllowed {
		t.Errorf("status = %d, want 405", resp.StatusCode)
	}
}
