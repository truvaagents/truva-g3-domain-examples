package main

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/truvaagents/truva-g3-domain-examples/software-maintenance/internal/lifecycle"
	"github.com/truvaagents/truva-g3/telemetry"
	"go.opentelemetry.io/otel/attribute"
)

type assessmentRecording struct {
	CheckID       string                 `json:"check_id"`
	ObservationID string                 `json:"observation_id"`
	Status        string                 `json:"status"`
	AssessmentID  string                 `json:"assessment_id,omitempty"`
	Error         string                 `json:"error,omitempty"`
	RecordedAt    time.Time              `json:"recorded_at"`
	ExecutionID   string                 `json:"execution_id,omitempty"`
	Intent        *assessmentWriteIntent `json:"intent,omitempty"`
}

// Persisted before dispatch. Re-entry reuses this exact CAS version and facts;
// reading a newer issue version would turn a lost-response retry into renewal.
type assessmentWriteIntent struct {
	IssueID              string `json:"issue_id"`
	ExpectedIssueVersion uint64 `json:"expected_issue_version"`
	FactsDigest          string `json:"facts_digest"`
}

// This application bookkeeping runs after durable case completion, not inside
// the model's plan. The passive tool validates all facts and conditional writes.
// Its failure does not change the completed model disposition or replay repair.
func (agent *maintenanceAgent) recordCompletedAssessments(ctx context.Context, input investigationInput, result investigationResult) {
	if agent.profile.AssessmentPolicy == nil {
		return
	}
	ctx = telemetry.WithBaggage(ctx, "request_id", result.ExecutionID)
	ctx, cancelRecording := context.WithTimeout(ctx, agent.lifecycleLimits.RecordingTimeout)
	defer cancelRecording()
	for _, observationID := range input.ObservationRefs {
		for _, rule := range agent.profile.AssessmentPolicy.Rules {
			started := time.Now()
			entry := assessmentRecording{CheckID: rule.CheckID, ObservationID: observationID, ExecutionID: result.ExecutionID, Status: "ineligible", RecordedAt: agent.now()}
			if !result.ExecutionSuccess || result.Status != "benign" && result.Status != "draft_pr_opened" {
				entry.Error = "completed case disposition or execution is ineligible for assessment issuance"
			} else {
				callCtx, cancel := context.WithTimeout(ctx, agent.httpClient.Timeout)
				err := agent.recordOneAssessment(callCtx, input, &entry)
				cancel()
				if err != nil {
					entry.Status, entry.Error = "unavailable", err.Error()
				} else {
					entry.Status = "recorded"
				}
			}
			entry.RecordedAt = agent.now()
			storeCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
			_, storeErr := agent.saveAssessmentRecording(storeCtx, input.CaseID, entry, false)
			cancel()
			if storeErr != nil && agent.Logger != nil {
				agent.Logger.ErrorWithContext(ctx, "Assessment issuance diagnostics could not be retained", map[string]interface{}{"operation": "record_assessment_diagnostics", "error_type": "assessment_store", "case_id": input.CaseID, "request_id": result.ExecutionID, "error": storeErr.Error()})
			}
			telemetry.AddSpanEvent(ctx, "maintenance.assessment.issuance", attribute.String("request_id", result.ExecutionID), attribute.String("maintenance.case_id", input.CaseID), attribute.String("check_id", rule.CheckID), attribute.String("status", entry.Status))
			if agent.Logger != nil {
				fields := map[string]interface{}{"operation": "record_completed_assessment", "request_id": result.ExecutionID, "case_id": input.CaseID, "check_id": rule.CheckID, "status": entry.Status, "assessment_id": entry.AssessmentID, "duration_ms": time.Since(started).Milliseconds(), "diagnostics_retained": storeErr == nil}
				if entry.Status == "unavailable" {
					// The case record retains the failure detail; logs carry a bounded
					// classification rather than an upstream response body.
					fields["error"], fields["error_type"] = "assessment issuance unavailable", "assessment_unavailable"
					agent.Logger.WarnWithContext(ctx, "Post-completion assessment issuance unavailable", fields)
				} else {
					agent.Logger.InfoWithContext(ctx, "Post-completion assessment issuance attempted", fields)
				}
			}
		}
	}
}

