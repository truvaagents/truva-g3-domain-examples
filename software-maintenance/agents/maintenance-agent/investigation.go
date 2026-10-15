package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"sort"
	"strings"
	"time"

	"github.com/truvaagents/truva-g3-domain-examples/software-maintenance/internal/agentfailure"
	"github.com/truvaagents/truva-g3-domain-examples/software-maintenance/internal/responsecontract"
	"github.com/truvaagents/truva-g3/core"
	"github.com/truvaagents/truva-g3/orchestration"
	"github.com/truvaagents/truva-g3/telemetry"
	"go.opentelemetry.io/otel/attribute"
)

var errCoordinatorStepNotFound = errors.New("orchestration step not found")

func (agent *maintenanceAgent) handleInvestigationTask(ctx context.Context, task *core.Task, reporter core.ProgressReporter) error {
	if task == nil || task.Type != investigationTaskType {
		return errors.New("investigation task identity is invalid")
	}
	input, err := agent.validateInvestigationTaskInput(task.Input)
	if err != nil {
		return err
	}
	storedTask, err := agent.taskStore.Get(ctx, task.ID)
	if err != nil {
		return fmt.Errorf("read investigation task state: %w", err)
	}
	if storedTask.Status == core.TaskStatusCancelled {
		return nil
	}
	_ = reporter.Report(&core.TaskProgress{CurrentStep: 1, TotalSteps: 3, StepName: "claim_case", Percentage: 10})
	record, getErr := agent.cases.get(ctx, input.CaseID)
	execute := false
	switch {
	case errors.Is(getErr, errCaseNotFound):
		record, execute, err = agent.cases.prepareManualExecution(ctx, agent.profile, input, task.ID, agent.now())
	case getErr != nil:
		err = getErr
	case record.CurrentTaskID == task.ID:
		record, execute, err = agent.cases.claimExecution(ctx, input.CaseID, task.ID, agent.now())
	default:
		record, execute, err = agent.cases.prepareManualExecution(ctx, agent.profile, input, task.ID, agent.now())
	}
	if err != nil {
		return err
	}
	if !execute {
		if record.Result != nil {
			task.Result = record.Result
		} else {
			task.Result = admissionResult{CaseID: record.CaseID, TaskID: record.CurrentTaskID, Status: record.Status, Reused: true}
		}
		return nil
	}
	_ = reporter.Report(&core.TaskProgress{CurrentStep: 2, TotalSteps: 3, StepName: "autonomous_investigation", Percentage: 30})
	orch, err := agent.waitForOrchestrator(ctx)
	if err != nil {
		agent.recordExecutionFailure(input.CaseID, task.ID, "ORCHESTRATOR_UNAVAILABLE")
		return err
	}
	payload, _ := json.Marshal(map[string]interface{}{
		"case_id": input.CaseID, "coordinator_profile_id": agent.profile.CoordinatorProfileID, "service_profile_id": agent.profile.ServiceProfileID,
		"observation_id":    input.ObservationRefs[0],
		"repository_id":     agent.profile.RepositoryID,
		"repair_profile_id": agent.profile.RepairProfileID, "review_profile_id": agent.profile.ReviewProfileID,
		"publication_profile_id": agent.profile.PublicationProfileID,
		"result_contract":        map[string]int{"maximum_finding_bytes": agent.profile.Limits.FindingLimit(), "maximum_findings": agent.profile.Limits.MaximumFindings, "maximum_summary_bytes": agent.profile.Limits.SummaryLimit(), "maximum_next_check_delay_seconds": int(agent.lifecycleLimits.RevalidateAfter.Seconds())},
		"objective":              input.Objective, "observation_refs": input.ObservationRefs,
		"assessment_checks": agent.assessmentChecks(),
	})
	query := "Investigate this admitted software-maintenance case using the current discovered capabilities and bound skills. " +
		"Choose the evidence, delegation, and conclusion adaptively. <maintenance_case>" + string(payload) + "</maintenance_case>"
	metadata := map[string]interface{}{
		"case_id": input.CaseID, "coordinator_profile_id": agent.profile.CoordinatorProfileID,
		"service_profile_id": agent.profile.ServiceProfileID, "repository_id": agent.profile.RepositoryID,
		"entity_type": "software-maintenance-case", "entity_id": agent.profile.ServiceProfileID + ":" + input.CaseID,
		"observation_refs": append([]string(nil), input.ObservationRefs...),
	}
	response, execution, err := orch.ProcessRequestWithExecution(ctx, query, metadata)
	ctx = agentfailure.Context(ctx, response, execution)
	// Keep observed effects before any final-response gate can fail. This is an
	// application checkpoint after harness return, not crash recovery of a plan.
	if publications := agent.confirmedPublications(input, execution); len(publications) > 0 {
		started := time.Now()
		storeCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
		executionID := orchestration.GetRequestID(ctx)
		if response != nil && response.RequestID != "" {
			executionID = response.RequestID
		}
		saveErr := agent.cases.retainExecutionEffects(storeCtx, input.CaseID, task.ID, executionID, publications, agent.now())
		cancel()
		if saveErr != nil {
			return errors.Join(err, fmt.Errorf("retain publication effects: %w", saveErr))
		}
		telemetry.AddSpanEvent(ctx, "maintenance.case.effects_retained",
			attribute.String("request_id", executionID), attribute.String("maintenance.case_id", input.CaseID), attribute.Int("maintenance.effect_count", len(publications)))
		if agent.Logger != nil {
			agent.Logger.InfoWithContext(ctx, "Publication effects retained independently of the case outcome", map[string]interface{}{
				"operation": "retain_case_effects", "status": "success", "duration_ms": time.Since(started).Milliseconds(),
				"request_id": executionID, "case_id": input.CaseID, "effect_count": len(publications),
			})
		}
	}
	if err != nil {
		runErr := fmt.Errorf("maintenance orchestration failed: %w", core.RedactSensitiveError(err))
		evidence := summarizeFailedExecution(execution)
		code := "ORCHESTRATION_FAILED"
		boundary := agentfailure.GuidanceBoundary(ctx, err)
		if boundary != nil {
			code = boundary.Code
			evidence.summary = agentfailure.GuidanceBudgetMessage + ". Reconsider when: " + boundary.ReconsiderWhen + ". " + evidence.summary
		}
		storeCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
		defer cancel()
		if saveErr := agent.cases.markExecutionFailedWithEvidence(storeCtx, input.CaseID, task.ID,
			code, evidence.summary, evidence.executionID, agent.now(), boundary); saveErr != nil {
			return errors.Join(runErr, fmt.Errorf("record failed maintenance execution: %w", core.RedactSensitiveError(saveErr)))
		}
		return runErr
	}
	_ = reporter.Report(&core.TaskProgress{CurrentStep: 3, TotalSteps: 3, StepName: "validate_and_record", Percentage: 85})
	result, err := agent.buildInvestigationResult(ctx, input, response, execution)
	if err != nil {
		evidence := agent.resultValidationEvidence(ctx, input, execution, err)
		storeCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
		defer cancel()
		if saveErr := agent.cases.markResultValidationFailed(storeCtx, input.CaseID, task.ID, evidence, agent.now()); saveErr != nil {
			return errors.Join(err, saveErr)
		}
		return err
	}
	result.FailedOperations = agentfailure.Project(ctx, execution)
	if err := agent.cases.complete(ctx, input.CaseID, task.ID, result, agent.now()); err != nil {
		return fmt.Errorf("record maintenance case disposition: %w", err)
	}
	task.Result = result
	agent.recordCompletedAssessments(ctx, input, result)
	return nil
}

