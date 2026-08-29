package main

import (
	"context"
	"fmt"

	"github.com/truvaagents/truva-g3/core"
	"github.com/truvaagents/truva-g3/orchestration"
)

const (
	governedResponseEnrichment = "governed_order_response"
	governedAuditEnrichment    = "governed_order_audit"
)

// governedOrderResponseHook replaces model-generated outcome narration with a
// deterministic projection of successful tool results. The LLM still plans and
// synthesizes for observability, but it cannot relabel Commerce Core's policy,
// command, verification, or notification outcome in the application response.
type governedOrderResponseHook struct{}

func (*governedOrderResponseHook) Name() string { return "governed-order-response" }

func (*governedOrderResponseHook) AfterExecution(_ context.Context, pctx *core.PipelineContext, results any) error {
	if pctx == nil {
		return fmt.Errorf("governed order response requires pipeline context")
	}
	execution, ok := results.(*orchestration.ExecutionResult)
	if !ok || execution == nil {
		return fmt.Errorf("governed order response requires execution results")
	}
	tracker := &recoveryOutcomeTracker{}
	for _, result := range execution.Steps {
		tracker.Record(orchestration.RoutingStep{}, result)
	}
	terminal, policy := tracker.Result()
	if pctx.Enrichments == nil {
		pctx.Enrichments = make(map[string]any)
	}
	pctx.Enrichments[governedResponseEnrichment] = tracker.DeterministicResponse(terminal, policy, tracker.ApprovalReference())
	pctx.Enrichments[governedAuditEnrichment] = tracker.AuditSummary()
	return nil
}

func (*governedOrderResponseHook) AfterSynthesis(_ context.Context, pctx *core.PipelineContext, _ string) (string, error) {
	if pctx == nil || pctx.Enrichments == nil {
		return "Governed outcome narration is unavailable; use the structured terminal_outcome, policy_outcome, and audit fields.", nil
	}
	response, _ := pctx.Enrichments[governedResponseEnrichment].(string)
	if response == "" {
		return "Governed outcome narration is unavailable; use the structured terminal_outcome, policy_outcome, and audit fields.", nil
	}
	return response, nil
}

var _ core.AfterExecutionHook = (*governedOrderResponseHook)(nil)
var _ core.AfterSynthesisHook = (*governedOrderResponseHook)(nil)
