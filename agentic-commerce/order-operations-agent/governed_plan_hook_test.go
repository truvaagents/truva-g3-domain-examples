package main

import (
	"context"
	"testing"

	"github.com/truvaagents/truva-g3/core"
	"github.com/truvaagents/truva-g3/orchestration"
)

func TestGovernedOrderPlanHookBindsTrustedRequestIdentity(t *testing.T) {
	states := newGovernedPlanStateStore()
	tracker := &recoveryOutcomeTracker{}
	tracker.Record(orchestration.RoutingStep{StepID: "get"}, orchestration.StepResult{
		StepID: "get", Capability: "get_order_context", Success: true,
		Response: `{"success":true,"data":{"order_id":"ORD-1004","channel":"marketplace","version":1}}`,
	})
	tracker.Record(orchestration.RoutingStep{StepID: "options"}, orchestration.StepResult{
		StepID: "options", Capability: "list_fulfillment_options", Success: true,
		Response: `{"success":true,"data":{"options":[{"option_id":"carrier-service-claim","evidence_channel":"shipping"}]}}`,
	})
	states.Register("task-bind", tracker)
	plan := &orchestration.RoutingPlan{Steps: []orchestration.RoutingStep{
		{StepID: "get", Metadata: map[string]any{"capability": "get_order_context", "parameters": map[string]any{"order_id": "ORD-ATTACK"}}},
		{StepID: "search", Metadata: map[string]any{"capability": "search_evidence", "parameters": map[string]any{"as_of": "2099-01-01T00:00:00Z", "channel": "order_interaction"}}},
		{StepID: "traverse", Metadata: map[string]any{"capability": "traverse_relations", "parameters": map[string]any{"max_depth": 99, "limit": 999}}},
		{StepID: "evaluate", Metadata: map[string]any{"capability": "evaluate_recovery_policy", "parameters": map[string]any{"order_id": "", "option_id": "refund-merchandise", "decision_time": ""}}},
		{StepID: "verify", Metadata: map[string]any{"capability": "verify_order_outcome", "parameters": map[string]any{"order_id": "ORD-ATTACK"}}},
	}}
	pctx := &core.PipelineContext{Metadata: map[string]any{
		"task_id": "task-bind", "order_id": "ORD-1004", "requested_option_id": "same-sku-replacement", "decision_time": "2026-08-20T18:00:00Z",
	}}

	mutated, err := (&governedOrderPlanHook{states: states}).AfterPlanning(context.Background(), pctx, plan)
	if err != nil {
		t.Fatalf("AfterPlanning: %v", err)
	}
	result := mutated.(*orchestration.RoutingPlan)
	assertParameter := func(step int, name string, want any) {
		t.Helper()
		parameters := result.Steps[step].Metadata["parameters"].(map[string]any)
		if got := parameters[name]; got != want {
			t.Fatalf("step %d parameter %s=%v, want %v", step, name, got, want)
		}
	}
	assertParameter(0, "order_id", "ORD-1004")
	assertParameter(1, "as_of", "2026-08-20T18:00:00Z")
	assertParameter(1, "channel", "marketplace")
	assertParameter(2, "max_depth", 1)
	assertParameter(2, "limit", 20)
	assertParameter(3, "order_id", "ORD-1004")
	assertParameter(3, "option_id", "same-sku-replacement")
	assertParameter(3, "decision_time", "2026-08-20T18:00:00Z")
	assertParameter(4, "order_id", "ORD-1004")
}

func TestGovernedOrderPlanHookUsesOptionEvidenceChannel(t *testing.T) {
	states := newGovernedPlanStateStore()
	tracker := &recoveryOutcomeTracker{}
	tracker.Record(orchestration.RoutingStep{}, orchestration.StepResult{
		Capability: "get_order_context", Success: true,
		Response: `{"success":true,"data":{"order_id":"ORD-1004","channel":"online","version":1}}`,
	})
	tracker.Record(orchestration.RoutingStep{}, orchestration.StepResult{
		Capability: "list_fulfillment_options", Success: true,
		Response: `{"success":true,"data":{"options":[{"option_id":"carrier-service-claim","evidence_channel":"shipping"}]}}`,
	})
	states.Register("task-carrier", tracker)
	plan := &orchestration.RoutingPlan{Steps: []orchestration.RoutingStep{{
		StepID: "search", Metadata: map[string]any{"capability": "search_evidence", "parameters": map[string]any{"channel": "online"}},
	}}}
	pctx := &core.PipelineContext{Metadata: map[string]any{
		"task_id": "task-carrier", "order_id": "ORD-1004", "requested_option_id": "carrier-service-claim", "decision_time": "2026-08-20T18:00:00Z",
	}}

	mutated, err := (&governedOrderPlanHook{states: states}).AfterPlanning(context.Background(), pctx, plan)
	if err != nil {
		t.Fatalf("AfterPlanning: %v", err)
	}
	parameters := mutated.(*orchestration.RoutingPlan).Steps[0].Metadata["parameters"].(map[string]any)
	if got := parameters["channel"]; got != "shipping" {
		t.Fatalf("search channel=%v, want shipping", got)
	}
}