func (agent *maintenanceAgent) recordExecutionFailure(caseID, taskID, code string) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_ = agent.cases.markExecutionFailed(ctx, caseID, taskID, code, agent.now())
}

// failedExecutionEvidence contains only bounded, admitted execution facts. It
// deliberately does not interpret the maintenance outcome or tool response.
type failedExecutionEvidence struct {
	executionID string
	summary     string
}

func summarizeFailedExecution(execution *orchestration.ExecutionResult) failedExecutionEvidence {
	if execution == nil {
		return failedExecutionEvidence{summary: "Orchestration failed; execution evidence was unavailable."}
	}
	reason := "execution_failed"
	if execution.Metadata != nil {
		if value, ok := execution.Metadata["termination_reason"].(string); ok && value == "plan_validation_exhausted" {
			reason = value
		}
	}
	var evidence failedExecutionEvidence
	if execution.Metadata != nil {
		if value, ok := execution.Metadata[orchestration.MetadataKeyExecutionRequestID].(string); ok && len(value) <= 128 && validCoordinatorText(value) {
			evidence.executionID = value
		}
	}
	completed, failed, skipped := 0, 0, 0
	var observed []string
	for _, step := range execution.Steps {
		status := "failed"
		switch {
		case step.Skipped:
			status = "skipped"
			skipped++
		case step.Success:
			status = "completed"
			completed++
		default:
			failed++
		}
		if len(observed) < 6 && len(step.StepID) <= 64 && validCoordinatorText(step.StepID) {
			observed = append(observed, step.StepID+"="+status)
		}
	}
	evidence.summary = fmt.Sprintf("Orchestration stopped (%s); admitted steps: %d completed, %d failed, %d skipped.", reason, completed, failed, skipped)
	if len(observed) > 0 {
		evidence.summary += " Steps: " + strings.Join(observed, ", ") + "."
	}
	if len(execution.Steps) > len(observed) {
		evidence.summary += fmt.Sprintf(" %d step details omitted.", len(execution.Steps)-len(observed))
	}
	return evidence
}

