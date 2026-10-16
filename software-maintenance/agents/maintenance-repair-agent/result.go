package main

import (
	"errors"
	"fmt"
	"math"
	"path"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/truvaagents/truva-g3-domain-examples/software-maintenance/internal/responsecontract"
	"github.com/truvaagents/truva-g3/orchestration"
)

var errRepairStepNotFound = errors.New("tool step not found")

func (agent *maintenanceRepairAgent) buildRepairResponse(profile repairProfile, request repairRequest, response *orchestration.OrchestratorResponse, execution *orchestration.ExecutionResult) (repairResponse, error) {
	if response == nil || execution == nil || response.RequestID == "" || len(response.RequestID) > 128 ||
		math.IsNaN(response.Confidence) || math.IsInf(response.Confidence, 0) || response.Confidence < 0 || response.Confidence > 1 {
		return repairResponse{}, responsecontract.Invalid("ORCHESTRATION_RESULT_INVALID", "orchestration_result", "A final response with execution identity and finite confidence between 0 and 1", errors.New("repair orchestration did not produce a valid final response"))
	}
	var decision repairDecision
	// Treat the declared decision fields as the synthesis contract. Additive
	// model metadata cannot affect a receipt and is ignored, while duplicate
	// fields, multiple JSON values, and invalid required values remain errors.
	if err := decodeRepairPartialBytes([]byte(response.Response), &decision); err != nil {
		return repairResponse{}, responsecontract.Invalid("DECISION_JSON_INVALID", "decision", "One JSON object with status, summary and findings", err)
	}
	if err := validateRepairDecision(profile, &decision); err != nil {
		return repairResponse{}, err
	}
	var service serviceProfileResponse
	contractReferences := []string{}
	serviceErr := latestRepairToolData(execution.Steps, "maintenance-evidence-tool", "get_service_profile", &service)
	if serviceErr == nil {
		var err error
		contractReferences, err = validateRepairServiceProfile(profile, service)
		if err != nil {
			return repairResponse{}, responsecontract.Invalid("TRUSTED_EVIDENCE_INVALID", "service_profile", "Service profile and contract references matching the repair profile", err)
		}
	} else if !errors.Is(serviceErr, errRepairStepNotFound) {
		return repairResponse{}, responsecontract.Invalid("TRUSTED_EVIDENCE_INVALID", "service_profile", "A successful service-profile response in the repair execution", serviceErr)
	}
	var workspace *workspaceReference
	var workspaceValue workspaceReference
	if err := latestRepairToolData(execution.Steps, "maintenance-workspace-tool", "create_workspace", &workspaceValue); err == nil {
		if err := validateWorkspaceReference(profile, request, workspaceValue, agent.now()); err != nil {
			return repairResponse{}, responsecontract.Invalid("TRUSTED_EVIDENCE_INVALID", "workspace", "An unexpired workspace receipt matching the authorized case, source and profile", err)
		}
		workspace = &workspaceValue
	} else if !errors.Is(err, errRepairStepNotFound) {
		return repairResponse{}, responsecontract.Invalid("TRUSTED_EVIDENCE_INVALID", "workspace", "A successful workspace response in the repair execution", err)
	}

	var candidate *candidateReference
	var candidateValue candidateReference
	if err := latestRepairToolData(execution.Steps, "maintenance-workspace-tool", "stage_candidate", &candidateValue); err == nil {
		if workspace == nil {
			return repairResponse{}, responsecontract.Invalid("TRUSTED_EVIDENCE_MISSING", "workspace", "A trusted workspace receipt for the staged candidate", errors.New("staged candidate has no trusted workspace receipt in the repair execution"))
		}
		if err := validateCandidateReference(profile, request, *workspace, candidateValue, agent.now()); err != nil {
			return repairResponse{}, responsecontract.Invalid("TRUSTED_EVIDENCE_INVALID", "candidate", "An unexpired candidate receipt matching the authorized workspace and case", err)
		}
		candidate = &candidateValue
	} else if !errors.Is(err, errRepairStepNotFound) {
		return repairResponse{}, responsecontract.Invalid("TRUSTED_EVIDENCE_INVALID", "candidate", "A successful candidate response in the repair execution", err)
	}

	var verification *verificationReference
	if candidate != nil {
		var verificationValue verificationReference
		if err := latestCandidateVerification(execution.Steps, profile, request, *candidate, agent.now(), &verificationValue); err == nil {
			verification = &verificationValue
		} else if !errors.Is(err, errRepairStepNotFound) {
			return repairResponse{}, responsecontract.Invalid("TRUSTED_EVIDENCE_INVALID", "verification", "A verification receipt matching the submitted candidate and profile", err)
		}
	}

	// Validate positive claims without replacing the model's decision. A
	// negative result may retain exploratory artifacts without recommending them.
	if decision.Status == "candidate_submitted" {
		if candidate == nil || verification == nil || workspace == nil {
			return repairResponse{}, responsecontract.Invalid("TRUSTED_EVIDENCE_MISSING", "candidate_submission", "Trusted workspace, candidate and verification receipts for candidate_submitted", errors.New("candidate submission lacks trusted candidate and verification receipts"))
		}
		if serviceErr != nil || !profile.RepairReady || !service.AssessmentReady || !workspace.VerificationReady || len(contractReferences) == 0 {
			return repairResponse{}, responsecontract.Invalid("REPAIR_PREREQUISITES_UNSATISFIED", "candidate_submission", "Approved repair, service contract and verification prerequisites", errors.New("candidate submission lacks approved repair, contract, or verification prerequisites"))
		}
		if verification.Status != "pending" && verification.Status != "running" && verification.Status != "succeeded" {
			return repairResponse{}, responsecontract.Invalid("VERIFICATION_STATUS_INVALID", "verification", "A pending, running or succeeded verification for the submitted candidate", errors.New("candidate verification failed"))
		}
		if verification.Status == "succeeded" && (!verification.ExpectedTestObserved || !verification.SuitePassed || !verification.PublicationEligible) {
			return repairResponse{}, responsecontract.Invalid("VERIFICATION_EVIDENCE_INCOMPLETE", "verification", "Succeeded verification with reproduction, passing suite and publication eligibility", errors.New("verification lacks reproduction, suite, or eligibility evidence"))
		}
	}
	findings := normalizeRepairStrings(decision.Findings, profile.Limits.MaximumFindings)
	status := decision.Status
	toolsUsed := normalizeRepairStrings(append([]string(nil), response.AgentsInvolved...), 32)
	result := repairResponse{
		ExecutionSuccess: execution.Success,
		SchemaVersion:    1, Status: status, RepairProfileSHA256: profile.profileSHA256,
		CaseID: request.CaseID, ObservationID: request.ObservationID, SnapshotID: request.SnapshotID, Workspace: workspace, Candidate: candidate, Verification: verification,
		ContractReferences: contractReferences, Findings: findings, Summary: decision.Summary,
		ExecutionID: response.RequestID, ToolsUsed: toolsUsed, Confidence: response.Confidence,
		Limitations: append([]string(nil), profile.Limitations...),
	}
	if workspace != nil {
		result.WorkspaceID = workspace.WorkspaceID
	}
	if candidate != nil {
		result.CandidateID = candidate.CandidateID
		result.TestArtifactID = candidate.TestArtifactID
	}
	if verification != nil {
		result.VerificationID = verification.VerificationID
	}
	return result, nil
}

