package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/truvaagents/truva-g3-domain-examples/software-maintenance/internal/lifecycle"
	"github.com/truvaagents/truva-g3-domain-examples/software-maintenance/internal/observation"
	"github.com/truvaagents/truva-g3-domain-examples/software-maintenance/internal/responsecontract"
	"github.com/truvaagents/truva-g3/core"
	"github.com/truvaagents/truva-g3/orchestration"
	"github.com/truvaagents/truva-g3/telemetry"
	"go.opentelemetry.io/otel/attribute"
)

// A link is a current, read-only decision about another case's work. It is not
// a publication receipt for this case or permission to reuse an approval.
type linkedRemediationObservation struct {
	SchemaVersion                  int       `json:"schema_version"`
	ProfileID                      string    `json:"profile_id"`
	ProfileSHA256                  string    `json:"profile_sha256"`
	RepositoryID                   string    `json:"repository_id"`
	PullRequestNumber              int       `json:"pull_request_number"`
	PullRequestURL                 string    `json:"pull_request_url"`
	State                          string    `json:"state"`
	ObservedAt                     time.Time `json:"observed_at"`
	ReferenceKind                  string    `json:"reference_kind"`
	OriginPublicationID            string    `json:"origin_publication_id"`
	OriginCaseID                   string    `json:"origin_case_id"`
	OriginPublicationProfileSHA256 string    `json:"origin_publication_profile_sha256"`
	PublishedCommitSHA             string    `json:"published_commit_sha"`
	HeadSHA                        string    `json:"head_sha"`
	BaseSHA                        string    `json:"base_sha"`
	MergeSHA                       string    `json:"merge_sha,omitempty"`
	HeadMatchesPublicationCommit   *bool     `json:"head_matches_publication_commit,omitempty"`
	Merged                         *bool     `json:"merged,omitempty"`
}

func isLinkedDisposition(status string) bool {
	return status == "known_issue" || status == "existing_remediation" || status == "awaiting_deployment"
}

