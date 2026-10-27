//go:build integration

package store

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"sync"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/prometheus/client_golang/prometheus/testutil"

	"github.com/TavoA2/taskflow-app/api/internal/task"
	"github.com/TavoA2/taskflow-app/api/internal/testdb"
)

func TestMain(m *testing.M) { os.Exit(testdb.Main(m)) }

// newStore returns a store on a fresh, fully migrated database.
func newStore(t *testing.T) (*Postgres, *pgxpool.Pool) {
	t.Helper()
	pool, err := pgxpool.New(context.Background(), testdb.MigratedDatabase(t))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	return NewPostgres(pool), pool
}

func ptr[T any](v T) *T { return &v }

func mustCreate(t *testing.T, s *Postgres, title string) task.Task {
	t.Helper()
	got, err := s.Create(context.Background(), task.CreateInput{Title: title, Status: task.StatusTodo})
	if err != nil {
		t.Fatalf("create %q: %v", title, err)
	}
	return got
}

func TestCreateAndGet(t *testing.T) {
	t.Parallel()
	s, _ := newStore(t)
	ctx := context.Background()

	created, err := s.Create(ctx, task.CreateInput{Title: "Deploy", Description: "to k3d", Status: task.StatusInProgress})
	if err != nil {
		t.Fatal(err)
	}
	if !task.ValidID(created.ID) || created.ID[14] != '7' {
		t.Errorf("id %q: want a database-generated UUIDv7", created.ID)
	}
	if created.CreatedAt.IsZero() || !created.CreatedAt.Equal(created.UpdatedAt) {
		t.Errorf("timestamps created=%v updated=%v: want set and equal on insert", created.CreatedAt, created.UpdatedAt)
	}

	got, err := s.Get(ctx, created.ID)
	if err != nil {
		t.Fatal(err)
	}
	// time.Time must be compared with Equal, not ==, which also compares location and monotonic data.
	if got.ID != created.ID || got.Title != created.Title || got.Description != created.Description ||
		got.Status != created.Status || !got.CreatedAt.Equal(created.CreatedAt) || !got.UpdatedAt.Equal(created.UpdatedAt) {
		t.Errorf("Get = %+v, want %+v", got, created)
	}
}

func TestGetMissingReturnsErrNotFound(t *testing.T) {
	t.Parallel()
	s, _ := newStore(t)
	if _, err := s.Get(context.Background(), "00000000-0000-7000-8000-000000000000"); !errors.Is(err, task.ErrNotFound) {
		t.Errorf("err = %v, want task.ErrNotFound", err)
	}
}

func TestListNewestFirstWithPagination(t *testing.T) {
	t.Parallel()
	s, _ := newStore(t)
	ctx := context.Background()

	empty, err := s.List(ctx, 10, 0)
	if err != nil || len(empty) != 0 {
		t.Fatalf("empty list = %v, %v", empty, err)
	}
	for i := 1; i <= 5; i++ {
		mustCreate(t, s, fmt.Sprintf("task %d", i))
	}

	page, err := s.List(ctx, 2, 1)
	if err != nil {
		t.Fatal(err)
	}
	if len(page) != 2 || page[0].Title != "task 4" || page[1].Title != "task 3" {
		t.Errorf("List(limit 2, offset 1) = %v, want [task 4, task 3]", titles(page))
	}
	past, err := s.List(ctx, 10, 50)
	if err != nil || len(past) != 0 {
		t.Errorf("offset past the end = %v, %v; want empty", titles(past), err)
	}
}

func titles(ts []task.Task) []string {
	out := make([]string, len(ts))
	for i, t := range ts {
		out[i] = t.Title
	}
	return out
}

func TestUpdateIsPartial(t *testing.T) {
	t.Parallel()
	s, _ := newStore(t)
	ctx := context.Background()
	orig, err := s.Create(ctx, task.CreateInput{Title: "orig", Description: "keep me", Status: task.StatusTodo})
	if err != nil {
		t.Fatal(err)
	}

	got, err := s.Update(ctx, orig.ID, task.UpdateInput{Status: ptr(task.StatusDone)})
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != task.StatusDone || got.Title != "orig" || got.Description != "keep me" {
		t.Errorf("after status-only update: %+v", got)
	}
	if !got.UpdatedAt.After(orig.UpdatedAt) || !got.CreatedAt.Equal(orig.CreatedAt) {
		t.Errorf("updated_at must advance and created_at stay: before %+v after %+v", orig, got)
	}

	// An explicit empty string is a value, not "absent": it must clear the description.
	got, err = s.Update(ctx, orig.ID, task.UpdateInput{Description: ptr("")})
	if err != nil {
		t.Fatal(err)
	}
	if got.Description != "" || got.Status != task.StatusDone {
		t.Errorf("clearing description: %+v", got)
	}

	if _, err := s.Update(ctx, "00000000-0000-7000-8000-000000000000", task.UpdateInput{Title: ptr("x")}); !errors.Is(err, task.ErrNotFound) {
		t.Errorf("update missing: err = %v, want task.ErrNotFound", err)
	}
}

