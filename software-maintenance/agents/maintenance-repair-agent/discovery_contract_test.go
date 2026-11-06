package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/truvaagents/truva-g3-domain-examples/software-maintenance/internal/discoverytest"
	"github.com/truvaagents/truva-g3/core"
)

func TestRepairDiscoveryContract(t *testing.T) {
	agent := &maintenanceRepairAgent{BaseAgent: core.NewBaseAgent(repairAgentName)}
	agent.registerCapabilities()
	capability := discoverytest.Capability(t, agent.GetCapabilities(), "propose_maintenance_change")
	for _, status := range []string{"no_change", "inconclusive", "candidate_submitted"} {
		response := repairResponse{Status: status, Findings: []string{}, ToolsUsed: []string{}, ContractReferences: []string{}, Limitations: []string{}}
		discoverytest.Output(t, capability, response, "repair_profile_sha256", "confidence", "limitations")
		response.FailedOperations = map[string]json.RawMessage{"step": json.RawMessage(`{"view_status":"unavailable"}`)}
		discoverytest.Output(t, capability, response, "failed_operations")
	}
	catalog := discoverytest.Catalog(t, agent.GetCapabilities())
	for _, expected := range []string{"repair_profile_sha256:string", "confidence:number", "limitations:array", "single-line UTF-8", "embedded newlines or tabs"} {
		if !strings.Contains(catalog, expected) {
			t.Errorf("planner catalog lacks %q", expected)
		}
	}
}

func TestRepairObjectiveRestrictionsAndFeedback(t *testing.T) {
	token := strings.Repeat("r", 40)
	auth, err := newRepairAuthorizer("maintenance-agent", token)
	if err != nil {
		t.Fatal(err)
	}
	profile := repairProfile{RepairProfileID: "repair", Limits: repairLimits{MaximumRequestBytes: 4096, MaximumObjectiveBytes: 16}, callerSet: map[string]struct{}{"maintenance-agent": {}}}
	agent := &maintenanceRepairAgent{BaseAgent: core.NewBaseAgent(repairAgentName), auth: auth, profiles: &repairProfileStore{profiles: map[string]repairProfile{"repair": profile}}, now: time.Now}
	for _, tc := range []struct {
		name, objective string
		valid           bool
	}{
		{"at byte limit", strings.Repeat("é", 8), true},
		{"trimmed", "  investigate  ", true},
		{"over byte limit", strings.Repeat("é", 9), false},
		{"newline", "check\nagain", false},
		{"tab", "check\tagain", false},
		{"empty", "  ", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			payload, err := json.Marshal(repairRequest{ProfileID: "repair", CaseID: "case-1", ObservationID: "observation-1", SnapshotID: "src_" + strings.Repeat("a", 64), Objective: tc.objective})
			if err != nil {
				t.Fatal(err)
			}
			request := httptest.NewRequest(http.MethodPost, "/", bytes.NewReader(payload))
			request.Header.Set("Authorization", "Bearer "+token)
			response := httptest.NewRecorder()
			agent.handleRepair(response, request)
			if tc.valid {
				if response.Code != http.StatusServiceUnavailable || !strings.Contains(response.Body.String(), "ORCHESTRATOR_INITIALIZING") {
					t.Fatalf("valid objective rejected: %d %s", response.Code, response.Body.String())
				}
			} else if response.Code != http.StatusBadRequest || !strings.Contains(response.Body.String(), "within 16 bytes") || !strings.Contains(response.Body.String(), "INVALID_REPAIR_SCOPE") {
				t.Fatalf("missing bounded objective feedback: %d %s", response.Code, response.Body.String())
			}
		})
	}
}
