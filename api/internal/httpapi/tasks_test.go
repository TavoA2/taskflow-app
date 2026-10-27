package httpapi

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/TavoA2/taskflow-app/api/internal/task"
)

type apiClient struct {
	t   *testing.T
	url string
}

func newAPI(t *testing.T, store TaskStore) *apiClient {
	t.Helper()
	srv := httptest.NewServer(NewRouter(slog.New(slog.NewTextHandler(io.Discard, nil)), store))
	t.Cleanup(srv.Close)
	return &apiClient{t: t, url: srv.URL}
}

// do sends a request and decodes a JSON response body into out (if non-nil).
func (c *apiClient) do(method, path, body string, out any) *http.Response {
	c.t.Helper()
	req, err := http.NewRequest(method, c.url+path, strings.NewReader(body))
	if err != nil {
		c.t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		c.t.Fatal(err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	if out != nil && len(raw) > 0 {
		if err := json.Unmarshal(raw, out); err != nil {
			c.t.Fatalf("decode %s %s response %q: %v", method, path, raw, err)
		}
	}
	return resp
}

func (c *apiClient) create(title string) task.Task {
	c.t.Helper()
	var got task.Task
	if resp := c.do("POST", "/api/v1/tasks", fmt.Sprintf(`{"title":%q}`, title), &got); resp.StatusCode != http.StatusCreated {
		c.t.Fatalf("create: status %d", resp.StatusCode)
	}
	return got
}

func wantStatus(t *testing.T, resp *http.Response, want int) {
	t.Helper()
	if resp.StatusCode != want {
		t.Fatalf("status = %d, want %d", resp.StatusCode, want)
	}
}

func TestCreateTask(t *testing.T) {
	api := newAPI(t, newFakeStore())

	var got task.Task
	resp := api.do("POST", "/api/v1/tasks", `{"title":"  Write ADR  ","description":"why k3d"}`, &got)

	wantStatus(t, resp, http.StatusCreated)
	if loc := resp.Header.Get("Location"); loc != "/api/v1/tasks/"+got.ID {
		t.Errorf("Location = %q, want /api/v1/tasks/%s", loc, got.ID)
	}
	if got.Title != "Write ADR" || got.Description != "why k3d" || got.Status != task.StatusTodo {
		t.Errorf("unexpected task %+v (title trimmed, default status todo)", got)
	}
}

func TestCreateTaskRejectsBadInput(t *testing.T) {
	cases := []struct {
		name, body, wantCode, wantField string
	}{
		{"empty title", `{"title":"   "}`, "validation_failed", "title"},
		{"bad status", `{"title":"a","status":"blocked"}`, "validation_failed", "status"},
		{"title too long", `{"title":"` + strings.Repeat("a", 201) + `"}`, "validation_failed", "title"},
		{"unknown field", `{"title":"a","priority":1}`, "bad_request", ""},
		{"malformed JSON", `{"title":`, "bad_request", ""},
		{"wrong type", `{"title":42}`, "bad_request", ""},
		{"empty body", ``, "bad_request", ""},
		{"two objects", `{"title":"a"}{"title":"b"}`, "bad_request", ""},
		{"oversize body", `{"title":"a","description":"` + strings.Repeat("x", maxBodyBytes) + `"}`, "bad_request", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			api := newAPI(t, newFakeStore())
			var got errorBody
			resp := api.do("POST", "/api/v1/tasks", tc.body, &got)

			wantStatus(t, resp, http.StatusBadRequest)
			if got.Code != tc.wantCode {
				t.Errorf("code = %q, want %q (error: %s)", got.Code, tc.wantCode, got.Error)
			}
			if tc.wantField != "" && got.Fields[tc.wantField] == "" {
				t.Errorf("expected an error for field %q, got %v", tc.wantField, got.Fields)
			}
		})
	}
}

func TestGetTask(t *testing.T) {
	api := newAPI(t, newFakeStore())
	created := api.create("read me")

	var got task.Task
	wantStatus(t, api.do("GET", "/api/v1/tasks/"+created.ID, "", &got), http.StatusOK)
	if got.ID != created.ID || got.Title != "read me" {
		t.Errorf("got %+v, want task %s", got, created.ID)
	}

	var notFound errorBody
	wantStatus(t, api.do("GET", "/api/v1/tasks/00000000-0000-7000-8000-999999999999", "", &notFound), http.StatusNotFound)
	if notFound.Code != "not_found" {
		t.Errorf("code = %q, want not_found", notFound.Code)
	}

	var badID errorBody
	wantStatus(t, api.do("GET", "/api/v1/tasks/not-a-uuid", "", &badID), http.StatusBadRequest)
	if badID.Fields["id"] == "" {
		t.Errorf("expected an id field error, got %+v", badID)
	}
}

func TestListTasks(t *testing.T) {
	api := newAPI(t, newFakeStore())

	var empty listResponse
	wantStatus(t, api.do("GET", "/api/v1/tasks", "", &empty), http.StatusOK)
	if empty.Items == nil || len(empty.Items) != 0 || empty.Limit != defaultLimit {
		t.Errorf("empty list = %+v, want items [] and default limit", empty)
	}

	for i := 1; i <= 3; i++ {
		api.create(fmt.Sprintf("task %d", i))
	}

	var page listResponse
	wantStatus(t, api.do("GET", "/api/v1/tasks?limit=2&offset=1", "", &page), http.StatusOK)
	if len(page.Items) != 2 || page.Items[0].Title != "task 2" || page.Items[1].Title != "task 1" {
		t.Errorf("page = %+v, want [task 2, task 1] (newest first, offset 1)", page.Items)
	}
}

func TestListTasksRejectsBadPagination(t *testing.T) {
	api := newAPI(t, newFakeStore())
	for _, q := range []string{"limit=0", "limit=101", "limit=abc", "offset=-1", "offset=x"} {
		t.Run(q, func(t *testing.T) {
			var got errorBody
			wantStatus(t, api.do("GET", "/api/v1/tasks?"+q, "", &got), http.StatusBadRequest)
			if got.Code != "validation_failed" {
				t.Errorf("code = %q, want validation_failed", got.Code)
			}
		})
	}
}

func TestUpdateTask(t *testing.T) {
	api := newAPI(t, newFakeStore())
	created := api.create("original")

	var got task.Task
	wantStatus(t, api.do("PATCH", "/api/v1/tasks/"+created.ID, `{"status":"in_progress"}`, &got), http.StatusOK)
	if got.Status != task.StatusInProgress || got.Title != "original" {
		t.Errorf("got %+v, want status changed and title untouched", got)
	}

	var verr errorBody
	wantStatus(t, api.do("PATCH", "/api/v1/tasks/"+created.ID, `{}`, &verr), http.StatusBadRequest)
	if verr.Fields["body"] == "" {
		t.Errorf("empty patch: expected a body error, got %+v", verr)
	}

	wantStatus(t, api.do("PATCH", "/api/v1/tasks/00000000-0000-7000-8000-999999999999", `{"title":"x"}`, nil), http.StatusNotFound)
}

func TestDeleteTask(t *testing.T) {
	api := newAPI(t, newFakeStore())
	created := api.create("to delete")

	resp := api.do("DELETE", "/api/v1/tasks/"+created.ID, "", nil)
	wantStatus(t, resp, http.StatusNoContent)

	wantStatus(t, api.do("GET", "/api/v1/tasks/"+created.ID, "", nil), http.StatusNotFound)
	wantStatus(t, api.do("DELETE", "/api/v1/tasks/"+created.ID, "", nil), http.StatusNotFound)
}

func TestStoreFailureDoesNotLeakDetails(t *testing.T) {
	store := newFakeStore()
	store.err = errors.New(`dial tcp 10.0.0.5:5432: password authentication failed for user "taskflow"`)
	api := newAPI(t, store)

	resp, err := http.Get(api.url + "/api/v1/tasks")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)

	wantStatus(t, resp, http.StatusInternalServerError)
	if strings.Contains(string(raw), "10.0.0.5") || strings.Contains(string(raw), "password") {
		t.Errorf("response leaks internal error: %s", raw)
	}
	if !strings.Contains(string(raw), `"code":"internal"`) {
		t.Errorf("body = %s, want code internal", raw)
	}
}
