package main

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/truvaagents/truva-g3-domain-examples/software-maintenance/internal/observation"
	"github.com/truvaagents/truva-g3/core"
	"github.com/truvaagents/truva-g3/telemetry"
	"go.opentelemetry.io/otel/attribute"
)

func (tool *maintenanceWorkspaceTool) handleCreateWorkspace(writer http.ResponseWriter, request *http.Request) {
	const capability = "create_workspace"
	started := time.Now()
	observation.Received(tool.Logger, request, capability)
	caller, ok := tool.authenticate(writer, request, capability, started)
	if !ok {
		return
	}
	var input createWorkspaceRequest
	if !tool.decode(writer, request, capability, started, 16<<10, &input) {
		return
	}
	profile, ok := tool.authorizeProfile(writer, request, capability, started, caller, input.ProfileID)
	if !ok {
		return
	}
	record, err := tool.operations.createWorkspace(request.Context(), profile, input.CaseID, input.SnapshotID)
	if err != nil {
		tool.failOperation(writer, request, capability, profile.WorkspaceProfileID, started, err)
		return
	}
	tool.succeed(writer, request, capability, profile.WorkspaceProfileID, started, http.StatusOK, record)
}

func (tool *maintenanceWorkspaceTool) handleStageCandidate(writer http.ResponseWriter, request *http.Request) {
	const capability = "stage_candidate"
	started := time.Now()
	observation.Received(tool.Logger, request, capability)
	caller, ok := tool.authenticate(writer, request, capability, started)
	if !ok {
		return
	}
	var input stageCandidateRequest
	if !tool.decode(writer, request, capability, started, 2<<20, &input) {
		return
	}
	workspace, profile, err := tool.operations.loadWorkspace(request.Context(), input.WorkspaceID)
	if err != nil {
		tool.failOperation(writer, request, capability, "", started, err)
		return
	}
	if !caseIDPattern.MatchString(input.CaseID) || input.CaseID != workspace.CaseID {
		tool.fail(writer, request, capability, workspace.ProfileID, started, http.StatusBadRequest, "CASE_SCOPE_MISMATCH", "Case scope does not match the workspace", core.CategoryInputError, false)
		return
	}
	if !profile.allows(caller) || !profile.allowsStage(caller) {
		tool.fail(writer, request, capability, profile.WorkspaceProfileID, started, http.StatusForbidden, "CALLER_FORBIDDEN", "Caller is not authorized to stage candidates for this workspace", core.CategoryAuthError, false)
		return
	}
	record, err := tool.operations.stageCandidate(request.Context(), caller, input)
	if err != nil {
		tool.failOperation(writer, request, capability, workspace.ProfileID, started, err)
		return
	}
	tool.succeed(writer, request, capability, workspace.ProfileID, started, http.StatusOK, publicCandidate(record))
}

func (tool *maintenanceWorkspaceTool) handleRunExperiment(writer http.ResponseWriter, request *http.Request) {
	const capability = "run_experiment"
	started := time.Now()
	observation.Received(tool.Logger, request, capability)
	caller, ok := tool.authenticate(writer, request, capability, started)
	if !ok {
		return
	}
	var input runExperimentRequest
	if !tool.decode(writer, request, capability, started, 16<<10, &input) {
		return
	}
	workspace, profile, err := tool.operations.loadWorkspace(request.Context(), input.WorkspaceID)
	if err != nil {
		tool.failOperation(writer, request, capability, "", started, err)
		return
	}
	if !caseIDPattern.MatchString(input.CaseID) || input.CaseID != workspace.CaseID {
		tool.fail(writer, request, capability, workspace.ProfileID, started, http.StatusBadRequest, "CASE_SCOPE_MISMATCH", "Case scope does not match the workspace", core.CategoryInputError, false)
		return
	}
	if !profile.allows(caller) {
		tool.fail(writer, request, capability, workspace.ProfileID, started, http.StatusForbidden, "CALLER_FORBIDDEN", "Caller is not authorized for this workspace", core.CategoryAuthError, false)
		return
	}
	record, err := tool.operations.runExperiment(request.Context(), input)
	if err != nil {
		tool.failOperation(writer, request, capability, workspace.ProfileID, started, err)
		return
	}
	tool.succeed(writer, request, capability, workspace.ProfileID, started, http.StatusAccepted, record)
}

