package main

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/truvaagents/truva-g3-domain-examples/software-maintenance/internal/responsecontract"
	"github.com/truvaagents/truva-g3/orchestration"
)

func TestRecoveredReviewStillRequiresCompleteVerification(t *testing.T) {
	now := time.Now().UTC()
	hash, revision := strings.Repeat("a", 64), strings.Repeat("b", 40)
	profile := reviewProfile{ReviewReady: true, ServiceProfileID: "service", WorkspaceProfileID: "workspace", ExpectedServiceProfileVersion: "v1", ExpectedWorkspaceProfileSHA256: hash, ExpectedSourceProfileSHA256: hash, RepositoryID: "repo", Limits: reviewLimits{MaximumReferenceBytes: 256, MaximumCandidateFiles: 10, MaximumCandidateBytes: 1024, ReceiptTTLSeconds: 3600}}
	request := reviewRequest{CaseID: "case", CandidateID: "cand_" + hash, VerificationID: "verify_" + hash}
	w := workspaceRecord{SchemaVersion: 1, WorkspaceID: "ws_" + hash, ProfileID: "workspace", ProfileSHA256: hash, SourceProfileSHA256: hash, CaseID: "case", RepositoryID: "repo", SnapshotID: "src_" + hash, RevisionSHA: revision, TreeSHA: revision, ArchiveSHA256: hash, VerificationReady: true, CreatedAt: now, ExpiresAt: now.Add(time.Hour)}
	c := candidateRecord{SchemaVersion: 1, CandidateID: request.CandidateID, TestArtifactID: "test_" + hash, WorkspaceID: w.WorkspaceID, ProfileID: w.ProfileID, ProfileSHA256: hash, CaseID: "case", RepositoryID: "repo", SnapshotID: w.SnapshotID, RevisionSHA: revision, ExpectedTest: "TestRegression", ChangeCount: 1, TestCount: 1, CandidateBytes: 2, CreatedAt: now, ExpiresAt: w.ExpiresAt, Files: []workspaceFile{{Path: "main.go", BaseSHA256: hash, Mode: "100644", ContentBase64: "YQ=="}, {Path: "main_test.go", BaseSHA256: "absent", Mode: "100644", ContentBase64: "Yg=="}}}
	v := verificationRecord{SchemaVersion: 1, VerificationID: request.VerificationID, CandidateID: c.CandidateID, ProfileID: w.ProfileID, ProfileSHA256: hash, ReproductionJobID: "job_" + hash, CandidateJobIDs: []string{"job_" + strings.Repeat("b", 64)}, Status: "succeeded", ExpectedTestObserved: true, SuitePassed: true, PublicationEligible: true, CreatedAt: now, UpdatedAt: now}
	jobs := make([]jobRecord, 2)
	for i, id := range append([]string{v.ReproductionJobID}, v.CandidateJobIDs...) {
		exit, state, target := int32(0), "succeeded", "candidate"
		if i == 0 {
			exit, state, target = 1, "failed", "reproduction"
		}
		jobs[i] = jobRecord{SchemaVersion: 1, JobID: id, WorkspaceID: w.WorkspaceID, CandidateID: c.CandidateID, VerificationID: v.VerificationID, ProfileID: w.ProfileID, ProfileSHA256: hash, CaseID: "case", Target: target, CommandID: "tests", CommandSHA256: hash, RunnerImage: "runner@sha256:" + hash, Status: state, ExitCode: &exit, ArtifactID: "art_" + hash, ArtifactSHA256: hash, ArtifactBytes: 10, ExpiresAt: w.ExpiresAt}
	}
	service := serviceProfileResponse{ProfileID: "service", ProfileVersion: "v1", AssessmentReady: true, ContractReferences: []contractReference{{Kind: "contract", Revision: revision, Path: "README.md"}}}
	agent := &maintenanceReviewAgent{now: func() time.Time { return now }}
	for _, scenario := range []string{"valid", "pending", "cross-case", "expired", "truncated", "approval-disabled"} {
		t.Run(scenario, func(t *testing.T) {
			scenarioProfile := profile
			bundle := reviewBundle{Workspace: w, Candidate: c, Verification: v, Jobs: append([]jobRecord(nil), jobs...)}
			switch scenario {
			case "approval-disabled":
				scenarioProfile.ReviewReady = false
			case "pending":
				bundle.Verification.Status = "pending"
			case "cross-case":
				bundle.Workspace.CaseID = "another-case"
			case "expired":
				bundle.Candidate.ExpiresAt = now
			case "truncated":
				bundle.Jobs[1].ArtifactTruncated = true
			}
			envelope := func(data any) string {
				value, err := json.Marshal(map[string]any{"success": true, "data": data})
				if err != nil {
					t.Fatal(err)
				}
				return string(value)
			}
			execution := &orchestration.ExecutionResult{Success: false, Steps: []orchestration.StepResult{
				{Error: "earlier exploratory read failed"},
				{Success: true, AgentName: "maintenance-evidence-tool", Capability: "get_service_profile", Parameters: map[string]interface{}{"profile_id": "service"}, Response: envelope(service)},
				{Success: true, AgentName: "maintenance-workspace-tool", Capability: "get_review_bundle", Parameters: map[string]interface{}{"case_id": "case", "candidate_id": c.CandidateID, "verification_id": v.VerificationID}, Response: envelope(bundle)},
			}}
			response := &orchestration.OrchestratorResponse{RequestID: "review-test", Confidence: 0.8, Response: `{"status":"approved","summary":"Evidence reviewed","blocking_findings":[]}`}
			receipt, _, err := agent.buildReceipt(scenarioProfile, request, response, execution)
			if (err == nil) != (scenario == "valid") {
				t.Fatalf("receipt=%+v err=%v", receipt, err)
			}
			if scenario == "valid" && (receipt.ReviewID == "" || receipt.ExpiresAt.After(w.ExpiresAt)) {
				t.Fatal("missing or overlong approval")
			}
			if execution.Success {
				t.Fatal("failed harness history was rewritten")
			}
			if scenario == "approval-disabled" {
				details := responsecontract.AddDetails(nil, err)
				reason := "review profile is not approved for autonomous approval"
				message := responsecontract.FailureMessage("fallback", err)
				if receipt.ReviewID != "" || details["validation_code"] != "APPROVAL_PREREQUISITES_UNSATISFIED" || details["reason"] != reason ||
					!strings.HasPrefix(details["expected"], "Approval-enabled review") || strings.Contains(details["expected"], reason) ||
					!strings.Contains(message, "; reason "+reason+"; expected Approval-enabled review") {
					t.Fatalf("approval rejection confused observed and expected conditions: receipt=%+v details=%v message=%q", receipt, details, message)
				}
			}
		})
	}
}

