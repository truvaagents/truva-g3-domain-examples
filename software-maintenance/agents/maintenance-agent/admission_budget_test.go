package main

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
	maintenanceconfig "github.com/truvaagents/truva-g3-domain-examples/software-maintenance/internal/config"
	"github.com/truvaagents/truva-g3/core"
)

func budgetAgent(t *testing.T) *maintenanceAgent {
	t.Helper()
	server := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: server.Addr()})
	t.Cleanup(func() { _ = client.Close() })
	keys, err := core.NewRedisKeyspace("automatic-admission-test")
	if err != nil {
		t.Fatal(err)
	}
	store, err := newCaseStore(client, keys)
	if err != nil {
		t.Fatal(err)
	}
	policy, err := maintenanceconfig.LoadAdmissionLimits(true, func(string) (string, bool) { return "", false })
	if err != nil {
		t.Fatal(err)
	}
	return &maintenanceAgent{cases: store, profile: coordinatorProfile{CoordinatorProfileID: "coordinator", ServiceProfileID: "service"}, admission: policy, now: func() time.Time { return time.Now().UTC() }}
}

// Same-run concurrency and a restart cannot multiply the paid-work allowance.
func TestAdmissionRunConcurrencyRestartAndAmbiguity(t *testing.T) {
	agent := budgetAgent(t)
	agent.admission.MaxCases, agent.admission.MaxPending = 1, 1
	var accepted atomic.Int32
	var workers sync.WaitGroup
	for _, id := range []string{"one", "two", "three", "four"} {
		workers.Add(1)
		go func() {
			defer workers.Done()
			if agent.reserveAdmissionBudget(context.Background(), id, "failure") == nil {
				accepted.Add(1)
			}
		}()
	}
	workers.Wait()
	if accepted.Load() != 1 {
		t.Fatalf("concurrent admissions=%d", accepted.Load())
	}
	budget, err := agent.updateAdmissionBudget(t.Context(), func(*admissionBudget) error { return nil })
	if err != nil {
		t.Fatal(err)
	}
	var original string
	for id := range budget.Entries {
		original = id
	}
	if err := agent.settleAdmissionBudget(t.Context(), original, "task", "ambiguous"); err != nil {
		t.Fatal(err)
	}
	restarted := &maintenanceAgent{cases: agent.cases, profile: agent.profile, admission: agent.admission, now: agent.now}
	err = restarted.reserveAdmissionBudget(t.Context(), "new", "success")
	var deferred *admissionDeferred
	if !errors.As(err, &deferred) || deferred.Reason != "ambiguous_handoff" {
		t.Fatalf("ambiguous reservation lost on restart: %v", err)
	}
	if err := agent.settleAdmissionBudget(t.Context(), original, "task", "admitted"); err != nil {
		t.Fatal(err)
	}
	budget, _ = agent.updateAdmissionBudget(t.Context(), func(*admissionBudget) error { return nil })
	if budget.StoppedReason != "ambiguous_handoff" || len(budget.Entries) != 1 {
		t.Fatal("uncertainty silently cleared")
	}
}

func TestAdmissionDeadlineAndConfirmedRejection(t *testing.T) {
	agent := budgetAgent(t)
	now := time.Now().UTC()
	agent.now = func() time.Time { return now }
	if err := agent.reserveAdmissionBudget(t.Context(), "rejected", "failure"); err != nil {
		t.Fatal(err)
	}
	if err := agent.settleAdmissionBudget(t.Context(), "rejected", "", "rejected"); err != nil {
		t.Fatal(err)
	}
	if err := agent.reserveAdmissionBudget(t.Context(), "accepted", "failure"); err != nil {
		t.Fatal("confirmed pre-dispatch rejection did not release allowance", err)
	}
	now = now.Add(agent.admission.RunDuration)
	var deferred *admissionDeferred
	if err := agent.reserveAdmissionBudget(t.Context(), "late", "success"); !errors.As(err, &deferred) || deferred.Reason != "run_deadline" {
		t.Fatalf("deadline bypassed: %v", err)
	}
	agent.admission.MaxCases++
	if _, err := agent.updateAdmissionBudget(t.Context(), func(*admissionBudget) error { return nil }); err == nil {
		t.Fatal("same run policy mutation silently re-armed budget")
	}
}

func TestAdmissionRollingQuotasDoNotRefundRunAllowance(t *testing.T) {
	agent := budgetAgent(t)
	now := time.Now().UTC()
	agent.now = func() time.Time { return now }
	agent.admission.MaxCases, agent.admission.MaxPending, agent.admission.MaxPerWindow = 4, 10, 10
	agent.admission.SuccessQuota = 2
	admit := func(id, category string) {
		t.Helper()
		if err := agent.reserveAdmissionBudget(t.Context(), id, category); err != nil {
			t.Fatal(err)
		}
		if err := agent.settleAdmissionBudget(t.Context(), id, "task-"+id, "terminal"); err != nil {
			t.Fatal(err)
		}
		now = now.Add(agent.admission.Cooldown)
	}
	deferFor := func(id, category, reason string) {
		t.Helper()
		var deferred *admissionDeferred
		if err := agent.reserveAdmissionBudget(t.Context(), id, category); !errors.As(err, &deferred) || deferred.Reason != reason {
			t.Fatalf("expected %s, got %v", reason, err)
		}
	}
	admit("failure-one", "failure")
	deferFor("failure-two", "failure", "failure_quota")
	admit("success-one", "success")
	admit("success-two", "success")
	deferFor("success-three", "success", "success_quota")
	now = now.Add(agent.admission.Window)
	admit("later-success", "success")
	// A rolling window and a mutable profile pin can change neither the
	// stable run identity nor its cumulative four-case allowance.
	agent.profile.ProfileSHA256 = "changed-profile-pin"
	deferFor("over-run", "failure", "run_allowance")
	budget, err := agent.updateAdmissionBudget(t.Context(), func(*admissionBudget) error { return nil })
	if err != nil || len(budget.Entries) != 4 {
		t.Fatalf("run accounting changed: entries=%d error=%v", len(budget.Entries), err)
	}
}