// First intent wins under the case-store transaction. A late failed attempt
// cannot erase a receipt another concurrent attempt has already retained.
func (agent *maintenanceAgent) saveAssessmentRecording(ctx context.Context, caseID string, entry assessmentRecording, prepare bool) (assessmentRecording, error) {
	origin, err := agent.cases.get(ctx, caseID)
	if err != nil {
		return entry, err
	}
	var saved assessmentRecording
	_, err = agent.cases.updateForTask(ctx, caseID, origin.CurrentTaskID, func(record *caseRecord) error {
		if record.Status != "completed" || record.Result == nil || record.Result.ExecutionID != entry.ExecutionID {
			return errCaseConflict
		}
		for i, existing := range record.AssessmentRecording {
			if existing.CheckID != entry.CheckID || existing.ObservationID != entry.ObservationID || existing.ExecutionID != entry.ExecutionID {
				continue
			}
			if existing.Status == "recorded" || prepare && existing.Intent != nil {
				saved = existing
				return nil
			}
			if existing.Intent != nil {
				if entry.Intent == nil {
					entry.Intent = existing.Intent
				} else if *entry.Intent != *existing.Intent {
					return errCaseConflict
				}
			}
			record.AssessmentRecording[i], saved = entry, entry
			return nil
		}
		record.AssessmentRecording = append(record.AssessmentRecording, entry)
		saved = entry
		return nil
	})
	return saved, err
}

func (agent *maintenanceAgent) recordOneAssessment(ctx context.Context, input investigationInput, entry *assessmentRecording) error {
	origin, err := agent.cases.get(ctx, input.CaseID)
	if err != nil {
		return fmt.Errorf("read assessment write intent: %w", err)
	}
	if origin.Status != "completed" || origin.Result == nil || origin.Result.ExecutionID != entry.ExecutionID {
		return errCaseConflict
	}
	for _, saved := range origin.AssessmentRecording {
		if saved.CheckID == entry.CheckID && saved.ObservationID == entry.ObservationID && saved.ExecutionID == entry.ExecutionID && saved.Intent != nil {
			*entry = saved
			break
		}
	}
	if entry.Status == "recorded" {
		return nil
	}
	if entry.Intent == nil {
		var lookup lifecycle.Lookup
		params := map[string]string{"profile_id": agent.profile.CoordinatorProfileID, "observation_id": entry.ObservationID, "check_id": entry.CheckID}
		if err := agent.callOwnedCapability(ctx, "maintenance-assessment-tool", "find_related_assessments", params, &lookup); err != nil {
			return fmt.Errorf("read current assessment facts: %w", err)
		}
		if !lookup.PolicyConfigured {
			return errors.New("assessment policy is unconfigured")
		}
		if lookup.CoordinatorProfileID != agent.profile.CoordinatorProfileID || lookup.CoordinatorProfileSHA256 != agent.profile.ProfileSHA256 {
			return errors.New("assessment issuer profile differs from coordinator profile")
		}
		if lookup.ObservationID != entry.ObservationID || lookup.CheckID != entry.CheckID || lookup.CurrentIssueID == "" || lookup.CurrentIssueVersion == nil || lookup.CurrentFactsDigest == "" {
			return errors.New("current assessment facts or authoritative issue version are unavailable")
		}
		// Truncated describes history, not the independently read current facts/version.
		entry.Intent = &assessmentWriteIntent{IssueID: lookup.CurrentIssueID, ExpectedIssueVersion: *lookup.CurrentIssueVersion, FactsDigest: lookup.CurrentFactsDigest}
		entry.Status, entry.Error = "pending", ""
		saved, err := agent.saveAssessmentRecording(ctx, input.CaseID, *entry, true)
		if err != nil {
			return fmt.Errorf("preserve assessment write intent before dispatch: %w", err)
		}
		*entry = saved
		if entry.Status == "recorded" {
			return nil
		}
	}
	var assessment lifecycle.Assessment
	payload := map[string]interface{}{"profile_id": agent.profile.CoordinatorProfileID, "case_id": input.CaseID, "observation_id": entry.ObservationID, "check_id": entry.CheckID, "expected_issue_version": entry.Intent.ExpectedIssueVersion, "expected_origin_execution_id": entry.ExecutionID, "expected_issue_id": entry.Intent.IssueID, "expected_facts_digest": entry.Intent.FactsDigest}
	if err := agent.callOwnedCapability(ctx, "maintenance-assessment-tool", "record_assessment", payload, &assessment); err != nil {
		return fmt.Errorf("write assessment with saved intent: %w", err)
	}
	if assessment.AssessmentID == "" || assessment.OriginCaseID != input.CaseID || assessment.OriginExecutionID != entry.ExecutionID || assessment.Facts.ObservationID != entry.ObservationID || assessment.Facts.CheckID != entry.CheckID || assessment.IssueID != entry.Intent.IssueID || assessment.Version != entry.Intent.ExpectedIssueVersion+1 || assessment.Facts.ComparedFactsDigest != entry.Intent.FactsDigest {
		return errors.New("assessment issuer returned mismatched origin or write intent")
	}
	entry.AssessmentID, entry.Error = assessment.AssessmentID, ""
	return nil
}
