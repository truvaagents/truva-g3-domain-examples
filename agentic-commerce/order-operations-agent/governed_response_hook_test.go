package main

import (
	"context"
	"strings"
	"testing"

	"github.com/truvaagents/truva-g3/core"
	"github.com/truvaagents/truva-g3/orchestration"
)

func TestGovernedResponseHookOverridesContradictoryModelNarrative(t *testing.T) {
	execution := &orchestration.ExecutionResult{Steps: []orchestration.StepResult{
		{Capability: "evaluate_recovery_policy", Success: true, Response: `{"success":true,"data":{"order_id":"ORD-1004","option_id":"same-sku-replacement","eligible":true,"approval_required":true,"outcome":"approval_required","reasons":["threshold requires approval"],"evidence_section_ids":["demo-auto-recovery-bounds","demo-approval-thresholds"],"release_id":"commerce-us-2026-08-20","release_hash":"sha256:test","order_version":1}}`},
		{Capability: "execute_recovery_command", Success: true, Response: `{"success":true,"data":{"command_id":"cmd-1","order_id":"ORD-1004","option_id":"same-sku-replacement","status":"completed","order_version":2,"approval_reference":"cp-1"}}`},
		{Capability: "verify_order_outcome", Success: true, Response: `{"success":true,"data":{"order_id":"ORD-1004","command_id":"cmd-1","verified":true,"fulfillment_status":"replacement_created","payment_status":"paid","order_version":2}}`},
		{Capability: "send_synthetic_notification", Success: true, Response: `{"success":true,"data":{"notification_id":"notice-1","order_id":"ORD-1004","command_id":"cmd-1","channel":"online"}}`},
	}}
	pctx := &core.PipelineContext{Enrichments: map[string]any{}}
	hook := &governedOrderResponseHook{}
	if err := hook.AfterExecution(context.Background(), pctx, execution); err != nil {
		t.Fatalf("AfterExecution: %v", err)
	}
	response, err := hook.AfterSynthesis(context.Background(), pctx, "incorrect model claim: insufficient_evidence")
	if err != nil {
		t.Fatalf("AfterSynthesis: %v", err)
	}
	for _, expected := range []string{"recovered", "approval_required", "cmd-1", "verified=true", "notice-1", "cp-1"} {
		if !strings.Contains(response, expected) {
			t.Fatalf("governed response %q does not contain %q", response, expected)
		}
	}
	if strings.Contains(response, "incorrect model claim") || strings.Contains(response, "insufficient_evidence") {
		t.Fatalf("contradictory model narrative escaped the governed response: %q", response)
	}
	audit, ok := pctx.Enrichments[governedAuditEnrichment].(map[string]any)
	if !ok || audit["verified"] != true || audit["command_executed"] != true {
		t.Fatalf("governed audit was not retained: %#v", pctx.Enrichments[governedAuditEnrichment])
	}
}
