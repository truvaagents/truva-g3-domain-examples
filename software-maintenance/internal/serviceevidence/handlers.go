package serviceevidence

import (
	"errors"
	"net/http"
	"time"

	"github.com/truvaagents/truva-g3-domain-examples/software-maintenance/internal/observation"
	"github.com/truvaagents/truva-g3/core"
	"github.com/truvaagents/truva-g3/telemetry"
	"go.opentelemetry.io/otel/attribute"
)

func (t *maintenanceEvidenceTool) handleGetServiceProfile(w http.ResponseWriter, r *http.Request) {
	const capability = "get_service_profile"
	started := time.Now()
	observation.Received(t.Logger, r, capability)
	caller, ok := t.authenticate(w, r, capability, started)
	if !ok {
		return
	}
	var request profileRequest
	if !t.decodeRequest(w, r, capability, started, &request) {
		return
	}
	profile, ok := t.authorizeProfile(w, r, capability, started, caller, request.ProfileID)
	if !ok {
		return
	}
	t.succeed(w, r, capability, profile.ProfileID, started, profile.public())
}

func (t *maintenanceEvidenceTool) handleListServiceObservations(w http.ResponseWriter, r *http.Request) {
	const capability = "list_service_observations"
	started := time.Now()
	observation.Received(t.Logger, r, capability)
	caller, ok := t.authenticate(w, r, capability, started)
	if !ok {
		return
	}
	var request observationsRequest
	if !t.decodeRequest(w, r, capability, started, &request) {
		return
	}
	profile, ok := t.authorizeProfile(w, r, capability, started, caller, request.ProfileID)
	if !ok {
		return
	}
	start, startErr := time.Parse(time.RFC3339Nano, request.Start)
	end, endErr := time.Parse(time.RFC3339Nano, request.End)
	if request.Limit == 0 {
		request.Limit = profile.Limits.MaximumItems
	}
	now := t.now()
	if startErr != nil || endErr != nil || !start.Before(end) || end.Sub(start) > time.Duration(profile.Limits.MaximumWindowSeconds)*time.Second || start.Before(now.Add(-time.Duration(profile.Limits.RetentionSeconds)*time.Second)) || end.After(now) || request.Limit < 1 || request.Limit > profile.Limits.MaximumItems {
		t.fail(w, r, capability, profile.ProfileID, started, http.StatusBadRequest, "INVALID_OBSERVATION_WINDOW", "Observation window or limit is outside the approved profile bounds", core.CategoryInputError, false)
		return
	}
	feed, err := t.client.listObservations(r.Context(), profile, start, end, request.Limit)
	if err != nil {
		t.failForUpstream(w, r, capability, profile.ProfileID, started, err, "EVIDENCE_NOT_FOUND", "EVIDENCE_SOURCE_UNAVAILABLE", "EVIDENCE_SOURCE_TIMEOUT")
		return
	}
	observations := make([]observationSummary, 0, len(feed.Candidates))
	for _, candidate := range feed.Candidates {
		observations = append(observations, observationSummary{
			ObservationID: candidate.ObservationID,
			Operation:     candidate.Operation,
			ObservedAt:    candidate.At,
			HTTPStatus:    candidate.HTTPStatus,
			ErrorCode:     candidate.ErrorCode,
		})
	}
	t.succeed(w, r, capability, profile.ProfileID, started, observationsResponse{
		ProfileID:      profile.ProfileID,
		ProfileVersion: profile.version,
		Window:         feed.Window,
		Observations:   observations,
		ReturnedCount:  len(observations),
		Truncated:      feed.Truncated,
		Coverage: coverage{
			Source:           "advisory-observation-index",
			Complete:         false,
			RetentionSeconds: profile.Limits.RetentionSeconds,
		},
		CollectedAt: t.now(),
	})
}

func (t *maintenanceEvidenceTool) handleGetServiceEvidence(w http.ResponseWriter, r *http.Request) {
	const capability = "get_service_evidence"
	started := time.Now()
	observation.Received(t.Logger, r, capability)
	caller, ok := t.authenticate(w, r, capability, started)
	if !ok {
		return
	}
	var request evidenceRequest
	if !t.decodeRequest(w, r, capability, started, &request) {
		return
	}
	profile, ok := t.authorizeProfile(w, r, capability, started, caller, request.ProfileID)
	if !ok {
		return
	}
	if !identifierPattern.MatchString(request.ObservationID) {
		t.fail(w, r, capability, profile.ProfileID, started, http.StatusBadRequest, "INVALID_OBSERVATION_ID", "Observation identifier is invalid", core.CategoryInputError, false)
		return
	}
	response, err := readEvidence(r.Context(), profile, t.client, t.now, request.ObservationID)
	if err != nil {
		t.failForUpstream(w, r, capability, profile.ProfileID, started, err, "EVIDENCE_NOT_FOUND", "EVIDENCE_SOURCE_UNAVAILABLE", "EVIDENCE_SOURCE_TIMEOUT")
		return
	}
	t.succeed(w, r, capability, profile.ProfileID, started, response)
}

