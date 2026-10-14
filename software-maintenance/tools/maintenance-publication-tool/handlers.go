package main

import (
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/truvaagents/truva-g3-domain-examples/software-maintenance/internal/observation"
	"github.com/truvaagents/truva-g3/core"
	"github.com/truvaagents/truva-g3/telemetry"
	"go.opentelemetry.io/otel/attribute"
)

func (tool *maintenancePublicationTool) handlePublishDraftPR(writer http.ResponseWriter, request *http.Request) {
	const capability = "publish_draft_pr"
	started := time.Now()
	caller, ok := tool.authenticate(writer, request, capability, started)
	if !ok {
		return
	}
	var input publishRequest
	if !tool.decode(writer, request, capability, started, 32<<10, &input) {
		return
	}
	profile, ok := tool.authorizeProfile(writer, request, capability, started, caller, input.ProfileID)
	if !ok {
		return
	}
	if !caseIDPattern.MatchString(input.CaseID) || !candidateIDPattern.MatchString(input.CandidateID) || !verificationIDPattern.MatchString(input.VerificationID) || !reviewIDPattern.MatchString(input.ReviewID) {
		tool.fail(writer, request, capability, profile.PublicationProfileID, started, http.StatusBadRequest, "INVALID_PUBLICATION_REFERENCE", "Publication references are invalid", core.CategoryInputError, false)
		return
	}
	record, err := tool.operations.publish(request.Context(), profile, input)
	if err != nil {
		tool.failOperation(writer, request, capability, profile.PublicationProfileID, started, err)
		return
	}
	tool.succeed(writer, request, capability, profile.PublicationProfileID, started, http.StatusOK, record)
}

func (tool *maintenancePublicationTool) handleGetPublication(writer http.ResponseWriter, request *http.Request) {
	const capability = "get_publication"
	started := time.Now()
	caller, ok := tool.authenticate(writer, request, capability, started)
	if !ok {
		return
	}
	var input getPublicationRequest
	if !tool.decode(writer, request, capability, started, 16<<10, &input) {
		return
	}
	profile, ok := tool.authorizeProfile(writer, request, capability, started, caller, input.ProfileID)
	if !ok {
		return
	}
	record, err := tool.operations.get(request.Context(), profile, input)
	if err != nil {
		tool.failOperation(writer, request, capability, profile.PublicationProfileID, started, err)
		return
	}
	tool.succeed(writer, request, capability, profile.PublicationProfileID, started, http.StatusOK, record)
}

func (tool *maintenancePublicationTool) authenticate(writer http.ResponseWriter, request *http.Request, capability string, started time.Time) (string, bool) {
	observation.Received(tool.Logger, request, capability)
	caller, ok := tool.auth.authenticate(request.Header.Get("Authorization"))
	if !ok {
		tool.fail(writer, request, capability, "", started, http.StatusUnauthorized, "UNAUTHENTICATED", "A valid publication-tool service credential is required", core.CategoryAuthError, false)
		return "", false
	}
	return caller, true
}

func (tool *maintenancePublicationTool) decode(writer http.ResponseWriter, request *http.Request, capability string, started time.Time, maximumBytes int64, destination any) bool {
	if request.Method != http.MethodPost {
		tool.fail(writer, request, capability, "", started, http.StatusMethodNotAllowed, "INVALID_REQUEST", "Capability requests must use POST", core.CategoryInputError, false)
		return false
	}
	if err := decodePublicationRequest(request, maximumBytes, destination); err != nil {
		tool.fail(writer, request, capability, "", started, http.StatusBadRequest, "INVALID_REQUEST", "Request body must be one bounded JSON object with only supported fields", core.CategoryInputError, false)
		return false
	}
	return true
}

func (tool *maintenancePublicationTool) authorizeProfile(writer http.ResponseWriter, request *http.Request, capability string, started time.Time, caller, profileID string) (publicationProfile, bool) {
	if !publicationNamePattern.MatchString(profileID) {
		tool.fail(writer, request, capability, "", started, http.StatusBadRequest, "INVALID_PROFILE", "Publication profile identifier is invalid", core.CategoryInputError, false)
		return publicationProfile{}, false
	}
	profile, exists := tool.profiles.get(profileID)
	if !exists {
		tool.fail(writer, request, capability, profileID, started, http.StatusNotFound, "PROFILE_NOT_FOUND", "Publication profile was not found", core.CategoryNotFound, false)
		return publicationProfile{}, false
	}
	if !profile.allows(caller) {
		tool.fail(writer, request, capability, profileID, started, http.StatusForbidden, "CALLER_FORBIDDEN", "Caller is not authorized for this publication profile", core.CategoryAuthError, false)
		return publicationProfile{}, false
	}
	telemetry.SetSpanAttributes(request.Context(), attribute.String("maintenance.publication_profile_id", profileID), attribute.String("maintenance.caller", caller))
	return profile, true
}

