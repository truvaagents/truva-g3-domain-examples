package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
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

var errReviewStepNotFound = errors.New("no successful exact capability result was recorded")

func (agent *maintenanceReviewAgent) buildReceipt(profile reviewProfile, request reviewRequest, response *orchestration.OrchestratorResponse, execution *orchestration.ExecutionResult) (reviewReceipt, reviewDecision, error) {
	if response == nil || execution == nil || response.RequestID == "" || len(response.RequestID) > 128 ||
		math.IsNaN(response.Confidence) || math.IsInf(response.Confidence, 0) || response.Confidence < 0 || response.Confidence > 1 {
		return reviewReceipt{}, reviewDecision{}, responsecontract.Invalid("ORCHESTRATION_RESULT_INVALID", "orchestration_result", "A final response with execution identity and finite confidence between 0 and 1", errors.New("review orchestration did not produce a valid final response"))
	}
	var decision reviewDecision
	err := decodeReviewPartialBytes([]byte(response.Response), &decision)
	if err != nil {
		return reviewReceipt{}, reviewDecision{}, responsecontract.Invalid("DECISION_JSON_INVALID", "decision", "One JSON object with status, summary and blocking_findings", err)
	}
	if err := validateDecision(profile, &decision); err != nil {
		return reviewReceipt{}, reviewDecision{}, err
	}
	// Negative decisions grant no authority and need no approval receipt.
	if decision.Status != "approved" {
		return reviewReceipt{}, decision, nil
	}
	var serviceProfile serviceProfileResponse
	if err := exactToolStep(execution.Steps, "maintenance-evidence-tool", "get_service_profile", map[string]string{
		"profile_id": profile.ServiceProfileID,
	}, &serviceProfile); err != nil {
		return reviewReceipt{}, reviewDecision{}, reviewEvidenceFailure("service_profile", "A successful exact service-profile result matching the authorized profile", err)
	}
	var bundle reviewBundle
	if err := exactToolStep(execution.Steps, "maintenance-workspace-tool", "get_review_bundle", map[string]string{
		"case_id": request.CaseID, "candidate_id": request.CandidateID, "verification_id": request.VerificationID,
	}, &bundle); err != nil {
		return reviewReceipt{}, reviewDecision{}, reviewEvidenceFailure("review_bundle", "A successful exact review bundle for the requested case, candidate and verification", err)
	}
	now := agent.now()
	if err := validateReviewBundle(profile, request, bundle, now); err != nil {
		return reviewReceipt{}, reviewDecision{}, responsecontract.Invalid("TRUSTED_EVIDENCE_INVALID", "review_bundle", "Unexpired, scope-matching workspace, candidate, verification and job receipts", err)
	}
	contractReferences, err := canonicalContractReferences(profile, serviceProfile)
	if err != nil {
		return reviewReceipt{}, reviewDecision{}, responsecontract.Invalid("TRUSTED_EVIDENCE_INVALID", "contract_references", "Contract references matching the authorized service profile", err)
	}
	evidenceReferences := reviewEvidenceReferences(bundle)
	hardBlockers := approvalBlockers(profile, serviceProfile, bundle, now)
	if len(hardBlockers) > 0 {
		reason := strings.Join(hardBlockers, "; ")
		return reviewReceipt{}, reviewDecision{}, &responsecontract.ValidationError{
			Code: "APPROVAL_PREREQUISITES_UNSATISFIED", Field: "approval",
			Expected: "Approval-enabled review, assessment-ready service with pinned contracts, and complete publication-eligible verification with valid job receipts",
			Reason:   reason,
			Cause:    fmt.Errorf("approval evidence does not satisfy policy: %s", reason),
		}
	}
	expiresAt := now.Add(profile.receiptTTL())
	for _, expiry := range append([]time.Time{bundle.Workspace.ExpiresAt, bundle.Candidate.ExpiresAt}, jobExpiries(bundle.Jobs)...) {
		if expiry.Before(expiresAt) {
			expiresAt = expiry
		}
	}
	if !expiresAt.After(now) {
		return reviewReceipt{}, reviewDecision{}, responsecontract.Invalid("TRUSTED_EVIDENCE_EXPIRED", "review_bundle", "Unexpired review prerequisites at receipt creation", errors.New("review prerequisites have expired"))
	}
	receipt := reviewReceipt{
		SchemaVersion: 1, CaseID: request.CaseID, CandidateID: request.CandidateID,
		TestArtifactID: bundle.Candidate.TestArtifactID, VerificationID: request.VerificationID,
		WorkspaceProfileSHA256: bundle.Workspace.ProfileSHA256, ReviewProfileSHA256: profile.profileSHA256,
		Status: decision.Status, ContractReferences: contractReferences, EvidenceReferences: evidenceReferences,
		BlockingFindings: []string{}, ReviewerExecutionID: response.RequestID, CreatedAt: now, ExpiresAt: expiresAt,
	}
	receipt.ReviewID = reviewIdentity(receipt)
	return receipt, decision, nil
}