func TestGovernedOrderPlanHookRequiresTrustedOrderIdentity(t *testing.T) {
	_, err := (&governedOrderPlanHook{}).AfterPlanning(context.Background(), &core.PipelineContext{Metadata: map[string]any{}}, &orchestration.RoutingPlan{})
	if err == nil {
		t.Fatal("hook accepted a plan without trusted order metadata")
	}
}

func TestGovernedOrderPlanHookInjectsCompleteExecutionChainAfterApprovalDecision(t *testing.T) {
	states := newGovernedPlanStateStore()
	tracker := &recoveryOutcomeTracker{}
	tracker.Record(orchestration.RoutingStep{StepID: "evaluate"}, orchestration.StepResult{
		StepID: "evaluate", Capability: "evaluate_recovery_policy", Success: true,
		Response: `{"success":true,"data":{"order_id":"ORD-1004","option_id":"same-sku-replacement","order_version":1,"outcome":"approval_required","eligible":true,"approval_required":true}}`,
	})
	states.Register("task-1", tracker)
	terminal := true
	plan := &orchestration.RoutingPlan{PlanID: "model-terminal", Terminal: &terminal, Steps: nil}
	pctx := &core.PipelineContext{Metadata: map[string]any{
		"task_id": "task-1", "order_id": "ORD-1004", "requested_option_id": "same-sku-replacement", "decision_time": "2026-08-20T18:00:00Z",
	}}

	mutated, err := (&governedOrderPlanHook{states: states}).AfterPlanning(context.Background(), pctx, plan)
	if err != nil {
		t.Fatalf("AfterPlanning: %v", err)
	}
	steps := mutated.(*orchestration.RoutingPlan).Steps
	if len(steps) != 3 {
		t.Fatalf("governed execution steps=%d, want 3", len(steps))
	}
	if got := steps[0].Metadata["capability"]; got != "execute_recovery_command" {
		t.Fatalf("first capability=%v", got)
	}
	if got := steps[0].Metadata["parameters"].(map[string]any)["decision_token"]; got != "{{evaluate.response.data.decision_token}}" {
		t.Fatalf("decision token template=%v", got)
	}
	if len(steps[1].DependsOn) != 1 || steps[1].DependsOn[0] != steps[0].StepID {
		t.Fatalf("verification dependencies=%v", steps[1].DependsOn)
	}
	if len(steps[2].DependsOn) != 2 || steps[2].DependsOn[0] != steps[0].StepID || steps[2].DependsOn[1] != steps[1].StepID {
		t.Fatalf("notification dependencies=%v", steps[2].DependsOn)
	}
}

func TestGovernedOrderPlanHookStripsMutationAfterDeniedDecision(t *testing.T) {
	states := newGovernedPlanStateStore()
	tracker := &recoveryOutcomeTracker{}
	tracker.Record(orchestration.RoutingStep{StepID: "evaluate"}, orchestration.StepResult{
		StepID: "evaluate", Capability: "evaluate_recovery_policy", Success: true,
		Response: `{"success":true,"data":{"order_id":"ORD-1005","option_id":"same-sku-replacement","order_version":1,"outcome":"denied","eligible":false,"approval_required":false}}`,
	})
	states.Register("task-2", tracker)
	terminal := false
	plan := &orchestration.RoutingPlan{PlanID: "unsafe-model-plan", Terminal: &terminal, Steps: []orchestration.RoutingStep{{
		StepID: "execute", AgentName: "commerce-core", Metadata: map[string]any{"capability": "execute_recovery_command", "parameters": map[string]any{}},
	}}}
	pctx := &core.PipelineContext{Metadata: map[string]any{
		"task_id": "task-2", "order_id": "ORD-1005", "requested_option_id": "same-sku-replacement", "decision_time": "2026-08-20T18:00:00Z",
	}}

	mutated, err := (&governedOrderPlanHook{states: states}).AfterPlanning(context.Background(), pctx, plan)
	if err != nil {
		t.Fatalf("AfterPlanning: %v", err)
	}
	result := mutated.(*orchestration.RoutingPlan)
	if len(result.Steps) != 0 || !result.IsTerminal() {
		t.Fatalf("denied plan was not forced to a no-mutation terminal: %+v", result)
	}
}