func latestCandidateVerification(steps []orchestration.StepResult, profile repairProfile, request repairRequest, candidate candidateReference, now time.Time, destination *verificationReference) error {
	for index := len(steps) - 1; index >= 0; index-- {
		var value verificationReference
		err := latestRepairToolData(steps[index:index+1], "maintenance-workspace-tool", "run_verification", &value)
		if errors.Is(err, errRepairStepNotFound) {
			continue
		}
		if err != nil {
			return err
		}
		if value.CandidateID != candidate.CandidateID {
			continue
		}
		if err := validateVerificationReference(profile, request, candidate, value, now); err != nil {
			return err
		}
		*destination = value
		return nil
	}
	return errRepairStepNotFound
}

func latestRepairToolData(steps []orchestration.StepResult, agentName, capability string, destination any) error {
	index, found := orchestration.LastSuccessfulStep(steps, orchestration.StepResultFilter{AgentName: agentName, Capability: capability})
	if !found {
		return errRepairStepNotFound
	}
	data, err := orchestration.SuccessfulToolData(steps[index])
	if err != nil {
		return fmt.Errorf("decode %s response: %w", capability, err)
	}
	if err := decodeRepairPartialBytes(data, destination); err != nil {
		return fmt.Errorf("decode %s data: %w", capability, err)
	}
	return nil
}

