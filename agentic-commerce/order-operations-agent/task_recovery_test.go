package main

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
	"github.com/truvaagents/truva-g3/core"
	"github.com/truvaagents/truva-g3/orchestration"
)

type testTaskStore struct {
	tasks map[string]*core.Task
}

func (s *testTaskStore) Create(_ context.Context, task *core.Task) error {
	if _, exists := s.tasks[task.ID]; exists {
		return core.ErrTaskAlreadyExists
	}
	s.tasks[task.ID] = task
	return nil
}

func (s *testTaskStore) Get(_ context.Context, id string) (*core.Task, error) {
	task, exists := s.tasks[id]
	if !exists {
		return nil, core.ErrTaskNotFound
	}
	return task, nil
}

func (s *testTaskStore) Update(_ context.Context, task *core.Task) error {
	if _, exists := s.tasks[task.ID]; !exists {
		return core.ErrTaskNotFound
	}
	s.tasks[task.ID] = task
	return nil
}

func (s *testTaskStore) Delete(_ context.Context, id string) error {
	delete(s.tasks, id)
	return nil
}

func (s *testTaskStore) Cancel(_ context.Context, id string) error {
	task, exists := s.tasks[id]
	if !exists {
		return core.ErrTaskNotFound
	}
	task.Status = core.TaskStatusCancelled
	return nil
}

type testTaskQueue struct {
	enqueued []*core.Task
}

func (q *testTaskQueue) Enqueue(_ context.Context, task *core.Task) error {
	q.enqueued = append(q.enqueued, task)
	return nil
}
func (q *testTaskQueue) Dequeue(context.Context, time.Duration) (*core.Task, error) {
	return nil, core.ErrTaskQueueEmpty
}
func (q *testTaskQueue) Acknowledge(context.Context, string) error    { return nil }
func (q *testTaskQueue) Reject(context.Context, string, string) error { return nil }

type testCommandController struct {
	orchestration.InterruptController
	result *orchestration.ResumeResult
	err    error
	called bool
}

func (c *testCommandController) ProcessCommand(_ context.Context, _ *orchestration.Command) (*orchestration.ResumeResult, error) {
	c.called = true
	return c.result, c.err
}

type testCheckpointProjectionStore struct {
	orchestration.CheckpointStore
	checkpoint *orchestration.ExecutionCheckpoint
	err        error
}

func (s *testCheckpointProjectionStore) LoadCheckpoint(_ context.Context, _ string) (*orchestration.ExecutionCheckpoint, error) {
	return s.checkpoint, s.err
}

func TestUpdateTaskFromCheckpointReconcilesOriginalTask(t *testing.T) {
	task := core.NewTask("task-1", "order_recovery", map[string]any{})
	store := &testTaskStore{tasks: map[string]*core.Task{task.ID: task}}
	agent := &OrderOperationsAgent{taskStore: store}
	result := map[string]any{"status": "completed", "request_id": "resume-1"}
	checkpoint := &orchestration.ExecutionCheckpoint{UserContext: map[string]any{"task_id": task.ID}}
	if err := agent.updateTaskFromCheckpoint(context.Background(), checkpoint, result); err != nil {
		t.Fatalf("updateTaskFromCheckpoint: %v", err)
	}
	if store.tasks[task.ID].Result.(map[string]any)["request_id"] != "resume-1" {
		t.Fatalf("original task did not receive resumed result: %+v", store.tasks[task.ID].Result)
	}
	if store.tasks[task.ID].Status != core.TaskStatusCompleted || store.tasks[task.ID].CompletedAt == nil {
		t.Fatalf("original task terminal state was not reconciled: %+v", store.tasks[task.ID])
	}
}

func TestResumeFailureMakesOriginalEventRetryable(t *testing.T) {
	task := core.NewTask("task-2", "order_recovery", map[string]any{})
	task.Status = core.TaskStatusCompleted
	store := &testTaskStore{tasks: map[string]*core.Task{task.ID: task}}
	agent := &OrderOperationsAgent{taskStore: store}
	checkpoint := &orchestration.ExecutionCheckpoint{UserContext: map[string]any{"task_id": task.ID}}
	if err := agent.updateTaskFromCheckpoint(context.Background(), checkpoint, map[string]any{"status": "resume_failed"}); err != nil {
		t.Fatalf("updateTaskFromCheckpoint: %v", err)
	}
	if task.Status != core.TaskStatusFailed || task.Error == nil {
		t.Fatalf("resume failure did not make the event retryable: %+v", task)
	}
}

func TestStaleBasisOutcomeMakesOriginalEventRetryableWithClassification(t *testing.T) {
	task := core.NewTask("task-stale", "order_recovery", map[string]any{})
	task.Status = core.TaskStatusCompleted
	store := &testTaskStore{tasks: map[string]*core.Task{task.ID: task}}
	agent := &OrderOperationsAgent{taskStore: store}
	checkpoint := &orchestration.ExecutionCheckpoint{UserContext: map[string]any{"task_id": task.ID}}
	result := map[string]any{"status": "replan_required", "terminal_outcome": terminalOutcomePolicyChanged, "retry_same_event": true}
	if err := agent.updateTaskFromCheckpoint(context.Background(), checkpoint, result); err != nil {
		t.Fatalf("updateTaskFromCheckpoint: %v", err)
	}
	if task.Status != core.TaskStatusFailed || task.Error == nil || task.Result.(map[string]any)["terminal_outcome"] != terminalOutcomePolicyChanged {
		t.Fatalf("stale-basis task was not retryable and classified: %+v", task)
	}
}

