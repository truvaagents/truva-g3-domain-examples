package main

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/truvaagents/truva-g3-domain-examples/software-maintenance/internal/discoverytest"
	"github.com/truvaagents/truva-g3/core"
)

func TestReviewDiscoveryContract(t *testing.T) {
	agent := &maintenanceReviewAgent{BaseAgent: core.NewBaseAgent(reviewAgentName)}
	agent.registerCapabilities()
	capability := discoverytest.Capability(t, agent.GetCapabilities(), "review_maintenance_change")
	for _, status := range []string{"approved", "rejected", "inconclusive"} {
		response := reviewResponse{Status: status, BlockingFindings: []string{}, ToolsUsed: []string{}}
		if status == "approved" {
			response.Review = &reviewReceipt{}
			response.ReviewID = "review-fixture"
		}
		discoverytest.Output(t, capability, response, "review", "review_id")
		response.FailedOperations = map[string]json.RawMessage{"step": json.RawMessage(`null`)}
		discoverytest.Output(t, capability, response, "failed_operations")
	}
	catalog := discoverytest.Catalog(t, agent.GetCapabilities())
	if !strings.Contains(catalog, "line breaks and tabs are allowed") {
		t.Fatal("planner lacks review text restrictions")
	}
}
