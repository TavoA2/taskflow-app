// Package task holds the domain model and its validation rules. It knows nothing about
// HTTP or SQL; the limits mirror the CHECK constraints in migration 00001 so invalid input
// is rejected with a clear message before it reaches the database.
package task

import (
	"errors"
	"sort"
	"strings"
	"time"
	"unicode/utf8"
)

const (
	MaxTitleLen       = 200
	MaxDescriptionLen = 2000
)

type Status string

const (
	StatusTodo       Status = "todo"
	StatusInProgress Status = "in_progress"
	StatusDone       Status = "done"
)

func (s Status) Valid() bool {
	switch s {
	case StatusTodo, StatusInProgress, StatusDone:
		return true
	}
	return false
}

type Task struct {
	ID          string    `json:"id"`
	Title       string    `json:"title"`
	Description string    `json:"description"`
	Status      Status    `json:"status"`
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
}

// ErrNotFound is returned by stores when no task has the requested ID.
var ErrNotFound = errors.New("task not found")

// ValidationError maps field names to human-readable problems.
type ValidationError struct {
	Fields map[string]string
}

func (e *ValidationError) Error() string {
	names := make([]string, 0, len(e.Fields))
	for name := range e.Fields {
		names = append(names, name)
	}
	sort.Strings(names)
	parts := make([]string, len(names))
	for i, name := range names {
		parts[i] = name + ": " + e.Fields[name]
	}
	return "validation failed: " + strings.Join(parts, "; ")
}

func (e *ValidationError) add(field, msg string) {
	if e.Fields == nil {
		e.Fields = map[string]string{}
	}
	e.Fields[field] = msg
}

func (e *ValidationError) orNil() error {
	if len(e.Fields) == 0 {
		return nil
	}
	return e
}

type CreateInput struct {
	Title       string `json:"title"`
	Description string `json:"description"`
	Status      Status `json:"status"`
}

// Normalize trims the title and applies the default status.
func (in *CreateInput) Normalize() {
	in.Title = strings.TrimSpace(in.Title)
	if in.Status == "" {
		in.Status = StatusTodo
	}
}

func (in CreateInput) Validate() error {
	var verr ValidationError
	validateTitle(&verr, in.Title)
	validateDescription(&verr, in.Description)
	validateStatus(&verr, in.Status)
	return verr.orNil()
}

// UpdateInput is a partial update: nil fields are left unchanged.
type UpdateInput struct {
	Title       *string `json:"title"`
	Description *string `json:"description"`
	Status      *Status `json:"status"`
}

func (in *UpdateInput) Normalize() {
	if in.Title != nil {
		t := strings.TrimSpace(*in.Title)
		in.Title = &t
	}
}

func (in UpdateInput) Validate() error {
	var verr ValidationError
	if in.Title == nil && in.Description == nil && in.Status == nil {
		verr.add("body", "at least one of title, description or status is required")
		return &verr
	}
	if in.Title != nil {
		validateTitle(&verr, *in.Title)
	}
	if in.Description != nil {
		validateDescription(&verr, *in.Description)
	}
	if in.Status != nil {
		validateStatus(&verr, *in.Status)
	}
	return verr.orNil()
}

func validateTitle(verr *ValidationError, title string) {
	switch n := utf8.RuneCountInString(title); {
	case n == 0:
		verr.add("title", "is required")
	case n > MaxTitleLen:
		verr.add("title", "must be at most 200 characters")
	}
}

func validateDescription(verr *ValidationError, desc string) {
	if utf8.RuneCountInString(desc) > MaxDescriptionLen {
		verr.add("description", "must be at most 2000 characters")
	}
}

func validateStatus(verr *ValidationError, s Status) {
	if !s.Valid() {
		verr.add("status", "must be one of todo, in_progress, done")
	}
}

// ValidID reports whether s is a canonical UUID (8-4-4-4-12 hex digits). Checking it here
// turns malformed IDs into a 400 instead of a database type error.
func ValidID(s string) bool {
	if len(s) != 36 {
		return false
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch i {
		case 8, 13, 18, 23:
			if c != '-' {
				return false
			}
		default:
			if !('0' <= c && c <= '9' || 'a' <= c && c <= 'f' || 'A' <= c && c <= 'F') {
				return false
			}
		}
	}
	return true
}
