package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/truvaagents/truva-g3-domain-examples/software-maintenance/internal/responsecontract"
	"github.com/truvaagents/truva-g3/core"
	"github.com/truvaagents/truva-g3/orchestration"
)

func TestRepairResultValidationFeedback(t *testing.T) {
	for _, tc := range []struct {
		decision string
		code     string
		field    string
	}{
		{`{"status":`, "DECISION_JSON_INVALID", "decision"},
		{`{"status":"untrusted-status","summary":"Checked","findings":[]}`, "DECISION_STATUS_INVALID", "status"},
		{`{"status":"inconclusive","summary":"","findings":[]}`, "SUMMARY_INVALID_TEXT", "summary"},
		{`{"status":"inconclusive","summary":"` + strings.Repeat("x", 10001) + `","findings":[]}`, "SUMMARY_TOO_LONG", "summary"},
		{`{"status":"inconclusive","summary":"Checked","findings":["a","b"]}`, "FINDINGS_TOO_MANY", "findings"},
		{`{"status":"inconclusive","summary":"Checked","findings":[""]}`, "FINDING_INVALID_TEXT", "findings"},
		{`{"status":"candidate_submitted","summary":"Checked","findings":[]}`, "TRUSTED_EVIDENCE_MISSING", "candidate_submission"},
	} {
		agent := &maintenanceRepairAgent{}
		response := &orchestration.OrchestratorResponse{RequestID: "child-execution", Response: tc.decision}
		execution := &orchestration.ExecutionResult{Success: false}
		result, err := agent.buildRepairResponse(repairProfile{Limits: repairLimits{MaximumFindings: 1, MaximumReferenceBytes: 1024}}, repairRequest{}, response, execution)
		if err == nil || result.CandidateID != "" || result.VerificationID != "" || execution.Success {
			t.Fatalf("invalid decision granted authority: %+v %v", result, err)
		}
		details := responsecontract.AddDetails(map[string]string{"execution_id": response.RequestID, "failed_operations": "retained"}, err)
		writer := httptest.NewRecorder()
		writeRepairError(writer, http.StatusUnprocessableEntity, "REPAIR_EVIDENCE_INVALID", responsecontract.FailureMessage("Repair result validation failed", err), core.CategoryInputError, false, details)
		var envelope core.ToolResponse
		if err := json.Unmarshal(writer.Body.Bytes(), &envelope); err != nil {
			t.Fatal(err)
		}
		if writer.Code != 422 || envelope.Success || envelope.Error == nil || envelope.Error.Code != "REPAIR_EVIDENCE_INVALID" || envelope.Error.Retryable || envelope.Error.Details["validation_code"] != tc.code || envelope.Error.Details["field"] != tc.field || envelope.Error.Details["execution_id"] != response.RequestID || envelope.Error.Details["failed_operations"] != "retained" {
			t.Fatalf("incorrect error envelope: %s", writer.Body.String())
		}
		if tc.code == "SUMMARY_TOO_LONG" && (details["actual_bytes"] != "10001" || details["limit_bytes"] != "10000") {
			t.Fatalf("length facts lost: %v", details)
		}
		if tc.code == "SUMMARY_TOO_LONG" && !strings.Contains(envelope.Error.Message, "10001 > 10000") {
			t.Fatal("top-level message lost the length cause")
		}
		if strings.Contains(writer.Body.String(), "untrusted-status") {
			t.Fatal("model status promoted to contract guidance")
		}
	}
}
