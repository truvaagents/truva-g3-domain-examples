package main

import (
	"net/http"
	"time"

	"github.com/truvaagents/truva-g3/core"
	"github.com/truvaagents/truva-g3/orchestration"
)

// repairFailureEvidence exports bounded identities from independently validated
// tool receipts, never model prose or a replacement repair decision. A failed
// execution stays failed; these handles only let its caller account for partial
// work and request authoritative evidence through normal discovered capabilities.
func (agent *maintenanceRepairAgent) repairFailureEvidence(profile repairProfile, input repairRequest, response *orchestration.OrchestratorResponse, execution *orchestration.ExecutionResult) map[string]string {
	details := map[string]string{
		"case_id": input.CaseID, "snapshot_id": input.SnapshotID,
		"evidence_scope": "Partial tool receipt identities only; no successful repair decision, completed verification, review, or publication is asserted.",
	}
	if response != nil && response.RequestID != "" && len(response.RequestID) <= 128 && validRepairText(response.RequestID) {
		details["repair_execution_id"] = response.RequestID
	}
	if execution == nil {
		return details
	}
	now := agent.now()
	var workspace workspaceReference
	if latestRepairToolData(execution.Steps, "maintenance-workspace-tool", "create_workspace", &workspace) != nil ||
		validateWorkspaceReference(profile, input, workspace, now) != nil {
		return details
	}
	details["workspace_id"] = workspace.WorkspaceID
	var candidate candidateReference
	if latestRepairToolData(execution.Steps, "maintenance-workspace-tool", "stage_candidate", &candidate) != nil ||
		validateCandidateReference(profile, input, workspace, candidate, now) != nil {
		return details
	}
	details["candidate_id"] = candidate.CandidateID
	details["test_artifact_id"] = candidate.TestArtifactID
	var verification verificationReference
	if latestRepairToolData(execution.Steps, "maintenance-workspace-tool", "run_verification", &verification) == nil &&
		validateVerificationReference(profile, input, candidate, verification, now) == nil {
		details["verification_id"] = verification.VerificationID
	}
	return details
}

func (agent *maintenanceRepairAgent) failWithEvidence(writer http.ResponseWriter, request *http.Request, started time.Time, status int, code, message string, category core.ErrorCategory, retryable bool, details map[string]string) {
	agent.record(request, "propose_maintenance_change", "error", code, started)
	setRepairTraceHeaders(writer, request)
	writeRepairError(writer, status, code, message, category, retryable, details)
}
