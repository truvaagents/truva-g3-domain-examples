package main

import (
	"encoding/json"
	"errors"
	"net/http"
	"sort"

	"github.com/truvaagents/truva-g3-domain-examples/software-maintenance/internal/lifecycle"
	"github.com/truvaagents/truva-g3/core"
)

type recordInspectionInput struct {
	ProfileID    string `json:"profile_id"`
	AssessmentID string `json:"assessment_id,omitempty"`
}

func (tool *assessmentTool) handleInspectRecords(w http.ResponseWriter, r *http.Request) {
	var input recordInspectionInput
	if !tool.assessmentRequest(w, r, &input) || !tool.assessmentProfile(w, input.ProfileID) {
		return
	}
	records := []any{}
	refs := []string{}
	addRef := func(id string) {
		for _, old := range refs {
			if old == id {
				return
			}
		}
		if id != "" {
			refs = append(refs, id)
		}
	}
	truncated := false
	var err error
	if input.AssessmentID != "" {
		var record lifecycle.Assessment
		record, err = tool.assessments.Get(r.Context(), input.AssessmentID)
		if err == nil {
			records = append(records, record)
			addRef(record.OriginExecutionID)
		}
	} else {
		var values []lifecycle.Assessment
		values, truncated, err = tool.assessments.List(r.Context(), "", tool.lifecycleLimits.PageLimit)
		for _, record := range values {
			records = append(records, lifecycle.AssessmentReference{AssessmentID: record.AssessmentID, IssueID: record.IssueID, Version: record.Version, OriginCaseID: record.OriginCaseID, OriginExecutionID: record.OriginExecutionID, Disposition: record.Disposition, Summary: record.Summary, RevalidateAfter: record.RevalidateAfter})
			addRef(record.OriginExecutionID)
		}
	}
	if err != nil {
		if errors.Is(err, lifecycle.ErrNotFound) {
			writeAssessmentError(w, 404, "APPLICATION_RECORD_NOT_FOUND", "Authorized record was not found", core.CategoryNotFound, false)
		} else {
			writeAssessmentError(w, 503, "APPLICATION_RECORD_UNAVAILABLE", "Application records are unavailable", core.CategoryServiceError, true)
		}
		return
	}
	view := map[string]any{"schema_version": 1, "component": assessmentToolName, "records": records, "execution_refs": refs, "truncated": truncated, "inspected_at": tool.now(), "policy_configured": tool.profile.AssessmentPolicy != nil, "automatic_suppression_enabled": false, "automatic_reconsideration_enabled": false, "limitations": []string{"Assessment records are application-owned evidence, not separate framework executions. Case records remain with maintenance-agent. Stored history alone proves neither current applicability nor current PR state."}}
	for {
		sort.Strings(refs)
		view["records"], view["execution_refs"], view["truncated"] = records, refs, truncated
		encoded, encodeErr := json.Marshal(view)
		if encodeErr == nil && len(encoded) <= tool.lifecycleLimits.LookupMaxBytes {
			break
		}
		if input.AssessmentID != "" || len(records) == 0 {
			writeAssessmentError(w, 422, "RECORD_EXCEEDS_VIEW_BUDGET", "Exact record exceeds inspection budget", core.CategoryInputError, false)
			return
		}
		records = records[:len(records)-1]
		truncated = true
		refs = []string{}
		for _, raw := range records {
			if record, ok := raw.(lifecycle.AssessmentReference); ok {
				addRef(record.OriginExecutionID)
			}
		}
	}
	writeAssessmentSuccess(w, 200, view)
}

func (tool *assessmentTool) registerRecordInspection() {
	tool.RegisterCapability(core.Capability{Name: "inspect_application_records_v1", Internal: true, Description: "Read-only operator inspection of assessment history. Cases remain with their coordinator; no execution is fabricated for a lookup.", Endpoint: "/maintenance/records/inspect", InputTypes: []string{"json"}, OutputTypes: []string{"json"}, Handler: tool.observeAssessment("inspect_application_records_v1", tool.handleInspectRecords), InputSummary: &core.SchemaSummary{RequiredFields: []core.FieldHint{{Name: "profile_id", Type: "string", Description: "Authorized coordinator_profile_id."}}, OptionalFields: []core.FieldHint{{Name: "assessment_id", Type: "string"}}}, OutputSummary: &core.SchemaSummary{RequiredFields: []core.FieldHint{{Name: "schema_version", Type: "number"}, {Name: "component", Type: "string"}, {Name: "records", Type: "array"}, {Name: "execution_refs", Type: "array"}, {Name: "truncated", Type: "boolean"}, {Name: "inspected_at", Type: "string"}, {Name: "limitations", Type: "array"}}}})
}
