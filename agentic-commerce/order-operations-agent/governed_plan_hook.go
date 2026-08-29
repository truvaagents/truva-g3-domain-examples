package main

import (
	"context"
	"fmt"
	"sync"

	"github.com/truvaagents/truva-g3/core"
	"github.com/truvaagents/truva-g3/orchestration"
)

// governedOrderPlanHook binds security-relevant request identity to trusted
// webhook metadata after LLM planning. The model still chooses the evidence
// workflow, while empty or altered order/option/time parameters cannot cross
// the final validated-plan boundary.
type governedPlanStateStore struct {
	mu       sync.RWMutex
	trackers map[string]*recoveryOutcomeTracker
}

func newGovernedPlanStateStore() *governedPlanStateStore {
	return &governedPlanStateStore{trackers: make(map[string]*recoveryOutcomeTracker)}
}

func (store *governedPlanStateStore) Register(taskID string, tracker *recoveryOutcomeTracker) {
	if store == nil || taskID == "" || tracker == nil {
		return
	}
	store.mu.Lock()
	store.trackers[taskID] = tracker
	store.mu.Unlock()
}

func (store *governedPlanStateStore) Remove(taskID string) {
	if store == nil || taskID == "" {
		return
	}
	store.mu.Lock()
	delete(store.trackers, taskID)
	store.mu.Unlock()
}

func (store *governedPlanStateStore) Tracker(taskID string) *recoveryOutcomeTracker {
	if store == nil || taskID == "" {
		return nil
	}
	store.mu.RLock()
	defer store.mu.RUnlock()
	return store.trackers[taskID]
}

type governedOrderPlanHook struct {
	states *governedPlanStateStore
}

func (*governedOrderPlanHook) Name() string { return "governed-order-plan-binding" }

func (h *governedOrderPlanHook) AfterPlanning(_ context.Context, pctx *core.PipelineContext, candidate any) (any, error) {
	plan, ok := candidate.(*orchestration.RoutingPlan)
	if !ok || plan == nil {
		return nil, fmt.Errorf("governed order plan must be a routing plan")
	}
	if pctx == nil {
		return nil, fmt.Errorf("governed order plan requires pipeline context")
	}
	orderID, _ := pctx.Metadata["order_id"].(string)
	if orderID == "" {
		return nil, fmt.Errorf("governed order plan requires trusted order_id metadata")
	}
	optionID, _ := pctx.Metadata["requested_option_id"].(string)
	decisionTime, _ := pctx.Metadata["decision_time"].(string)
	var evidenceChannel string
	if h != nil && h.states != nil {
		taskID, _ := pctx.Metadata["task_id"].(string)
		if tracker := h.states.Tracker(taskID); tracker != nil {
			evidenceChannel, _ = tracker.OrderChannel()
			if scopedChannel, ok := tracker.EvidenceChannel(optionID); ok {
				evidenceChannel = scopedChannel
			}
		}
	}

	for index := range plan.Steps {
		step := &plan.Steps[index]
		capability, _ := step.Metadata["capability"].(string)
		parameters, _ := step.Metadata["parameters"].(map[string]any)
		if parameters == nil {
			parameters = map[string]any{}
			if step.Metadata == nil {
				step.Metadata = map[string]any{}
			}
			step.Metadata["parameters"] = parameters
		}

		switch capability {
		case "get_order_context", "list_fulfillment_options", "evaluate_recovery_policy", "verify_order_outcome", "send_synthetic_notification":
			parameters["order_id"] = orderID
		}
		switch capability {
		case "search_evidence", "read_exact_section":
			if decisionTime != "" {
				parameters["as_of"] = decisionTime
			}
			// Evidence channel is an observed order fact, not a free-form
			// planning choice. Binding it after get_order_context prevents a
			// model-generated label such as "order_interaction" from filtering
			// out channel-specific policy sections.
			if evidenceChannel != "" {
				parameters["channel"] = evidenceChannel
			}
		case "traverse_relations":
			// The control-tower workflow only follows direct policy links. Bind
			// these deterministic tool bounds so an LLM cannot create a noisy
			// validation failure or an unexpectedly broad graph traversal.
			parameters["max_depth"] = 1
			parameters["limit"] = 20
		case "evaluate_recovery_policy":
			if optionID != "" {
				parameters["option_id"] = optionID
			}
			if decisionTime != "" {
				parameters["decision_time"] = decisionTime
			}
		}
	}
	h.enforceDeterministicTerminalPlan(pctx, plan)
	return plan, nil
}

func (h *governedOrderPlanHook) enforceDeterministicTerminalPlan(pctx *core.PipelineContext, plan *orchestration.RoutingPlan) {
	if h == nil || h.states == nil || pctx == nil || plan == nil {
		return
	}
	taskID, _ := pctx.Metadata["task_id"].(string)
	tracker := h.states.Tracker(taskID)
	if tracker == nil {
		return
	}
	basis, ok := tracker.ExecutionBasis()
	if !ok {
		return
	}
	switch basis.Decision.Outcome {
	case "denied", "insufficient_evidence", "conflicting_evidence":
		terminal := true
		plan.Terminal = &terminal
		plan.NeedsUserInput = nil
		plan.ContinuationNote = ""
		plan.Steps = nil
		return
	case "allowed", "approval_required":
		plan.Steps = governedRecoveryExecutionSteps(basis)
		terminal := true
		plan.Terminal = &terminal
		plan.NeedsUserInput = nil
		plan.ContinuationNote = ""
	}
}

func governedRecoveryExecutionSteps(basis recoveryExecutionBasis) []orchestration.RoutingStep {
	executeID := basis.PolicyStepID + "-governed-execute"
	verifyID := basis.PolicyStepID + "-governed-verify"
	notifyID := basis.PolicyStepID + "-governed-notify"
	idempotencyKey := fmt.Sprintf("%s:%s:v%d", basis.Decision.OrderID, basis.Decision.OptionID, basis.Decision.OrderVersion)
	return []orchestration.RoutingStep{
		{
			StepID: executeID, AgentName: "commerce-core", Namespace: "default",
			Instruction:  "Execute only the authoritative Commerce Core recovery decision. Omit approval_reference and approval_proof so the framework can interrupt and bind an approved resume.",
			ImplicitDeps: []string{basis.PolicyStepID},
			Metadata: map[string]any{"capability": "execute_recovery_command", "parameters": map[string]any{
				"decision_token":  fmt.Sprintf("{{%s.response.data.decision_token}}", basis.PolicyStepID),
				"idempotency_key": idempotencyKey,
			}},
		},
		{
			StepID: verifyID, AgentName: "commerce-core", Namespace: "default",
			Instruction: "Verify the recovery command's authoritative order-state postconditions and require verified=true before completion.",
			DependsOn:   []string{executeID},
			Metadata: map[string]any{"capability": "verify_order_outcome", "parameters": map[string]any{
				"order_id":   basis.Decision.OrderID,
				"command_id": fmt.Sprintf("{{%s.response.data.command_id}}", executeID),
			}},
		},
		{
			StepID: notifyID, AgentName: "commerce-core", Namespace: "default",
			Instruction: "Record a customer-safe synthetic notification only after the recovery command completes and its postconditions verify.",
			DependsOn:   []string{executeID, verifyID},
			Metadata: map[string]any{"capability": "send_synthetic_notification", "parameters": map[string]any{
				"order_id":   basis.Decision.OrderID,
				"command_id": fmt.Sprintf("{{%s.response.data.command_id}}", executeID),
				"channel":    basis.Channel,
				"message":    "Your governed order recovery was processed and verified. Updated fulfillment details are available in your order status.",
			}},
		},
	}
}