// Concurrent PATCHes of different fields must not overwrite each other. A read-modify-write
// implementation (SELECT, change in Go, UPDATE all columns) loses one of them; the single
// UPDATE ... COALESCE statement does not.
//
// Each of many tasks gets exactly one title change and one description change, released
// together by a barrier. With one write per field, a lost write cannot be hidden by a later
// one, so a single lost update anywhere fails the test.
func TestConcurrentPartialUpdatesDoNotLoseWrites(t *testing.T) {
	t.Parallel()
	s, _ := newStore(t)
	ctx := context.Background()

	const tasks = 100
	ids := make([]string, tasks)
	for i := range ids {
		ids[i] = mustCreate(t, s, "original").ID
	}

	start := make(chan struct{})
	var wg sync.WaitGroup
	errs := make(chan error, 2*tasks)
	for _, id := range ids {
		wg.Go(func() {
			<-start
			_, err := s.Update(ctx, id, task.UpdateInput{Title: ptr("new title")})
			errs <- err
		})
		wg.Go(func() {
			<-start
			_, err := s.Update(ctx, id, task.UpdateInput{Description: ptr("new description")})
			errs <- err
		})
	}
	close(start)
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}

	lost := 0
	for _, id := range ids {
		got, err := s.Get(ctx, id)
		if err != nil {
			t.Fatal(err)
		}
		if got.Title != "new title" || got.Description != "new description" {
			lost++
		}
	}
	if lost > 0 {
		t.Errorf("lost update: %d of %d tasks are missing one of their two concurrent writes", lost, tasks)
	}
}

func TestDelete(t *testing.T) {
	t.Parallel()
	s, _ := newStore(t)
	ctx := context.Background()
	created := mustCreate(t, s, "bye")

	if err := s.Delete(ctx, created.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Get(ctx, created.ID); !errors.Is(err, task.ErrNotFound) {
		t.Errorf("get after delete: %v, want ErrNotFound", err)
	}
	if err := s.Delete(ctx, created.ID); !errors.Is(err, task.ErrNotFound) {
		t.Errorf("second delete: %v, want ErrNotFound", err)
	}
}

// The domain limits count characters; so must the database. 200 multi-byte characters
// (400 bytes) are valid in both layers, 201 are rejected by the CHECK constraint even if
// the application validation were bypassed.
func TestDatabaseConstraintsMatchDomainRules(t *testing.T) {
	t.Parallel()
	s, _ := newStore(t)
	ctx := context.Background()

	if _, err := s.Create(ctx, task.CreateInput{Title: strings.Repeat("ñ", task.MaxTitleLen), Status: task.StatusTodo}); err != nil {
		t.Errorf("200 multi-byte characters rejected by the database: %v", err)
	}

	cases := map[string]struct {
		in         task.CreateInput
		constraint string
	}{
		"title 201 chars":  {task.CreateInput{Title: strings.Repeat("ñ", task.MaxTitleLen+1), Status: task.StatusTodo}, "tasks_title_length"},
		"empty title":      {task.CreateInput{Title: "", Status: task.StatusTodo}, "tasks_title_length"},
		"description 2001": {task.CreateInput{Title: "a", Description: strings.Repeat("a", task.MaxDescriptionLen+1), Status: task.StatusTodo}, "tasks_description_length"},
		"unknown status":   {task.CreateInput{Title: "a", Status: "blocked"}, "tasks_status_valid"},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			_, err := s.Create(ctx, tc.in) // deliberately skips Validate(): the database is the last line of defence
			if err == nil || !strings.Contains(err.Error(), tc.constraint) {
				t.Errorf("err = %v, want violation of %s", err, tc.constraint)
			}
		})
	}
}

func TestValuesAreParameterizedNotInterpolated(t *testing.T) {
	t.Parallel()
	s, _ := newStore(t)
	ctx := context.Background()
	evil := `x'); DROP TABLE tasks; --`

	created := mustCreate(t, s, evil)
	got, err := s.Get(ctx, created.ID)
	if err != nil {
		t.Fatalf("table damaged? %v", err)
	}
	if got.Title != evil {
		t.Errorf("title stored as %q, want it verbatim", got.Title)
	}
}

func TestPoolCollectorReportsRealStats(t *testing.T) {
	t.Parallel()
	s, pool := newStore(t)
	mustCreate(t, s, "generate some pool activity")

	c := NewPoolCollector(pool)
	if n := testutil.CollectAndCount(c); n != 8 {
		t.Errorf("collector emitted %d metrics, want 8", n)
	}
	expected := fmt.Sprintf(`
# HELP taskflow_db_pool_max_conns Maximum pool size.
# TYPE taskflow_db_pool_max_conns gauge
taskflow_db_pool_max_conns %d
`, pool.Stat().MaxConns())
	if err := testutil.CollectAndCompare(c, strings.NewReader(expected), "taskflow_db_pool_max_conns"); err != nil {
		t.Error(err)
	}
	if pool.Stat().AcquireCount() == 0 {
		t.Error("expected at least one acquisition after a query")
	}
}
