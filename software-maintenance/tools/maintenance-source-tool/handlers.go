package main

import (
	"encoding/base64"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/truvaagents/truva-g3-domain-examples/software-maintenance/internal/observation"
	"github.com/truvaagents/truva-g3/core"
	"github.com/truvaagents/truva-g3/telemetry"
	"go.opentelemetry.io/otel/attribute"
)

func (tool *maintenanceSourceTool) handleResolveSourceRevision(writer http.ResponseWriter, request *http.Request) {
	const capability = "resolve_source_revision"
	started := time.Now()
	observation.Received(tool.Logger, request, capability)
	caller, ok := tool.authenticate(writer, request, capability, started)
	if !ok {
		return
	}
	var input resolveRevisionRequest
	if !tool.decode(writer, request, capability, started, &input) {
		return
	}
	profile, ok := tool.authorizeProfile(writer, request, capability, started, caller, input.RepositoryID)
	if !ok {
		return
	}
	if input.Revision == "" {
		input.Revision = profile.DefaultRef
	}
	if !profile.allowsRevision(input.Revision) {
		tool.fail(writer, request, capability, profile.RepositoryID, started, http.StatusBadRequest, "REVISION_FORBIDDEN", "Revision is not allowed by the repository profile", core.CategoryInputError, false)
		return
	}
	response, err := tool.operations.resolveRevision(request.Context(), profile, input.Revision)
	if err != nil {
		tool.failSourceOperation(writer, request, capability, profile.RepositoryID, started, err, "REVISION_NOT_FOUND")
		return
	}
	tool.succeed(writer, request, capability, profile.RepositoryID, started, response)
}

func (tool *maintenanceSourceTool) handleListSourceTree(writer http.ResponseWriter, request *http.Request) {
	const capability = "list_source_tree"
	started := time.Now()
	observation.Received(tool.Logger, request, capability)
	caller, ok := tool.authenticate(writer, request, capability, started)
	if !ok {
		return
	}
	var input listTreeRequest
	if !tool.decode(writer, request, capability, started, &input) {
		return
	}
	profile, ok := tool.authorizeProfile(writer, request, capability, started, caller, input.RepositoryID)
	if !ok {
		return
	}
	if !gitSHAPattern.MatchString(input.RevisionSHA) {
		tool.fail(writer, request, capability, profile.RepositoryID, started, http.StatusBadRequest, "INVALID_REVISION", "An exact 40-hex commit SHA is required", core.CategoryInputError, false)
		return
	}
	response, err := tool.operations.listTree(request.Context(), profile, input)
	if err != nil {
		tool.failSourceOperation(writer, request, capability, profile.RepositoryID, started, err, "SOURCE_TREE_NOT_FOUND")
		return
	}
	tool.succeed(writer, request, capability, profile.RepositoryID, started, response)
}

func (tool *maintenanceSourceTool) handleReadSourceFile(writer http.ResponseWriter, request *http.Request) {
	const capability = "read_source_file"
	started := time.Now()
	observation.Received(tool.Logger, request, capability)
	caller, ok := tool.authenticate(writer, request, capability, started)
	if !ok {
		return
	}
	var input readFileRequest
	if !tool.decode(writer, request, capability, started, &input) {
		return
	}
	profile, ok := tool.authorizeProfile(writer, request, capability, started, caller, input.RepositoryID)
	if !ok {
		return
	}
	if !gitSHAPattern.MatchString(input.RevisionSHA) {
		tool.fail(writer, request, capability, profile.RepositoryID, started, http.StatusBadRequest, "INVALID_REVISION", "An exact 40-hex commit SHA is required", core.CategoryInputError, false)
		return
	}
	response, err := tool.operations.readFile(request.Context(), profile, input)
	if err != nil {
		tool.failSourceOperation(writer, request, capability, profile.RepositoryID, started, err, "SOURCE_FILE_NOT_FOUND")
		return
	}
	tool.succeed(writer, request, capability, profile.RepositoryID, started, response)
}