// Checks positive claims only. The model chooses the disposition and references.
// Read-only acceptance checks refresh those exact references after synthesis;
// they are application validation, not fabricated plan steps or a repair flow.
func (agent *maintenanceAgent) validateLinkedDecision(ctx context.Context, input investigationInput, decision investigationDecision, execution *orchestration.ExecutionResult, result *investigationResult) (validationErr error) {
	ctx = core.WithRequestID(ctx, result.ExecutionID)
	ctx = telemetry.WithBaggage(ctx, "request_id", result.ExecutionID)
	started := time.Now()
	var lookup lifecycle.Lookup
	var state linkedRemediationObservation
	defer func() {
		status := "accepted"
		if validationErr != nil {
			status = "rejected"
			telemetry.RecordSpanError(ctx, validationErr)
		}
		fields := map[string]interface{}{"operation": "validate_linked_decision", "status": status, "duration_ms": time.Since(started).Milliseconds(), "request_id": observation.ContextRequestID(ctx), "case_id": input.CaseID, "disposition": decision.Status}
		attrs := []attribute.KeyValue{attribute.String("request_id", observation.ContextRequestID(ctx)), attribute.String("status", status), attribute.String("maintenance.case_id", input.CaseID)}
		var contractErr *responsecontract.ValidationError
		if errors.As(validationErr, &contractErr) {
			fields["validation_code"] = contractErr.Code
			fields["validation_field"] = contractErr.Field
			attrs = append(attrs, attribute.String("validation_code", contractErr.Code), attribute.String("validation_field", contractErr.Field))
		}
		for name, observed := range map[string]time.Time{"assessment": lookup.CheckedAt, "remediation": state.ObservedAt} {
			if !observed.IsZero() {
				age := agent.now().Sub(observed).Milliseconds()
				fields[name+"_age_ms"] = age
				attrs = append(attrs, attribute.Int64(name+"_age_ms", age))
			}
		}
		telemetry.AddSpanEvent(ctx, "maintenance.linked_decision.validated", attrs...)
		if agent.BaseAgent != nil && agent.Logger != nil {
			if validationErr != nil {
				fields["error"] = validationErr.Error()
				fields["error_type"] = "linked_evidence_invalid"
				agent.Logger.WarnWithContext(ctx, "Linked decision rejected after evidence validation", fields)
			} else {
				agent.Logger.InfoWithContext(ctx, "Linked decision accepted after evidence validation", fields)
			}
		}
	}()
	invalid := errors.New("linked disposition requires a current, applicable assessment and attributable fresh evidence")
	if execution == nil || !execution.Success || agent.profile.AssessmentPolicy == nil || agent.assessments == nil || decision.Assessment == nil || decision.NextCheck == nil {
		return invalid
	}
	ref := decision.Assessment
	belongs := false
	for _, observation := range input.ObservationRefs {
		belongs = belongs || observation == ref.ObservationID
	}
	if !belongs {
		return &responsecontract.ValidationError{
			Code: "LINKED_OBSERVATION_MISMATCH", Field: "assessment.observation_id",
			Expected: "the current applicability comparison's observation_id, matching this case's observation_refs",
			Reason:   "the selected observation does not belong to this case",
			Cause:    errors.New("linked assessment.observation_id must identify a current case observation"),
		}
	}
	if ref.CheckID == "" || ref.AssessmentID == "" {
		return invalid
	}
	if agent.httpClient == nil || agent.httpClient.Timeout <= 0 {
		return errors.New("linked evidence refresh requires a bounded application HTTP client")
	}
	// One shared existing HTTP budget bounds both reads and the final store
	// checks. Parent cancellation still wins; no retries or background work.
	ctx, cancel := context.WithTimeout(ctx, agent.httpClient.Timeout)
	defer cancel()
	if err := agent.callOwnedCapability(ctx, "maintenance-assessment-tool", "find_related_assessments", map[string]string{
		"profile_id": agent.profile.CoordinatorProfileID, "observation_id": ref.ObservationID, "check_id": ref.CheckID,
	}, &lookup); err != nil {
		return fmt.Errorf("refresh linked assessment: %w", err)
	}
	if decision.Status != "known_issue" {
		if decision.Remediation == nil {
			return invalid
		}
		if err := agent.callOwnedCapability(ctx, "maintenance-publication-tool", "get_remediation_state", map[string]string{
			"profile_id": agent.profile.PublicationProfileID, "publication_id": decision.Remediation.PublicationID, "origin_case_id": decision.Remediation.OriginCaseID,
		}, &state); err != nil {
			return fmt.Errorf("refresh linked remediation: %w", err)
		}
	}
	// Ages and deadlines are evaluated after network reads, not before them.
	now := agent.now()
	if !decision.NextCheck.NotBefore.After(now) || decision.NextCheck.NotBefore.After(now.Add(agent.lifecycleLimits.RevalidateAfter)) {
		return linkedNextCheckError("the selected time is outside the configured revalidation window", decision.NextCheck.NotBefore, now, now.Add(agent.lifecycleLimits.RevalidateAfter))
	}
	if !lookup.PolicyConfigured || lookup.CoordinatorProfileID != agent.profile.CoordinatorProfileID || lookup.CoordinatorProfileSHA256 != agent.profile.ProfileSHA256 ||
		lookup.ObservationID != ref.ObservationID || lookup.CheckID != ref.CheckID || !freshLinkedObservation(lookup.CheckedAt, now, agent.lifecycleLimits.RemediationMaxAge) {
		return invalid
	}
	var accepted *lifecycle.Match
	for index := range lookup.Candidates {
		candidate := &lookup.Candidates[index]
		if candidate.Assessment.AssessmentID == ref.AssessmentID && candidate.Eligible && candidate.Reason == "exact_applicability" {
			accepted = candidate
		}
	}
	if accepted == nil {
		return invalid
	}
	assessment, err := agent.assessments.Get(ctx, ref.AssessmentID)
	if err != nil {
		return err
	}
	current, err := agent.assessments.Current(ctx, assessment)
	if err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	now = agent.now()
	if !freshLinkedObservation(lookup.CheckedAt, now, agent.lifecycleLimits.RemediationMaxAge) {
		return invalid
	}
	if !decision.NextCheck.NotBefore.After(now) {
		return linkedNextCheckError("the selected time passed during evidence refresh", decision.NextCheck.NotBefore, now, assessment.RevalidateAfter)
	}
	if !current || accepted.Assessment.Version != assessment.Version || accepted.ComparedFactsDigest != assessment.Facts.ComparedFactsDigest ||
		assessment.Facts.PolicySHA256 != lifecycle.PolicyDigest(agent.profile.AssessmentPolicy) || assessment.Facts.CheckID != ref.CheckID ||
		!now.Before(assessment.RevalidateAfter) || !now.Before(assessment.AssessedAt.Add(agent.lifecycleLimits.RevalidateAfter)) {
		return invalid
	}
	if decision.NextCheck.NotBefore.After(assessment.RevalidateAfter) {
		return linkedNextCheckError("the selected time exceeds the selected assessment's revalidate_after", decision.NextCheck.NotBefore, now, assessment.RevalidateAfter)
	}
	result.LinkedAssessment = &accepted.Assessment
	result.LinkedAssessmentCheckedAt = &lookup.CheckedAt
	result.Limitations = append(result.Limitations, "This link preserves the original case/execution. Automatic suppression and reconsideration are disabled; next_check is a recorded condition, not a scheduled task.")
	if decision.Status == "known_issue" {
		if decision.Remediation != nil {
			return errors.New("use existing_remediation or awaiting_deployment for a positive PR-state claim")
		}
		return nil
	}
	if decision.Remediation == nil {
		return invalid
	}
	if state.SchemaVersion != 1 || state.ProfileID != agent.profile.PublicationProfileID || state.ProfileSHA256 != agent.profile.ExpectedPublicationSHA256 || state.RepositoryID != agent.profile.RepositoryID ||
		state.ReferenceKind != "publication_receipt" || state.OriginPublicationID != decision.Remediation.PublicationID || state.OriginCaseID != decision.Remediation.OriginCaseID ||
		state.PullRequestNumber <= 0 || state.PullRequestURL == "" || !freshLinkedObservation(state.ObservedAt, now, agent.lifecycleLimits.RemediationMaxAge) ||
		state.PublishedCommitSHA == "" || state.HeadSHA != state.PublishedCommitSHA || state.HeadMatchesPublicationCommit == nil || !*state.HeadMatchesPublicationCommit {
		return invalid
	}
	matched := false
	for _, remediation := range assessment.Remediations {
		if remediation.PublicationID == state.OriginPublicationID && remediation.OriginCaseID == state.OriginCaseID && remediation.PublicationProfileID == state.ProfileID &&
			remediation.PublicationProfileSHA256 == state.OriginPublicationProfileSHA256 && remediation.PullRequestURL == state.PullRequestURL {
			matched = true
		}
	}
	if !matched {
		return invalid
	}
	switch decision.Status {
	case "existing_remediation":
		if state.State != "open" || state.Merged != nil && *state.Merged {
			return invalid
		}
	case "awaiting_deployment":
		var serving string
		var sourceMatches bool
		// An exact comparison to the still-serving base establishes this narrow
		// waiting claim. Mere inequality of Git SHAs cannot prove non-deployment.
		if json.Unmarshal(assessment.Facts.Comparison["/release/git_revision"], &serving) != nil || json.Unmarshal(assessment.Facts.Comparison["/release/source_matches_commit"], &sourceMatches) != nil ||
			!sourceMatches || state.State != "merged" || state.Merged == nil || !*state.Merged || state.MergeSHA == "" || serving == "" || state.BaseSHA != serving || state.MergeSHA == serving {
			return invalid
		}
	}
	result.LinkedRemediation = &state
	result.Limitations = append(result.Limitations, "GitHub state does not establish production resolution; current-case publication fields remain empty for this historical link.")
	return nil
}

func linkedNextCheckError(reason string, selected, now, deadline time.Time) error {
	return &responsecontract.ValidationError{
		Code: "LINKED_NEXT_CHECK_INVALID", Field: "next_check.not_before",
		Reason:   fmt.Sprintf("%s (selected %s)", reason, selected.UTC().Format(time.RFC3339Nano)),
		Expected: fmt.Sprintf("an RFC3339 time after %s and at or before %s, also within result_contract.maximum_next_check_delay_seconds; use the selected assessment's revalidate_after as an upper bound, and reassess if that window has expired", now.UTC().Format(time.RFC3339Nano), deadline.UTC().Format(time.RFC3339Nano)),
	}
}

func freshLinkedObservation(observed, now time.Time, maximumAge time.Duration) bool {
	return maximumAge > 0 && !observed.IsZero() && !observed.After(now.Add(time.Second)) && !observed.Before(now.Add(-maximumAge))
}
