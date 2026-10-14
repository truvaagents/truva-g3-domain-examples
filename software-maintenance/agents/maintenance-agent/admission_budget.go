package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/redis/go-redis/v9"
	maintenanceconfig "github.com/truvaagents/truva-g3-domain-examples/software-maintenance/internal/config"
	"github.com/truvaagents/truva-g3-domain-examples/software-maintenance/internal/lifecycle"
	"github.com/truvaagents/truva-g3/telemetry"
	"go.opentelemetry.io/otel/attribute"
)

type admissionDeferred struct{ Reason string }

func (e *admissionDeferred) Error() string { return "admission deferred: " + e.Reason }

type budgetEntry struct {
	Category string    `json:"category"`
	State    string    `json:"state"`
	TaskID   string    `json:"task_id,omitempty"`
	At       time.Time `json:"at"`
}

type admissionBudget struct {
	SchemaVersion int                               `json:"schema_version"`
	Policy        maintenanceconfig.AdmissionLimits `json:"policy"`
	StartedAt     time.Time                         `json:"started_at"`
	Deadline      time.Time                         `json:"deadline"`
	StoppedReason string                            `json:"stopped_reason,omitempty"`
	Entries       map[string]budgetEntry            `json:"entries"`
}

func (agent *maintenanceAgent) admissionBudgetKey() string {
	identity := agent.profile.CoordinatorProfileID + "\x00" + agent.profile.ServiceProfileID + "\x00" + agent.admission.RunID
	return agent.cases.keyspace.Tagged("maintenance-admission", lifecycle.Digest([]byte(identity)), "run")
}

// One bounded application ledger is atomic within one Redis cluster slot. It
// reserves capacity, not a transaction with the framework task store or queue.
func (agent *maintenanceAgent) updateAdmissionBudget(ctx context.Context, update func(*admissionBudget) error) (admissionBudget, error) {
	key := agent.admissionBudgetKey()
	for attempt := 0; attempt < 8; attempt++ {
		var result admissionBudget
		err := agent.cases.client.Watch(ctx, func(tx *redis.Tx) error {
			data, err := tx.Get(ctx, key).Bytes()
			if errors.Is(err, redis.Nil) {
				now := agent.now()
				result = admissionBudget{SchemaVersion: 1, Policy: agent.admission, StartedAt: now,
					Deadline: now.Add(agent.admission.RunDuration), Entries: map[string]budgetEntry{}}
			} else if err != nil {
				return err
			} else if err := decodeCoordinatorBytes(data, &result); err != nil {
				return err
			}
			if result.SchemaVersion != 1 || result.Policy != agent.admission || result.Entries == nil {
				return errors.New("admission run policy changed; use an explicitly new run ID to re-arm")
			}
			if err := update(&result); err != nil {
				return err
			}
			encoded, err := json.Marshal(result)
			if err != nil {
				return err
			}
			_, err = tx.TxPipelined(ctx, func(pipe redis.Pipeliner) error { pipe.Set(ctx, key, encoded, 0); return nil })
			return err
		}, key)
		if errors.Is(err, redis.TxFailedErr) {
			continue
		}
		return result, err
	}
	return admissionBudget{}, errors.New("admission budget reservation contention")
}

func (agent *maintenanceAgent) reserveAdmissionBudget(ctx context.Context, caseID, category string) error {
	if !agent.admission.Enabled {
		return nil
	}
	var deferred string
	_, err := agent.updateAdmissionBudget(ctx, func(budget *admissionBudget) error {
		deferred = "" // WATCH may repeat this callback after contention.
		now := agent.now()
		total, pending, window, failures, successes := 0, 0, 0, 0, 0
		var latest time.Time
		for id, entry := range budget.Entries {
			if entry.State == "rejected" {
				continue
			}
			total++
			if entry.State == "reserved" || entry.State == "admitted" {
				record, err := agent.cases.get(ctx, id)
				terminal := err == nil && (record.Status == "completed" || record.Status == "failed")
				if terminal && agent.taskStore != nil {
					task, taskErr := agent.taskStore.Get(ctx, record.CurrentTaskID)
					if taskErr != nil {
						return taskErr
					}
					terminal = task != nil && task.Status.IsTerminal()
				}
				if terminal {
					entry.State = "terminal"
					budget.Entries[id] = entry
				} else if err != nil && !errors.Is(err, errCaseNotFound) {
					return err
				} else {
					pending++
				}
			}
			if entry.At.After(latest) {
				latest = entry.At
			}
			if !entry.At.Before(now.Add(-agent.admission.Window)) {
				window++
				if entry.Category == "failure" {
					failures++
				} else {
					successes++
				}
			}
		}
		switch {
		case budget.StoppedReason != "":
			deferred = budget.StoppedReason
		case !now.Before(budget.Deadline):
			deferred = "run_deadline"
		case total >= agent.admission.MaxCases:
			deferred = "run_allowance"
		case pending >= agent.admission.MaxPending:
			deferred = "pending_capacity"
		case window >= agent.admission.MaxPerWindow:
			deferred = "window_allowance"
		case category == "failure" && failures >= agent.admission.FailureQuota:
			deferred = "failure_quota"
		case category == "success" && successes >= agent.admission.SuccessQuota:
			deferred = "success_quota"
		case !latest.IsZero() && now.Before(latest.Add(agent.admission.Cooldown)):
			deferred = "cooldown"
		}
		if deferred != "" {
			return nil
		}
		if entry, exists := budget.Entries[caseID]; exists && entry.State != "rejected" {
			deferred = "admission_pending"
			return nil
		}
		budget.Entries[caseID] = budgetEntry{Category: category, State: "reserved", At: now}
		return nil
	})
	status := "reserved"
	if deferred != "" {
		status = deferred
	}
	if err != nil {
		status = "store_unavailable"
	}
	telemetry.AddSpanEvent(ctx, "maintenance.admission.disposition", attribute.String("maintenance.case_id", caseID), attribute.String("run_id", agent.admission.RunID), attribute.String("status", status))
	if err != nil {
		return err
	}
	if deferred != "" {
		return &admissionDeferred{Reason: deferred}
	}
	return nil
}

func (agent *maintenanceAgent) settleAdmissionBudget(ctx context.Context, caseID, taskID, state string) error {
	if !agent.admission.Enabled {
		return nil
	}
	_, err := agent.updateAdmissionBudget(ctx, func(budget *admissionBudget) error {
		entry, exists := budget.Entries[caseID]
		if !exists {
			return fmt.Errorf("admission reservation is missing for %s", caseID)
		}
		entry.State, entry.TaskID = state, taskID
		budget.Entries[caseID] = entry
		if state == "rejected" {
			delete(budget.Entries, caseID)
		}
		if state == "ambiguous" {
			budget.StoppedReason = "ambiguous_handoff"
		}
		return nil
	})
	return err
}
