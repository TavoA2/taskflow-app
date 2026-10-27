package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/testutil"

	"github.com/TavoA2/taskflow-app/api/internal/task"
)

// send performs a request against h and returns the recorded response.
func send(h http.Handler, method, target string, header http.Header) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, target, nil)
	for k, v := range header {
		req.Header[k] = v
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func TestRouteLabelUsesPatternNotRawPath(t *testing.T) {
	d := testDeps(newFakeStore())
	h := NewRouter(d)

	for _, id := range []string{
		"00000000-0000-7000-8000-000000000001",
		"00000000-0000-7000-8000-000000000002",
		"00000000-0000-7000-8000-000000000003",
	} {
		send(h, "GET", "/api/v1/tasks/"+id, nil)
	}
	send(h, "GET", "/does/not/exist/1", nil)
	send(h, "GET", "/does/not/exist/2", nil)

	if got := testutil.ToFloat64(d.Metrics.requests.WithLabelValues("GET", "/api/v1/tasks/{id}", "404")); got != 3 {
		t.Errorf(`requests{route="/api/v1/tasks/{id}",code="404"} = %v, want 3`, got)
	}
	if got := testutil.ToFloat64(d.Metrics.requests.WithLabelValues("GET", "unmatched", "404")); got != 2 {
		t.Errorf(`requests{route="unmatched"} = %v, want 2`, got)
	}
	// 3 different IDs + 2 unknown paths must produce exactly 2 series, not 5.
	if n := testutil.CollectAndCount(d.Metrics.requests); n != 2 {
		t.Errorf("http_requests_total has %d series, want 2 (bounded cardinality)", n)
	}
}

func TestUnknownMethodsShareOneLabel(t *testing.T) {
	d := testDeps(newFakeStore())
	h := NewRouter(d)
	send(h, "FOO", "/api/v1/tasks", nil)
	send(h, "BAR", "/api/v1/tasks", nil)

	if got := testutil.ToFloat64(d.Metrics.requests.WithLabelValues("OTHER", "unmatched", "405")); got != 2 {
		t.Errorf(`requests{method="OTHER"} = %v, want 2`, got)
	}
}

func TestDurationAndInFlight(t *testing.T) {
	d := testDeps(newFakeStore())
	h := NewRouter(d)
	send(h, "GET", "/api/v1/tasks", nil)

	if n := testutil.CollectAndCount(d.Metrics.duration, "http_request_duration_seconds"); n != 1 {
		t.Errorf("duration histogram series = %d, want 1", n)
	}
	if got := testutil.ToFloat64(d.Metrics.inFlight); got != 0 {
		t.Errorf("in-flight after request = %v, want 0", got)
	}
}

func TestRequestIDGeneratedPropagatedAndSanitized(t *testing.T) {
	h := NewRouter(testDeps(newFakeStore()))

	generated := send(h, "GET", "/healthz", nil).Header().Get("X-Request-ID")
	if len(generated) != 32 {
		t.Errorf("generated request ID %q, want 32 hex chars", generated)
	}

	if got := send(h, "GET", "/healthz", http.Header{"X-Request-Id": {"abc-123"}}).Header().Get("X-Request-ID"); got != "abc-123" {
		t.Errorf("caller ID not propagated: got %q", got)
	}

	for _, bad := range []string{"evil\ninjected log line", strings.Repeat("a", 65), "<script>"} {
		if got := send(h, "GET", "/healthz", http.Header{"X-Request-Id": {bad}}).Header().Get("X-Request-ID"); got == bad {
			t.Errorf("unsafe caller ID %q was accepted", bad)
		}
	}
}

func TestPanicBecomesLogged500AndIsCounted(t *testing.T) {
	var logs bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&logs, nil))
	m := NewMetrics(prometheus.NewRegistry())

	mux := http.NewServeMux()
	mux.HandleFunc("GET /boom", func(http.ResponseWriter, *http.Request) { panic("nil map write") })
	h := observe(recoverer(mux, logger), logger, m)

	rec := send(h, "GET", "/boom", nil)
	if rec.Code != http.StatusInternalServerError || !strings.Contains(rec.Body.String(), `"code":"internal"`) {
		t.Errorf("got %d %s, want 500 internal", rec.Code, rec.Body.String())
	}
	if strings.Contains(rec.Body.String(), "nil map") {
		t.Error("panic value leaked to the client")
	}
	if got := testutil.ToFloat64(m.requests.WithLabelValues("GET", "/boom", "500")); got != 1 {
		t.Errorf("panicked request counted %v times as 500, want 1", got)
	}
	if !strings.Contains(logs.String(), "panic serving request") || !strings.Contains(logs.String(), "nil map write") {
		t.Errorf("panic not logged with its value: %s", logs.String())
	}
}