func TestReviewNegativeDoesNotRequireEveryExplorationToSucceed(t *testing.T) {
	agent := &maintenanceReviewAgent{}
	for _, status := range []string{"rejected", "inconclusive"} {
		response := &orchestration.OrchestratorResponse{RequestID: "review-test", Confidence: 0.5, Response: `{"status":"` + status + `","summary":"Insufficient evidence","blocking_findings":[]}`}
		execution := &orchestration.ExecutionResult{Success: false}
		receipt, decision, err := agent.buildReceipt(reviewProfile{}, reviewRequest{}, response, execution)
		if err != nil || decision.Status != status || receipt.ReviewID != "" || execution.Success {
			t.Fatalf("receipt=%+v decision=%+v err=%v", receipt, decision, err)
		}
	}
}

func TestReviewApprovalStillRequiresExactEvidence(t *testing.T) {
	agent := &maintenanceReviewAgent{}
	response := &orchestration.OrchestratorResponse{RequestID: "review-test", Confidence: 0.5, Response: `{"status":"approved","summary":"Ready","blocking_findings":[]}`}
	if _, _, err := agent.buildReceipt(reviewProfile{}, reviewRequest{}, response, &orchestration.ExecutionResult{}); err == nil {
		t.Fatal("approval without receipts accepted")
	}
}

func TestReviewLatestExactReceiptCannotFallBack(t *testing.T) {
	steps := []orchestration.StepResult{
		{Success: true, AgentName: "tool", Capability: "receipt", Parameters: map[string]interface{}{"case_id": "case"}, Response: `{"success":true,"data":{"id":"old"}}`},
		{Success: true, AgentName: "tool", Capability: "receipt", Parameters: map[string]interface{}{"case_id": "case"}, Response: `malformed`},
	}
	var result map[string]interface{}
	if err := exactToolStep(steps, "tool", "receipt", map[string]string{"case_id": "case"}, &result); err == nil {
		t.Fatal("malformed latest evidence fell back to old receipt")
	}
}