func (tool *maintenanceWorkspaceTool) handleRunVerification(writer http.ResponseWriter, request *http.Request) {
	const capability = "run_verification"
	started := time.Now()
	observation.Received(tool.Logger, request, capability)
	caller, ok := tool.authenticate(writer, request, capability, started)
	if !ok {
		return
	}
	var input runVerificationRequest
	if !tool.decode(writer, request, capability, started, 16<<10, &input) {
		return
	}
	_, workspace, profile, err := tool.operations.loadCandidate(request.Context(), input.CandidateID)
	if err != nil {
		tool.failOperation(writer, request, capability, "", started, err)
		return
	}
	if !caseIDPattern.MatchString(input.CaseID) || input.CaseID != workspace.CaseID {
		tool.fail(writer, request, capability, workspace.ProfileID, started, http.StatusBadRequest, "CASE_SCOPE_MISMATCH", "Case scope does not match the candidate", core.CategoryInputError, false)
		return
	}
	if !profile.allows(caller) {
		tool.fail(writer, request, capability, workspace.ProfileID, started, http.StatusForbidden, "CALLER_FORBIDDEN", "Caller is not authorized for this candidate", core.CategoryAuthError, false)
		return
	}
	record, err := tool.operations.runVerification(request.Context(), input.CandidateID)
	if err != nil {
		tool.failOperation(writer, request, capability, workspace.ProfileID, started, err)
		return
	}
	tool.succeed(writer, request, capability, workspace.ProfileID, started, http.StatusAccepted, record)
}

func (tool *maintenanceWorkspaceTool) handleGetJob(writer http.ResponseWriter, request *http.Request) {
	const capability = "get_job"
	started := time.Now()
	observation.Received(tool.Logger, request, capability)
	caller, ok := tool.authenticate(writer, request, capability, started)
	if !ok {
		return
	}
	var input getJobRequest
	if !tool.decode(writer, request, capability, started, 16<<10, &input) {
		return
	}
	if !jobIDPattern.MatchString(input.JobID) {
		tool.fail(writer, request, capability, "", started, http.StatusBadRequest, "INVALID_JOB", "Job identifier is invalid", core.CategoryInputError, false)
		return
	}
	var stored jobRecord
	if err := tool.operations.store.get(request.Context(), "job", input.JobID, &stored); err != nil {
		tool.failOperation(writer, request, capability, "", started, err)
		return
	}
	if !caseIDPattern.MatchString(input.CaseID) || input.CaseID != stored.CaseID {
		tool.fail(writer, request, capability, stored.ProfileID, started, http.StatusBadRequest, "CASE_SCOPE_MISMATCH", "Case scope does not match the Job", core.CategoryInputError, false)
		return
	}
	profile, exists := tool.profiles.get(stored.ProfileID)
	if !exists || !profile.allows(caller) {
		tool.fail(writer, request, capability, stored.ProfileID, started, http.StatusForbidden, "CALLER_FORBIDDEN", "Caller is not authorized for this Job", core.CategoryAuthError, false)
		return
	}
	if input.WaitSeconds < 0 || int64(input.WaitSeconds) > int64(tool.maxJobWait/time.Second) {
		tool.fail(writer, request, capability, stored.ProfileID, started, http.StatusBadRequest, "INVALID_WAIT", "wait_seconds must be nonnegative and within the advertised tool maximum", core.CategoryInputError, false)
		return
	}
	record, err := tool.operations.getJobWithWait(request.Context(), input.JobID, time.Duration(input.WaitSeconds)*time.Second, tool.jobPollInterval)
	if err != nil {
		tool.failOperation(writer, request, capability, stored.ProfileID, started, err)
		return
	}
	telemetry.SetSpanAttributes(request.Context(), attribute.Int("maintenance.job.wait_seconds", input.WaitSeconds), attribute.String("maintenance.job.status", record.Status))
	tool.succeed(writer, request, capability, record.ProfileID, started, http.StatusOK, record)
}

