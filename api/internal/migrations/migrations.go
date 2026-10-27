// Package migrations embeds the SQL schema migrations and applies them with goose.
// Migrations run as a separate one-shot step (see ADR-0004), never on API startup.
package migrations

import (
	"context"
	"database/sql"
	"embed"
	"errors"
	"fmt"
	"log/slog"

	"github.com/pressly/goose/v3"
	"github.com/pressly/goose/v3/lock"
)

//go:embed *.sql
var files embed.FS

// Run executes a goose action: "up" (apply all pending), "status", or "down"
// (roll back the latest migration; exceptional and manual, see ADR-0004).
func Run(ctx context.Context, db *sql.DB, action string, logger *slog.Logger) error {
	// The advisory lock serializes concurrent runs (e.g. two Jobs started by mistake).
	locker, err := lock.NewPostgresSessionLocker()
	if err != nil {
		return fmt.Errorf("create session locker: %w", err)
	}
	provider, err := goose.NewProvider(goose.DialectPostgres, db, files, goose.WithSessionLocker(locker))
	if err != nil {
		return fmt.Errorf("create goose provider: %w", err)
	}

	switch action {
	case "up":
		results, err := provider.Up(ctx)
		for _, r := range results {
			logResult(logger, r)
		}
		if err != nil {
			return fmt.Errorf("migrate up: %w", err)
		}
		if len(results) == 0 {
			logger.Info("no pending migrations")
		}
	case "down":
		r, err := provider.Down(ctx)
		if errors.Is(err, goose.ErrNoNextVersion) {
			logger.Info("no migration to roll back")
			return nil
		}
		if r != nil {
			logResult(logger, r)
		}
		if err != nil {
			return fmt.Errorf("migrate down: %w", err)
		}
	case "status":
		statuses, err := provider.Status(ctx)
		if err != nil {
			return fmt.Errorf("migrate status: %w", err)
		}
		for _, s := range statuses {
			attrs := []any{"migration", s.Source.Version, "file", s.Source.Path, "state", string(s.State)}
			if !s.AppliedAt.IsZero() {
				attrs = append(attrs, "applied_at", s.AppliedAt)
			}
			logger.Info("migration", attrs...)
		}
	default:
		return fmt.Errorf("unknown migrate action %q (want up, status or down)", action)
	}

	version, err := provider.GetDBVersion(ctx)
	if err != nil {
		return fmt.Errorf("read schema version: %w", err)
	}
	// Attribute keys avoid "version", which the base logger already uses for the build version.
	logger.Info("schema version", "schema_version", version)
	return nil
}

func logResult(logger *slog.Logger, r *goose.MigrationResult) {
	attrs := []any{"migration", r.Source.Version, "file", r.Source.Path, "direction", r.Direction, "duration", r.Duration.String()}
	if r.Error != nil {
		logger.Error("migration failed", append(attrs, "err", r.Error)...)
		return
	}
	logger.Info("migration applied", attrs...)
}
