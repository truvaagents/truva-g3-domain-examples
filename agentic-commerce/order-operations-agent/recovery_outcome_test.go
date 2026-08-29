package main

import (
	"testing"

	"github.com/truvaagents/truva-g3/orchestration"
)

func TestRecoveryOutcomeTrackerClassifiesDeterministicTerminalOutcomes(t *testing.T) {
	tests := []struct {
		policy   string
		terminal string
	}{
		{policy: "denied", terminal: terminalOutcomeDenied},
		{policy: "insufficient_evidence", terminal: terminalOutcomeInsufficient},
		{policy: "conflicting_evidence", terminal: terminalOutcomeConflicting},
		{policy: "approval_required", terminal: terminalOutcomeApproval},
	}
	for _, test := range tests {
		t.Run(test.policy, func(t *testing.T) {
			tracker := &recoveryOutcomeTracker{}
			tracker.Record(orchestration.RoutingStep{}, orchestration.StepResult{
				Capability: "evaluate_recovery_policy",
				Success:    true,
				Response:   `{"success":true,"data":{"outcome":"` + test.policy + `"}}`,
			})
			terminal, policy := tracker.Result()
			if terminal != test.terminal || policy != test.policy {
				t.Fatalf("terminal=%q policy=%q, want %q/%q", terminal, policy, test.terminal, test.policy)
			}
		})
	}
}

func TestIncompleteGovernedOutcomesFailClosed(t *testing.T) {
	if err := incompleteGovernedOutcomeError(terminalOutcomeNoMutation, "allowed"); err == nil {
		t.Fatal("allowed policy outcome completed without verified mutation")
	}
	if err := incompleteGovernedOutcomeError(terminalOutcomeApproval, "approval_required"); err == nil {
		t.Fatal("approval-required policy outcome completed without a checkpoint")
	}
	if err := incompleteGovernedOutcomeError(terminalOutcomeInsufficient, "insufficient_evidence"); err != nil {
		t.Fatalf("safe terminal insufficiency was rejected: %v", err)
	}
}

func TestRecoveryOutcomeTrackerClassifiesStaleHITLBasis(t *testing.T) {
	tests := []struct {
		name     string
		detail   string
		terminal string
	}{
		{name: "policy", detail: `component returned status 409: {"success":false,"error":{"code":"POLICY_BASIS_CHANGED"}}`, terminal: terminalOutcomePolicyChanged},
		{name: "state", detail: `component returned status 409: {"success":false,"error":{"code":"ORDER_STATE_CHANGED"}}`, terminal: terminalOutcomeStateChanged},
		{name: "expiry", detail: `component returned status 409: {"success":false,"error":{"code":"DECISION_BASIS_EXPIRED"}}`, terminal: terminalOutcomeBasisExpired},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			tracker := &recoveryOutcomeTracker{}
			tracker.Record(orchestration.RoutingStep{}, orchestration.StepResult{Capability: "evaluate_recovery_policy", Success: true, Response: `{"success":true,"data":{"outcome":"approval_required"}}`})
			tracker.Record(orchestration.RoutingStep{}, orchestration.StepResult{Capability: "execute_recovery_command", Success: false, Error: test.detail})
			terminal, policy := tracker.Result()
			if terminal != test.terminal || policy != "approval_required" || tracker.ResumeReason() != test.detail {
				t.Fatalf("terminal=%q policy=%q reason=%q", terminal, policy, tracker.ResumeReason())
			}
			if !isSafeStaleBasisOutcome(terminal) {
				t.Fatalf("%q was not recognized as a safe no-mutation stale-basis result", terminal)
			}
		})
	}
}

func TestApprovedResumeRequiresRestoredApprovalAndVerifiedRecovery(t *testing.T) {
	if err := incompleteApprovedResumeOutcomeError(terminalOutcomeRecovered, "approval_required"); err != nil {
		t.Fatalf("valid approved recovery was rejected: %v", err)
	}
	if err := incompleteApprovedResumeOutcomeError(terminalOutcomeRecovered, ""); err == nil {
		t.Fatal("approved resume accepted a missing pre-checkpoint policy outcome")
	}
	if err := incompleteApprovedResumeOutcomeError(terminalOutcomeUnverified, "approval_required"); err == nil {
		t.Fatal("approved resume accepted an unverified mutation")
	}
}

func TestRecoveryOutcomeTrackerRequiresVerifiedMutation(t *testing.T) {
	tracker := &recoveryOutcomeTracker{}
	tracker.Record(orchestration.RoutingStep{}, orchestration.StepResult{Capability: "execute_recovery_command", Success: true, Response: `{"success":true,"data":{"status":"completed"}}`})
	terminal, _ := tracker.Result()
	if terminal != terminalOutcomeUnverified {
		t.Fatalf("unverified command terminal=%q, want %q", terminal, terminalOutcomeUnverified)
	}
	tracker.Record(orchestration.RoutingStep{}, orchestration.StepResult{Capability: "verify_order_outcome", Success: true, Response: `{"success":true,"data":{"verified":true}}`})
	terminal, _ = tracker.Result()
	if terminal != terminalOutcomeRecovered {
		t.Fatalf("verified command terminal=%q, want %q", terminal, terminalOutcomeRecovered)
	}
}

func TestRecoveryOutcomeTrackerRestoresPreCheckpointPolicyDecision(t *testing.T) {
	tracker := &recoveryOutcomeTracker{}
	tracker.Restore(map[string]*orchestration.StepResult{
		"evaluate": {
			Capability: "evaluate_recovery_policy",
			Success:    true,
			Response:   `{"success":true,"data":{"outcome":"approval_required"}}`,
		},
	})
	tracker.Record(orchestration.RoutingStep{}, orchestration.StepResult{
		Capability: "execute_recovery_command", Success: true,
		Response: `{"success":true,"data":{"command_id":"cmd-1"}}`,
	})
	tracker.Record(orchestration.RoutingStep{}, orchestration.StepResult{
		Capability: "verify_order_outcome", Success: true,
		Response: `{"success":true,"data":{"verified":true}}`,
	})
	terminal, policy := tracker.Result()
	if terminal != terminalOutcomeRecovered || policy != "approval_required" {
		t.Fatalf("terminal=%q policy=%q", terminal, policy)
	}
}

func TestRecoveryOutcomeTrackerIgnoresFailedOrMalformedSteps(t *testing.T) {
	tracker := &recoveryOutcomeTracker{}
	tracker.Record(orchestration.RoutingStep{}, orchestration.StepResult{Capability: "evaluate_recovery_policy", Success: false, Response: `{"success":true,"data":{"outcome":"denied"}}`})
	tracker.Record(orchestration.RoutingStep{}, orchestration.StepResult{Capability: "evaluate_recovery_policy", Success: true, Response: `not-json`})
	terminal, policy := tracker.Result()
	if terminal != terminalOutcomeNoMutation || policy != "" {
		t.Fatalf("terminal=%q policy=%q, want empty no-mutation classification", terminal, policy)
	}
}