func (agent *maintenanceAgent) validateInvestigationTaskInput(values map[string]interface{}) (investigationInput, error) {
	data, err := json.Marshal(values)
	if err != nil {
		return investigationInput{}, errors.New("investigation task input is invalid")
	}
	var input investigationInput
	if err := decodeCoordinatorBytes(data, &input); err != nil {
		return investigationInput{}, errors.New("investigation task input is invalid")
	}
	return agent.validateInvestigationInput(input)
}

func (agent *maintenanceAgent) validateInvestigationInput(input investigationInput) (investigationInput, error) {
	input.Objective = strings.TrimSpace(input.Objective)
	if input.ProfileID != agent.profile.CoordinatorProfileID || !coordinatorCaseIDPattern.MatchString(input.CaseID) ||
		input.Objective == "" || len(input.Objective) > agent.profile.Limits.MaximumObjectiveBytes || !validCoordinatorText(input.Objective) {
		return investigationInput{}, errors.New("investigation task scope is invalid")
	}
	var err error
	input.ObservationRefs, err = normalizeObservationRefs(input.ObservationRefs, agent.profile.Limits.MaximumObservationRefs)
	if err != nil {
		return investigationInput{}, err
	}
	return input, nil
}

func (agent *maintenanceAgent) buildInvestigationResult(ctx context.Context, input investigationInput, response *orchestration.OrchestratorResponse, execution *orchestration.ExecutionResult) (investigationResult, error) {
	if response == nil || execution == nil || response.RequestID == "" || len(response.RequestID) > 128 ||
		math.IsNaN(response.Confidence) || math.IsInf(response.Confidence, 0) || response.Confidence < 0 || response.Confidence > 1 {
		return investigationResult{}, errors.New("maintenance orchestration did not produce a valid final response")
	}
	var decision investigationDecision
	// Synthesis may include additive explanatory metadata. The coordinator's
	// receipt boundary is the required decision fields plus the deterministic
	// validations below, so tolerate unknown fields while continuing to reject
	// duplicate keys, multiple JSON values, and missing or invalid required values.
	if err := decodeCoordinatorPartialBytes([]byte(response.Response), &decision); err != nil {
		return investigationResult{}, errors.New("maintenance synthesis did not return the required JSON decision")
	}
	if err := agent.validateInvestigationDecision(&decision); err != nil {
		return investigationResult{}, err
	}
	result := investigationResult{
		ExecutionSuccess: execution.Success,
		// Keep the validated model decision separate from receipt-derived status
		// and policy findings below; never mutate its findings through an alias.
		ModelDecision: investigationDecision{Status: decision.Status, Summary: decision.Summary, Findings: append([]string{}, decision.Findings...), Assessment: decision.Assessment, Remediation: decision.Remediation, NextCheck: decision.NextCheck},
		SchemaVersion: 1, CoordinatorProfileSHA256: agent.profile.ProfileSHA256,
		CaseID: input.CaseID, ObservationRefs: append([]string(nil), input.ObservationRefs...),
		ExecutionID: response.RequestID, ToolsUsed: normalizeCoordinatorStrings(response.AgentsInvolved, 32), Confidence: response.Confidence,
		Limitations: append([]string(nil), agent.profile.Limitations...),
	}
	var repair repairStepResponse
	repairErr := latestCoordinatorStepData(execution.Steps, "maintenance-repair-agent", "propose_maintenance_change", &repair)
	if repairErr == nil && repair.CaseID == input.CaseID && repair.RepairProfileSHA256 == agent.profile.ExpectedRepairProfileSHA256 && repair.ExecutionID != "" {
		result.RepairExecutionID = repair.ExecutionID
	}
	var review reviewStepResponse
	reviewErr := latestCoordinatorStepData(execution.Steps, "maintenance-review-agent", "review_maintenance_change", &review)
	if reviewErr == nil && review.Review.CaseID == input.CaseID && review.Review.ReviewProfileSHA256 == agent.profile.ExpectedReviewProfileSHA256 && review.Review.ReviewID != "" {
		result.ReviewID = review.Review.ReviewID
	}
	reviewSupportsCandidate := reviewErr == nil && agent.reviewSupportsCandidate(input, review, execution)
	publication := agent.confirmedPublication(input, execution)
	publicationSucceeded := publication != nil
	result.Status = decision.Status
	result.NextCheck = decision.NextCheck
	result.Findings = normalizeCoordinatorStrings(decision.Findings, agent.profile.Limits.MaximumFindings)
	result.Summary = decision.Summary
	if publicationSucceeded {
		if isLinkedDisposition(decision.Status) {
			return investigationResult{}, errors.New("a linked-only conclusion conflicts with a new publication in this execution")
		}
		result.Status = "draft_pr_opened"
		result.PublicationID = publication.PublicationID
		result.PullRequestURL = publication.PullRequestURL
	}
	if isLinkedDisposition(result.Status) {
		if err := agent.validateLinkedDecision(ctx, input, decision, execution, &result); err != nil {
			return investigationResult{}, err
		}
	}
	if !isLinkedDisposition(decision.Status) && (decision.Assessment != nil || decision.Remediation != nil) {
		return investigationResult{}, errors.New("assessment/remediation decision references belong to a validated linked disposition")
	}
	if result.Status == "action_proposed" && !reviewSupportsCandidate && (repairErr != nil || repair.CaseID != input.CaseID ||
		repair.RepairProfileSHA256 != agent.profile.ExpectedRepairProfileSHA256 || repair.ExecutionID == "" ||
		repair.Status != "candidate_submitted" || repair.Candidate == nil || repair.Candidate.CandidateID == "") {
		result.Status = "inconclusive"
		result.Findings = append(result.Findings, "the proposed-action disposition lacked a trusted submission or current independent approval")
	}
	if decision.Status == "publication_requested" && !publicationSucceeded {
		result.Status = "inconclusive"
		result.Findings = append(result.Findings, "draft publication was requested but no successful draft publication receipt was returned")
	}
	if !agent.profile.InvestigationReady && !publicationSucceeded && result.Status != "blocked" && result.Status != "inconclusive" {
		result.Status = "inconclusive"
		result.Findings = append(result.Findings, "the coordinator profile is not investigation-ready")
	}
	result.Findings = normalizeCoordinatorStrings(result.Findings, agent.profile.Limits.MaximumFindings)
	if result.Status == "inconclusive" && len(result.Findings) == 0 {
		result.Findings = []string{"the execution did not establish a currently supported maintenance disposition"}
	}
	return result, nil
}