func validateRepairDecision(profile repairProfile, decision *repairDecision) error {
	decision.Status = strings.TrimSpace(decision.Status)
	decision.Summary = strings.TrimSpace(decision.Summary)
	if decision.Status != "candidate_submitted" && decision.Status != "no_change" && decision.Status != "not_reproduced" && decision.Status != "inconclusive" {
		return responsecontract.Invalid("DECISION_STATUS_INVALID", "status", "candidate_submitted, no_change, not_reproduced or inconclusive", errors.New("repair decision status is invalid"))
	}
	if len(decision.Summary) > profile.Limits.summaryLimit() {
		return &responsecontract.FieldError{Field: "summary", Actual: len(decision.Summary), Limit: profile.Limits.summaryLimit()}
	}
	if !responsecontract.ValidSummaryText(decision.Summary) {
		return responsecontract.Invalid("SUMMARY_INVALID_TEXT", "summary", "Non-empty valid UTF-8 prose; line breaks and tabs are allowed, other control characters are rejected", errors.New("repair decision summary is invalid"))
	}
	if len(decision.Findings) > profile.Limits.MaximumFindings {
		return responsecontract.Invalid("FINDINGS_TOO_MANY", "findings", fmt.Sprintf("At most %d findings", profile.Limits.MaximumFindings), errors.New("repair decision has too many findings"))
	}
	for _, finding := range decision.Findings {
		if len(finding) > profile.Limits.findingLimit() {
			return &responsecontract.FieldError{Field: "findings", Actual: len(finding), Limit: profile.Limits.findingLimit()}
		}
		if strings.TrimSpace(finding) == "" || !validRepairText(finding) {
			return responsecontract.Invalid("FINDING_INVALID_TEXT", "findings", "Non-empty valid UTF-8 finding strings without control characters", errors.New("repair decision contains an invalid finding"))
		}
	}
	return nil
}

func validateRepairServiceProfile(profile repairProfile, service serviceProfileResponse) ([]string, error) {
	if service.ProfileID != profile.ServiceProfileID || service.ProfileVersion != profile.ExpectedServiceProfileVersion {
		return nil, errors.New("service profile identity does not match repair policy")
	}
	if len(service.ContractReferences) > 32 {
		return nil, errors.New("service profile has too many contract references")
	}
	result := make([]string, 0, len(service.ContractReferences))
	for _, reference := range service.ContractReferences {
		normalized := path.Clean(reference.Path)
		value := reference.Kind + ":" + reference.Revision + ":" + reference.Path
		if !repairNamePattern.MatchString(reference.Kind) || !gitSHAPattern.MatchString(reference.Revision) || reference.Path == "" ||
			normalized != reference.Path || normalized == "." || normalized == ".." || strings.HasPrefix(normalized, "../") || strings.HasPrefix(normalized, "/") ||
			len(value) > profile.Limits.MaximumReferenceBytes || !validRepairText(value) {
			return nil, errors.New("service profile contains an invalid contract reference")
		}
		result = append(result, value)
	}
	sort.Strings(result)
	return uniqueRepairStrings(result), nil
}

func validateWorkspaceReference(profile repairProfile, request repairRequest, workspace workspaceReference, now time.Time) error {
	if workspace.SchemaVersion != 1 || !workspaceIDPattern.MatchString(workspace.WorkspaceID) ||
		workspace.ProfileID != profile.WorkspaceProfileID || workspace.ProfileSHA256 != profile.ExpectedWorkspaceProfileSHA256 ||
		workspace.SourceProfileSHA256 != profile.ExpectedSourceProfileSHA256 || workspace.CaseID != request.CaseID ||
		workspace.RepositoryID != profile.RepositoryID || workspace.SnapshotID != request.SnapshotID ||
		!gitSHAPattern.MatchString(workspace.RevisionSHA) || !gitSHAPattern.MatchString(workspace.TreeSHA) ||
		!sha256Pattern.MatchString(workspace.ArchiveSHA256) || !snapshotIDPattern.MatchString(workspace.SnapshotID) ||
		workspace.CreatedAt.IsZero() || workspace.CreatedAt.After(now.Add(time.Minute)) || !workspace.ExpiresAt.After(now) {
		return errors.New("workspace reference is invalid or outside repair scope")
	}
	return nil
}

