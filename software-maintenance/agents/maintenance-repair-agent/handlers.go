package main

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/truvaagents/truva-g3-domain-examples/software-maintenance/internal/agentfailure"
	"github.com/truvaagents/truva-g3-domain-examples/software-maintenance/internal/observation"
	"github.com/truvaagents/truva-g3-domain-examples/software-maintenance/internal/responsecontract"
	"github.com/truvaagents/truva-g3/core"
	"github.com/truvaagents/truva-g3/telemetry"
	"go.opentelemetry.io/otel/attribute"
)

func (agent *maintenanceRepairAgent) handleRepair(writer http.ResponseWriter, request *http.Request) {
	const capability = "propose_maintenance_change"
	started := time.Now()
	observation.Received(agent.Logger, request, capability)
	caller, ok := agent.authenticate(writer, request, capability, started)
	if !ok {
		return
	}
	if request.Method != http.MethodPost {
		agent.fail(writer, request, capability, started, http.StatusMethodNotAllowed, "INVALID_REQUEST", "Capability requests must use POST", core.CategoryInputError, false)
		return
	}
	var wire repairWireRequest
	if err := decodeRepairRequest(request, agent.profiles.maximumRequestBytes(), &wire); err != nil {
		agent.fail(writer, request, capability, started, http.StatusBadRequest, "INVALID_REQUEST", "Request must be one bounded JSON object with only supported fields", core.CategoryInputError, false)
		return
	}
	input, validWire := wire.normalized()
	if !validWire {
		agent.fail(writer, request, capability, started, http.StatusBadRequest, "INVALID_REQUEST", "Use either the framework data wrapper or direct repair fields, not both", core.CategoryInputError, false)
		return
	}
	profile, exists := agent.profiles.get(input.ProfileID)
	if !exists || !profile.allows(caller) {
		agent.fail(writer, request, capability, started, http.StatusForbidden, "CALLER_FORBIDDEN", "Caller is not authorized for this repair profile", core.CategoryAuthError, false)
		return
	}
	input.Objective = strings.TrimSpace(input.Objective)
	if !caseIDPattern.MatchString(input.CaseID) || !observationIDPattern.MatchString(input.ObservationID) || !snapshotIDPattern.MatchString(input.SnapshotID) || input.Objective == "" ||
		len(input.Objective) > profile.Limits.MaximumObjectiveBytes || !validRepairText(input.Objective) {
		agent.fail(writer, request, capability, started, http.StatusBadRequest, "INVALID_REPAIR_SCOPE", "Repair identifiers or objective are invalid", core.CategoryInputError, false)
		return
	}
	orch := agent.GetOrchestrator()
	if orch == nil {
		agent.fail(writer, request, capability, started, http.StatusServiceUnavailable, "ORCHESTRATOR_INITIALIZING", "Repair orchestrator is not ready", core.CategoryServiceError, true)
		return
	}
	promptPayload, _ := json.Marshal(map[string]interface{}{
		"case_id": input.CaseID, "observation_id": input.ObservationID, "snapshot_id": input.SnapshotID,
		"service_profile_id": profile.ServiceProfileID, "repository_id": profile.RepositoryID,
		"workspace_profile_id": profile.WorkspaceProfileID, "objective": input.Objective,
		"result_contract": map[string]int{"maximum_finding_bytes": profile.Limits.findingLimit(), "maximum_findings": profile.Limits.MaximumFindings, "maximum_summary_bytes": profile.Limits.summaryLimit()},
	})
	query := "Assess this software-maintenance repair request using the current discovered capabilities and bound skills. " +
		"Choose the investigation and outcome adaptively from the evidence and authority available to this agent. " +
		"<repair_request>" + string(promptPayload) + "</repair_request>"
	metadata := map[string]interface{}{"case_id": input.CaseID, "observation_id": input.ObservationID, "snapshot_id": input.SnapshotID, "repair_profile_id": profile.RepairProfileID}
	telemetry.AddSpanEvent(request.Context(), "maintenance_repair.orchestration_started",
		attribute.String("maintenance.case_id", input.CaseID), attribute.String("maintenance.snapshot_id", input.SnapshotID))
	result, execution, err := orch.ProcessRequestWithExecution(request.Context(), query, metadata)
	request = request.WithContext(agentfailure.Context(request.Context(), result, execution))
	if err != nil {
		details := agent.repairFailureEvidence(profile, input, result, execution)
		for key, value := range agentfailure.Details(request.Context(), execution) {
			details[key] = value
		}
		if boundary := agentfailure.GuidanceBoundary(request.Context(), err); boundary != nil {
			details = agentfailure.AddBoundaryDetails(details, boundary)
			agent.failWithEvidence(writer, request, started, http.StatusInternalServerError, boundary.Code, agentfailure.GuidanceBudgetMessage, core.CategoryServiceError, false, details)
			return
		}
		var providerError core.ProviderError
		if errors.As(err, &providerError) && providerError.StatusCode() >= 400 && providerError.StatusCode() < 500 {
			agent.failWithEvidence(writer, request, started, providerError.StatusCode(), "AI_PROVIDER_REJECTED", "AI provider rejected the repair request", core.CategoryServiceError, providerError.IsTransient(), details)
			return
		}
		agent.failWithEvidence(writer, request, started, http.StatusBadGateway, "REPAIR_ORCHESTRATION_FAILED", "Repair orchestration failed", core.CategoryServiceError, true, details)
		return
	}
	response, err := agent.buildRepairResponse(profile, input, result, execution)
	if err != nil {
		details := agent.repairFailureEvidence(profile, input, result, execution)
		for key, value := range agentfailure.Details(request.Context(), execution) {
			details[key] = value
		}
		details = responsecontract.AddDetails(details, err)
		agent.Logger.WarnWithContext(request.Context(), "Repair output validation failed", map[string]interface{}{
			"operation": capability, "request_id": observation.RequestID(request), "error_type": "result_validation", "case_id": input.CaseID, "snapshot_id": input.SnapshotID, "error": err.Error(),
			"validation_code": details["validation_code"], "field": details["field"],
		})
		telemetry.RecordSpanError(request.Context(), err)
		telemetry.AddSpanEvent(request.Context(), "maintenance_repair.result_validation_failed",
			attribute.String("maintenance.validation_code", details["validation_code"]), attribute.String("maintenance.validation_field", details["field"]))
		agent.failWithEvidence(writer, request, started, http.StatusUnprocessableEntity, "REPAIR_EVIDENCE_INVALID", responsecontract.FailureMessage("Repair execution did not produce the trusted structured evidence required for a result", err), core.CategoryInputError, false, details)
		return
	}
	response.FailedOperations = agentfailure.Project(request.Context(), execution)
	agent.succeed(writer, request, capability, started, response)
}

