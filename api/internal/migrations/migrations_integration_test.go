//go:build integration

package migrations_test

import (
	"context"
	"database/sql"
	"os"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/pressly/goose/v3/lock"

	"github.com/TavoA2/taskflow-app/api/internal/migrations"
	"github.com/TavoA2/taskflow-app/api/internal/testdb"
)

func TestMain(m *testing.M) { os.Exit(testdb.Main(m)) }

func run(t *testing.T, db *sql.DB, action string) {
	t.Helper()
	if err := migrations.Run(context.Background(), db, action, testdb.DiscardLogger); err != nil {
		t.Fatalf("migrate %s: %v", action, err)
	}
}

func schemaVersion(t *testing.T, db *sql.DB) int64 {
	t.Helper()
	var v sql.NullInt64
	err := db.QueryRow(`SELECT max(version_id) FROM goose_db_version WHERE is_applied`).Scan(&v)
	if err != nil {
		t.Fatalf("read schema version: %v", err)
	}
	return v.Int64
}

func tableExists(t *testing.T, db *sql.DB, name string) bool {
	t.Helper()
	var exists bool
	if err := db.QueryRow(`SELECT to_regclass($1) IS NOT NULL`, "public."+name).Scan(&exists); err != nil {
		t.Fatal(err)
	}
	return exists
}

func TestUpCreatesSchemaWithConstraints(t *testing.T) {
	t.Parallel()
	db := testdb.OpenSQL(t, testdb.NewDatabase(t))

	run(t, db, "up")

	if v := schemaVersion(t, db); v != 1 {
		t.Errorf("schema version = %d, want 1", v)
	}
	rows, err := db.Query(`SELECT conname FROM pg_constraint WHERE conrelid = 'tasks'::regclass AND contype = 'c' ORDER BY conname`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var got []string
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			t.Fatal(err)
		}
		got = append(got, name)
	}
	want := []string{"tasks_description_length", "tasks_status_valid", "tasks_title_length"}
	if !slices.Equal(got, want) {
		t.Errorf("CHECK constraints = %v, want %v", got, want)
	}
}

func TestUpIsIdempotent(t *testing.T) {
	t.Parallel()
	db := testdb.OpenSQL(t, testdb.NewDatabase(t))

	run(t, db, "up")
	run(t, db, "up") // second run must be a no-op, not an error

	var applied int
	if err := db.QueryRow(`SELECT count(*) FROM goose_db_version WHERE version_id > 0`).Scan(&applied); err != nil {
		t.Fatal(err)
	}
	if applied != 1 {
		t.Errorf("goose_db_version has %d applied rows, want 1", applied)
	}
}

// Every migration must be reversible, and re-applying it must give the same result.
func TestFullRollbackAndReapply(t *testing.T) {
	t.Parallel()
	db := testdb.OpenSQL(t, testdb.NewDatabase(t))
	run(t, db, "up")
	top := schemaVersion(t, db)

	for v := top; v > 0; v-- {
		run(t, db, "down")
	}
	if v := schemaVersion(t, db); v != 0 {
		t.Fatalf("after rolling back everything, version = %d, want 0", v)
	}
	if tableExists(t, db, "tasks") {
		t.Error("table tasks still exists after full rollback")
	}

	run(t, db, "up")
	if v := schemaVersion(t, db); v != top {
		t.Errorf("after re-apply, version = %d, want %d", v, top)
	}
	if !tableExists(t, db, "tasks") {
		t.Error("table tasks missing after re-apply")
	}
}

func TestDownWithNothingAppliedIsNotAnError(t *testing.T) {
	t.Parallel()
	db := testdb.OpenSQL(t, testdb.NewDatabase(t))
	run(t, db, "down")
}

func TestStatusAndUnknownAction(t *testing.T) {
	t.Parallel()
	db := testdb.OpenSQL(t, testdb.NewDatabase(t))
	run(t, db, "status")

	if err := migrations.Run(context.Background(), db, "sideways", testdb.DiscardLogger); err == nil {
		t.Error("unknown action: expected an error")
	}
}

// Deterministic lock check: while another session holds goose's advisory lock, `up` must wait;
// once the lock is released it must proceed. (The concurrent test below exercises the same
// guarantee behaviourally, but a 15 ms migration rarely overlaps, so it cannot prove it alone.)
func TestUpWaitsForMigrationLock(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	dbURL := testdb.NewDatabase(t)

	holder, err := pgx.Connect(ctx, dbURL)
	if err != nil {
		t.Fatal(err)
	}
	defer holder.Close(ctx)
	if _, err := holder.Exec(ctx, "SELECT pg_advisory_lock($1)", lock.DefaultLockID); err != nil {
		t.Fatal(err)
	}

	db := testdb.OpenSQL(t, dbURL)
	done := make(chan error, 1)
	go func() { done <- migrations.Run(ctx, db, "up", testdb.DiscardLogger) }()

	select {
	case err := <-done:
		t.Fatalf("migrate up finished (err=%v) while the migration lock was held by another session", err)
	case <-time.After(1500 * time.Millisecond):
	}
	if tableExists(t, testdb.OpenSQL(t, dbURL), "tasks") {
		t.Fatal("schema changed while the migration lock was held")
	}

	if _, err := holder.Exec(ctx, "SELECT pg_advisory_unlock($1)", lock.DefaultLockID); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-done: // goose retries pg_try_advisory_lock every 5s by default
		if err != nil {
			t.Fatalf("migrate up after lock release: %v", err)
		}
	case <-time.After(15 * time.Second):
		t.Fatal("migrate up did not proceed after the lock was released")
	}
}

// Two replicas or Jobs migrating at once must not both apply the same migration:
// the advisory lock serializes them.
func TestConcurrentUpAppliesOnce(t *testing.T) {
	t.Parallel()
	dbURL := testdb.NewDatabase(t)

	const runners = 4
	// Separate *sql.DB per runner, like separate processes. Opened here because t.Fatal
	// (inside OpenSQL) must not be called from other goroutines.
	dbs := make([]*sql.DB, runners)
	for i := range dbs {
		dbs[i] = testdb.OpenSQL(t, dbURL)
	}
	var wg sync.WaitGroup
	errs := make([]error, runners)
	for i := range runners {
		wg.Go(func() {
			errs[i] = migrations.Run(context.Background(), dbs[i], "up", testdb.DiscardLogger)
		})
	}
	wg.Wait()

	for i, err := range errs {
		if err != nil {
			t.Errorf("runner %d: %v", i, err)
		}
	}
	db := testdb.OpenSQL(t, dbURL)
	var applied int
	if err := db.QueryRow(`SELECT count(*) FROM goose_db_version WHERE version_id = 1`).Scan(&applied); err != nil {
		t.Fatal(err)
	}
	if applied != 1 {
		t.Errorf("migration 1 recorded %d times, want exactly 1", applied)
	}
}
