// Package store persists tasks in PostgreSQL using a pgx connection pool.
// All queries use bind parameters ($1, $2...), never string concatenation.
package store

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/TavoA2/taskflow-app/api/internal/task"
)

const columns = "id, title, description, status, created_at, updated_at"

type Postgres struct {
	pool *pgxpool.Pool
}

func NewPostgres(pool *pgxpool.Pool) *Postgres {
	return &Postgres{pool: pool}
}

// List returns tasks newest first. uuidv7 IDs are time-ordered, so ordering by the
// primary key avoids a separate index on created_at.
func (s *Postgres) List(ctx context.Context, limit, offset int) ([]task.Task, error) {
	rows, err := s.pool.Query(ctx,
		"SELECT "+columns+" FROM tasks ORDER BY id DESC LIMIT $1 OFFSET $2", limit, offset)
	if err != nil {
		return nil, fmt.Errorf("list tasks: %w", err)
	}
	tasks, err := pgx.CollectRows(rows, scanTask)
	if err != nil {
		return nil, fmt.Errorf("list tasks: %w", err)
	}
	return tasks, nil
}

func (s *Postgres) Get(ctx context.Context, id string) (task.Task, error) {
	return s.one(ctx, "get task", "SELECT "+columns+" FROM tasks WHERE id = $1", id)
}

func (s *Postgres) Create(ctx context.Context, in task.CreateInput) (task.Task, error) {
	return s.one(ctx, "create task",
		"INSERT INTO tasks (title, description, status) VALUES ($1, $2, $3) RETURNING "+columns,
		in.Title, in.Description, string(in.Status))
}

// Update applies a partial update in one statement: COALESCE keeps the current value for nil fields.
func (s *Postgres) Update(ctx context.Context, id string, in task.UpdateInput) (task.Task, error) {
	var status *string
	if in.Status != nil {
		v := string(*in.Status)
		status = &v
	}
	return s.one(ctx, "update task", `
		UPDATE tasks SET
			title       = COALESCE($2, title),
			description = COALESCE($3, description),
			status      = COALESCE($4, status),
			updated_at  = now()
		WHERE id = $1
		RETURNING `+columns,
		id, in.Title, in.Description, status)
}

func (s *Postgres) Delete(ctx context.Context, id string) error {
	tag, err := s.pool.Exec(ctx, "DELETE FROM tasks WHERE id = $1", id)
	if err != nil {
		return fmt.Errorf("delete task: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return task.ErrNotFound
	}
	return nil
}

func (s *Postgres) one(ctx context.Context, op, query string, args ...any) (task.Task, error) {
	rows, err := s.pool.Query(ctx, query, args...)
	if err != nil {
		return task.Task{}, fmt.Errorf("%s: %w", op, err)
	}
	t, err := pgx.CollectExactlyOneRow(rows, scanTask)
	if errors.Is(err, pgx.ErrNoRows) {
		return task.Task{}, task.ErrNotFound
	}
	if err != nil {
		return task.Task{}, fmt.Errorf("%s: %w", op, err)
	}
	return t, nil
}

func scanTask(row pgx.CollectableRow) (task.Task, error) {
	var t task.Task
	var status string
	err := row.Scan(&t.ID, &t.Title, &t.Description, &status, &t.CreatedAt, &t.UpdatedAt)
	t.Status = task.Status(status)
	return t, err
}