func (tool *maintenanceWorkspaceTool) handleReadJobArtifact(writer http.ResponseWriter, request *http.Request) {
	const capability = "read_job_artifact"
	started := time.Now()
	observation.Received(tool.Logger, request, capability)
	caller, ok := tool.authenticate(writer, request, capability, started)
	if !ok {
		return
	}
	var input readArtifactRequest
	if !tool.decode(writer, request, capability, started, 16<<10, &input) {
		return
	}
	if !caseIDPattern.MatchString(input.CaseID) {
		tool.fail(writer, request, capability, "", started, http.StatusBadRequest, "CASE_SCOPE_MISMATCH", "Case scope does not match the artifact", core.CategoryInputError, false)
		return
	}
	var metadata artifactRecord
	if err := tool.operations.store.getArtifactMetadata(request.Context(), input.CaseID, input.ArtifactID, &metadata); err != nil {
		tool.failOperation(writer, request, capability, "", started, err)
		return
	}
	if input.CaseID != metadata.CaseID {
		tool.fail(writer, request, capability, metadata.ProfileID, started, http.StatusBadRequest, "CASE_SCOPE_MISMATCH", "Case scope does not match the artifact", core.CategoryInputError, false)
		return
	}
	profile, exists := tool.profiles.get(metadata.ProfileID)
	if !exists || !profile.allows(caller) {
		tool.fail(writer, request, capability, metadata.ProfileID, started, http.StatusForbidden, "CALLER_FORBIDDEN", "Caller is not authorized for this artifact", core.CategoryAuthError, false)
		return
	}
	response, err := tool.operations.readArtifact(request.Context(), input)
	if err != nil {
		tool.failOperation(writer, request, capability, metadata.ProfileID, started, err)
		return
	}
	if input.Format == "text" {
		page, err := textArtifactPage(response)
		if err != nil {
			tool.failOperation(writer, request, capability, metadata.ProfileID, started, err)
			return
		}
		telemetry.SetSpanAttributes(request.Context(), attribute.String("maintenance.artifact.encoding", page.Encoding), attribute.Int64("maintenance.artifact.returned_bytes", page.ByteEnd-page.ByteStart))
		tool.succeed(writer, request, capability, metadata.ProfileID, started, http.StatusOK, page)
		return
	}
	tool.succeed(writer, request, capability, metadata.ProfileID, started, http.StatusOK, response)
}

func (tool *maintenanceWorkspaceTool) handleGetPublicationBundle(writer http.ResponseWriter, request *http.Request) {
	const capability = "get_publication_bundle"
	started := time.Now()
	observation.Received(tool.Logger, request, capability)
	caller, ok := tool.authenticate(writer, request, capability, started)
	if !ok {
		return
	}
	if caller != tool.publicationIdentity {
		tool.fail(writer, request, capability, "", started, http.StatusForbidden, "CALLER_FORBIDDEN", "Only the configured publication tool may retrieve publication bundles", core.CategoryAuthError, false)
		return
	}
	var input publicationBundleRequest
	if !tool.decode(writer, request, capability, started, 16<<10, &input) {
		return
	}
	bundle, profile, err := tool.loadReceiptBundle(request.Context(), input)
	if err != nil {
		tool.failOperation(writer, request, capability, "", started, err)
		return
	}
	if bundle.Verification.Status != "succeeded" || !bundle.Verification.PublicationEligible {
		tool.fail(writer, request, capability, profile.WorkspaceProfileID, started, http.StatusUnprocessableEntity, "VERIFICATION_NOT_ELIGIBLE", "Candidate verification is incomplete, failed, or not publication-eligible", core.CategoryInputError, false)
		return
	}
	tool.succeed(writer, request, capability, profile.WorkspaceProfileID, started, http.StatusOK, bundle)
}

func (tool *maintenanceWorkspaceTool) handleGetReviewBundle(writer http.ResponseWriter, request *http.Request) {
	const capability = "get_review_bundle"
	started := time.Now()
	observation.Received(tool.Logger, request, capability)
	caller, ok := tool.authenticate(writer, request, capability, started)
	if !ok {
		return
	}
	if caller != tool.reviewIdentity {
		tool.fail(writer, request, capability, "", started, http.StatusForbidden, "CALLER_FORBIDDEN", "Only the configured review agent may retrieve review bundles", core.CategoryAuthError, false)
		return
	}
	var input publicationBundleRequest
	if !tool.decode(writer, request, capability, started, 16<<10, &input) {
		return
	}
	bundle, profile, err := tool.loadReceiptBundle(request.Context(), input)
	if err != nil {
		tool.failOperation(writer, request, capability, "", started, err)
		return
	}
	response, err := buildReviewBundleResponse(bundle)
	if err != nil {
		tool.failOperation(writer, request, capability, profile.WorkspaceProfileID, started, err)
		return
	}
	tool.succeed(writer, request, capability, profile.WorkspaceProfileID, started, http.StatusOK, response)
}

