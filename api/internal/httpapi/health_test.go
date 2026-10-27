package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func readyz(t *testing.T, rd *Readiness) (int, readyBody, string) {
	t.Helper()
	rec := httptest.NewRecorder()
	rd.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/readyz", nil))
	var body readyBody
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode %q: %v", rec.Body.String(), err)
	}
	return rec.Code, body, rec.Body.String()
}

func TestReadyzWhenDatabaseUp(t *testing.T) {
	rd := NewReadiness(pingerFunc(func(context.Context) error { return nil }), discardLogger)
	code, body, _ := readyz(t, rd)
	if code != http.StatusOK || body.Status != "ready" || body.Checks["database"] != "up" {
		t.Errorf("got %d %+v, want 200 ready/database up", code, body)
	}
}

func TestReadyzWhenDatabaseDownHidesDetails(t *testing.T) {
	rd := NewReadiness(pingerFunc(func(context.Context) error {
		return errors.New(`dial tcp 10.0.0.5:5432: password authentication failed`)
	}), discardLogger)
	code, body, raw := readyz(t, rd)
	if code != http.StatusServiceUnavailable || body.Status != "unavailable" || body.Checks["database"] != "down" {
		t.Errorf("got %d %+v, want 503 unavailable/database down", code, body)
	}
	if strings.Contains(raw, "10.0.0.5") || strings.Contains(raw, "password") {
		t.Errorf("probe response leaks error detail: %s", raw)
	}
}

func TestReadyzRecoversWhenDatabaseReturns(t *testing.T) {
	var down bool
	rd := NewReadiness(pingerFunc(func(context.Context) error {
		if down {
			return errors.New("connection refused")
		}
		return nil
	}), discardLogger)

	down = true
	if code, _, _ := readyz(t, rd); code != http.StatusServiceUnavailable {
		t.Fatalf("while down: status %d, want 503", code)
	}
	down = false
	if code, _, _ := readyz(t, rd); code != http.StatusOK {
		t.Errorf("after recovery: status %d, want 200", code)
	}
}

func TestReadyzPingIsBoundedByTimeout(t *testing.T) {
	// A database that hangs must not hang the probe: the orchestrator would see a timeout
	// instead of a clear 503.
	rd := NewReadiness(pingerFunc(func(ctx context.Context) error {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(2 * time.Second): // guard: if the timeout is missing, fail fast instead of hanging
			return errors.New("ping was never cancelled")
		}
	}), discardLogger)
	rd.timeout = 50 * time.Millisecond

	start := time.Now()
	code, _, _ := readyz(t, rd)
	if elapsed := time.Since(start); elapsed > time.Second {
		t.Errorf("probe took %s, want about %s", elapsed, rd.timeout)
	}
	if code != http.StatusServiceUnavailable {
		t.Errorf("status = %d, want 503", code)
	}
}

func TestReadyzFailsDuringShutdownEvenIfDatabaseUp(t *testing.T) {
	pinged := false
	rd := NewReadiness(pingerFunc(func(context.Context) error { pinged = true; return nil }), discardLogger)
	rd.StartShutdown()

	code, body, _ := readyz(t, rd)
	if code != http.StatusServiceUnavailable || body.Status != "shutting_down" {
		t.Errorf("got %d %+v, want 503 shutting_down", code, body)
	}
	if pinged {
		t.Error("database should not be pinged once shutting down")
	}
}

func TestReadyzIsRoutedAndLivenessIgnoresDatabase(t *testing.T) {
	d := testDeps(newFakeStore())
	d.Readiness = NewReadiness(pingerFunc(func(context.Context) error { return errors.New("down") }), discardLogger)
	srv := newTestServer(t, d)

	for path, want := range map[string]int{"/readyz": http.StatusServiceUnavailable, "/healthz": http.StatusOK} {
		resp, err := http.Get(srv.URL + path)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		if resp.StatusCode != want {
			t.Errorf("%s with database down: status %d, want %d", path, resp.StatusCode, want)
		}
	}
}