func (agent *maintenanceRepairAgent) handleHealth(writer http.ResponseWriter, request *http.Request) {
	status := http.StatusOK
	state := "ready"
	if agent.GetOrchestrator() == nil {
		status = http.StatusServiceUnavailable
		state = "initializing"
	}
	writer.Header().Set("Content-Type", "application/json")
	writer.WriteHeader(status)
	_ = json.NewEncoder(writer).Encode(map[string]interface{}{"status": state, "service": repairAgentName, "timestamp": agent.now()})
}

func (agent *maintenanceRepairAgent) authenticate(writer http.ResponseWriter, request *http.Request, capability string, started time.Time) (string, bool) {
	caller, ok := agent.auth.authenticate(request.Header.Get("Authorization"))
	if !ok {
		agent.fail(writer, request, capability, started, http.StatusUnauthorized, "UNAUTHENTICATED", "A valid repair-agent service credential is required", core.CategoryAuthError, false)
		return "", false
	}
	return caller, true
}

func (agent *maintenanceRepairAgent) succeed(writer http.ResponseWriter, request *http.Request, capability string, started time.Time, data any) {
	agent.record(request, capability, "success", "", started)
	setRepairTraceHeaders(writer, request)
	writeRepairSuccess(writer, http.StatusOK, data)
}

func (agent *maintenanceRepairAgent) fail(writer http.ResponseWriter, request *http.Request, capability string, started time.Time, status int, code, message string, category core.ErrorCategory, retryable bool) {
	agent.record(request, capability, "error", code, started)
	setRepairTraceHeaders(writer, request)
	writeRepairError(writer, status, code, message, category, retryable)
}

func (agent *maintenanceRepairAgent) record(request *http.Request, capability, status, errorCode string, started time.Time) {
	duration := time.Since(started)
	fields := map[string]interface{}{
		"operation": capability, "status": status, "duration_ms": duration.Milliseconds(), "request_id": observation.RequestID(request),
	}
	if errorCode != "" {
		fields["error_code"] = errorCode
		fields["error"] = errorCode
		fields["error_type"] = "repair_error"
		telemetry.RecordSpanError(request.Context(), errors.New(errorCode))
	}
	telemetry.SetSpanAttributes(request.Context(), attribute.String("truvag3.agent.name", repairAgentName),
		attribute.String("truvag3.capability", capability), attribute.String("maintenance.status", status),
		attribute.Int64("maintenance.duration_ms", duration.Milliseconds()))
	telemetry.RecordRequest(telemetry.ModuleOrchestration, capability, float64(duration.Milliseconds()), status)
	if agent.Logger == nil {
		return
	}
	if status == "success" {
		agent.Logger.InfoWithContext(request.Context(), "Maintenance repair capability completed", fields)
	} else {
		agent.Logger.WarnWithContext(request.Context(), "Maintenance repair capability failed", fields)
	}
}

func setRepairTraceHeaders(writer http.ResponseWriter, request *http.Request) {
	trace := telemetry.GetTraceContext(request.Context())
	if trace.TraceID != "" {
		writer.Header().Set("X-Trace-ID", trace.TraceID)
		writer.Header().Set("X-Span-ID", trace.SpanID)
	}
	if requestID := observation.RequestID(request); requestID != "" {
		writer.Header().Set("X-Request-ID", requestID)
	}
}