func buildReviewBundleResponse(bundle publicationBundleResponse) (reviewBundleResponse, error) {
	const maximumPreviewBytes = 128 << 10
	remaining := maximumPreviewBytes
	previews := make([]reviewCandidateFilePreview, 0, len(bundle.Candidate.Files))
	for index, file := range bundle.Candidate.Files {
		preview := reviewCandidateFilePreview{
			Path: file.Path, BaseSHA256: file.BaseSHA256, Mode: file.Mode,
			Operation: "write", Test: index >= bundle.Candidate.ChangeCount,
		}
		if file.Delete {
			preview.Operation = "delete"
			previews = append(previews, preview)
			continue
		}
		content, err := base64.StdEncoding.DecodeString(file.ContentBase64)
		if err != nil || base64.StdEncoding.EncodeToString(content) != file.ContentBase64 {
			return reviewBundleResponse{}, errors.New("candidate preview content is invalid")
		}
		digest := sha256.Sum256(content)
		preview.ContentSHA256 = hex.EncodeToString(digest[:])
		if !utf8.Valid(content) {
			previews = append(previews, preview)
			continue
		}
		visible := content
		if len(visible) > remaining {
			visible = visible[:remaining]
			for len(visible) > 0 && !utf8.Valid(visible) {
				visible = visible[:len(visible)-1]
			}
			preview.Truncated = true
		}
		preview.TextAvailable = true
		preview.ContentText = string(visible)
		remaining -= len(visible)
		previews = append(previews, preview)
	}
	return reviewBundleResponse{
		Workspace: bundle.Workspace, Candidate: bundle.Candidate, Verification: bundle.Verification,
		Jobs: bundle.Jobs, CandidateFilePreviews: previews,
	}, nil
}

func (tool *maintenanceWorkspaceTool) loadReceiptBundle(ctx context.Context, input publicationBundleRequest) (publicationBundleResponse, workspaceProfile, error) {
	if !caseIDPattern.MatchString(input.CaseID) || !candidateIDPattern.MatchString(input.CandidateID) || !verificationIDPattern.MatchString(input.VerificationID) {
		return publicationBundleResponse{}, workspaceProfile{}, errors.New("receipt bundle references are invalid")
	}
	candidate, workspace, profile, err := tool.operations.loadCandidate(ctx, input.CandidateID)
	if err != nil {
		return publicationBundleResponse{}, workspaceProfile{}, err
	}
	if workspace.CaseID != input.CaseID {
		return publicationBundleResponse{}, workspaceProfile{}, errors.New("case scope does not match the candidate")
	}
	var verification verificationRecord
	if err := tool.operations.store.get(ctx, "verification", input.VerificationID, &verification); err != nil {
		return publicationBundleResponse{}, workspaceProfile{}, err
	}
	if verification.VerificationID != input.VerificationID || verification.CandidateID != candidate.CandidateID || verification.ProfileID != profile.WorkspaceProfileID {
		return publicationBundleResponse{}, workspaceProfile{}, errors.New("verification does not bind to the requested candidate")
	}
	jobIDs := append([]string{verification.ReproductionJobID}, verification.CandidateJobIDs...)
	for _, jobID := range jobIDs {
		_, jobErr := tool.operations.observeJob(ctx, jobID, false)
		if jobErr != nil {
			return publicationBundleResponse{}, workspaceProfile{}, jobErr
		}
	}
	verification, jobs, err := tool.operations.refreshVerificationSnapshot(ctx, input.VerificationID)
	if err != nil {
		return publicationBundleResponse{}, workspaceProfile{}, err
	}
	return publicationBundleResponse{Workspace: workspace, Candidate: candidate, Verification: verification, Jobs: jobs}, profile, nil
}

func (tool *maintenanceWorkspaceTool) authenticate(writer http.ResponseWriter, request *http.Request, capability string, started time.Time) (string, bool) {
	caller, ok := tool.auth.authenticate(request.Header.Get("Authorization"))
	if !ok {
		tool.fail(writer, request, capability, "", started, http.StatusUnauthorized, "UNAUTHENTICATED", "A valid workspace-tool service credential is required", core.CategoryAuthError, false)
		return "", false
	}
	return caller, true
}

