package main

import (
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/truvaagents/truva-g3-domain-examples/software-maintenance/internal/caseorigin"
	"github.com/truvaagents/truva-g3-domain-examples/software-maintenance/internal/lifecycle"
	"github.com/truvaagents/truva-g3-domain-examples/software-maintenance/internal/serviceevidence"
	"github.com/truvaagents/truva-g3/core"
	"github.com/truvaagents/truva-g3/telemetry"
	"go.opentelemetry.io/otel/attribute"
)

type assessmentLookupInput struct {
	ProfileID     string `json:"profile_id"`
	ObservationID string `json:"observation_id,omitempty"`
	CheckID       string `json:"check_id,omitempty"`
}

type assessmentReadInput struct {
	ProfileID    string `json:"profile_id"`
	AssessmentID string `json:"assessment_id"`
}

type assessmentWriteInput struct {
	ProfileID                 string  `json:"profile_id"`
	CaseID                    string  `json:"case_id"`
	ObservationID             string  `json:"observation_id"`
	CheckID                   string  `json:"check_id"`
	ExpectedIssueVersion      *uint64 `json:"expected_issue_version"`
	ExpectedOriginExecutionID string  `json:"expected_origin_execution_id"`
	ExpectedIssueID           string  `json:"expected_issue_id"`
	ExpectedFactsDigest       string  `json:"expected_facts_digest"`
}

var errAssessmentOrigin = errors.New("assessment origin is not eligible")
var errAssessmentIntent = errors.New("assessment write intent no longer matches origin or facts")

func (agent *assessmentTool) assessmentRequest(writer http.ResponseWriter, request *http.Request, input any) bool {
	return agent.assessmentRequestWithDecoder(writer, request, input, decodeAssessmentRequest)
}

func (agent *assessmentTool) assessmentRequestWithDecoder(writer http.ResponseWriter, request *http.Request, input any, decode func(*http.Request, int64, any) error) bool {
	if request.Method != http.MethodPost {
		writeAssessmentError(writer, http.StatusMethodNotAllowed, "METHOD_NOT_ALLOWED", "Assessment capabilities require POST", core.CategoryInputError, false)
		return false
	}
	caller, ok := agent.auth.authenticate(request.Header.Get("Authorization"))
	if !ok && agent.serviceAuth != nil {
		caller, ok = agent.serviceAuth.authenticate(request.Header.Get("Authorization"))
	}
	if !ok {
		writeAssessmentError(writer, http.StatusUnauthorized, "UNAUTHENTICATED", "An authorized assessment credential is required", core.CategoryAuthError, false)
		return false
	}
	if !agent.profile.AllowsAssessment(caller) {
		writeAssessmentError(writer, http.StatusForbidden, "ASSESSMENT_CALLER_FORBIDDEN", "Caller has no assessment access in the configured profile", core.CategoryAuthError, false)
		return false
	}
	if err := decode(request, agent.profile.Limits.MaximumRequestBytes, input); err != nil {
		writeAssessmentError(writer, http.StatusBadRequest, "INVALID_REQUEST", "Supply one bounded assessment JSON object with its declared fields", core.CategoryInputError, false)
		return false
	}
	return true
}

func (agent *assessmentTool) assessmentProfile(writer http.ResponseWriter, inputProfile string) bool {
	if inputProfile != agent.profile.CoordinatorProfileID {
		writeAssessmentError(writer, http.StatusForbidden, "ASSESSMENT_PROFILE_FORBIDDEN", "Use the authorized coordinator_profile_id, not the service profile ID", core.CategoryAuthError, false)
		return false
	}
	return true
}