func reviewEvidenceFailure(field, expected string, cause error) error {
	code := "TRUSTED_EVIDENCE_INVALID"
	if errors.Is(cause, errReviewStepNotFound) {
		code = "TRUSTED_EVIDENCE_MISSING"
	}
	return responsecontract.Invalid(code, field, expected, cause)
}

func exactToolStep(steps []orchestration.StepResult, agentName, capability string, parameters map[string]string, destination any) error {
	filter := orchestration.StepResultFilter{AgentName: agentName, Capability: capability, Parameters: make(map[string]any, len(parameters))}
	for name, value := range parameters {
		filter.Parameters[name] = value
	}
	index, found := orchestration.LastSuccessfulStep(steps, filter)
	if !found {
		return errReviewStepNotFound
	}
	data, err := orchestration.SuccessfulToolData(steps[index])
	if err != nil {
		return fmt.Errorf("latest exact capability result has an invalid envelope: %w", err)
	}
	if err := decodeReviewPartialBytes(data, destination); err != nil {
		return fmt.Errorf("latest exact capability result is invalid: %w", err)
	}
	return nil
}

func validateDecision(profile reviewProfile, decision *reviewDecision) error {
	decision.Status = strings.TrimSpace(decision.Status)
	decision.Summary = strings.TrimSpace(decision.Summary)
	if decision.Status != "approved" && decision.Status != "rejected" && decision.Status != "inconclusive" {
		return responsecontract.Invalid("DECISION_STATUS_INVALID", "status", "approved, rejected or inconclusive", fmt.Errorf("review decision status %q is invalid", decision.Status))
	}
	if len(decision.Summary) > profile.Limits.summaryLimit() {
		return &responsecontract.FieldError{Field: "summary", Actual: len(decision.Summary), Limit: profile.Limits.summaryLimit()}
	}
	if !responsecontract.ValidSummaryText(decision.Summary) {
		return responsecontract.Invalid("SUMMARY_INVALID_TEXT", "summary", "Non-empty valid UTF-8 prose; line breaks and tabs are allowed, other control characters are rejected", errors.New("review decision summary is invalid"))
	}
	if len(decision.BlockingFindings) > profile.Limits.MaximumFindings {
		return responsecontract.Invalid("FINDINGS_TOO_MANY", "blocking_findings", fmt.Sprintf("At most %d blocking findings", profile.Limits.MaximumFindings), errors.New("review decision has too many findings"))
	}
	for _, finding := range decision.BlockingFindings {
		if len(finding) > profile.Limits.findingLimit() {
			return &responsecontract.FieldError{Field: "blocking_findings", Actual: len(finding), Limit: profile.Limits.findingLimit()}
		}
		if strings.TrimSpace(finding) == "" || !validReviewText(finding) {
			return responsecontract.Invalid("FINDING_INVALID_TEXT", "blocking_findings", "Non-empty valid UTF-8 finding strings without control characters", fmt.Errorf("review decision status %q contains an invalid finding of %d bytes", decision.Status, len(finding)))
		}
	}
	if decision.Status == "approved" && len(decision.BlockingFindings) != 0 {
		return responsecontract.Invalid("APPROVAL_HAS_BLOCKING_FINDINGS", "blocking_findings", "An empty blocking_findings array for approved reviews", errors.New("an approved review cannot contain blocking findings"))
	}
	return nil
}

