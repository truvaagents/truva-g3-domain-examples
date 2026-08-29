package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"
)

type scenarioExpectationManifest struct {
	SchemaVersion int                            `json:"schema_version"`
	Scenarios     map[string]scenarioExpectation `json:"scenarios"`
}

type scenarioExpectation struct {
	Fixture                 string   `json:"fixture"`
	ExpectedTerminalOutcome string   `json:"expected_terminal_outcome"`
	ExpectedPolicyOutcome   string   `json:"expected_policy_outcome"`
	BusinessMutation        bool     `json:"business_mutation_expected"`
	RetrySameEvent          bool     `json:"retry_same_event"`
	ControlActions          []string `json:"control_actions"`
	ReservedLiveTest        bool     `json:"reserved_live_test"`
}

type scenarioEvent struct {
	EventID           string `json:"event_id"`
	OrderID           string `json:"order_id"`
	Signal            string `json:"signal"`
	RequestedOptionID string `json:"requested_option_id"`
	DecisionTime      string `json:"decision_time"`
}

func TestReservedLiveScenarioGroundworkIsComplete(t *testing.T) {
	scenarioRoot := filepath.Join("..", "scenarios")
	data, err := os.ReadFile(filepath.Join(scenarioRoot, "expected-results.json"))
	if err != nil {
		t.Fatalf("read expected results: %v", err)
	}
	var manifest scenarioExpectationManifest
	if err := json.Unmarshal(data, &manifest); err != nil {
		t.Fatalf("decode expected results: %v", err)
	}
	if manifest.SchemaVersion != 1 {
		t.Fatalf("schema_version=%d, want 1", manifest.SchemaVersion)
	}
	want := map[string]string{
		"conflicting-evidence":       terminalOutcomeConflicting,
		"policy-changed-during-hitl": terminalOutcomePolicyChanged,
		"policy-changed-reapproval":  terminalOutcomeApproval,
		"state-changed-during-hitl":  terminalOutcomeStateChanged,
		"alternate-domain-switching": "",
	}
	insufficient := manifest.Scenarios["insufficient-evidence"]
	if insufficient.ReservedLiveTest || insufficient.BusinessMutation || insufficient.ExpectedTerminalOutcome != terminalOutcomeInsufficient {
		t.Fatalf("default abstention metadata is unsafe: %+v", insufficient)
	}
	for name, terminal := range want {
		expectation, ok := manifest.Scenarios[name]
		if !ok || !expectation.ReservedLiveTest || expectation.BusinessMutation {
			t.Fatalf("scenario %q has incomplete safe reserved-live metadata: %+v", name, expectation)
		}
		if expectation.ExpectedTerminalOutcome != terminal {
			t.Fatalf("scenario %q terminal=%q, want %q", name, expectation.ExpectedTerminalOutcome, terminal)
		}
		if expectation.Fixture == "" {
			continue
		}
		fixtureData, err := os.ReadFile(filepath.Join(scenarioRoot, expectation.Fixture))
		if err != nil {
			t.Fatalf("read %s fixture: %v", name, err)
		}
		var event scenarioEvent
		if err := json.Unmarshal(fixtureData, &event); err != nil {
			t.Fatalf("decode %s fixture: %v", name, err)
		}
		if event.EventID == "" || event.OrderID == "" || event.Signal == "" || event.RequestedOptionID == "" {
			t.Fatalf("scenario %q has an incomplete event fixture: %+v", name, event)
		}
		if _, err := time.Parse(time.RFC3339, event.DecisionTime); err != nil {
			t.Fatalf("scenario %q decision_time: %v", name, err)
		}
	}
	if len(manifest.Scenarios["policy-changed-during-hitl"].ControlActions) != 2 {
		t.Fatal("policy-change scenario must switch both knowledge and enforcement releases")
	}
	if !manifest.Scenarios["state-changed-during-hitl"].RetrySameEvent || !manifest.Scenarios["policy-changed-during-hitl"].RetrySameEvent {
		t.Fatal("stale HITL scenarios must be retryable from current state")
	}
}
