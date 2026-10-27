//go:build integration

// Package testdb provides real PostgreSQL databases for integration tests.
//
// One container is started per test package (Main, from TestMain); each test then gets its own
// freshly created database (NewDatabase), so tests are isolated and can run in parallel without
// paying the container start-up cost every time. Requires a reachable Docker daemon.
package testdb

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"fmt"
	"io"
	"log/slog"
	"net/url"
	"os"
	"testing"

	"github.com/jackc/pgx/v5"
	_ "github.com/jackc/pgx/v5/stdlib"
	"github.com/testcontainers/testcontainers-go"
	tcpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"

	"github.com/TavoA2/taskflow-app/api/internal/migrations"
)

// defaultImage must match the postgres image in compose.yaml, so tests run against the same version.
const defaultImage = "postgres:18.6-alpine3.24"

var adminURL string

// Main starts the PostgreSQL container, runs the package's tests and removes the container.
// Use it as: func TestMain(m *testing.M) { os.Exit(testdb.Main(m)) }
func Main(m *testing.M) int {
	ctx := context.Background()
	image := os.Getenv("TEST_POSTGRES_IMAGE")
	if image == "" {
		image = defaultImage
	}

	ctr, err := tcpostgres.Run(ctx, image,
		tcpostgres.WithDatabase("admin"),
		tcpostgres.WithUsername("test"),
		tcpostgres.WithPassword("test"),
		// Waits for the "ready" log twice (the image restarts after its init server) and for the port.
		tcpostgres.BasicWaitStrategies(),
	)
	if err != nil {
		fmt.Fprintln(os.Stderr, "testdb: start postgres container (is Docker running?):", err)
		return 1
	}
	defer func() {
		if err := testcontainers.TerminateContainer(ctr); err != nil {
			fmt.Fprintln(os.Stderr, "testdb: terminate container:", err)
		}
	}()

	if adminURL, err = ctr.ConnectionString(ctx, "sslmode=disable"); err != nil {
		fmt.Fprintln(os.Stderr, "testdb: connection string:", err)
		return 1
	}
	return m.Run()
}

// NewDatabase creates an empty database unique to this test and returns its URL.
// The database is dropped when the test finishes.
func NewDatabase(t testing.TB) string {
	t.Helper()
	if adminURL == "" {
		t.Fatal("testdb: no container; call testdb.Main from TestMain")
	}
	ctx := context.Background()
	var b [6]byte
	_, _ = rand.Read(b[:])
	name := "t_" + hex.EncodeToString(b[:]) // generated, so safe to place in DDL

	admin, err := pgx.Connect(ctx, adminURL)
	if err != nil {
		t.Fatalf("testdb: connect admin: %v", err)
	}
	defer admin.Close(ctx)
	if _, err := admin.Exec(ctx, "CREATE DATABASE "+name); err != nil {
		t.Fatalf("testdb: create database: %v", err)
	}
	t.Cleanup(func() {
		conn, err := pgx.Connect(ctx, adminURL)
		if err != nil {
			t.Errorf("testdb: connect admin for cleanup: %v", err)
			return
		}
		defer conn.Close(ctx)
		if _, err := conn.Exec(ctx, "DROP DATABASE "+name+" WITH (FORCE)"); err != nil {
			t.Errorf("testdb: drop database %s: %v", name, err)
		}
	})

	u, err := url.Parse(adminURL)
	if err != nil {
		t.Fatalf("testdb: parse url: %v", err)
	}
	u.Path = "/" + name
	return u.String()
}

// MigratedDatabase is NewDatabase with all migrations applied, as `taskflow-api migrate up` would.
func MigratedDatabase(t testing.TB) string {
	t.Helper()
	dbURL := NewDatabase(t)
	db := OpenSQL(t, dbURL)
	if err := migrations.Run(context.Background(), db, "up", DiscardLogger); err != nil {
		t.Fatalf("testdb: migrate up: %v", err)
	}
	return dbURL
}

// OpenSQL opens a database/sql handle (the API used by goose) that is closed at test end.
func OpenSQL(t testing.TB, dbURL string) *sql.DB {
	t.Helper()
	db, err := sql.Open("pgx", dbURL)
	if err != nil {
		t.Fatalf("testdb: open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db
}

var DiscardLogger = slog.New(slog.NewTextHandler(io.Discard, nil))
