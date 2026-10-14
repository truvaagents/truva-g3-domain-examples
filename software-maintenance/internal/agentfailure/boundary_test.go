package agentfailure

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"

	"github.com/truvaagents/truva-g3/core"
	"github.com/truvaagents/truva-g3/orchestration"
)

func TestGuidanceBoundaryPreservesCauseAndOwnExecution(t *testing.T) {
	cause := &orchestration.SkillDomainError{Category: orchestration.ErrSkillLimitExceeded, Operation: "admit required skill content", Ref: orchestration.SkillRef{Namespace: "software-maintenance", Name: "review-result-contract"}}
	err := fmt.Errorf("boundary: %w", core.RedactSensitiveError(cause))
	execution := &orchestration.ExecutionResult{Metadata: map[string]interface{}{orchestration.MetadataKeyExecutionRequestID: "own-execution"}}
	ctx := Context(orchestration.WithRequestID(context.Background(), "parent-execution"), nil, execution)
	boundary := GuidanceBoundary(ctx, err)
	if boundary == nil || boundary.Code != GuidanceBudgetCode || boundary.ExecutionRequestID != "own-execution" || boundary.Operation != cause.Operation {
		t.Fatalf("typed cause or child identity lost: %+v", boundary)
	}
	details := AddBoundaryDetails(map[string]string{"candidate_id": "retained", "failed_operations": "{}"}, boundary)
	var roundtrip BoundaryFailure
	if json.Unmarshal([]byte(details["boundary_failure"]), &roundtrip) != nil || roundtrip.Code != GuidanceBudgetCode || details["candidate_id"] != "retained" || details["failed_operations"] != "{}" {
		t.Fatal("error details did not preserve independent evidence")
	}
	conflict := Context(ctx, &orchestration.OrchestratorResponse{RequestID: "different-execution"}, execution)
	if GuidanceBoundary(conflict, err).ExecutionRequestID != "" {
		t.Fatal("conflicting child identities became an attributed execution")
	}
	if GuidanceBoundary(ctx, context.Canceled) != nil {
		t.Fatal("unrelated cancellation was reclassified")
	}
}