func (tool *maintenanceSourceTool) handleSearchSource(writer http.ResponseWriter, request *http.Request) {
	const capability = "search_source"
	started := time.Now()
	observation.Received(tool.Logger, request, capability)
	caller, ok := tool.authenticate(writer, request, capability, started)
	if !ok {
		return
	}
	var input searchSourceRequest
	if !tool.decode(writer, request, capability, started, &input) {
		return
	}
	profile, ok := tool.authorizeProfile(writer, request, capability, started, caller, input.RepositoryID)
	if !ok {
		return
	}
	if !gitSHAPattern.MatchString(input.RevisionSHA) {
		tool.fail(writer, request, capability, profile.RepositoryID, started, http.StatusBadRequest, "INVALID_REVISION", "An exact 40-hex commit SHA is required", core.CategoryInputError, false)
		return
	}
	if input.Limit < 0 || input.Limit > profile.Limits.MaximumSearchResults {
		tool.fail(writer, request, capability, profile.RepositoryID, started, http.StatusUnprocessableEntity, "SOURCE_SEARCH_LIMIT_INVALID",
			fmt.Sprintf("limit must be omitted or between 1 and %d for this repository", profile.Limits.MaximumSearchResults), core.CategoryInputError, false)
		return
	}
	response, err := tool.operations.search(request.Context(), profile, input)
	if err != nil {
		tool.failSourceOperation(writer, request, capability, profile.RepositoryID, started, err, "SOURCE_NOT_FOUND")
		return
	}
	tool.succeed(writer, request, capability, profile.RepositoryID, started, response)
}

func (tool *maintenanceSourceTool) handleCreateSourceSnapshot(writer http.ResponseWriter, request *http.Request) {
	const capability = "create_source_snapshot"
	started := time.Now()
	observation.Received(tool.Logger, request, capability)
	caller, ok := tool.authenticate(writer, request, capability, started)
	if !ok {
		return
	}
	var input createSnapshotRequest
	if !tool.decode(writer, request, capability, started, &input) {
		return
	}
	profile, ok := tool.authorizeProfile(writer, request, capability, started, caller, input.RepositoryID)
	if !ok {
		return
	}
	if !gitSHAPattern.MatchString(input.RevisionSHA) {
		tool.fail(writer, request, capability, profile.RepositoryID, started, http.StatusBadRequest, "INVALID_REVISION", "An exact 40-hex commit SHA is required", core.CategoryInputError, false)
		return
	}
	response, err := tool.operations.createSnapshot(request.Context(), profile, input.RevisionSHA)
	if err != nil {
		tool.failSourceOperation(writer, request, capability, profile.RepositoryID, started, err, "SOURCE_NOT_FOUND")
		return
	}
	tool.succeed(writer, request, capability, profile.RepositoryID, started, response)
}