func (agent *assessmentTool) assessmentReply(writer http.ResponseWriter, request *http.Request, operation string, started time.Time, value any, err error) {
	status, code, message, category, retryable := http.StatusOK, "", "", core.CategoryServiceError, false
	if err != nil {
		status, code, message, retryable = http.StatusServiceUnavailable, "ASSESSMENT_UNAVAILABLE", "Assessment evidence/store is unavailable; reuse is not established", true
		if failure, ok := serviceevidence.ClassifyReadFailure(err); ok {
			status, code, message, category, retryable = failure.Status, failure.Code, failure.Message, failure.Category, failure.Retryable
		}
		switch {
		case errors.Is(err, errAssessmentIntent):
			status, code, message, category, retryable = http.StatusConflict, "ASSESSMENT_INTENT_CHANGED", "The saved write intent differs from the completed execution or current facts; retain the original intent and reassess explicitly", core.CategoryInputError, false
		case errors.Is(err, caseorigin.ErrTooLarge):
			status, code, message, category, retryable = http.StatusBadGateway, "ASSESSMENT_ORIGIN_TOO_LARGE", "Origin case exceeds MAINTENANCE_ASSESSMENT_ORIGIN_MAX_BYTES; adjust the operator read bound before reconsidering reuse", core.CategoryServiceError, false
		case errors.Is(err, lifecycle.ErrNotFound), errors.Is(err, caseorigin.ErrNotFound):
			status, code, message, category, retryable = http.StatusNotFound, "ASSESSMENT_NOT_FOUND", "Authorized assessment or origin case was not found", core.CategoryNotFound, false
		case errors.Is(err, lifecycle.ErrConflict):
			status, code, message, category, retryable = http.StatusConflict, "ASSESSMENT_CONFLICT", "Read the current issue version and reevaluate before writing; stale versions do not overwrite evidence", core.CategoryInputError, false
		case errors.Is(err, errAssessmentOrigin):
			status, code, message, category, retryable = http.StatusUnprocessableEntity, "ASSESSMENT_INELIGIBLE", "The origin case/policy/evidence does not support a reusable assessment", core.CategoryInputError, false
		}
	}
	if err != nil {
		writeAssessmentError(writer, status, code, message, category, retryable)
		return
	}
	writeAssessmentSuccess(writer, status, value)
}

type assessmentStatusWriter struct {
	http.ResponseWriter
	status  int
	failure *core.ToolError
}

func (writer *assessmentStatusWriter) recordToolError(failure *core.ToolError) {
	writer.failure = failure
}

func (writer *assessmentStatusWriter) WriteHeader(status int) {
	if writer.status != 0 {
		return
	}
	writer.status = status
	writer.ResponseWriter.WriteHeader(status)
}

func (writer *assessmentStatusWriter) Write(value []byte) (int, error) {
	if writer.status == 0 {
		writer.WriteHeader(http.StatusOK)
	}
	return writer.ResponseWriter.Write(value)
}