func (t *maintenanceEvidenceTool) handleGetBusinessOutcome(w http.ResponseWriter, r *http.Request) {
	const capability = "get_business_outcome"
	started := time.Now()
	observation.Received(t.Logger, r, capability)
	caller, ok := t.authenticate(w, r, capability, started)
	if !ok {
		return
	}
	var request outcomeRequest
	if !t.decodeRequest(w, r, capability, started, &request) {
		return
	}
	profile, ok := t.authorizeProfile(w, r, capability, started, caller, request.ProfileID)
	if !ok {
		return
	}
	if !identifierPattern.MatchString(request.OutcomeID) {
		t.fail(w, r, capability, profile.ProfileID, started, http.StatusBadRequest, "INVALID_OUTCOME_ID", "Use the exact outcome_reference.outcome_id string returned by get_service_evidence. This request was rejected before looking up the outcome.", core.CategoryInputError, false)
		return
	}
	response, err := readOutcome(r.Context(), profile, t.client, t.now, request.OutcomeID)
	if err != nil {
		t.failForUpstream(w, r, capability, profile.ProfileID, started, err, "OUTCOME_NOT_FOUND", "OUTCOME_SOURCE_UNAVAILABLE", "OUTCOME_SOURCE_TIMEOUT")
		return
	}
	t.succeed(w, r, capability, profile.ProfileID, started, response)
}

func (t *maintenanceEvidenceTool) handleGetReleaseContext(w http.ResponseWriter, r *http.Request) {
	const capability = "get_release_context"
	started := time.Now()
	observation.Received(t.Logger, r, capability)
	caller, ok := t.authenticate(w, r, capability, started)
	if !ok {
		return
	}
	var request releaseRequest
	if !t.decodeRequest(w, r, capability, started, &request) {
		return
	}
	profile, ok := t.authorizeProfile(w, r, capability, started, caller, request.ProfileID)
	if !ok {
		return
	}
	if request.ExpectedRevision != "" && !buildRevisionPattern.MatchString(request.ExpectedRevision) {
		t.fail(w, r, capability, profile.ProfileID, started, http.StatusBadRequest, "INVALID_EXPECTED_REVISION", "Use observation.serving_revision returned by get_service_evidence (build-<64-hex-digest>); git_revision identifies source separately. Omit expected_revision for a current-release read without comparison.", core.CategoryInputError, false)
		return
	}
	response, err := readRelease(r.Context(), profile, t.client, t.now, request.ExpectedRevision)
	if err != nil {
		t.failForUpstream(w, r, capability, profile.ProfileID, started, err, "RELEASE_NOT_FOUND", "RELEASE_SOURCE_UNAVAILABLE", "RELEASE_SOURCE_TIMEOUT")
		return
	}
	t.succeed(w, r, capability, profile.ProfileID, started, response)
}

func (t *maintenanceEvidenceTool) handleGetRuntimeConfiguration(w http.ResponseWriter, r *http.Request) {
	const capability = "get_runtime_configuration"
	started := time.Now()
	observation.Received(t.Logger, r, capability)
	caller, ok := t.authenticate(w, r, capability, started)
	if !ok {
		return
	}
	var request profileRequest
	if !t.decodeRequest(w, r, capability, started, &request) {
		return
	}
	profile, ok := t.authorizeProfile(w, r, capability, started, caller, request.ProfileID)
	if !ok {
		return
	}
	response, err := readConfiguration(r.Context(), profile, t.client, t.now)
	if err != nil {
		if errors.Is(err, errReleaseChanged) {
			t.fail(w, r, capability, profile.ProfileID, started, http.StatusConflict, "RELEASE_CHANGED", "Serving revision changed while configuration was collected", core.CategoryServiceError, true)
			return
		}
		t.failForUpstream(w, r, capability, profile.ProfileID, started, err, "CONFIGURATION_NOT_FOUND", "CONFIGURATION_SOURCE_UNAVAILABLE", "CONFIGURATION_SOURCE_TIMEOUT")
		return
	}
	t.succeed(w, r, capability, profile.ProfileID, started, response)
}

func (t *maintenanceEvidenceTool) authenticate(w http.ResponseWriter, r *http.Request, capability string, started time.Time) (string, bool) {
	caller, ok := t.auth.authenticate(r.Header.Get("Authorization"))
	if !ok {
		t.fail(w, r, capability, "", started, http.StatusUnauthorized, "UNAUTHENTICATED", "A valid service credential is required", core.CategoryAuthError, false)
		return "", false
	}
	return caller, true
}

