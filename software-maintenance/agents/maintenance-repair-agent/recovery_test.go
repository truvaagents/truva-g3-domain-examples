package main

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/truvaagents/truva-g3/orchestration"
)

func TestRepairRecoveredSubmissionUsesScopedReceipts(t *testing.T) {
	now := time.Now().UTC()
	hash, revision := strings.Repeat("a", 64), strings.Repeat("b", 40)
	profile := repairProfile{RepairReady: true, ServiceProfileID: "service", WorkspaceProfileID: "workspace", ExpectedServiceProfileVersion: "v1", ExpectedWorkspaceProfileSHA256: hash, ExpectedSourceProfileSHA256: hash, RepositoryID: "repo", Limits: repairLimits{MaximumReferenceBytes: 256}}
	request := repairRequest{CaseID: "case", SnapshotID: "src_" + hash}
	workspace := workspaceReference{SchemaVersion: 1, WorkspaceID: "ws_" + hash, ProfileID: "workspace", ProfileSHA256: hash, SourceProfileSHA256: hash, CaseID: "case", RepositoryID: "repo", SnapshotID: request.SnapshotID, RevisionSHA: revision, TreeSHA: revision, ArchiveSHA256: hash, VerificationReady: true, CreatedAt: now, ExpiresAt: now.Add(time.Hour)}
	candidate := candidateReference{SchemaVersion: 1, CandidateID: "cand_" + hash, TestArtifactID: "test_" + hash, WorkspaceID: workspace.WorkspaceID, ProfileID: "workspace", ProfileSHA256: hash, CaseID: "case", RepositoryID: "repo", SnapshotID: request.SnapshotID, RevisionSHA: revision, ExpectedTest: "TestRegression", ChangeCount: 1, TestCount: 1, CandidateBytes: 10, ProposedBy: repairAgentName, CreatedAt: now, ExpiresAt: now.Add(time.Hour)}
	verification := verificationReference{SchemaVersion: 1, VerificationID: "verify_" + hash, CandidateID: candidate.CandidateID, ProfileID: "workspace", ProfileSHA256: hash, ReproductionJobID: "job_" + hash, CandidateJobIDs: []string{"job_" + strings.Repeat("b", 64)}, Status: "pending", CreatedAt: now, UpdatedAt: now}
	service := serviceProfileResponse{ProfileID: "service", ProfileVersion: "v1", AssessmentReady: true, ContractReferences: []contractReference{{Kind: "contract", Revision: revision, Path: "README.md"}}}
	agent := &maintenanceRepairAgent{now: func() time.Time { return now }}
	for _, scenario := range []string{"pending", "succeeded", "cross-case", "expired", "failed", "malformed", "superseded-negative", "superseded-positive", "matching-earlier"} {
		t.Run(scenario, func(t *testing.T) {
			c, v := candidate, verification
			switch scenario {
			case "succeeded":
				v.Status, v.ExpectedTestObserved, v.SuitePassed, v.PublicationEligible = "succeeded", true, true, true
			case "cross-case":
				c.CaseID = "another-case"
			case "expired":
				c.ExpiresAt = now
			case "failed":
				v.Status = "failed"
			}
			execution := &orchestration.ExecutionResult{Success: false, Steps: []orchestration.StepResult{{Error: "earlier rejected exploration"}}}
			for i, item := range []struct {
				name  string
				value any
			}{{"get_service_profile", service}, {"create_workspace", workspace}, {"stage_candidate", c}, {"run_verification", v}} {
				body, err := json.Marshal(map[string]any{"success": true, "data": item.value})
				if err != nil {
					t.Fatal(err)
				}
				tool := "maintenance-workspace-tool"
				if i == 0 {
					tool = "maintenance-evidence-tool"
				}
				execution.Steps = append(execution.Steps, orchestration.StepResult{Success: true, AgentName: tool, Capability: item.name, Response: string(body)})
			}
			if scenario == "malformed" {
				execution.Steps = append(execution.Steps, orchestration.StepResult{Success: true, AgentName: "maintenance-workspace-tool", Capability: "stage_candidate", Response: "invalid"})
			}
			response := &orchestration.OrchestratorResponse{RequestID: "repair-test", Confidence: 0.8, Response: `{"status":"candidate_submitted","summary":"Submitted for verification","findings":[]}`}
			if strings.HasPrefix(scenario, "superseded-") {
				c.CandidateID = "cand_" + strings.Repeat("c", 64)
				body, err := json.Marshal(map[string]any{"success": true, "data": c})
				if err != nil {
					t.Fatal(err)
				}
				execution.Steps = append(execution.Steps, orchestration.StepResult{Success: true, AgentName: "maintenance-workspace-tool", Capability: "stage_candidate", Response: string(body)})
				if scenario == "superseded-negative" {
					response.Response = `{"status":"inconclusive","summary":"New candidate remains unverified","findings":[]}`
				}
			}
			if scenario == "matching-earlier" {
				v.CandidateID = "cand_" + strings.Repeat("c", 64)
				body, err := json.Marshal(map[string]any{"success": true, "data": v})
				if err != nil {
					t.Fatal(err)
				}
				execution.Steps = append(execution.Steps, orchestration.StepResult{Success: true, AgentName: "maintenance-workspace-tool", Capability: "run_verification", Response: string(body)})
			}
			got, err := agent.buildRepairResponse(profile, request, response, execution)
			want := scenario == "pending" || scenario == "succeeded" || scenario == "superseded-negative" || scenario == "matching-earlier"
			if (err == nil) != want {
				t.Fatalf("result=%+v err=%v", got, err)
			}
			if execution.Success || got.ExecutionSuccess {
				t.Fatal("failed harness history was rewritten")
			}
			if scenario == "superseded-negative" && (got.Verification != nil || got.CandidateID != c.CandidateID) {
				t.Fatal("negative attached another candidate's verification")
			}
		})
	}
}

func TestRepairNegativeRetainsFailedExecution(t *testing.T) {
	agent := &maintenanceRepairAgent{}
	for _, status := range []string{"no_change", "not_reproduced", "inconclusive"} {
		execution := &orchestration.ExecutionResult{Success: false, Steps: []orchestration.StepResult{{StepID: "failed", Error: "exploratory read failed"}}}
		response := &orchestration.OrchestratorResponse{RequestID: "repair-test", Confidence: 0.5, Response: `{"status":"` + status + `","summary":"Evidence was insufficient","findings":[]}`}
		got, err := agent.buildRepairResponse(repairProfile{}, repairRequest{}, response, execution)
		if err != nil || got.Status != status || got.ExecutionSuccess || execution.Success {
			t.Fatalf("status=%s got=%+v err=%v", status, got, err)
		}
	}
}

func TestRepairSubmissionStillRequiresReceipts(t *testing.T) {
	agent := &maintenanceRepairAgent{}
	response := &orchestration.OrchestratorResponse{RequestID: "repair-test", Confidence: 0.5, Response: `{"status":"candidate_submitted","summary":"Candidate ready","findings":[]}`}
	if _, err := agent.buildRepairResponse(repairProfile{}, repairRequest{}, response, &orchestration.ExecutionResult{}); err == nil {
		t.Fatal("submission without receipts accepted")
	}
}