func TestTerminalHITLCommandReconcilesOriginalTask(t *testing.T) {
	for _, test := range []struct {
		name     string
		command  orchestration.CommandType
		terminal string
	}{
		{name: "reject", command: orchestration.CommandReject, terminal: terminalOutcomeRejected},
		{name: "abort", command: orchestration.CommandAbort, terminal: terminalOutcomeAborted},
	} {
		t.Run(test.name, func(t *testing.T) {
			task := core.NewTask("task-hitl-"+test.name, "order_recovery", map[string]any{})
			task.Status = core.TaskStatusCompleted
			task.Result = map[string]any{"status": "pending_approval"}
			store := &testTaskStore{tasks: map[string]*core.Task{task.ID: task}}
			agent := &OrderOperationsAgent{taskStore: store}
			checkpoint := &orchestration.ExecutionCheckpoint{
				CheckpointID: "cp-" + test.name,
				UserContext:  map[string]any{"task_id": task.ID, "order_id": "ORD-1004"},
			}
			agent.HandleHITLCommandProcessed(context.Background(), checkpoint, &orchestration.Command{CheckpointID: checkpoint.CheckpointID, Type: test.command}, nil)

			result := task.Result.(map[string]any)
			if result["terminal_outcome"] != test.terminal || result["policy_outcome"] != terminalOutcomeApproval {
				t.Fatalf("terminal task result = %#v", result)
			}
			if task.Status != core.TaskStatusCompleted || task.CompletedAt == nil {
				t.Fatalf("terminal task state = %+v", task)
			}
		})
	}
}

func TestHITLCommandProjectionDecoratorReconcilesOnlySuccessfulCommands(t *testing.T) {
	task := core.NewTask("task-hitl-decorator", "order_recovery", map[string]any{})
	task.Status = core.TaskStatusCompleted
	task.Result = map[string]any{"status": "pending_approval"}
	taskStore := &testTaskStore{tasks: map[string]*core.Task{task.ID: task}}
	agent := &OrderOperationsAgent{taskStore: taskStore}
	checkpoint := &orchestration.ExecutionCheckpoint{
		CheckpointID: "cp-decorator",
		UserContext:  map[string]any{"task_id": task.ID, "order_id": "ORD-1004"},
	}
	store := &testCheckpointProjectionStore{checkpoint: checkpoint}
	controller := &testCommandController{result: &orchestration.ResumeResult{
		CheckpointID: checkpoint.CheckpointID,
		Action:       orchestration.CommandReject,
		ShouldResume: false,
	}}
	decorated := newHITLCommandProjectingController(
		controller,
		store,
		agent.HandleHITLCommandProcessed,
	)
	command := &orchestration.Command{CheckpointID: checkpoint.CheckpointID, Type: orchestration.CommandReject}

	if _, err := decorated.ProcessCommand(context.Background(), command); err != nil {
		t.Fatalf("ProcessCommand: %v", err)
	}
	if !controller.called {
		t.Fatal("authoritative controller was not called")
	}
	if terminal := task.Result.(map[string]any)["terminal_outcome"]; terminal != terminalOutcomeRejected {
		t.Fatalf("terminal outcome = %v, want %s", terminal, terminalOutcomeRejected)
	}

	task.Result = map[string]any{"status": "pending_approval"}
	controller.err = errors.New("command persistence failed")
	if _, err := decorated.ProcessCommand(context.Background(), command); err == nil {
		t.Fatal("expected authoritative command failure")
	}
	if terminal := task.Result.(map[string]any)["terminal_outcome"]; terminal != nil {
		t.Fatalf("failed command unexpectedly projected terminal outcome %v", terminal)
	}
}

func TestExpiredHITLReconcilesOriginalTask(t *testing.T) {
	task := core.NewTask("task-hitl-expiry", "order_recovery", map[string]any{})
	task.Status = core.TaskStatusCompleted
	task.Result = map[string]any{"status": "pending_approval"}
	store := &testTaskStore{tasks: map[string]*core.Task{task.ID: task}}
	agent := &OrderOperationsAgent{taskStore: store}
	checkpoint := &orchestration.ExecutionCheckpoint{
		CheckpointID: "cp-expired", Status: orchestration.CheckpointStatusExpiredRejected,
		UserContext: map[string]any{"task_id": task.ID, "order_id": "ORD-1004"},
	}

	agent.HandleHITLExpiry(context.Background(), checkpoint, orchestration.CommandReject)

	result := task.Result.(map[string]any)
	if result["terminal_outcome"] != terminalOutcomeExpired || result["approval_action"] != string(orchestration.CommandReject) {
		t.Fatalf("expired task result = %#v", result)
	}
}

