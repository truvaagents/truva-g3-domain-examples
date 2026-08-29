package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/truvaagents/truva-g3/core"
)

func TestExecuteHandlerRejectsApprovalTokenWithoutRuntimeProof(t *testing.T) {
	store := newTestCommerceStore(t)
	decision := evaluateWithReceipts(t, store, "ORD-1004", "same-sku-replacement", "demo-auto-recovery-bounds", "demo-approval-thresholds")
	tool := &CommerceTool{BaseTool: core.NewTool("commerce-core-test"), store: store}
	body, _ := json.Marshal(map[string]string{"decision_token": decision.DecisionToken, "idempotency_key": "handler-high-value"})
	request := httptest.NewRequest(http.MethodPost, "/api/capabilities/execute_recovery_command", strings.NewReader(string(body)))
	response := httptest.NewRecorder()
	tool.handleExecute(response, request)
	if response.Code != http.StatusForbidden {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
}

func TestProcessNormalOrderHandler(t *testing.T) {
	tool := &CommerceTool{BaseTool: core.NewTool("commerce-core-test"), store: newTestCommerceStore(t)}
	request := httptest.NewRequest(http.MethodPost, "/api/capabilities/process_normal_order", strings.NewReader(`{"order_id":"ORD-1000"}`))
	response := httptest.NewRecorder()

	tool.handleProcessNormalOrder(response, request)

	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"terminal_outcome":"normal_order_confirmed"`) || !strings.Contains(response.Body.String(), `"llm_invoked":false`) {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
}

func TestEvaluateHandlerRequiresReceiptPairAndOneJSONObject(t *testing.T) {
	store := newTestCommerceStore(t)
	tool := &CommerceTool{BaseTool: core.NewTool("commerce-core-test"), store: store}
	body := `{"order_id":"ORD-1003","option_id":"same-sku-replacement","release_id":"commerce-us-2026-08-20","release_hash":"` + testReleaseHash + `","decision_time":"2026-08-20T18:00:00Z","evidence_section_ids":["demo-auto-recovery-bounds"],"evidence_receipts":[]}`
	request := httptest.NewRequest(http.MethodPost, "/api/capabilities/evaluate_recovery_policy", strings.NewReader(body))
	response := httptest.NewRecorder()
	tool.handleEvaluate(response, request)
	if response.Code != http.StatusBadRequest {
		t.Fatalf("missing receipt status=%d body=%s", response.Code, response.Body.String())
	}
	request = httptest.NewRequest(http.MethodPost, "/api/capabilities/get_order_context", strings.NewReader(`{"order_id":"ORD-1003"} {}`))
	response = httptest.NewRecorder()
	tool.handleGetOrder(response, request)
	if response.Code != http.StatusBadRequest {
		t.Fatalf("trailing JSON status=%d body=%s", response.Code, response.Body.String())
	}
}

func TestExecuteHandlerReturnsAuditableStaleBasisCodes(t *testing.T) {
	tests := []struct {
		name       string
		mutate     func(*CommerceStore)
		wantCode   string
		checkpoint string
	}{
		{
			name: "policy changed",
			mutate: func(store *CommerceStore) {
				_, _ = store.ManageDemoState(t.Context(), demoStateRequest{Action: demoActionActivatePolicyRelease, ReleaseID: "commerce-us-2026-08-20-policy-change"})
			},
			wantCode: "POLICY_BASIS_CHANGED", checkpoint: "handler-policy-change",
		},
		{
			name: "order state changed",
			mutate: func(store *CommerceStore) {
				_, _ = store.ManageDemoState(t.Context(), demoStateRequest{Action: demoActionSetInventoryUnavailable, OrderID: "ORD-1004", ExpectedVersion: 1})
			},
			wantCode: "ORDER_STATE_CHANGED", checkpoint: "handler-state-change",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			store := newTestCommerceStore(t)
			decision := evaluateWithReceipts(t, store, "ORD-1004", "same-sku-replacement", "demo-auto-recovery-bounds", "demo-approval-thresholds")
			test.mutate(store)
			idempotencyKey := "execute-" + test.checkpoint
			body, _ := json.Marshal(map[string]string{
				"decision_token": decision.DecisionToken, "idempotency_key": idempotencyKey,
				"approval_reference": test.checkpoint, "approval_proof": signTestApproval(test.checkpoint, decision.DecisionToken, idempotencyKey),
			})
			tool := &CommerceTool{BaseTool: core.NewTool("commerce-core-test"), store: store}
			response := httptest.NewRecorder()
			tool.handleExecute(response, httptest.NewRequest(http.MethodPost, "/api/capabilities/execute_recovery_command", strings.NewReader(string(body))))
			if response.Code != http.StatusConflict {
				t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
			}
			var envelope core.ToolResponse
			if err := json.Unmarshal(response.Body.Bytes(), &envelope); err != nil || envelope.Error == nil || envelope.Error.Code != test.wantCode {
				t.Fatalf("envelope=%+v err=%v, want code %s", envelope, err, test.wantCode)
			}
		})
	}
}