// The wrapper observes all paths, including authentication/JSON/profile rejection.
// It records bounded metadata, not credentials, submitted facts or raw bodies.
func (agent *assessmentTool) observeAssessment(operation string, handler http.HandlerFunc) http.HandlerFunc {
	return func(writer http.ResponseWriter, request *http.Request) {
		started := time.Now()
		requestID := core.GetRequestID(request.Context())
		if id := telemetry.GetBaggage(request.Context())["request_id"]; id != "" {
			requestID = id
		}
		if requestID == "" {
			requestID = request.Header.Get("X-TruvaG3-Request-ID")
		}
		telemetry.AddSpanEvent(request.Context(), "maintenance.assessment.request_received", attribute.String("request_id", requestID), attribute.String("operation", operation))
		if agent.Logger != nil {
			agent.Logger.InfoWithContext(request.Context(), "Assessment operation received", map[string]any{"operation": operation, "request_id": requestID, "method": request.Method})
		}
		observed := &assessmentStatusWriter{ResponseWriter: writer}
		handler(observed, request)
		status := observed.status
		if status == 0 {
			status = http.StatusOK
		}
		attributes := []attribute.KeyValue{attribute.String("request_id", requestID), attribute.String("operation", operation), attribute.Int("http.status_code", status)}
		if observed.failure != nil {
			attributes = append(attributes, attribute.String("error_code", observed.failure.Code), attribute.String("error_category", string(observed.failure.Category)), attribute.String("error_type", assessmentErrorType(observed.failure)), attribute.Bool("retryable", observed.failure.Retryable))
		}
		telemetry.AddSpanEvent(request.Context(), "maintenance.assessment.operation", attributes...)
		if status >= 400 {
			telemetry.RecordSpanError(request.Context(), fmt.Errorf("assessment %s returned HTTP %d", operation, status))
		}
		if agent.Logger != nil {
			fields := map[string]any{"request_id": requestID, "operation": operation, "status": "success", "duration_ms": time.Since(started).Milliseconds(), "coordinator_profile_id": agent.profile.CoordinatorProfileID}
			if status >= 400 {
				fields["status"], fields["http_status"] = "error", status
				fields["error_type"] = "unknown"
				fields["error"] = "assessment request failed"
				if failure := observed.failure; failure != nil {
					fields["error_code"], fields["error_category"] = failure.Code, string(failure.Category)
					fields["error_type"], fields["retryable"] = assessmentErrorType(failure), failure.Retryable
					fields["error"] = failure.Message
				}
				agent.Logger.WarnWithContext(request.Context(), "Assessment operation rejected or unavailable", fields)
			} else {
				agent.Logger.InfoWithContext(request.Context(), "Assessment operation completed", fields)
			}
		}
		telemetry.RecordToolCall(assessmentToolName, operation, float64(time.Since(started).Milliseconds()), map[bool]string{true: "error", false: "success"}[status >= 400])
	}
}

func assessmentErrorType(failure *core.ToolError) string {
	switch failure.Code {
	case "ASSESSMENT_ORIGIN_TOO_LARGE", "RECORD_EXCEEDS_VIEW_BUDGET":
		return "response_limit"
	case "INVALID_UPSTREAM_RESPONSE":
		return "invalid_response"
	case "EVIDENCE_SOURCE_TIMEOUT":
		return "timeout"
	case "ASSESSMENT_CONFLICT", "RELEASE_CHANGED":
		return "conflict"
	case "ASSESSMENT_INELIGIBLE":
		return "evidence_ineligible"
	}
	switch failure.Category {
	case core.CategoryNotFound:
		return "not_found"
	case core.CategoryAuthError:
		return "authorization"
	case core.CategoryInputError:
		return "validation"
	case core.CategoryServiceError:
		return "backend_unavailable"
	default:
		return "unknown"
	}
}

type assessmentCheckHint struct {
	CheckID   string `json:"check_id"`
	Operation string `json:"operation"`
}

func (agent *assessmentTool) assessmentChecks() []assessmentCheckHint {
	checks := []assessmentCheckHint{}
	if agent.profile.AssessmentPolicy != nil {
		for _, rule := range agent.profile.AssessmentPolicy.Rules {
			checks = append(checks, assessmentCheckHint{rule.CheckID, rule.Operation})
		}
	}
	return checks
}

func (agent *assessmentTool) handleFindAssessments(writer http.ResponseWriter, request *http.Request) {
	started := time.Now()
	var input assessmentLookupInput
	if !agent.assessmentRequest(writer, request, &input) || !agent.assessmentProfile(writer, input.ProfileID) {
		return
	}
	if input.ObservationID != "" && !observationIDPattern.MatchString(input.ObservationID) {
		writeAssessmentError(writer, http.StatusBadRequest, "INVALID_OBSERVATION_ID", "Use an exact observation identifier", core.CategoryInputError, false)
		return
	}
	if input.CheckID != "" {
		if _, err := agent.assessmentRule(input.CheckID); err != nil {
			writeAssessmentError(writer, http.StatusBadRequest, "INVALID_ASSESSMENT_CHECK", "Use a check_id from configured_checks, or omit it for related history", core.CategoryInputError, false)
			return
		}
	}
	result, err := agent.findAssessments(request.Context(), input)
	checks := agent.assessmentChecks()
	agent.assessmentReply(writer, request, "find_related_assessments", started, struct {
		lifecycle.Lookup
		ConfiguredChecks any `json:"configured_checks"`
	}{result, checks}, err)
}

