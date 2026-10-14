package main

import (
	"crypto/sha256"
	"fmt"
	"testing"
	"time"

	"github.com/truvaagents/truva-g3/orchestration"
)

func TestRecordedRemediationAnswerAcceptance(t *testing.T) {
	now := time.Now().UTC()
	response := `{"status":"inconclusive","summary":"Historical PR remains open; no current publication or production resolution is established."}`
	digest := fmt.Sprintf("%x", sha256.Sum256([]byte(response)))
	for _, variant := range []string{"accepted", "wrong_response", "wrong_request", "missing_check", "inconsistent_prose", "fixture", "failed_read", "unreviewed", "empty_answer"} {
		t.Run(variant, func(t *testing.T) {
			capture := remediationAssessmentCapture{RequestID: "diagnostic", ResponseSHA256: digest, PaidCalls: 2, ModelMode: "paid OpenRouter", Response: &orchestration.OrchestratorResponse{RequestID: "diagnostic", Response: response}, Execution: &orchestration.ExecutionResult{Success: true, Steps: []orchestration.StepResult{{Success: true, Capability: "get_remediation_state"}, {Success: true, Capability: "get_remediation_state"}}}}
			assessment := remediationAnswerAssessment{RequestID: "diagnostic", ResponseSHA256: digest, Reviewer: "fixture-reviewer", ReviewedAt: now, PRStatesMatchObservations: true, OriginalAttributionCorrect: true, NoCurrentPublicationClaim: true, NoUnsupportedApplicability: true, NoUnsupportedResolution: true, SummaryAndFindingsConsistent: true, EvidenceNote: "Labelled offline contract fixture; no real model acceptance claimed."}
			switch variant {
			case "wrong_response":
				capture.Response.Response = `{"summary":"This case created the PR."}`
			case "wrong_request":
				assessment.RequestID = "other"
			case "missing_check":
				assessment.OriginalAttributionCorrect = false
			case "inconsistent_prose":
				assessment.SummaryAndFindingsConsistent = false
			case "fixture":
				capture.PaidCalls, capture.ModelMode = 0, "labelled local fixture"
			case "failed_read":
				capture.Execution.Steps[0].Success = false
			case "unreviewed":
				assessment.Reviewer = ""
			case "empty_answer":
				capture.Response.Response = ""
			}
			err := validateRemediationAssessment(capture, assessment, now)
			if (err == nil) != (variant == "accepted") {
				t.Fatalf("acceptance=%v variant=%s", err, variant)
			}
		})
	}
}