func validateReviewBundle(profile reviewProfile, request reviewRequest, bundle reviewBundle, now time.Time) error {
	workspace := bundle.Workspace
	candidate := bundle.Candidate
	verification := bundle.Verification
	if workspace.SchemaVersion != 1 || !workspaceIDPattern.MatchString(workspace.WorkspaceID) ||
		workspace.ProfileID != profile.WorkspaceProfileID || workspace.ProfileSHA256 != profile.ExpectedWorkspaceProfileSHA256 ||
		workspace.SourceProfileSHA256 != profile.ExpectedSourceProfileSHA256 || workspace.CaseID != request.CaseID ||
		workspace.RepositoryID != profile.RepositoryID || !snapshotIDPattern.MatchString(workspace.SnapshotID) ||
		!gitSHAPattern.MatchString(workspace.RevisionSHA) || !gitSHAPattern.MatchString(workspace.TreeSHA) ||
		!sha256Pattern.MatchString(workspace.ArchiveSHA256) ||
		workspace.CreatedAt.IsZero() || !workspace.ExpiresAt.After(now) {
		return errors.New("workspace review receipt is invalid")
	}
	if candidate.SchemaVersion != 1 || candidate.CandidateID != request.CandidateID || candidate.WorkspaceID != workspace.WorkspaceID ||
		candidate.ProfileID != workspace.ProfileID || candidate.ProfileSHA256 != workspace.ProfileSHA256 || candidate.CaseID != request.CaseID ||
		candidate.RepositoryID != workspace.RepositoryID || candidate.SnapshotID != workspace.SnapshotID || candidate.RevisionSHA != workspace.RevisionSHA ||
		!testArtifactIDPattern.MatchString(candidate.TestArtifactID) || !goTestPattern.MatchString(candidate.ExpectedTest) ||
		candidate.ChangeCount < 1 || candidate.TestCount < 1 || len(candidate.Files) != candidate.ChangeCount+candidate.TestCount ||
		len(candidate.Files) > profile.Limits.MaximumCandidateFiles || candidate.CandidateBytes < 0 || candidate.CandidateBytes > profile.Limits.MaximumCandidateBytes ||
		candidate.CreatedAt.Before(workspace.CreatedAt) || !candidate.ExpiresAt.After(now) {
		return errors.New("candidate review receipt is invalid")
	}
	_, total, err := canonicalReviewFiles(candidate.Files, candidate.ChangeCount, profile.Limits.MaximumCandidateBytes)
	if err != nil || total != candidate.CandidateBytes {
		return errors.New("candidate content identity is invalid")
	}
	// The authenticated owner validates its opaque receipt identities. Here
	// validate scope, content bounds, and relationships, not its hashing scheme.
	if verification.SchemaVersion != 1 || verification.VerificationID != request.VerificationID ||
		!verificationIDPattern.MatchString(verification.VerificationID) || verification.CandidateID != candidate.CandidateID ||
		verification.ProfileID != workspace.ProfileID || verification.ProfileSHA256 != workspace.ProfileSHA256 ||
		verification.CreatedAt.Before(candidate.CreatedAt) || verification.UpdatedAt.Before(verification.CreatedAt) || verification.UpdatedAt.After(now.Add(time.Minute)) {
		return errors.New("verification review receipt is invalid")
	}
	return validateReviewJobs(bundle, now)
}