func (tool *maintenanceSourceTool) handleGetSourceSnapshot(writer http.ResponseWriter, request *http.Request) {
	const capability = "get_source_snapshot"
	started := time.Now()
	observation.Received(tool.Logger, request, capability)
	caller, ok := tool.authenticate(writer, request, capability, started)
	if !ok {
		return
	}
	if caller != tool.workspaceIdentity {
		tool.fail(writer, request, capability, "", started, http.StatusForbidden, "CALLER_FORBIDDEN", "Only the configured workspace tool may retrieve source snapshot bytes", core.CategoryAuthError, false)
		return
	}
	var input getSnapshotRequest
	if !tool.decode(writer, request, capability, started, &input) {
		return
	}
	if !snapshotIDPattern.MatchString(input.SnapshotID) || input.ByteStart < 0 || input.ByteLimit < 1 || input.ByteLimit > 1<<20 {
		tool.fail(writer, request, capability, "", started, http.StatusBadRequest, "INVALID_SNAPSHOT_SLICE", "Snapshot identifier or byte range is invalid", core.CategoryInputError, false)
		return
	}
	metadata, content, err := tool.operations.store.getSlice(request.Context(), input.SnapshotID, input.ByteStart, input.ByteLimit)
	if errors.Is(err, errSnapshotNotFound) {
		tool.fail(writer, request, capability, "", started, http.StatusNotFound, "SNAPSHOT_NOT_FOUND", "Source snapshot was not found or has expired", core.CategoryNotFound, false)
		return
	}
	if err != nil {
		tool.fail(writer, request, capability, "", started, http.StatusServiceUnavailable, "SNAPSHOT_STORE_UNAVAILABLE", "Source snapshot store is temporarily unavailable", core.CategoryServiceError, true)
		return
	}
	profile, exists := tool.profiles.get(metadata.RepositoryID)
	if !exists || metadata.SourceProfileSHA256 != profile.profileSHA256 || input.ByteLimit > profile.Limits.MaximumSnapshotDownloadBytes || input.ByteStart >= metadata.ArchiveBytes || metadata.ExpiresAt.Before(tool.operations.now()) || len(content) == 0 {
		tool.fail(writer, request, capability, "", started, http.StatusBadRequest, "INVALID_SNAPSHOT_SLICE", "Snapshot metadata or byte range is invalid", core.CategoryInputError, false)
		return
	}
	end := input.ByteStart + int64(len(content))
	tool.succeed(writer, request, capability, metadata.RepositoryID, started, getSnapshotResponse{
		SnapshotID: input.SnapshotID, RepositoryID: metadata.RepositoryID,
		SourceProfileSHA256: metadata.SourceProfileSHA256, AssessmentReady: metadata.AssessmentReady,
		RevisionSHA: metadata.RevisionSHA,
		TreeSHA:     metadata.TreeSHA, ArchiveSHA256: metadata.ArchiveSHA256, Format: metadata.Format,
		ByteStart: input.ByteStart, ByteEnd: end, TotalBytes: metadata.ArchiveBytes,
		ContentBase64: base64.StdEncoding.EncodeToString(content), Truncated: end < metadata.ArchiveBytes,
	})
}

func (tool *maintenanceSourceTool) authenticate(writer http.ResponseWriter, request *http.Request, capability string, started time.Time) (string, bool) {
	caller, ok := tool.auth.authenticate(request.Header.Get("Authorization"))
	if !ok {
		tool.fail(writer, request, capability, "", started, http.StatusUnauthorized, "UNAUTHENTICATED", "A valid source-tool service credential is required", core.CategoryAuthError, false)
		return "", false
	}
	return caller, true
}

func (tool *maintenanceSourceTool) decode(writer http.ResponseWriter, request *http.Request, capability string, started time.Time, destination any) bool {
	if request.Method != http.MethodPost {
		tool.fail(writer, request, capability, "", started, http.StatusMethodNotAllowed, "INVALID_REQUEST", "Capability requests must use POST", core.CategoryInputError, false)
		return false
	}
	if err := decodeSourceRequest(request, 16<<10, destination); err != nil {
		tool.fail(writer, request, capability, "", started, http.StatusBadRequest, "INVALID_REQUEST", "Request body must be one bounded JSON object with only supported fields", core.CategoryInputError, false)
		return false
	}
	return true
}

func (tool *maintenanceSourceTool) authorizeProfile(writer http.ResponseWriter, request *http.Request, capability string, started time.Time, caller, repositoryID string) (sourceProfile, bool) {
	if !sourceIDPattern.MatchString(repositoryID) {
		tool.fail(writer, request, capability, "", started, http.StatusBadRequest, "INVALID_REPOSITORY", "Repository profile identifier is invalid", core.CategoryInputError, false)
		return sourceProfile{}, false
	}
	profile, ok := tool.profiles.get(repositoryID)
	if !ok {
		tool.fail(writer, request, capability, repositoryID, started, http.StatusNotFound, "REPOSITORY_NOT_FOUND", "Repository profile was not found", core.CategoryNotFound, false)
		return sourceProfile{}, false
	}
	if !profile.allows(caller) {
		tool.fail(writer, request, capability, repositoryID, started, http.StatusForbidden, "REPOSITORY_FORBIDDEN", "Caller is not authorized for this repository profile", core.CategoryAuthError, false)
		return sourceProfile{}, false
	}
	telemetry.SetSpanAttributes(request.Context(), attribute.String("maintenance.repository_id", repositoryID), attribute.String("maintenance.caller", caller))
	return profile, true
}