func (agent *assessmentTool) handleGetAssessment(writer http.ResponseWriter, request *http.Request) {
	started := time.Now()
	var input assessmentReadInput
	if !agent.assessmentRequest(writer, request, &input) || !agent.assessmentProfile(writer, input.ProfileID) {
		return
	}
	result, err := agent.assessments.Get(request.Context(), input.AssessmentID)
	agent.assessmentReply(writer, request, "get_assessment", started, assessmentResponse(result), err)
}

func (agent *assessmentTool) handleRecordAssessment(writer http.ResponseWriter, request *http.Request) {
	started := time.Now()
	var input assessmentWriteInput
	if !agent.assessmentRequest(writer, request, &input) || !agent.assessmentProfile(writer, input.ProfileID) {
		return
	}
	if input.ExpectedIssueVersion == nil {
		writeAssessmentError(writer, http.StatusBadRequest, "MISSING_ISSUE_VERSION", "Supply expected_issue_version: 0 for creation or the current issue version for an update", core.CategoryInputError, false)
		return
	}
	if !coordinatorCaseIDPattern.MatchString(input.CaseID) || !observationIDPattern.MatchString(input.ObservationID) || agent.profile.AssessmentPolicy == nil {
		agent.assessmentReply(writer, request, "record_assessment", started, nil, errAssessmentOrigin)
		return
	}
	if _, err := agent.assessmentRule(input.CheckID); err != nil {
		agent.assessmentReply(writer, request, "record_assessment", started, nil, errAssessmentOrigin)
		return
	}
	result, err := agent.recordAssessment(request.Context(), input)
	agent.assessmentReply(writer, request, "record_assessment", started, assessmentResponse(result), err)
}

// Present the declared array without changing durable bytes or the opaque
// assessment identity. The internal inspector continues to expose raw records.
func assessmentResponse(record lifecycle.Assessment) lifecycle.Assessment {
	if record.Remediations == nil {
		record.Remediations = []lifecycle.RemediationReference{}
	}
	return record
}