func canonicalReviewFiles(files []workspaceFile, changeCount int, maximumBytes int64) ([]workspaceFile, int64, error) {
	if changeCount < 1 || changeCount >= len(files) {
		return nil, 0, errors.New("invalid candidate partition")
	}
	result := append([]workspaceFile(nil), files...)
	seen := make(map[string]struct{}, len(result))
	var total int64
	for index, file := range result {
		normalized, err := normalizeReviewPath(file.Path)
		isTest := index >= changeCount
		if err != nil || normalized != file.Path || isTest != strings.HasSuffix(file.Path, "_test.go") || isTest && file.Delete ||
			file.BaseSHA256 != "absent" && !sha256Pattern.MatchString(file.BaseSHA256) {
			return nil, 0, errors.New("candidate path or operation is invalid")
		}
		if _, exists := seen[file.Path]; exists {
			return nil, 0, errors.New("candidate paths are not unique")
		}
		seen[file.Path] = struct{}{}
		if file.Delete {
			if file.ContentBase64 != "" || file.Mode != "" || file.BaseSHA256 == "absent" {
				return nil, 0, errors.New("candidate deletion is invalid")
			}
			continue
		}
		if file.Mode != "100644" && file.Mode != "100755" {
			return nil, 0, errors.New("candidate file mode is invalid")
		}
		content, err := base64.StdEncoding.DecodeString(file.ContentBase64)
		if err != nil || base64.StdEncoding.EncodeToString(content) != file.ContentBase64 || bytes.IndexByte(content, 0) >= 0 {
			return nil, 0, errors.New("candidate content is invalid")
		}
		total += int64(len(content))
		if total > maximumBytes {
			return nil, 0, errors.New("candidate bytes exceed review policy")
		}
	}
	if !sort.SliceIsSorted(result[:changeCount], func(i, j int) bool { return result[i].Path < result[j].Path }) ||
		!sort.SliceIsSorted(result[changeCount:], func(i, j int) bool { return result[changeCount+i].Path < result[changeCount+j].Path }) {
		return nil, 0, errors.New("candidate files are not canonical")
	}
	return result, total, nil
}

func normalizeReviewPath(value string) (string, error) {
	if value == "" || value != strings.TrimSpace(value) || strings.Contains(value, "\\") || strings.HasPrefix(value, "/") {
		return "", errors.New("invalid path")
	}
	normalized := path.Clean(value)
	if normalized == "." || normalized == ".." || strings.HasPrefix(normalized, "../") || normalized != value {
		return "", errors.New("invalid path")
	}
	return normalized, nil
}

func validateReviewJobs(bundle reviewBundle, now time.Time) error {
	expected := append([]string{bundle.Verification.ReproductionJobID}, bundle.Verification.CandidateJobIDs...)
	if !jobIDPattern.MatchString(bundle.Verification.ReproductionJobID) || len(bundle.Verification.CandidateJobIDs) == 0 || len(bundle.Jobs) != len(expected) {
		return errors.New("verification job set is invalid")
	}
	seen := make(map[string]struct{}, len(expected))
	for index, job := range bundle.Jobs {
		if job.JobID != expected[index] || !jobIDPattern.MatchString(job.JobID) {
			return errors.New("verification job identity is invalid")
		}
		if _, exists := seen[job.JobID]; exists {
			return errors.New("verification jobs are not unique")
		}
		seen[job.JobID] = struct{}{}
		target := "candidate"
		if index == 0 {
			target = "reproduction"
		}
		if job.SchemaVersion != 1 || job.WorkspaceID != bundle.Workspace.WorkspaceID ||
			job.CandidateID != bundle.Candidate.CandidateID || job.VerificationID != bundle.Verification.VerificationID || job.ProfileID != bundle.Workspace.ProfileID ||
			job.ProfileSHA256 != bundle.Workspace.ProfileSHA256 || job.CaseID != bundle.Workspace.CaseID || job.Target != target ||
			!reviewNamePattern.MatchString(job.CommandID) || !sha256Pattern.MatchString(job.CommandSHA256) || !job.ExpiresAt.After(now) {
			return errors.New("verification job receipt is invalid")
		}
		if job.Status != "pending" && job.Status != "running" && job.Status != "succeeded" && job.Status != "failed" && job.Status != "cancelled" && job.Status != "timed_out" {
			return errors.New("verification job status is invalid")
		}
		if job.ArtifactID != "" || job.ArtifactSHA256 != "" || job.ArtifactBytes != 0 {
			if !artifactIDPattern.MatchString(job.ArtifactID) || !sha256Pattern.MatchString(job.ArtifactSHA256) || job.ArtifactBytes < 1 {
				return errors.New("verification job artifact identity is invalid")
			}
		}
	}
	return nil
}

