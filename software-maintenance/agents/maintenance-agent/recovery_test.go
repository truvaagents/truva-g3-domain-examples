package main

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/truvaagents/truva-g3/orchestration"
)

func TestCoordinatorIndependentReviewAfterFailedRepair(t *testing.T) {
	now := time.Now().UTC()
	agent := &maintenanceAgent{now: func() time.Time { return now }}
	agent.profile.InvestigationReady = true
	agent.profile.ExpectedReviewProfileSHA256 = "profile"
	agent.profile.Limits.MaximumFindings = 10
	var review reviewStepResponse
	review.Review.ReviewID = "review-id"
	review.Review.CaseID = "case-id"
	review.Review.CandidateID = "candidate-id"
	review.Review.VerificationID = "verification-id"
	review.Review.ReviewProfileSHA256 = "profile"
	review.Review.Status = "approved"
	review.Review.CreatedAt = now.Add(-time.Minute)
	review.Review.ExpiresAt = now.Add(time.Minute)
	for _, expired := range []bool{false, true} {
		if expired {
			review.Review.ExpiresAt = now.Add(-time.Second)
		}
		body, err := json.Marshal(map[string]interface{}{"success": true, "data": review})
		if err != nil {
			t.Fatal(err)
		}
		execution := &orchestration.ExecutionResult{Success: false, Steps: []orchestration.StepResult{
			{AgentName: "maintenance-repair-agent", Capability: "propose_maintenance_change", Error: "earlier failure"},
			{AgentName: "maintenance-review-agent", Capability: "review_maintenance_change", Success: true, Response: string(body), Parameters: map[string]interface{}{"case_id": "case-id", "candidate_id": "candidate-id", "verification_id": "verification-id"}},
		}}
		response := &orchestration.OrchestratorResponse{RequestID: "case-test", Confidence: 0.5, Response: `{"status":"action_proposed","summary":"Independently assessed","findings":[]}`}
		got, err := agent.buildInvestigationResult(context.Background(), investigationInput{CaseID: "case-id"}, response, execution)
		want := "action_proposed"
		if expired {
			want = "inconclusive"
		}
		if err != nil || got.Status != want || got.ExecutionSuccess || execution.Success {
			t.Fatalf("expired=%v result=%+v err=%v", expired, got, err)
		}
	}
}
