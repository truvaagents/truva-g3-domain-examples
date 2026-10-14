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

func TestReviewResultValidationFeedback(t *testing.T) {
	for _, tc := range []struct {
		decision string
		code     string
		field    string
	}{
		{`{"status":`, "DECISION_JSON_INVALID", "decision"},
		{`{"status":"untrusted-status","summary":"Checked","blocking_findings":[]}`, "DECISION_STATUS_INVALID", "status"},
		{`{"status":"inconclusive","summary":"","blocking_findings":[]}`, "SUMMARY_INVALID_TEXT", "summary"},
		{`{"status":"inconclusive","summary":"` + strings.Repeat("x", 10001) + `","blocking_findings":[]}`, "SUMMARY_TOO_LONG", "summary"},
		{`{"status":"inconclusive","summary":"Checked","blocking_findings":["a","b"]}`, "FINDINGS_TOO_MANY", "blocking_findings"},
		{`{"status":"inconclusive","summary":"Checked","blocking_findings":[""]}`, "FINDING_INVALID_TEXT", "blocking_findings"},
		{`{"status":"approved","summary":"Checked","blocking_findings":["blocker"]}`, "APPROVAL_HAS_BLOCKING_FINDINGS", "blocking_findings"},
		{`{"status":"approved","summary":"Checked","blocking_findings":[]}`, "TRUSTED_EVIDENCE_MISSING", "service_profile"},
	} {
		agent := &maintenanceReviewAgent{}
		response := &orchestration.OrchestratorResponse{RequestID: "child-execution", Response: tc.decision}
		execution := &orchestration.ExecutionResult{Success: false}
		receipt, _, err := agent.buildReceipt(reviewProfile{Limits: reviewLimits{MaximumFindings: 1, MaximumReferenceBytes: 1024}}, reviewRequest{}, response, execution)
		if err == nil || receipt.ReviewID != "" || execution.Success {
			t.Fatalf("invalid decision granted approval: %+v %v", receipt, err)
		}
		details := responsecontract.AddDetails(map[string]string{"execution_id": response.RequestID, "failed_operations": "retained"}, err)
		writer := httptest.NewRecorder()
		writeReviewError(writer, http.StatusUnprocessableEntity, "REVIEW_EVIDENCE_INVALID", responsecontract.FailureMessage("Review result validation failed", err), core.CategoryInputError, false, details)
		var envelope core.ToolResponse
		if err := json.Unmarshal(writer.Body.Bytes(), &envelope); err != nil {
			t.Fatal(err)
		}
		if writer.Code != 422 || envelope.Success || envelope.Error == nil || envelope.Error.Code != "REVIEW_EVIDENCE_INVALID" || envelope.Error.Retryable || envelope.Error.Details["validation_code"] != tc.code || envelope.Error.Details["field"] != tc.field || envelope.Error.Details["execution_id"] != response.RequestID || envelope.Error.Details["failed_operations"] != "retained" {
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
	// A malformed matching result is different from missing evidence, and cannot
	// fall back to an older result or grant an approval receipt.
	steps := []orchestration.StepResult{{Success: true, AgentName: "maintenance-evidence-tool", Capability: "get_service_profile", Parameters: map[string]any{"profile_id": "service"}, Response: "malformed"}}
	_, _, err := (&maintenanceReviewAgent{}).buildReceipt(reviewProfile{ServiceProfileID: "service"}, reviewRequest{}, &orchestration.OrchestratorResponse{RequestID: "child", Response: `{"status":"approved","summary":"Checked","blocking_findings":[]}`}, &orchestration.ExecutionResult{Steps: steps})
	if details := responsecontract.AddDetails(nil, err); details["validation_code"] != "TRUSTED_EVIDENCE_INVALID" {
		t.Fatalf("malformed receipt misclassified: %v %v", details, err)
	}
}