func canonicalContractReferences(profile reviewProfile, service serviceProfileResponse) ([]string, error) {
	if service.ProfileID != profile.ServiceProfileID || service.ProfileVersion != profile.ExpectedServiceProfileVersion {
		return nil, errors.New("service profile identity does not match review policy")
	}
	result := make([]string, 0, len(service.ContractReferences))
	for _, reference := range service.ContractReferences {
		value := reference.Kind + ":" + reference.Revision + ":" + reference.Path
		normalizedPath, pathErr := normalizeReviewPath(reference.Path)
		if !reviewNamePattern.MatchString(reference.Kind) || !gitSHAPattern.MatchString(reference.Revision) || pathErr != nil || normalizedPath != reference.Path || len(value) > profile.Limits.MaximumReferenceBytes || !validReviewText(value) {
			return nil, errors.New("service profile contains an invalid contract reference")
		}
		result = append(result, value)
	}
	sort.Strings(result)
	return uniqueStrings(result), nil
}

func reviewEvidenceReferences(bundle reviewBundle) []string {
	values := []string{
		"source:" + bundle.Workspace.SnapshotID,
		"workspace:" + bundle.Workspace.WorkspaceID,
		"candidate:" + bundle.Candidate.CandidateID,
		"test:" + bundle.Candidate.TestArtifactID,
		"verification:" + bundle.Verification.VerificationID,
	}
	for _, job := range bundle.Jobs {
		values = append(values, "job:"+job.JobID)
		if job.ArtifactID != "" {
			values = append(values, "artifact:"+job.ArtifactID)
		}
	}
	sort.Strings(values)
	return uniqueStrings(values)
}

func approvalBlockers(profile reviewProfile, service serviceProfileResponse, bundle reviewBundle, now time.Time) []string {
	blockers := make([]string, 0, 8)
	if !profile.ReviewReady {
		blockers = append(blockers, "review profile is not approved for autonomous approval")
	}
	if !service.AssessmentReady {
		blockers = append(blockers, "service profile is not assessment-ready")
	}
	if len(service.ContractReferences) == 0 {
		blockers = append(blockers, "no owner-approved contract reference is pinned")
	}
	if !bundle.Workspace.VerificationReady {
		blockers = append(blockers, "workspace profile is not verification-ready")
	}
	verification := bundle.Verification
	if verification.Status != "succeeded" || !verification.ExpectedTestObserved || !verification.SuitePassed || !verification.PublicationEligible {
		blockers = append(blockers, "trusted verification is incomplete, failed, or not publication-eligible")
	}
	if !approvalJobsValid(bundle, now) {
		blockers = append(blockers, "trusted before-and-after job receipts do not satisfy approval policy")
	}
	return blockers
}

func approvalJobsValid(bundle reviewBundle, now time.Time) bool {
	for index, job := range bundle.Jobs {
		if !runnerDigestPattern.MatchString(job.RunnerImage) || job.ArtifactTruncated || job.TimedOut || !job.ExpiresAt.After(now) ||
			!artifactIDPattern.MatchString(job.ArtifactID) || !sha256Pattern.MatchString(job.ArtifactSHA256) || job.ArtifactBytes < 1 || job.ExitCode == nil {
			return false
		}
		if index == 0 {
			if job.Status != "failed" || *job.ExitCode == 0 {
				return false
			}
		} else if job.Status != "succeeded" || *job.ExitCode != 0 {
			return false
		}
	}
	return true
}

