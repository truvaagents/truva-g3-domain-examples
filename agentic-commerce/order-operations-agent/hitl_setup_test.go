package main

import (
	"context"
	"testing"

	"github.com/truvaagents/truva-g3/core"
	"github.com/truvaagents/truva-g3/orchestration"
)

func TestDecisionAwarePolicyUsesDeterministicTokenClass(t *testing.T) {
	policy := newDecisionAwarePolicy(orchestration.HITLConfig{Enabled: true}, &core.NoOpLogger{})
	step := orchestration.RoutingStep{StepID: "execute", AgentName: "commerce-core", Metadata: map[string]any{"capability": "execute_recovery_command"}}
	plan := validApprovalRecoveryPlan(step)
	tests := []struct {
		name      string
		token     string
		interrupt bool
	}{
		{name: "bounded automatic", token: "automatic.abc", interrupt: false},
		{name: "business approval", token: "approval.abc", interrupt: true},
		{name: "missing token fails closed", token: "", interrupt: true},
		{name: "unknown token fails closed", token: "abc", interrupt: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			ctx := orchestration.WithResolvedParams(context.Background(), map[string]any{"decision_token": test.token})
			decision, err := policy.ShouldApproveBeforeStep(ctx, step, plan)
			if err != nil {
				t.Fatalf("ShouldApproveBeforeStep: %v", err)
			}
			if decision.ShouldInterrupt != test.interrupt {
				t.Fatalf("interrupt=%v, want %v: %+v", decision.ShouldInterrupt, test.interrupt, decision)
			}
		})
	}
}

func TestApprovalPolicyRejectsExecuteOnlyPlan(t *testing.T) {
	policy := newDecisionAwarePolicy(orchestration.HITLConfig{Enabled: true}, &core.NoOpLogger{})
	step := orchestration.RoutingStep{StepID: "execute", AgentName: "commerce-core", Metadata: map[string]any{"capability": "execute_recovery_command"}}
	ctx := orchestration.WithResolvedParams(context.Background(), map[string]any{"decision_token": "approval.abc"})
	if _, err := policy.ShouldApproveBeforeStep(ctx, step, &orchestration.RoutingPlan{Steps: []orchestration.RoutingStep{step}}); err == nil {
		t.Fatal("approval policy accepted an execute-only resumable plan")
	}
}

func TestApprovalPolicyRejectsNotificationWithoutDirectExecuteDependency(t *testing.T) {
	policy := newDecisionAwarePolicy(orchestration.HITLConfig{Enabled: true}, &core.NoOpLogger{})
	execute := orchestration.RoutingStep{StepID: "execute", AgentName: "commerce-core", Metadata: map[string]any{"capability": "execute_recovery_command"}}
	verify := orchestration.RoutingStep{StepID: "verify", DependsOn: []string{execute.StepID}, Metadata: map[string]any{"capability": "verify_order_outcome"}}
	notify := orchestration.RoutingStep{StepID: "notify", DependsOn: []string{verify.StepID}, Metadata: map[string]any{"capability": "send_synthetic_notification"}}
	plan := &orchestration.RoutingPlan{Steps: []orchestration.RoutingStep{execute, verify, notify}}
	ctx := orchestration.WithResolvedParams(context.Background(), map[string]any{"decision_token": "approval.abc"})
	if _, err := policy.ShouldApproveBeforeStep(ctx, execute, plan); err == nil {
		t.Fatal("approval policy accepted a notification that references execution without a direct dependency")
	}
}

func validApprovalRecoveryPlan(execute orchestration.RoutingStep) *orchestration.RoutingPlan {
	verify := orchestration.RoutingStep{StepID: "verify", DependsOn: []string{execute.StepID}, Metadata: map[string]any{"capability": "verify_order_outcome"}}
	notify := orchestration.RoutingStep{StepID: "notify", DependsOn: []string{execute.StepID, verify.StepID}, Metadata: map[string]any{"capability": "send_synthetic_notification"}}
	return &orchestration.RoutingPlan{Steps: []orchestration.RoutingStep{execute, verify, notify}}
}

func TestApprovalProofIsBoundToCheckpointAndDecision(t *testing.T) {
	key := []byte("0123456789abcdef0123456789abcdef")
	proof := approvalProof(key, "cp-1", "approval.token", "test-key-a")
	if proof == approvalProof(key, "cp-2", "approval.token", "test-key-a") {
		t.Fatal("approval proof is not bound to checkpoint")
	}
	if proof == approvalProof(key, "cp-1", "approval.other", "test-key-a") {
		t.Fatal("approval proof is not bound to decision token")
	}
	if proof == approvalProof(key, "cp-1", "approval.token", "test-key-b") {
		t.Fatal("approval proof is not bound to idempotency key")
	}
}
