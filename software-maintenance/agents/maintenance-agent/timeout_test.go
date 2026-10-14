package main

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/truvaagents/truva-g3-domain-examples/software-maintenance/internal/config"
	"github.com/truvaagents/truva-g3/core"
	"github.com/truvaagents/truva-g3/orchestration"
)

type timeoutCaptureStore struct {
	core.TaskStore
	task *core.Task
}

func (s *timeoutCaptureStore) Create(_ context.Context, task *core.Task) error {
	s.task = task
	return nil
}

type timeoutCaptureQueue struct{ core.TaskQueue }

func (*timeoutCaptureQueue) Enqueue(context.Context, *core.Task) error { return nil }

func TestSubmitTimeoutUsesSelectedPolicyAndPreservesShorterLimit(t *testing.T) {
	for _, profile := range []string{"", "expanded-budget"} {
		limits, err := config.LoadExecutionLimits(func(string) (string, bool) { return profile, profile != "" })
		if err != nil {
			t.Fatal(err)
		}
		for _, duration := range []time.Duration{time.Minute, limits.Task, limits.Task + time.Second, -time.Second} {
			t.Run(profile+"/"+duration.String(), func(t *testing.T) {
				store := &timeoutCaptureStore{}
				agent := &maintenanceAgent{limits: limits, profile: coordinatorProfile{
					CoordinatorProfileID: "test-profile",
					Limits:               coordinatorLimits{MaximumRequestBytes: 4096, MaximumObjectiveBytes: 1024, MaximumObservationRefs: 4},
				}}
				api := orchestration.NewTaskAPIHandler(&timeoutCaptureQueue{}, store, &core.NoOpLogger{})
				body := fmt.Sprintf(`{"type":"maintenance.investigate","timeout":%q,"input":{"profile_id":"test-profile","case_id":"test-case","objective":"Assess evidence","observation_refs":["test-observation"]}}`, duration.String())
				writer := httptest.NewRecorder()
				agent.validateTaskSubmit(api)(writer, httptest.NewRequest(http.MethodPost, "/api/v1/tasks", strings.NewReader(body)))
				if duration <= 0 || duration > limits.Task {
					if writer.Code != http.StatusBadRequest || store.task != nil {
						t.Fatalf("invalid timeout admitted: HTTP %d", writer.Code)
					}
					return
				}
				if writer.Code != http.StatusAccepted || store.task == nil || store.task.Options.Timeout != duration {
					t.Fatalf("timeout was not preserved: HTTP %d task=%+v", writer.Code, store.task)
				}
			})
		}
	}
}