func (tool *maintenanceWorkspaceTool) decode(writer http.ResponseWriter, request *http.Request, capability string, started time.Time, maximumBytes int64, destination any) bool {
	if request.Method != http.MethodPost {
		tool.fail(writer, request, capability, "", started, http.StatusMethodNotAllowed, "INVALID_REQUEST", "Capability requests must use POST", core.CategoryInputError, false)
		return false
	}
	if err := decodeWorkspaceRequest(request, maximumBytes, destination); err != nil {
		tool.fail(writer, request, capability, "", started, http.StatusBadRequest, "INVALID_REQUEST", "Request body must be one bounded JSON object with only supported fields", core.CategoryInputError, false)
		return false
	}
	return true
}

func (tool *maintenanceWorkspaceTool) authorizeProfile(writer http.ResponseWriter, request *http.Request, capability string, started time.Time, caller, profileID string) (workspaceProfile, bool) {
	if !workspaceNamePattern.MatchString(profileID) {
		tool.fail(writer, request, capability, "", started, http.StatusBadRequest, "INVALID_PROFILE", tool.profileCorrection(caller, profileID, "Workspace profile identifier is invalid"), core.CategoryInputError, false)
		return workspaceProfile{}, false
	}
	profile, ok := tool.profiles.get(profileID)
	if !ok {
		tool.fail(writer, request, capability, profileID, started, http.StatusNotFound, "PROFILE_NOT_FOUND", tool.profileCorrection(caller, profileID, "Workspace profile was not found"), core.CategoryNotFound, false)
		return workspaceProfile{}, false
	}
	if !profile.allows(caller) {
		tool.fail(writer, request, capability, profileID, started, http.StatusForbidden, "CALLER_FORBIDDEN", "Caller is not authorized for this workspace profile", core.CategoryAuthError, false)
		return workspaceProfile{}, false
	}
	telemetry.SetSpanAttributes(request.Context(), attribute.String("maintenance.workspace_profile_id", profileID), attribute.String("maintenance.caller", caller))
	return profile, true
}

// profileCorrection supplies hints only from this authenticated caller's scope.
// The response bounds are example-owned disclosure policy, not selection defaults.
func (tool *maintenanceWorkspaceTool) profileCorrection(caller, rejected, message string) string {
	const maxHintProfiles = 8
	const maxRejectedBytes = 128
	var permitted []string
	for id, profile := range tool.profiles.profiles {
		if profile.allows(caller) {
			permitted = append(permitted, id)
		}
	}
	if len(permitted) == 0 {
		return message
	}
	sort.Strings(permitted)
	hintsTruncated := len(permitted) > maxHintProfiles
	if hintsTruncated {
		permitted = permitted[:maxHintProfiles]
	}
	valueTruncated := len(rejected) > maxRejectedBytes
	if valueTruncated {
		rejected = rejected[:maxRejectedBytes]
		for !utf8.ValidString(rejected) {
			rejected = rejected[:len(rejected)-1]
		}
	}
	return fmt.Sprintf("%s. Rejected profile_id=%q (value_truncated=%t). Use the workspace runner profile from the repair request's workspace_profile_id. Caller-permitted workspace profiles: %s (hints_truncated=%t). Select one appropriate to the case; permission alone does not establish suitability.", message, rejected, valueTruncated, strings.Join(permitted, ", "), hintsTruncated)
}