func (tool *maintenanceSourceTool) failSourceOperation(writer http.ResponseWriter, request *http.Request, capability, repositoryID string, started time.Time, err error, notFoundCode string) {
	var failure *sourceFailure
	if errors.As(err, &failure) {
		switch failure.kind {
		case sourceFailureNotFound:
			tool.fail(writer, request, capability, repositoryID, started, http.StatusNotFound, notFoundCode, "Requested source was not found", core.CategoryNotFound, false)
		case sourceFailureTimeout:
			tool.fail(writer, request, capability, repositoryID, started, http.StatusGatewayTimeout, "SOURCE_PROVIDER_TIMEOUT", "Source provider timed out", core.CategoryServiceError, true)
		case sourceFailureRateLimit:
			tool.fail(writer, request, capability, repositoryID, started, http.StatusTooManyRequests, "SOURCE_PROVIDER_RATE_LIMITED", "Source provider rate limit was reached", core.CategoryRateLimit, true)
		case sourceFailureUnavailable:
			tool.fail(writer, request, capability, repositoryID, started, http.StatusServiceUnavailable, "SOURCE_PROVIDER_UNAVAILABLE", "Source provider is temporarily unavailable", core.CategoryServiceError, true)
		case sourceFailureAuth:
			tool.fail(writer, request, capability, repositoryID, started, http.StatusServiceUnavailable, "SOURCE_PROVIDER_AUTH_FAILED", "Source provider credentials are unavailable or insufficient", core.CategoryServiceError, false)
		default:
			tool.fail(writer, request, capability, repositoryID, started, http.StatusBadGateway, "INVALID_SOURCE_RESPONSE", "Source provider returned an invalid response", core.CategoryServiceError, false)
		}
		return
	}
	tool.fail(writer, request, capability, repositoryID, started, http.StatusUnprocessableEntity, "SOURCE_POLICY_REJECTED", "Source operation violates the configured repository bounds", core.CategoryInputError, false)
}

func (tool *maintenanceSourceTool) succeed(writer http.ResponseWriter, request *http.Request, capability, repositoryID string, started time.Time, data any) {
	tool.record(request, capability, repositoryID, "success", "", started)
	setSourceTraceHeaders(writer, request)
	writeSourceSuccess(writer, data)
}

func (tool *maintenanceSourceTool) fail(writer http.ResponseWriter, request *http.Request, capability, repositoryID string, started time.Time, status int, code, message string, category core.ErrorCategory, retryable bool) {
	tool.record(request, capability, repositoryID, "error", code, started)
	setSourceTraceHeaders(writer, request)
	writeSourceError(writer, status, code, message, category, retryable)
}

func (tool *maintenanceSourceTool) record(request *http.Request, capability, repositoryID, status, errorCode string, started time.Time) {
	duration := time.Since(started)
	ctx := request.Context()
	fields := map[string]interface{}{
		"operation": capability, "status": status, "duration_ms": duration.Milliseconds(),
		"request_id": observation.RequestID(request), "repository_id": repositoryID,
	}
	if errorCode != "" {
		fields["error_code"] = errorCode
		fields["error"] = errorCode
		fields["error_type"] = "source_error"
		telemetry.RecordSpanError(request.Context(), errors.New(errorCode))
	}
	telemetry.SetSpanAttributes(ctx,
		attribute.String("truvag3.tool.name", sourceToolName), attribute.String("truvag3.capability", capability),
		attribute.String("maintenance.status", status), attribute.Int64("maintenance.duration_ms", duration.Milliseconds()),
	)
	telemetry.RecordToolCall(sourceToolName, capability, float64(duration.Milliseconds()), status)
	if tool.Logger == nil {
		return
	}
	if status == "success" {
		tool.Logger.InfoWithContext(ctx, "Maintenance source capability completed", fields)
	} else {
		tool.Logger.WarnWithContext(ctx, "Maintenance source capability failed", fields)
	}
}

func setSourceTraceHeaders(writer http.ResponseWriter, request *http.Request) {
	trace := telemetry.GetTraceContext(request.Context())
	if trace.TraceID != "" {
		writer.Header().Set("X-Trace-ID", trace.TraceID)
		writer.Header().Set("X-Span-ID", trace.SpanID)
	}
}