// panickingStore panics on List, to prove the production router (not just the middleware
// in isolation) recovers from handler panics.
type panickingStore struct{ *fakeStore }

func (panickingStore) List(context.Context, int, int) ([]task.Task, error) { panic("store bug") }

func TestRouterRecoversHandlerPanic(t *testing.T) {
	d := testDeps(panickingStore{newFakeStore()})
	h := NewRouter(d)

	if rec := send(h, "GET", "/api/v1/tasks", nil); rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500", rec.Code)
	}
	if got := testutil.ToFloat64(d.Metrics.requests.WithLabelValues("GET", "/api/v1/tasks", "500")); got != 1 {
		t.Errorf("panic counted %v times as 500, want 1", got)
	}
	if rec := send(h, "GET", "/healthz", nil); rec.Code != http.StatusOK {
		t.Errorf("server unhealthy after a panic: %d", rec.Code)
	}
}

func TestAccessLog(t *testing.T) {
	var logs bytes.Buffer
	d := testDeps(newFakeStore())
	d.Logger = slog.New(slog.NewJSONHandler(&logs, &slog.HandlerOptions{Level: slog.LevelInfo}))
	h := NewRouter(d)

	send(h, "GET", "/healthz", nil) // probe: debug level, filtered out at info
	send(h, "GET", "/api/v1/tasks?limit=5&email=someone@example.com", http.Header{"X-Request-Id": {"req-1"}})

	lines := strings.Split(strings.TrimSpace(logs.String()), "\n")
	if len(lines) != 1 {
		t.Fatalf("want exactly 1 access log line at info (probe excluded), got %d:\n%s", len(lines), logs.String())
	}
	var entry map[string]any
	if err := json.Unmarshal([]byte(lines[0]), &entry); err != nil {
		t.Fatal(err)
	}
	for key, want := range map[string]any{
		"msg": "http request", "request_id": "req-1", "method": "GET",
		"route": "/api/v1/tasks", "path": "/api/v1/tasks", "status": float64(200),
	} {
		if entry[key] != want {
			t.Errorf("log %s = %v, want %v", key, entry[key], want)
		}
	}
	if _, ok := entry["duration_ms"]; !ok {
		t.Error("log entry has no duration_ms")
	}
	if strings.Contains(lines[0], "someone@example.com") {
		t.Error("query string (possible personal data) was logged")
	}
}

func TestMetricsEndpointExposesHTTPMetrics(t *testing.T) {
	h := NewRouter(testDeps(newFakeStore()))
	send(h, "GET", "/api/v1/tasks", nil)

	rec := send(h, "GET", "/metrics", nil)
	body, _ := io.ReadAll(rec.Body)
	for _, want := range []string{
		`http_requests_total{code="200",method="GET",route="/api/v1/tasks"} 1`,
		`http_request_duration_seconds_bucket{method="GET",route="/api/v1/tasks",le="0.005"}`,
		`http_requests_in_flight`,
	} {
		if !strings.Contains(string(body), want) {
			t.Errorf("/metrics missing %q", want)
		}
	}
}
