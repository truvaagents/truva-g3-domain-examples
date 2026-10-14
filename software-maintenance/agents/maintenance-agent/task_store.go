package main

import (
	"context"
	"errors"

	"github.com/truvaagents/truva-g3/core"
)

// cancellationPreservingTaskStore is a best-effort compatibility guard for the
// v0.5.0 worker's stale task writes, not a complete cancellation guarantee.
// Get followed by Update is not atomic: cancellation between them can still be
// overwritten. A durable fix requires framework-owned conditional transitions
// and a cancellation-aware worker. Active orchestration relies on deadlines;
// v0.5.0 does not propagate later cancellation into an in-flight handler.
type cancellationPreservingTaskStore struct {
	delegate core.TaskStore
}

func newCancellationPreservingTaskStore(delegate core.TaskStore) (*cancellationPreservingTaskStore, error) {
	if delegate == nil {
		return nil, errors.New("task store is required")
	}
	return &cancellationPreservingTaskStore{delegate: delegate}, nil
}

func (store *cancellationPreservingTaskStore) Create(ctx context.Context, task *core.Task) error {
	return store.delegate.Create(ctx, task)
}

func (store *cancellationPreservingTaskStore) Get(ctx context.Context, taskID string) (*core.Task, error) {
	return store.delegate.Get(ctx, taskID)
}

func (store *cancellationPreservingTaskStore) Update(ctx context.Context, task *core.Task) error {
	if task == nil {
		return errors.New("task is required")
	}
	current, err := store.delegate.Get(ctx, task.ID)
	if err != nil {
		return err
	}
	if current.Status == core.TaskStatusCancelled && task.Status != core.TaskStatusCancelled {
		return core.ErrTaskNotCancellable
	}
	return store.delegate.Update(ctx, task)
}

func (store *cancellationPreservingTaskStore) Delete(ctx context.Context, taskID string) error {
	return store.delegate.Delete(ctx, taskID)
}

func (store *cancellationPreservingTaskStore) Cancel(ctx context.Context, taskID string) error {
	return store.delegate.Cancel(ctx, taskID)
}
