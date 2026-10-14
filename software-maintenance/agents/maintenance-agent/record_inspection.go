package main

import (
	"encoding/json"
	"errors"
	"net/http"
	"sort"

	"github.com/truvaagents/truva-g3/core"
)

type recordInspectionInput struct {
	ProfileID    string `json:"profile_id"`
	CaseID       string `json:"case_id,omitempty"`
	AssessmentID string `json:"assessment_id,omitempty"`
}

// Read-only, application-owned records are separate from framework executions.
// The viewer can inspect this protocol without knowing maintenance semantics.
func (agent *maintenanceAgent) handleInspectRecords(writer http.ResponseWriter, request *http.Request) {
	var input recordInspectionInput
	if !agent.assessmentRequestWithDecoder(writer, request, &input, decodeCoordinatorRequest) || !agent.assessmentProfile(writer, input.ProfileID) {
		return
	}
	if input.CaseID != "" && input.AssessmentID != "" {
		writeCoordinatorError(writer, http.StatusBadRequest, "INVALID_RECORD_REFERENCE", "Use case_id for coordinator cases; assessment records are owned by maintenance-assessment-tool", core.CategoryInputError, false)
		return
	}
	var records = []any{}
	var executionRefs = []string{}
	truncated := false
	addExecution := func(id string) {
		if id == "" {
			return
		}
		for _, existing := range executionRefs {
			if existing == id {
				return
			}
		}
		executionRefs = append(executionRefs, id)
	}
	var err error
	switch {
	case input.CaseID != "":
		if !coordinatorCaseIDPattern.MatchString(input.CaseID) {
			writeCoordinatorError(writer, http.StatusBadRequest, "INVALID_CASE_ID", "Use an exact case_id", core.CategoryInputError, false)
			return
		}
		var record caseRecord
		record, err = agent.cases.get(request.Context(), input.CaseID)
		if err == nil && record.CoordinatorProfileID != agent.profile.CoordinatorProfileID {
			writeCoordinatorError(writer, http.StatusForbidden, "CASE_PROFILE_FORBIDDEN", "Case is outside the authorized coordinator profile", core.CategoryAuthError, false)
			return
		}
		if err == nil {
			records = append(records, record)
			addExecution(record.ExecutionID)
			if record.BoundaryFailure != nil {
				addExecution(record.BoundaryFailure.ExecutionRequestID)
			}
			if record.ValidationEvidence != nil {
				addExecution(record.ValidationEvidence.ExecutionID)
			}
			if record.Result != nil {
				addExecution(record.Result.ExecutionID)
				addExecution(record.Result.RepairExecutionID)
				if record.Result.LinkedAssessment != nil {
					addExecution(record.Result.LinkedAssessment.OriginExecutionID)
				}
			}
			for _, effect := range record.PublicationEffects {
				addExecution(effect.OriginExecutionID)
				for _, id := range effect.ObservedExecutionIDs {
					addExecution(id)
				}
			}
		}
	default:
		if input.AssessmentID != "" {
			writeCoordinatorError(writer, http.StatusBadRequest, "RECORD_OWNER_CHANGED", "Inspect assessments through maintenance-assessment-tool", core.CategoryInputError, false)
			return
		}

	}
	if err != nil {
		if errors.Is(err, errCaseNotFound) {
			writeCoordinatorError(writer, http.StatusNotFound, "APPLICATION_RECORD_NOT_FOUND", "Authorized record was not found", core.CategoryNotFound, false)
		} else {
			writeCoordinatorError(writer, http.StatusServiceUnavailable, "APPLICATION_RECORD_UNAVAILABLE", "Application records are unavailable", core.CategoryServiceError, true)
		}
		return
	}
	// Full case records stay exact. Derived history is bounded and whole-entry.
	// The common envelope allows the viewer to show that no new DAG was made.
	view := map[string]any{
		"schema_version": 1, "component": maintenanceAgentName, "records": records, "execution_refs": executionRefs,
		"truncated": truncated, "inspected_at": agent.now(), "policy_configured": agent.profile.AssessmentPolicy != nil,
		"automatic_suppression_enabled": false, "automatic_reconsideration_enabled": false,
		"limitations": []string{"Use case_id for coordinator records. Assessment history is served by maintenance-assessment-tool.", "Application records are not framework execution records. A next_check is recorded guidance, not an automatically scheduled task. No execution is fabricated for a lookup or linked decision. Empty execution_refs means this application record did not persist an execution reference; it does not mean no execution took place."},
	}
	budget := min(agent.lifecycleLimits.LookupMaxBytes, maximumEvidenceResponseBytes-1024)
	sort.Strings(executionRefs)
	encoded, encodeErr := json.Marshal(view)
	if encodeErr != nil || len(encoded) > budget {
		writeCoordinatorError(writer, http.StatusUnprocessableEntity, "RECORD_EXCEEDS_VIEW_BUDGET", "Exact case exceeds inspection view budget; raise MAINTENANCE_ASSESSMENT_LOOKUP_MAX_BYTES", core.CategoryInputError, false)
		return
	}
	writeCoordinatorSuccess(writer, http.StatusOK, view)
}

func (agent *maintenanceAgent) registerRecordInspection() {
	agent.RegisterCapability(core.Capability{Name: "inspect_application_records_v1", Internal: true,
		Description: "Read-only operator inspection of coordinator-owned application cases. Use an exact case_id; assessment history is served by maintenance-assessment-tool. Independent of framework DAG status.",
		Endpoint:    "/maintenance/records/inspect", InputTypes: []string{"json"}, OutputTypes: []string{"json"}, Handler: agent.observeAssessment("inspect_application_records_v1", agent.handleInspectRecords),
		InputSummary:  &core.SchemaSummary{RequiredFields: []core.FieldHint{{Name: "profile_id", Type: "string", Description: "Authorized coordinator_profile_id."}}, OptionalFields: []core.FieldHint{{Name: "case_id", Type: "string", Description: "Exact application case ID."}}},
		OutputSummary: &core.SchemaSummary{RequiredFields: []core.FieldHint{{Name: "schema_version", Type: "number"}, {Name: "component", Type: "string"}, {Name: "records", Type: "array"}, {Name: "execution_refs", Type: "array"}, {Name: "truncated", Type: "boolean"}, {Name: "inspected_at", Type: "string"}, {Name: "limitations", Type: "array"}}},
	})
}