func uniqueStrings(values []string) []string {
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

func validReviewText(value string) bool {
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

func jobExpiries(jobs []jobRecord) []time.Time {
	result := make([]time.Time, 0, len(jobs))
	for _, job := range jobs {
		result = append(result, job.ExpiresAt)
	}
	return result
}

func validateStoredReviewReceipt(profile reviewProfile, receipt reviewReceipt, now time.Time) error {
	if receipt.SchemaVersion != 1 || !reviewIDPattern.MatchString(receipt.ReviewID) ||
		!caseIDPattern.MatchString(receipt.CaseID) || !candidateIDPattern.MatchString(receipt.CandidateID) ||
		!testArtifactIDPattern.MatchString(receipt.TestArtifactID) || !verificationIDPattern.MatchString(receipt.VerificationID) ||
		!sha256Pattern.MatchString(receipt.WorkspaceProfileSHA256) || receipt.WorkspaceProfileSHA256 != profile.ExpectedWorkspaceProfileSHA256 ||
		receipt.ReviewProfileSHA256 != profile.profileSHA256 || receipt.ReviewID != reviewIdentity(receipt) {
		return errors.New("review receipt identity is invalid")
	}
	if receipt.Status != "approved" && receipt.Status != "rejected" && receipt.Status != "inconclusive" {
		return errors.New("review receipt status is invalid")
	}
	if receipt.Status == "approved" && len(receipt.BlockingFindings) != 0 ||
		receipt.Status != "approved" && len(receipt.BlockingFindings) == 0 ||
		len(receipt.BlockingFindings) > profile.Limits.MaximumFindings {
		return errors.New("review receipt findings are invalid")
	}
	if !validCanonicalReviewStrings(receipt.ContractReferences, 32, profile.Limits.MaximumReferenceBytes, true) ||
		!validCanonicalReviewStrings(receipt.EvidenceReferences, 64, profile.Limits.MaximumReferenceBytes, false) ||
		!validCanonicalReviewStrings(receipt.BlockingFindings, profile.Limits.MaximumFindings, profile.Limits.findingLimit(), true) {
		return errors.New("review receipt references are invalid")
	}
	if receipt.Status == "approved" && len(receipt.ContractReferences) == 0 {
		return errors.New("approved review has no contract reference")
	}
	if receipt.ReviewerExecutionID == "" || len(receipt.ReviewerExecutionID) > 128 || !validReviewText(receipt.ReviewerExecutionID) ||
		receipt.CreatedAt.IsZero() || receipt.CreatedAt.After(now.Add(time.Minute)) || !receipt.ExpiresAt.After(now) || !receipt.ExpiresAt.After(receipt.CreatedAt) {
		return errors.New("review receipt lifetime or execution identity is invalid")
	}
	return nil
}

func validCanonicalReviewStrings(values []string, maximumCount, maximumBytes int, allowEmpty bool) bool {
	if (!allowEmpty && len(values) == 0) || len(values) > maximumCount || !sort.StringsAreSorted(values) {
		return false
	}
	previous := ""
	for index, value := range values {
		if value == "" || len(value) > maximumBytes || !validReviewText(value) || index > 0 && value == previous {
			return false
		}
		previous = value
	}
	return true
}

func reviewIdentity(receipt reviewReceipt) string {
	identity := struct {
		SchemaVersion          int       `json:"schema_version"`
		CaseID                 string    `json:"case_id"`
		CandidateID            string    `json:"candidate_id"`
		TestArtifactID         string    `json:"test_artifact_id"`
		VerificationID         string    `json:"verification_id"`
		WorkspaceProfileSHA256 string    `json:"workspace_profile_sha256"`
		ReviewProfileSHA256    string    `json:"review_profile_sha256"`
		Status                 string    `json:"status"`
		ContractReferences     []string  `json:"contract_references"`
		EvidenceReferences     []string  `json:"evidence_references"`
		BlockingFindings       []string  `json:"blocking_findings"`
		ReviewerExecutionID    string    `json:"reviewer_execution_id"`
		CreatedAt              time.Time `json:"created_at"`
		ExpiresAt              time.Time `json:"expires_at"`
	}{
		SchemaVersion: receipt.SchemaVersion, CaseID: receipt.CaseID, CandidateID: receipt.CandidateID,
		TestArtifactID: receipt.TestArtifactID, VerificationID: receipt.VerificationID,
		WorkspaceProfileSHA256: receipt.WorkspaceProfileSHA256, ReviewProfileSHA256: receipt.ReviewProfileSHA256,
		Status: receipt.Status, ContractReferences: receipt.ContractReferences, EvidenceReferences: receipt.EvidenceReferences,
		BlockingFindings: receipt.BlockingFindings, ReviewerExecutionID: receipt.ReviewerExecutionID,
		CreatedAt: receipt.CreatedAt, ExpiresAt: receipt.ExpiresAt,
	}
	encoded, _ := json.Marshal(identity)
	digest := sha256.Sum256(append([]byte("maintenance-review-v1\x00"), encoded...))
	return "review_" + hex.EncodeToString(digest[:])
}
