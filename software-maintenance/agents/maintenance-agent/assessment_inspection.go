package main

import (
	"fmt"
	"net/http"
	"time"

	"github.com/truvaagents/truva-g3-domain-examples/software-maintenance/internal/observation"
	"github.com/truvaagents/truva-g3/core"
	"github.com/truvaagents/truva-g3/telemetry"
	"go.opentelemetry.io/otel/attribute"
)

func (agent *maintenanceAgent) assessmentRequestWithDecoder(writer http.ResponseWriter, request *http.Request, input any, decode func(*http.Request, int64, any) error) bool {
	if request.Method != http.MethodPost {
		writeCoordinatorError(writer, http.StatusMethodNotAllowed, "METHOD_NOT_ALLOWED", "Assessment capabilities require POST", core.CategoryInputError, false)
		return false
	}
	caller, ok := agent.auth.authenticate(request.Header.Get("Authorization"))
	if !ok && agent.assessmentAuth != nil {
		caller, ok = agent.assessmentAuth.authenticate(request.Header.Get("Authorization"))
	}
	if !ok {
		writeCoordinatorError(writer, http.StatusUnauthorized, "UNAUTHENTICATED", "An authorized assessment credential is required", core.CategoryAuthError, false)
		return false
	}
	if !agent.profile.AllowsAssessment(caller) {
		writeCoordinatorError(writer, http.StatusForbidden, "ASSESSMENT_CALLER_FORBIDDEN", "Caller has no assessment access in the configured profile", core.CategoryAuthError, false)
		return false
	}
	if err := decode(request, agent.profile.Limits.MaximumRequestBytes, input); err != nil {
		writeCoordinatorError(writer, http.StatusBadRequest, "INVALID_REQUEST", "Supply one bounded assessment JSON object with its declared fields", core.CategoryInputError, false)
		return false
	}
	return true
}

func (agent *maintenanceAgent) assessmentProfile(writer http.ResponseWriter, inputProfile string) bool {
	if inputProfile != agent.profile.CoordinatorProfileID {
		writeCoordinatorError(writer, http.StatusForbidden, "ASSESSMENT_PROFILE_FORBIDDEN", "Use the authorized coordinator_profile_id, not the service profile ID", core.CategoryAuthError, false)
		return false
	}
	return true
}

type assessmentStatusWriter struct {
	http.ResponseWriter
	status int
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
func (agent *maintenanceAgent) observeAssessment(operation string, handler http.HandlerFunc) http.HandlerFunc {
	return func(writer http.ResponseWriter, request *http.Request) {
		started := time.Now()
		observation.Received(agent.Logger, request, operation)
		observed := &assessmentStatusWriter{ResponseWriter: writer}
		handler(observed, request)
		status := observed.status
		if status == 0 {
			status = http.StatusOK
		}
		requestID := observation.RequestID(request)
		telemetry.AddSpanEvent(request.Context(), "maintenance.assessment.operation",
			attribute.String("request_id", requestID), attribute.String("operation", operation), attribute.Int("http.status_code", status))
		if status >= 400 {
			telemetry.RecordSpanError(request.Context(), fmt.Errorf("assessment %s returned HTTP %d", operation, status))
		}
		if agent.Logger != nil {
			fields := map[string]any{"request_id": requestID, "operation": operation, "status": "success", "duration_ms": time.Since(started).Milliseconds(), "coordinator_profile_id": agent.profile.CoordinatorProfileID}
			if status >= 400 {
				fields["status"], fields["http_status"] = "error", status
				fields["error"], fields["error_type"] = "inspection request failed", "inspection_error"
				agent.Logger.WarnWithContext(request.Context(), "Assessment operation rejected or unavailable", fields)
			} else {
				agent.Logger.InfoWithContext(request.Context(), "Assessment operation completed", fields)
			}
		}
		telemetry.RecordToolCall(maintenanceAgentName, operation, float64(time.Since(started).Milliseconds()), map[bool]string{true: "error", false: "success"}[status >= 400])
	}
}

type assessmentCheckHint struct {
	CheckID   string `json:"check_id"`
	Operation string `json:"operation"`
}

func (agent *maintenanceAgent) assessmentChecks() []assessmentCheckHint {
	checks := []assessmentCheckHint{}
	if agent.profile.AssessmentPolicy != nil {
		for _, rule := range agent.profile.AssessmentPolicy.Rules {
			checks = append(checks, assessmentCheckHint{rule.CheckID, rule.Operation})
		}
	}
	return checks
}