func (t *maintenanceEvidenceTool) decodeRequest(w http.ResponseWriter, r *http.Request, capability string, started time.Time, destination any) bool {
	if r.Method != http.MethodPost {
		t.fail(w, r, capability, "", started, http.StatusMethodNotAllowed, "INVALID_REQUEST", "Capability requests must use POST", core.CategoryInputError, false)
		return false
	}
	if err := decodeStrictRequest(r, t.profiles.maximumRequestBytes(), destination); err != nil {
		t.fail(w, r, capability, "", started, http.StatusBadRequest, "INVALID_REQUEST", "Request body must be one bounded JSON object with only supported fields", core.CategoryInputError, false)
		return false
	}
	return true
}

func (t *maintenanceEvidenceTool) authorizeProfile(w http.ResponseWriter, r *http.Request, capability string, started time.Time, caller, profileID string) (serviceProfile, bool) {
	if !profileIDPattern.MatchString(profileID) {
		t.fail(w, r, capability, "", started, http.StatusBadRequest, "INVALID_REQUEST", "Profile identifier is invalid", core.CategoryInputError, false)
		return serviceProfile{}, false
	}
	profile, ok := t.profiles.get(profileID)
	if !ok {
		t.fail(w, r, capability, profileID, started, http.StatusNotFound, "PROFILE_NOT_FOUND", "Maintenance profile was not found", core.CategoryNotFound, false)
		return serviceProfile{}, false
	}
	if !profile.allows(caller) {
		t.fail(w, r, capability, profileID, started, http.StatusForbidden, "PROFILE_FORBIDDEN", "Caller is not authorized for this maintenance profile", core.CategoryAuthError, false)
		return serviceProfile{}, false
	}
	telemetry.SetSpanAttributes(r.Context(), attribute.String("maintenance.profile_id", profileID), attribute.String("maintenance.caller", caller))
	return profile, true
}

func (t *maintenanceEvidenceTool) failForUpstream(w http.ResponseWriter, r *http.Request, capability, profileID string, started time.Time, err error, notFoundCode, unavailableCode, timeoutCode string) {
	failure, ok := ClassifyReadFailure(err)
	if !ok {
		t.fail(w, r, capability, profileID, started, http.StatusBadGateway, "INVALID_UPSTREAM_RESPONSE", "Evidence source returned an invalid response", core.CategoryServiceError, false)
		return
	}
	switch failure.Code {
	case "EVIDENCE_NOT_FOUND":
		failure.Code = notFoundCode
	case "EVIDENCE_SOURCE_TIMEOUT":
		failure.Code = timeoutCode
	case "EVIDENCE_SOURCE_UNAVAILABLE":
		failure.Code = unavailableCode
	}
	t.fail(w, r, capability, profileID, started, failure.Status, failure.Code, failure.Message, failure.Category, failure.Retryable)
}

func (t *maintenanceEvidenceTool) succeed(w http.ResponseWriter, r *http.Request, capability, profileID string, started time.Time, data any) {
	t.recordCompletion(r, capability, profileID, "success", started, "")
	setTraceHeaders(w, r)
	writeSuccess(w, data)
}

func (t *maintenanceEvidenceTool) fail(w http.ResponseWriter, r *http.Request, capability, profileID string, started time.Time, status int, code, message string, category core.ErrorCategory, retryable bool) {
	t.recordCompletion(r, capability, profileID, "error", started, code)
	setTraceHeaders(w, r)
	writeToolError(w, status, code, message, category, retryable)
}

func (t *maintenanceEvidenceTool) recordCompletion(r *http.Request, capability, profileID, status string, started time.Time, errorCode string) {
	duration := time.Since(started)
	ctx := r.Context()
	fields := map[string]interface{}{
		"operation": capability, "status": status, "duration_ms": duration.Milliseconds(),
		"request_id": observation.RequestID(r), "profile_id": profileID,
	}
	if errorCode != "" {
		fields["error_code"] = errorCode
		fields["error"] = errorCode
		fields["error_type"] = "evidence_error"
		telemetry.RecordSpanError(r.Context(), errors.New(errorCode))
	}
	telemetry.SetSpanAttributes(ctx,
		attribute.String("truvag3.tool.name", toolName),
		attribute.String("truvag3.capability", capability),
		attribute.String("maintenance.status", status),
		attribute.Int64("maintenance.duration_ms", duration.Milliseconds()),
	)
	if profileID != "" {
		telemetry.SetSpanAttributes(ctx, attribute.String("maintenance.profile_id", profileID))
	}
	telemetry.RecordToolCall(toolName, capability, float64(duration.Milliseconds()), status)
	if t.Logger == nil {
		return
	}
	if status == "success" {
		t.Logger.InfoWithContext(ctx, "Maintenance evidence capability completed", fields)
	} else {
		t.Logger.WarnWithContext(ctx, "Maintenance evidence capability failed", fields)
	}
}

func setTraceHeaders(w http.ResponseWriter, r *http.Request) {
	trace := telemetry.GetTraceContext(r.Context())
	if trace.TraceID != "" {
		w.Header().Set("X-Trace-ID", trace.TraceID)
		w.Header().Set("X-Span-ID", trace.SpanID)
	}
}