// A current independently approved candidate may support an action proposal even
// when its earlier repair delegation failed. This grants no publication authority.
func (agent *maintenanceAgent) reviewSupportsCandidate(input investigationInput, review reviewStepResponse, execution *orchestration.ExecutionResult) bool {
	r := review.Review
	now := agent.now()
	if r.Status != "approved" || r.CaseID != input.CaseID || r.ReviewProfileSHA256 != agent.profile.ExpectedReviewProfileSHA256 ||
		r.ReviewID == "" || r.CandidateID == "" || r.VerificationID == "" || r.CreatedAt.IsZero() || r.CreatedAt.After(now.Add(time.Minute)) || !r.ExpiresAt.After(now) {
		return false
	}
	for i := len(execution.Steps) - 1; i >= 0; i-- {
		step := execution.Steps[i]
		if !step.Success || step.Skipped || step.AgentName != "maintenance-review-agent" || step.Capability != "review_maintenance_change" {
			continue
		}
		return step.Parameters["case_id"] == input.CaseID && step.Parameters["candidate_id"] == r.CandidateID && step.Parameters["verification_id"] == r.VerificationID
	}
	return false
}

func (agent *maintenanceAgent) validateInvestigationDecision(decision *investigationDecision) error {
	decision.Status = strings.TrimSpace(decision.Status)
	decision.Summary = strings.TrimSpace(decision.Summary)
	switch decision.Status {
	case "benign", "inconclusive", "blocked", "action_proposed", "publication_requested", "known_issue", "existing_remediation", "awaiting_deployment":
	default:
		return errors.New("maintenance decision status is invalid")
	}
	if len(decision.Summary) > agent.profile.Limits.SummaryLimit() {
		return &responsecontract.FieldError{Field: "summary", Actual: len(decision.Summary), Limit: agent.profile.Limits.SummaryLimit()}
	}
	if !responsecontract.ValidSummaryText(decision.Summary) {
		return responsecontract.Invalid("SUMMARY_INVALID_TEXT", "summary", "Non-empty valid UTF-8 prose; line breaks and tabs are allowed, other control characters are rejected", errors.New("maintenance decision summary is invalid"))
	}
	if len(decision.Findings) > agent.profile.Limits.MaximumFindings {
		return errors.New("maintenance decision content is invalid")
	}
	for _, finding := range decision.Findings {
		if len(finding) > agent.profile.Limits.FindingLimit() {
			return &responsecontract.FieldError{Field: "findings", Actual: len(finding), Limit: agent.profile.Limits.FindingLimit()}
		}
		if strings.TrimSpace(finding) == "" || !validCoordinatorText(finding) {
			return errors.New("maintenance decision contains an invalid finding")
		}
	}
	if decision.NextCheck != nil && (strings.TrimSpace(decision.NextCheck.Condition) == "" || len(decision.NextCheck.Condition) > agent.profile.Limits.FindingLimit() || !validCoordinatorText(decision.NextCheck.Condition) || decision.NextCheck.NotBefore.IsZero()) {
		return errors.New("maintenance next_check requires a bounded condition and an explicit deadline")
	}
	return nil
}