func TestDuplicateFailedEventIsSafelyRequeued(t *testing.T) {
	server := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: server.Addr()})
	defer client.Close()
	eventID := "retryable-event"
	task := core.NewTask("order-event-"+eventID, "order_recovery", map[string]any{"event_id": eventID, "order_id": "ORD-1003"})
	task.Status = core.TaskStatusFailed
	task.Error = &core.TaskError{Code: core.TaskErrorCodeHandlerError, Message: "transient startup error"}
	completedAt := time.Now()
	task.CompletedAt = &completedAt
	store := &testTaskStore{tasks: map[string]*core.Task{task.ID: task}}
	queue := &testTaskQueue{}
	receiver := &webhookReceiver{redis: client, queue: queue, store: store, logger: &core.NoOpLogger{}}
	if err := client.Set(context.Background(), "truvag3:commerce:event:"+eventID, "ORD-1003", time.Hour).Err(); err != nil {
		t.Fatal(err)
	}
	body := `{"event_id":"` + eventID + `","order_id":"ORD-1003","signal":"missing","decision_time":"2026-08-20T18:00:00Z"}`
	request := httptest.NewRequest(http.MethodPost, "/webhook/order-risk", strings.NewReader(body))
	response := httptest.NewRecorder()
	receiver.Handle(response, request)
	if response.Code != http.StatusAccepted || len(queue.enqueued) != 1 {
		t.Fatalf("status=%d enqueued=%d body=%s", response.Code, len(queue.enqueued), response.Body.String())
	}
	if task.Status != core.TaskStatusQueued || task.Error != nil || task.CompletedAt != nil {
		t.Fatalf("failed task was not reset for retry: %+v", task)
	}
	var payload map[string]any
	if err := json.Unmarshal(response.Body.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	if payload["retry"] != true {
		t.Fatalf("retry response not identified: %v", payload)
	}
}

func TestReplanRetryUsesFreshDecisionBoundaryAndClearsStaleResult(t *testing.T) {
	server := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: server.Addr()})
	defer client.Close()
	eventID := "replan-event"
	task := core.NewTask("order-event-"+eventID, "order_recovery", map[string]any{
		"event_id": eventID, "order_id": "ORD-1007", "decision_time": "2026-08-20T18:00:00Z",
	})
	task.Status = core.TaskStatusFailed
	task.Result = map[string]any{"status": "replan_required", "terminal_outcome": terminalOutcomePolicyChanged}
	task.Error = &core.TaskError{Code: core.TaskErrorCodeHandlerError, Message: "decision basis changed"}
	completedAt := time.Now()
	task.CompletedAt = &completedAt
	store := &testTaskStore{tasks: map[string]*core.Task{task.ID: task}}
	queue := &testTaskQueue{}
	receiver := &webhookReceiver{redis: client, queue: queue, store: store, logger: &core.NoOpLogger{}}
	if err := client.Set(context.Background(), "truvag3:commerce:event:"+eventID, "ORD-1007", time.Hour).Err(); err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodPost, "/webhook/order-risk", strings.NewReader(
		`{"event_id":"`+eventID+`","order_id":"ORD-1007","signal":"missing","decision_time":"2026-08-20T18:00:00Z"}`,
	))
	response := httptest.NewRecorder()
	before := time.Now().UTC().Add(-time.Second)

	receiver.Handle(response, request)

	if response.Code != http.StatusAccepted || len(queue.enqueued) != 1 {
		t.Fatalf("status=%d enqueued=%d body=%s", response.Code, len(queue.enqueued), response.Body.String())
	}
	if task.Result != nil {
		t.Fatalf("stale replan result was not cleared: %#v", task.Result)
	}
	decisionTime, err := time.Parse(time.RFC3339, task.Input["decision_time"].(string))
	if err != nil || decisionTime.Before(before) {
		t.Fatalf("retry decision boundary = %v, err=%v", decisionTime, err)
	}
}

func TestDuplicateActiveEventIsNotRequeued(t *testing.T) {
	server := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: server.Addr()})
	defer client.Close()
	eventID := "active-event"
	task := core.NewTask("order-event-"+eventID, "order_recovery", map[string]any{})
	task.Status = core.TaskStatusRunning
	store := &testTaskStore{tasks: map[string]*core.Task{task.ID: task}}
	queue := &testTaskQueue{}
	receiver := &webhookReceiver{redis: client, queue: queue, store: store, logger: &core.NoOpLogger{}}
	if err := client.Set(context.Background(), "truvag3:commerce:event:"+eventID, "ORD-1003", time.Hour).Err(); err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodPost, "/webhook/order-risk", strings.NewReader(`{"event_id":"`+eventID+`","order_id":"ORD-1003","signal":"missing"}`))
	response := httptest.NewRecorder()
	receiver.Handle(response, request)
	if response.Code != http.StatusConflict || len(queue.enqueued) != 0 {
		t.Fatalf("status=%d enqueued=%d body=%s", response.Code, len(queue.enqueued), response.Body.String())
	}
}

var _ core.TaskStore = (*testTaskStore)(nil)
var _ core.TaskQueue = (*testTaskQueue)(nil)
