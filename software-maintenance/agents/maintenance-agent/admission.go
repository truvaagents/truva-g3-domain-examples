package main

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/truvaagents/truva-g3-domain-examples/software-maintenance/internal/observation"
	"github.com/truvaagents/truva-g3/core"
	"github.com/truvaagents/truva-g3/telemetry"
)

func (agent *maintenanceAgent) admitInvestigation(ctx context.Context, input investigationInput, source string, observations ...observationSummary) (admissionResult, error) {
	var err error
	input, err = agent.validateInvestigationInput(input)
	if err != nil {
		return admissionResult{}, err
	}
	if agent.admission.Enabled {
		// Existing exact observations do not spend another admission. No retry of
		// failed or uncertain admissions is implied by redelivery in a bounded run.
		existing, getErr := agent.cases.get(ctx, input.CaseID)
		if getErr == nil {
			if !sameCaseIdentity(existing, agent.profile, input) {
				return admissionResult{}, errCaseConflict
			}
			if existing.Status == "admission_failed" || existing.Status == "pending_admission" {
				return admissionResult{}, &admissionDeferred{Reason: "admission_pending"}
			}
			return admissionResult{CaseID: existing.CaseID, TaskID: existing.CurrentTaskID, Status: existing.Status, Reused: true}, nil
		}
		if !errors.Is(getErr, errCaseNotFound) {
			return admissionResult{}, getErr
		}
		category := "failure"
		if len(observations) == 0 {
			var evidence struct {
				Observation struct {
					HTTPStatus int    `json:"http_status"`
					ErrorCode  string `json:"error_code"`
				} `json:"observation"`
			}
			if err := agent.callEvidenceCapability(ctx, "get_service_evidence", map[string]string{"profile_id": agent.profile.ServiceProfileID, "observation_id": input.ObservationRefs[0]}, &evidence); err != nil {
				return admissionResult{}, err
			}
			if evidence.Observation.HTTPStatus >= 200 && evidence.Observation.HTTPStatus < 300 && evidence.Observation.ErrorCode == "" {
				category = "success"
			}
		} else if observation := observations[0]; observation.HTTPStatus >= 200 && observation.HTTPStatus < 300 && observation.ErrorCode == "" {
			category = "success"
		}
		if err := agent.reserveAdmissionBudget(ctx, input.CaseID, category); err != nil {
			return admissionResult{}, err
		}
	}
	record, dispatch, err := agent.cases.reserveObservation(ctx, agent.profile, input, source, agent.now())
	if err != nil {
		if errors.Is(err, errCaseConflict) {
			err = errors.Join(err, agent.settleAdmission(ctx, input.CaseID, "", "rejected"))
		} else {
			err = errors.Join(err, agent.settleAdmission(ctx, input.CaseID, "", "ambiguous"))
		}
		return admissionResult{}, err
	}
	result := admissionResult{CaseID: record.CaseID, TaskID: record.CurrentTaskID, Status: record.Status, Reused: !dispatch}
	if !dispatch {
		return result, agent.settleAdmission(ctx, input.CaseID, record.CurrentTaskID, "rejected")
	}
	task := core.NewTaskWithTimeout(record.CurrentTaskID, investigationTaskType, map[string]interface{}{
		"profile_id": input.ProfileID, "case_id": input.CaseID, "objective": input.Objective,
		"observation_refs": append([]string(nil), input.ObservationRefs...),
	}, agent.limits.Task)
	trace := telemetry.GetTraceContext(ctx)
	task.TraceID = trace.TraceID
	task.ParentSpanID = trace.SpanID
	if err := agent.taskStore.Create(ctx, task); err != nil {
		code := "TASK_STORE_FAILED"
		if errors.Is(err, core.ErrTaskAlreadyExists) {
			code = "TASK_ID_ALREADY_EXISTS"
		}
		_ = agent.cases.markAdmissionFailed(ctx, input.CaseID, task.ID, code, agent.now())
		return admissionResult{}, errors.Join(fmt.Errorf("create investigation task: %w", err), agent.settleAdmission(ctx, input.CaseID, task.ID, "ambiguous"))
	}
	if err := agent.taskQueue.Enqueue(ctx, task); err != nil {
		// Preserve the task on an uncertain enqueue: deleting it could orphan
		// work the queue accepted before its acknowledgement was lost.
		_ = agent.cases.markAdmissionFailed(ctx, input.CaseID, task.ID, "TASK_ENQUEUE_FAILED", agent.now())
		return admissionResult{}, errors.Join(fmt.Errorf("enqueue investigation task: %w", err), agent.settleAdmission(ctx, input.CaseID, task.ID, "ambiguous"))
	}
	if err := agent.cases.markQueued(ctx, input.CaseID, task.ID, agent.now()); err != nil {
		return admissionResult{}, errors.Join(fmt.Errorf("record queued investigation: %w", err), agent.settleAdmission(ctx, input.CaseID, task.ID, "ambiguous"))
	}
	result.Status = "queued"
	result.Reused = false
	if err := agent.settleAdmission(ctx, input.CaseID, task.ID, "admitted"); err != nil {
		return admissionResult{}, err
	}
	return result, nil
}

func (agent *maintenanceAgent) settleAdmission(ctx context.Context, caseID, taskID, state string) error {
	storeCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancel()
	err := agent.settleAdmissionBudget(storeCtx, caseID, taskID, state)
	if err != nil && agent.Logger != nil {
		agent.Logger.ErrorWithContext(ctx, "Admission accounting requires reconciliation", map[string]interface{}{"operation": "admission_accounting", "request_id": observation.ContextRequestID(ctx), "error_type": "admission_accounting", "case_id": caseID, "task_id": taskID, "status": "uncertain", "error": err.Error()})
	}
	return err
}
