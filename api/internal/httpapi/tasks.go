package httpapi

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strconv"

	"github.com/TavoA2/taskflow-app/api/internal/task"
)

const (
	defaultLimit = 20
	maxLimit     = 100
	maxBodyBytes = 64 << 10 // far above the largest valid task (~9 KB of UTF-8)
)

type taskHandlers struct {
	logger *slog.Logger
	store  TaskStore
}

// errorBody is the single error shape returned by every endpoint.
type errorBody struct {
	Error  string            `json:"error"`
	Code   string            `json:"code"`
	Fields map[string]string `json:"fields,omitempty"`
}

type listResponse struct {
	Items  []task.Task `json:"items"`
	Limit  int         `json:"limit"`
	Offset int         `json:"offset"`
}

func (h *taskHandlers) list(w http.ResponseWriter, r *http.Request) {
	limit, offset, verr := parsePage(r)
	if verr != nil {
		h.fail(w, r, verr)
		return
	}
	items, err := h.store.List(r.Context(), limit, offset)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	if items == nil {
		items = []task.Task{} // always a JSON array, never null
	}
	writeJSON(w, h.logger, http.StatusOK, listResponse{Items: items, Limit: limit, Offset: offset})
}

func (h *taskHandlers) create(w http.ResponseWriter, r *http.Request) {
	var in task.CreateInput
	if err := decodeJSON(w, r, &in); err != nil {
		h.fail(w, r, err)
		return
	}
	in.Normalize()
	if err := in.Validate(); err != nil {
		h.fail(w, r, err)
		return
	}
	t, err := h.store.Create(r.Context(), in)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	w.Header().Set("Location", "/api/v1/tasks/"+t.ID)
	writeJSON(w, h.logger, http.StatusCreated, t)
}

func (h *taskHandlers) get(w http.ResponseWriter, r *http.Request) {
	id, ok := h.pathID(w, r)
	if !ok {
		return
	}
	t, err := h.store.Get(r.Context(), id)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	writeJSON(w, h.logger, http.StatusOK, t)
}

func (h *taskHandlers) update(w http.ResponseWriter, r *http.Request) {
	id, ok := h.pathID(w, r)
	if !ok {
		return
	}
	var in task.UpdateInput
	if err := decodeJSON(w, r, &in); err != nil {
		h.fail(w, r, err)
		return
	}
	in.Normalize()
	if err := in.Validate(); err != nil {
		h.fail(w, r, err)
		return
	}
	t, err := h.store.Update(r.Context(), id, in)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	writeJSON(w, h.logger, http.StatusOK, t)
}

func (h *taskHandlers) delete(w http.ResponseWriter, r *http.Request) {
	id, ok := h.pathID(w, r)
	if !ok {
		return
	}
	if err := h.store.Delete(r.Context(), id); err != nil {
		h.fail(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *taskHandlers) pathID(w http.ResponseWriter, r *http.Request) (string, bool) {
	id := r.PathValue("id")
	if !task.ValidID(id) {
		h.fail(w, r, &task.ValidationError{Fields: map[string]string{"id": "must be a UUID"}})
		return "", false
	}
	return id, true
}

// badRequestError is a client error that is not field validation (malformed JSON, oversize body...).
type badRequestError struct{ msg string }

func (e *badRequestError) Error() string { return e.msg }

// fail maps an error to a status code. Unexpected errors are logged with detail but the
// client only gets a generic message, so internals (SQL, hostnames) never leak.
func (h *taskHandlers) fail(w http.ResponseWriter, r *http.Request, err error) {
	var verr *task.ValidationError
	var bad *badRequestError
	switch {
	case errors.As(err, &verr):
		writeJSON(w, h.logger, http.StatusBadRequest, errorBody{Error: "validation failed", Code: "validation_failed", Fields: verr.Fields})
	case errors.As(err, &bad):
		writeJSON(w, h.logger, http.StatusBadRequest, errorBody{Error: bad.msg, Code: "bad_request"})
	case errors.Is(err, task.ErrNotFound):
		writeJSON(w, h.logger, http.StatusNotFound, errorBody{Error: "task not found", Code: "not_found"})
	default:
		h.logger.Error("request failed", "request_id", RequestID(r.Context()), "method", r.Method, "path", r.URL.Path, "err", err)
		writeJSON(w, h.logger, http.StatusInternalServerError, errorBody{Error: "internal error", Code: "internal"})
	}
}

// decodeJSON reads exactly one JSON object, rejecting unknown fields, trailing data and oversize bodies.
func decodeJSON(w http.ResponseWriter, r *http.Request, dst any) error {
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxBodyBytes))
	dec.DisallowUnknownFields()
	if err := dec.Decode(dst); err != nil {
		var maxErr *http.MaxBytesError
		switch {
		case errors.As(err, &maxErr):
			return &badRequestError{fmt.Sprintf("request body exceeds %d bytes", maxBodyBytes)}
		case errors.Is(err, io.EOF):
			return &badRequestError{"request body is empty"}
		default:
			return &badRequestError{"invalid JSON: " + err.Error()}
		}
	}
	if dec.More() {
		return &badRequestError{"request body must contain a single JSON object"}
	}
	return nil
}

func parsePage(r *http.Request) (limit, offset int, err error) {
	var verr task.ValidationError
	limit, offset = defaultLimit, 0
	q := r.URL.Query()
	if v := q.Get("limit"); v != "" {
		n, convErr := strconv.Atoi(v)
		if convErr != nil || n < 1 || n > maxLimit {
			verr.Fields = map[string]string{"limit": fmt.Sprintf("must be an integer between 1 and %d", maxLimit)}
		}
		limit = n
	}
	if v := q.Get("offset"); v != "" {
		n, convErr := strconv.Atoi(v)
		if convErr != nil || n < 0 {
			if verr.Fields == nil {
				verr.Fields = map[string]string{}
			}
			verr.Fields["offset"] = "must be a non-negative integer"
		}
		offset = n
	}
	if len(verr.Fields) > 0 {
		return 0, 0, &verr
	}
	return limit, offset, nil
}
