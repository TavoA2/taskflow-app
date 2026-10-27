-- +goose Up
-- uuidv7() (PostgreSQL 18+) is time-ordered, so the primary key index also serves
-- "newest first" listings without a separate created_at index.
CREATE TABLE tasks (
    id          uuid        PRIMARY KEY DEFAULT uuidv7(),
    title       text        NOT NULL,
    description text        NOT NULL DEFAULT '',
    status      text        NOT NULL DEFAULT 'todo',
    created_at  timestamptz NOT NULL DEFAULT now(),
    updated_at  timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT tasks_title_length       CHECK (char_length(title) BETWEEN 1 AND 200),
    CONSTRAINT tasks_description_length CHECK (char_length(description) <= 2000),
    CONSTRAINT tasks_status_valid       CHECK (status IN ('todo', 'in_progress', 'done'))
);

-- +goose Down
DROP TABLE tasks;