func (tool *maintenanceWorkspaceTool) failOperation(writer http.ResponseWriter, request *http.Request, capability, profileID string, started time.Time, err error) {
	if errors.Is(err, errEvidenceChanged) {
		tool.fail(writer, request, capability, profileID, started, http.StatusServiceUnavailable, "WORKSPACE_EVIDENCE_CHANGED", "Concurrent evidence update; read the same reference again to obtain a consistent snapshot", core.CategoryServiceError, true)
		return
	}
	if errors.Is(err, errWorkspaceRecordNotFound) {
		tool.fail(writer, request, capability, profileID, started, http.StatusNotFound, "RECORD_NOT_FOUND", "Requested workspace record was not found or has expired", core.CategoryNotFound, false)
		return
	}
	if errors.Is(err, context.DeadlineExceeded) {
		tool.fail(writer, request, capability, profileID, started, http.StatusGatewayTimeout, "OPERATION_TIMEOUT", "Workspace operation timed out", core.CategoryServiceError, true)
		return
	}
	var serviceError *workspaceServiceFailure
	if errors.As(err, &serviceError) {
		status := http.StatusServiceUnavailable
		code := "WORKSPACE_DEPENDENCY_UNAVAILABLE"
		if !serviceError.retryable {
			status = http.StatusInternalServerError
			code = "WORKSPACE_STATE_INVALID"
		}
		tool.fail(writer, request, capability, profileID, started, status, code, "Workspace dependency or stored state is unavailable", core.CategoryServiceError, serviceError.retryable)
		return
	}
	var policyError *workspacePolicyFailure
	if errors.As(err, &policyError) {
		tool.fail(writer, request, capability, profileID, started, http.StatusUnprocessableEntity, policyError.code, policyError.message, core.CategoryInputError, false)
		return
	}
	tool.fail(writer, request, capability, profileID, started, http.StatusUnprocessableEntity, "WORKSPACE_OPERATION_REJECTED", "Workspace operation failed policy or dependency validation", core.CategoryInputError, false)
}

func (tool *maintenanceWorkspaceTool) succeed(writer http.ResponseWriter, request *http.Request, capability, profileID string, started time.Time, status int, data any) {
	tool.record(request, capability, profileID, "success", "", started)
	setWorkspaceTraceHeaders(writer, request)
	writeWorkspaceSuccess(writer, status, data)
}

func (tool *maintenanceWorkspaceTool) fail(writer http.ResponseWriter, request *http.Request, capability, profileID string, started time.Time, status int, code, message string, category core.ErrorCategory, retryable bool) {
	tool.record(request, capability, profileID, "error", code, started)
	setWorkspaceTraceHeaders(writer, request)
	writeWorkspaceError(writer, status, code, message, category, retryable)
}

func (tool *maintenanceWorkspaceTool) record(request *http.Request, capability, profileID, status, errorCode string, started time.Time) {
	duration := time.Since(started)
	ctx := request.Context()
	fields := map[string]interface{}{
		"operation": capability, "status": status, "duration_ms": duration.Milliseconds(),
		"request_id": observation.RequestID(request), "workspace_profile_id": profileID,
	}
	if errorCode != "" {
		fields["error_code"] = errorCode
		fields["error"] = errorCode
		telemetry.RecordSpanError(request.Context(), errors.New(errorCode))
		fields["error_type"] = workspaceErrorType(errorCode)
	}
	telemetry.SetSpanAttributes(ctx,
		attribute.String("truvag3.tool.name", workspaceToolName), attribute.String("truvag3.capability", capability),
		attribute.String("maintenance.status", status), attribute.Int64("maintenance.duration_ms", duration.Milliseconds()),
	)
	telemetry.RecordToolCall(workspaceToolName, capability, float64(duration.Milliseconds()), status)
	if tool.Logger == nil {
		return
	}
	if status == "success" {
		tool.Logger.InfoWithContext(ctx, "Maintenance workspace capability completed", fields)
	} else {
		tool.Logger.WarnWithContext(ctx, "Maintenance workspace capability failed", fields)
	}
}

func workspaceErrorType(code string) string {
	switch code {
	case "INVALID_PROFILE", "PROFILE_NOT_FOUND":
		return "profile_input"
	case "UNAUTHORIZED", "CALLER_FORBIDDEN":
		return "authorization"
	case "WORKSPACE_DEPENDENCY_UNAVAILABLE", "WORKSPACE_STATE_INVALID", "RECORD_NOT_FOUND":
		return "dependency_or_state"
	case "OPERATION_TIMEOUT":
		return "timeout"
	default:
		return "input_or_policy"
	}
}

func setWorkspaceTraceHeaders(writer http.ResponseWriter, request *http.Request) {
	trace := telemetry.GetTraceContext(request.Context())
	if trace.TraceID != "" {
		writer.Header().Set("X-Trace-ID", trace.TraceID)
		writer.Header().Set("X-Span-ID", trace.SpanID)
	}
	if requestID := observation.RequestID(request); requestID != "" {
		writer.Header().Set("X-Request-ID", requestID)
	}
}
