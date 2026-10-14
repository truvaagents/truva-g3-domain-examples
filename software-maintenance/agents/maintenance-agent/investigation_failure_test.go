package main

import (
	"strings"
	"testing"

	"github.com/truvaagents/truva-g3/orchestration"
)

func TestSummarizeFailedExecutionKeepsFailureAndAdmittedCounts(t *testing.T) {
	got := summarizeFailedExecution(&orchestration.ExecutionResult{
		Success: false,
		Metadata: map[string]interface{}{
			"termination_reason":                        "plan_validation_exhausted",
			orchestration.MetadataKeyExecutionRequestID: "maintenance-case-123",
		},
		Steps: []orchestration.StepResult{
			{StepID: "step-1", Success: true},
			{StepID: "step-2", Success: true, Skipped: true},
			{StepID: "step-3", Success: false},
		},
	})
	if got.executionID != "maintenance-case-123" ||
		!strings.Contains(got.summary, "1 completed, 1 failed, 1 skipped") ||
		!strings.Contains(got.summary, "plan_validation_exhausted") {
		t.Fatalf("failed execution summary = %#v", got)
	}
	if missing := summarizeFailedExecution(nil); missing.executionID != "" || !strings.Contains(missing.summary, "unavailable") {
		t.Fatalf("missing execution summary = %#v", missing)
	}
}
