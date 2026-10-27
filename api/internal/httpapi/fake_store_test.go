package httpapi

import (
	"context"
	"fmt"
	"sort"
	"sync"
	"time"

	"github.com/TavoA2/taskflow-app/api/internal/task"
)

// fakeStore is an in-memory TaskStore. Setting err makes every call fail with it,
// to exercise the handlers' internal-error path.
type fakeStore struct {
	mu    sync.Mutex
	tasks map[string]task.Task
	seq   int
	err   error
}

func newFakeStore() *fakeStore { return &fakeStore{tasks: map[string]task.Task{}} }

func (s *fakeStore) List(_ context.Context, limit, offset int) ([]task.Task, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.err != nil {
		return nil, s.err
	}
	all := make([]task.Task, 0, len(s.tasks))
	for _, t := range s.tasks {
		all = append(all, t)
	}
	sort.Slice(all, func(i, j int) bool { return all[i].ID > all[j].ID }) // newest first, like uuidv7
	if offset >= len(all) {
		return nil, nil
	}
	return all[offset:min(offset+limit, len(all))], nil
}

func (s *fakeStore) Get(_ context.Context, id string) (task.Task, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.err != nil {
		return task.Task{}, s.err
	}
	t, ok := s.tasks[id]
	if !ok {
		return task.Task{}, task.ErrNotFound
	}
	return t, nil
}

func (s *fakeStore) Create(_ context.Context, in task.CreateInput) (task.Task, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.err != nil {
		return task.Task{}, s.err
	}
	s.seq++
	now := time.Now().UTC()
	t := task.Task{
		ID:    fmt.Sprintf("00000000-0000-7000-8000-%012d", s.seq),
		Title: in.Title, Description: in.Description, Status: in.Status,
		CreatedAt: now, UpdatedAt: now,
	}
	s.tasks[t.ID] = t
	return t, nil
}

func (s *fakeStore) Update(_ context.Context, id string, in task.UpdateInput) (task.Task, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.err != nil {
		return task.Task{}, s.err
	}
	t, ok := s.tasks[id]
	if !ok {
		return task.Task{}, task.ErrNotFound
	}
	if in.Title != nil {
		t.Title = *in.Title
	}
	if in.Description != nil {
		t.Description = *in.Description
	}
	if in.Status != nil {
		t.Status = *in.Status
	}
	t.UpdatedAt = time.Now().UTC()
	s.tasks[id] = t
	return t, nil
}

func (s *fakeStore) Delete(_ context.Context, id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.err != nil {
		return s.err
	}
	if _, ok := s.tasks[id]; !ok {
		return task.ErrNotFound
	}
	delete(s.tasks, id)
	return nil
}