func (agent *maintenanceAgent) confirmedPublication(input investigationInput, execution *orchestration.ExecutionResult) *publicationStepResponse {
	if execution == nil {
		return nil
	}
	var publication publicationStepResponse
	err := latestCoordinatorStepData(execution.Steps, "maintenance-publication-tool", "publish_draft_pr", &publication)
	if err != nil || publication.CaseID != input.CaseID ||
		publication.ProfileID != agent.profile.PublicationProfileID || publication.ProfileSHA256 != agent.profile.ExpectedPublicationSHA256 ||
		publication.Status != "succeeded" || !publication.Draft || publication.PublicationID == "" || publication.PullRequestURL == "" {
		return nil
	}
	return &publication
}

func (agent *maintenanceAgent) resultValidationEvidence(ctx context.Context, input investigationInput, execution *orchestration.ExecutionResult, err error) resultValidationEvidence {
	// Only locally generated bounded validation facts, not model prose, escape.
	message := responsecontract.FailureMessage("maintenance result validation failed", err)
	executionID := orchestration.GetRequestID(ctx)
	if len(executionID) > 128 || !validCoordinatorText(executionID) {
		executionID = ""
	}
	return resultValidationEvidence{
		ExecutionID: executionID, Error: message,
		Publication: agent.confirmedPublication(input, execution),
	}
}

func latestCoordinatorStepData(steps []orchestration.StepResult, agentName, capability string, destination any) error {
	for index := len(steps) - 1; index >= 0; index-- {
		step := steps[index]
		if !step.Success || step.Skipped || step.AgentName != agentName || step.Capability != capability {
			continue
		}
		var envelope struct {
			Success bool            `json:"success"`
			Data    json.RawMessage `json:"data"`
			Error   *core.ToolError `json:"error"`
		}
		if err := decodeCoordinatorBytes([]byte(step.Response), &envelope); err != nil {
			return fmt.Errorf("decode %s response: %w", capability, err)
		}
		if !envelope.Success || len(envelope.Data) == 0 {
			return fmt.Errorf("%s returned no successful data", capability)
		}
		if err := decodeCoordinatorPartialBytes(envelope.Data, destination); err != nil {
			return fmt.Errorf("decode %s data: %w", capability, err)
		}
		return nil
	}
	return errCoordinatorStepNotFound
}

func normalizeCoordinatorStrings(values []string, maximum int) []string {
	result := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value != "" && validCoordinatorText(value) {
			result = append(result, value)
		}
	}
	sort.Strings(result)
	if len(result) == 0 {
		return []string{}
	}
	unique := result[:1]
	for _, value := range result[1:] {
		if value != unique[len(unique)-1] {
			unique = append(unique, value)
		}
	}
	if len(unique) > maximum {
		unique = unique[:maximum]
	}
	return unique
}
