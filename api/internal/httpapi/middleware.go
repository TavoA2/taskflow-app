package httpapi

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"log/slog"
	"net/http"
	"runtime/debug"
	"strconv"
	"time"
)

type requestIDKey struct{}

// RequestID returns the request ID stored by the observe middleware, or "".
func RequestID(ctx context.Context) string {
	id, _ := ctx.Value(requestIDKey{}).(string)
	return id
}

// recorder captures the status code and size written by the wrapped handler.
type recorder struct {
	http.ResponseWriter
	status      int
	bytes       int
	wroteHeader bool
}

func (r *recorder) WriteHeader(code int) {
	if !r.wroteHeader {
		r.status, r.wroteHeader = code, true
	}
	r.ResponseWriter.WriteHeader(code)
}

func (r *recorder) Write(b []byte) (int, error) {
	if !r.wroteHeader {
		r.WriteHeader(http.StatusOK)
	}
	n, err := r.ResponseWriter.Write(b)
	r.bytes += n
	return n, err
}

// Unwrap lets http.ResponseController reach the underlying writer.
func (r *recorder) Unwrap() http.ResponseWriter { return r.ResponseWriter }

// probePaths are hit every few seconds by orchestrators and scrapers; their access logs go to
// debug level so they do not drown real traffic. They are still counted in metrics.
var probePaths = map[string]bool{"/healthz": true, "/readyz": true, "/metrics": true}

// observe assigns a request ID, records RED metrics and writes one access log line per request.
func observe(next http.Handler, logger *slog.Logger, m *Metrics) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()

		id := r.Header.Get("X-Request-ID")
		if !validRequestID(id) {
			id = newRequestID()
		}
		w.Header().Set("X-Request-ID", id)
		r = r.WithContext(context.WithValue(r.Context(), requestIDKey{}, id))

		m.inFlight.Inc()
		defer m.inFlight.Dec()

		rec := &recorder{ResponseWriter: w, status: http.StatusOK}
		next.ServeHTTP(rec, r)

		elapsed := time.Since(start)
		method, route := methodLabel(r.Method), routeLabel(r.Pattern) // ServeMux sets r.Pattern on this request
		m.requests.WithLabelValues(method, route, strconv.Itoa(rec.status)).Inc()
		m.duration.WithLabelValues(method, route).Observe(elapsed.Seconds())

		level := slog.LevelInfo
		switch {
		case rec.status >= 500:
			level = slog.LevelError
		case probePaths[r.URL.Path]:
			level = slog.LevelDebug
		}
		// The query string is not logged: it may carry personal data.
		logger.LogAttrs(r.Context(), level, "http request",
			slog.String("request_id", id),
			slog.String("method", r.Method),
			slog.String("route", route),
			slog.String("path", r.URL.Path),
			slog.Int("status", rec.status),
			slog.Float64("duration_ms", float64(elapsed.Microseconds())/1000),
			slog.Int("bytes", rec.bytes),
			slog.String("remote_addr", r.RemoteAddr),
		)
	})
}

// recoverer turns a handler panic into a logged 500 instead of a dropped connection.
func recoverer(next http.Handler, logger *slog.Logger) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			v := recover()
			if v == nil {
				return
			}
			if err, ok := v.(error); ok && errors.Is(err, http.ErrAbortHandler) { // deliberate abort: let net/http handle it
				panic(v)
			}
			logger.Error("panic serving request", "request_id", RequestID(r.Context()),
				"panic", v, "stack", string(debug.Stack()))
			writeJSON(w, logger, http.StatusInternalServerError, errorBody{Error: "internal error", Code: "internal"})
		}()
		next.ServeHTTP(w, r)
	})
}

// validRequestID accepts caller-supplied IDs only if short and made of safe characters,
// so a client cannot inject newlines or huge values into logs.
func validRequestID(id string) bool {
	if len(id) == 0 || len(id) > 64 {
		return false
	}
	for i := 0; i < len(id); i++ {
		switch c := id[i]; {
		case 'a' <= c && c <= 'z', 'A' <= c && c <= 'Z', '0' <= c && c <= '9', c == '-', c == '_', c == '.':
		default:
			return false
		}
	}
	return true
}

func newRequestID() string {
	var b [16]byte
	_, _ = rand.Read(b[:]) // crypto/rand.Read never returns an error on supported platforms
	return hex.EncodeToString(b[:])
}