func (tool *maintenancePublicationTool) failOperation(writer http.ResponseWriter, request *http.Request, capability, profileID string, started time.Time, err error) {
	var remediationErr *remediationFailure
	if errors.As(err, &remediationErr) {
		tool.failRemediationRead(writer, request, capability, profileID, started, err)
		return
	}
	if errors.Is(err, errPublicationNotFound) {
		tool.fail(writer, request, capability, profileID, started, http.StatusNotFound, "PUBLICATION_NOT_FOUND", "Publication record was not found or has expired", core.CategoryNotFound, false)
		return
	}
	if errors.Is(err, context.DeadlineExceeded) {
		tool.fail(writer, request, capability, profileID, started, http.StatusGatewayTimeout, "PUBLICATION_TIMEOUT", "Publication operation timed out", core.CategoryServiceError, true)
		return
	}
	var failure *publicationFailure
	if errors.As(err, &failure) {
		status := http.StatusUnprocessableEntity
		category := core.CategoryInputError
		code := "PUBLICATION_REJECTED"
		if failure.kind == "publication_not_ready" {
			status = http.StatusConflict
			code = "PUBLICATION_NOT_READY"
		} else if failure.retryable {
			status = http.StatusServiceUnavailable
			category = core.CategoryServiceError
			code = "PUBLICATION_DEPENDENCY_UNAVAILABLE"
		} else if failure.kind == "record_invalid" || failure.kind == "state_invalid" || failure.kind == "state_encode" {
			status = http.StatusInternalServerError
			category = core.CategoryServiceError
			code = "PUBLICATION_STATE_INVALID"
		}
		if failure.github != nil {
			tool.record(request, capability, profileID, "error", code, started, failure.github)
			setPublicationTraceHeaders(writer, request)
			writePublicationErrorWithDetails(writer, status, code, failure.github.summary(), category, failure.retryable, failure.github.details())
			return
		}
		tool.fail(writer, request, capability, profileID, started, status, code, "Publication prerequisites, policy, or dependency validation failed", category, failure.retryable)
		return
	}
	tool.fail(writer, request, capability, profileID, started, http.StatusInternalServerError, "PUBLICATION_FAILED", "Publication failed without a valid outcome receipt", core.CategoryServiceError, false)
}

func (tool *maintenancePublicationTool) succeed(writer http.ResponseWriter, request *http.Request, capability, profileID string, started time.Time, status int, data any) {
	tool.record(request, capability, profileID, "success", "", started)
	setPublicationTraceHeaders(writer, request)
	writePublicationSuccess(writer, status, data)
}

func (tool *maintenancePublicationTool) fail(writer http.ResponseWriter, request *http.Request, capability, profileID string, started time.Time, status int, code, message string, category core.ErrorCategory, retryable bool) {
	tool.record(request, capability, profileID, "error", code, started)
	setPublicationTraceHeaders(writer, request)
	writePublicationError(writer, status, code, message, category, retryable)
}

func (tool *maintenancePublicationTool) record(request *http.Request, capability, profileID, status, errorCode string, started time.Time, upstream ...*githubFailureEvidence) {
	duration := time.Since(started)
	ctx := request.Context()
	fields := map[string]interface{}{
		"operation": capability, "status": status, "duration_ms": duration.Milliseconds(),
		"request_id": publicationRequestID(request), "publication_profile_id": profileID,
	}
	if len(upstream) > 0 && upstream[0] != nil {
		failure := upstream[0]
		// Provider text is untrusted data: only bounded metadata enters logs/spans.
		fields["upstream_operation"] = failure.operation
		fields["upstream_status"] = failure.status
		fields["github_request_id"] = failure.requestID
		telemetry.SetSpanAttributes(ctx,
			attribute.String("maintenance.github.operation", failure.operation),
			attribute.Int("maintenance.github.status_code", failure.status),
			attribute.String("maintenance.github.request_id", failure.requestID),
		)
	}
	if errorCode != "" {
		fields["error_code"] = errorCode
		fields["error"] = errorCode
		fields["error_type"] = "publication_error"
		telemetry.RecordSpanError(ctx, errors.New(errorCode))
	}
	telemetry.SetSpanAttributes(ctx,
		attribute.String("truvag3.tool.name", publicationToolName), attribute.String("truvag3.capability", capability),
		attribute.String("maintenance.status", status), attribute.Int64("maintenance.duration_ms", duration.Milliseconds()),
	)
	telemetry.RecordToolCall(publicationToolName, capability, float64(duration.Milliseconds()), status)
	if tool.Logger == nil {
		return
	}
	if status == "success" {
		tool.Logger.InfoWithContext(ctx, "Maintenance publication capability completed", fields)
	} else {
		tool.Logger.WarnWithContext(ctx, "Maintenance publication capability failed", fields)
	}
}

func setPublicationTraceHeaders(writer http.ResponseWriter, request *http.Request) {
	trace := telemetry.GetTraceContext(request.Context())
	if trace.TraceID != "" {
		writer.Header().Set("X-Trace-ID", trace.TraceID)
		writer.Header().Set("X-Span-ID", trace.SpanID)
	}
	if requestID := publicationRequestID(request); requestID != "" {
		writer.Header().Set("X-Request-ID", requestID)
	}
}

func publicationRequestID(request *http.Request) string {
	if id := telemetry.GetBaggage(request.Context())["request_id"]; id != "" {
		return id
	}
	if id := core.GetRequestID(request.Context()); id != "" {
		return id
	}
	return request.Header.Get("X-TruvaG3-Request-ID")
}