func validateCandidateReference(profile repairProfile, request repairRequest, workspace workspaceReference, candidate candidateReference, now time.Time) error {
	if candidate.SchemaVersion != 1 || !candidateIDPattern.MatchString(candidate.CandidateID) || !testArtifactIDPattern.MatchString(candidate.TestArtifactID) ||
		candidate.WorkspaceID != workspace.WorkspaceID || candidate.ProfileID != profile.WorkspaceProfileID ||
		candidate.ProfileSHA256 != profile.ExpectedWorkspaceProfileSHA256 || candidate.CaseID != request.CaseID ||
		candidate.RepositoryID != profile.RepositoryID || candidate.SnapshotID != request.SnapshotID || !gitSHAPattern.MatchString(candidate.RevisionSHA) ||
		!goTestPattern.MatchString(candidate.ExpectedTest) || candidate.ChangeCount < 1 || candidate.TestCount < 1 || candidate.CandidateBytes < 1 ||
		candidate.ProposedBy != repairAgentName || candidate.RevisionSHA != workspace.RevisionSHA || candidate.CreatedAt.Before(workspace.CreatedAt) ||
		candidate.CreatedAt.After(now.Add(time.Minute)) || !candidate.ExpiresAt.After(now) {
		return errors.New("staged candidate reference is invalid or outside repair scope")
	}
	return nil
}

func validateVerificationReference(profile repairProfile, request repairRequest, candidate candidateReference, verification verificationReference, now time.Time) error {
	if verification.SchemaVersion != 1 || !verificationIDPattern.MatchString(verification.VerificationID) || verification.CandidateID != candidate.CandidateID ||
		verification.ProfileID != profile.WorkspaceProfileID || verification.ProfileSHA256 != profile.ExpectedWorkspaceProfileSHA256 ||
		!jobIDPattern.MatchString(verification.ReproductionJobID) || len(verification.CandidateJobIDs) == 0 || len(verification.CandidateJobIDs) > 16 ||
		verification.CreatedAt.Before(candidate.CreatedAt) || verification.CreatedAt.After(now.Add(time.Minute)) || verification.UpdatedAt.Before(verification.CreatedAt) || verification.UpdatedAt.After(now.Add(time.Minute)) {
		return errors.New("verification reference is invalid or does not bind to the staged candidate")
	}
	seen := map[string]struct{}{verification.ReproductionJobID: {}}
	for _, jobID := range verification.CandidateJobIDs {
		if !jobIDPattern.MatchString(jobID) {
			return errors.New("verification contains an invalid job identity")
		}
		if _, exists := seen[jobID]; exists {
			return errors.New("verification job identities are not unique")
		}
		seen[jobID] = struct{}{}
	}
	if verification.Status != "pending" && verification.Status != "running" && verification.Status != "succeeded" && verification.Status != "failed" {
		return errors.New("verification status is invalid")
	}
	return nil
}

func normalizeRepairStrings(values []string, maximum int) []string {
	result := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value != "" && validRepairText(value) {
			result = append(result, value)
		}
	}
	sort.Strings(result)
	result = uniqueRepairStrings(result)
	if len(result) > maximum {
		result = result[:maximum]
	}
	return result
}

func uniqueRepairStrings(values []string) []string {
	if len(values) == 0 {
		return []string{}
	}
	result := values[:1]
	for _, value := range values[1:] {
		if value != result[len(result)-1] {
			result = append(result, value)
		}
	}
	return result
}

func validRepairText(value string) bool {
	if !utf8.ValidString(value) || strings.TrimSpace(value) != value {
		return false
	}
	for _, char := range value {
		if char < 0x20 || char == 0x7f {
			return false
		}
	}
	return true
}