func (agent *assessmentTool) registerAssessmentCapabilities() {
	profile := core.FieldHint{Name: "profile_id", Type: "string", Description: "Authorized coordinator_profile_id supplied with the current case; distinct from service_profile_id."}
	for _, capability := range []core.Capability{
		{Name: "find_related_assessments", Description: "Reads bounded, attributed assessment history. Optional observation_id and configured check_id trigger exact server-owned comparison; candidates[].eligible is false for unknown, changed, expired or superseded facts. Related history alone grants no suppression/publication authority.", Endpoint: "/maintenance/assessments/find", Handler: agent.handleFindAssessments,
			InputSummary:  &core.SchemaSummary{RequiredFields: []core.FieldHint{profile}, OptionalFields: []core.FieldHint{{Name: "observation_id", Type: "string"}, {Name: "check_id", Type: "string", Description: "A check_id from configured_checks; use with observation_id for current comparison."}}},
			OutputSummary: &core.SchemaSummary{RequiredFields: []core.FieldHint{{Name: "coordinator_profile_id", Type: "string"}, {Name: "coordinator_profile_sha256", Type: "string"}, {Name: "policy_configured", Type: "boolean"}, {Name: "configured_checks", Type: "array"}, {Name: "candidates", Type: "array", Description: "Each candidate holds assessment, eligible, reason and compared_facts_digest."}, {Name: "truncated", Type: "boolean"}, {Name: "checked_at", Type: "string"}, {Name: "limitations", Type: "array"}}, OptionalFields: []core.FieldHint{{Name: "observation_id", Type: "string"}, {Name: "check_id", Type: "string"}}}},
		{Name: "get_assessment", Description: "Reads one immutable assessment in the authorized scope, with its original case/execution, exact facts, receipt references and deadline. A stored assessment alone is not current applicability or current PR state.", Endpoint: "/maintenance/assessments/get", Handler: agent.handleGetAssessment,
			InputSummary: &core.SchemaSummary{RequiredFields: []core.FieldHint{profile, {Name: "assessment_id", Type: "string", Description: "Exact candidates[].assessment.assessment_id from find_related_assessments."}}}, OutputSummary: assessmentOutputSummary()},
		{Name: "record_assessment", Description: "Records a server-validated assessment from an already completed origin case, with exact authorized evidence and conditional issue version. Supports evidenced benign or draft_pr_opened origins. Does not complete an in-flight case, schedule a plan, or approve publication.", Endpoint: "/maintenance/assessments/record", Handler: agent.handleRecordAssessment,
			InputSummary: &core.SchemaSummary{RequiredFields: []core.FieldHint{profile, {Name: "case_id", Type: "string", Description: "Completed original case, not a currently running investigation."}, {Name: "observation_id", Type: "string", Description: "An original case observation."}, {Name: "check_id", Type: "string", Description: "An owner-configured check_id from configured_checks."}, {Name: "expected_issue_version", Type: "number", Description: "Use current_issue_version from find_related_assessments for these exact observation/check facts (0 for creation). Stale writers are rejected."}}}, OutputSummary: assessmentOutputSummary()},
	} {
		if capability.Name == "find_related_assessments" {
			capability.OutputSummary.OptionalFields = append(capability.OutputSummary.OptionalFields,
				core.FieldHint{Name: "current_issue_id", Type: "string", Description: "Exact issue signature computed from current owner-policy facts."},
				core.FieldHint{Name: "current_issue_version", Type: "number", Description: "Authoritative current issue version for CAS; 0 means no issue exists. Present only when current facts are available."})
			capability.OutputSummary.OptionalFields = append(capability.OutputSummary.OptionalFields, core.FieldHint{Name: "current_facts_digest", Type: "string", Description: "Digest of current comparison facts; independent of history pagination."})
		}
		if capability.Name == "record_assessment" {
			capability.InputSummary.RequiredFields = append(capability.InputSummary.RequiredFields,
				core.FieldHint{Name: "expected_origin_execution_id", Type: "string", Description: "Completed execution bound to the saved write intent."},
				core.FieldHint{Name: "expected_issue_id", Type: "string", Description: "Exact current_issue_id saved before the first write attempt."},
				core.FieldHint{Name: "expected_facts_digest", Type: "string", Description: "Exact current_facts_digest saved before the first write attempt. Replays retain all original expected fields."})
		}
		capability.InputTypes, capability.OutputTypes = []string{"json"}, []string{"json"}
		// Issuance belongs to the post-completion application handoff. Internal
		// excludes this writer from planning; handler authorization still applies.
		capability.Internal = capability.Name == "record_assessment"
		capability.Handler = agent.observeAssessment(capability.Name, capability.Handler)
		agent.RegisterCapability(capability)
	}
}

func assessmentOutputSummary() *core.SchemaSummary {
	return &core.SchemaSummary{RequiredFields: []core.FieldHint{
		{Name: "schema_version", Type: "number"}, {Name: "assessment_id", Type: "string"}, {Name: "issue_id", Type: "string"}, {Name: "version", Type: "number"},
		{Name: "origin_case_id", Type: "string"}, {Name: "origin_execution_id", Type: "string"}, {Name: "disposition", Type: "string"}, {Name: "summary", Type: "string"},
		{Name: "facts", Type: "object"}, {Name: "remediations", Type: "array"}, {Name: "assessed_at", Type: "string"}, {Name: "revalidate_after", Type: "string"},
	}, OptionalFields: []core.FieldHint{{Name: "supersedes_id", Type: "string"}}}
}
