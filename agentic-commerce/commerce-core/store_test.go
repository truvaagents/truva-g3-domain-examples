package main

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"
)

const (
	testControlKey  = "test-control-key-that-is-at-least-32-characters-long"
	testReleaseHash = "sha256:49643f3bb48d35aa04ea786ab1b0205475eadcbfd247cd74cc8c55a4a5153d28"
)

var testNow = time.Date(2026, 8, 20, 18, 0, 0, 0, time.UTC)

func newTestCommerceStore(t *testing.T) *CommerceStore {
	t.Helper()
	store, err := LoadCommerceStore("data/orders.json", testControlKey)
	if err != nil {
		t.Fatalf("LoadCommerceStore: %v", err)
	}
	store.now = func() time.Time { return testNow }
	return store
}

func evaluateWithReceipts(t *testing.T, store *CommerceStore, orderID, optionID string, sections ...string) PolicyDecision {
	t.Helper()
	order, err := store.GetOrder(context.Background(), orderID)
	if err != nil {
		t.Fatalf("GetOrder: %v", err)
	}
	receipts := make([]string, 0, len(sections))
	for _, sectionID := range sections {
		receipts = append(receipts, signTestReceipt(t, evidenceReceiptClaims{
			Version: 1, SectionID: sectionID, ReleaseID: "commerce-us-2026-08-20", ReleaseHash: testReleaseHash,
			TenantID: order.TenantID, Jurisdiction: order.Jurisdiction, Channel: order.Channel, AsOf: testNow,
			IssuedAt: testNow.Add(-time.Minute), ExpiresAt: testNow.Add(time.Hour),
		}))
	}
	decision, err := store.Evaluate(context.Background(), orderID, optionID, "commerce-us-2026-08-20", testReleaseHash, testNow, sections, receipts)
	if err != nil {
		t.Fatalf("Evaluate: %v", err)
	}
	return decision
}

func TestCarrierClaimDeclaresShippingEvidenceScope(t *testing.T) {
	store := newTestCommerceStore(t)
	options, err := store.ListOptions(context.Background(), "ORD-1004")
	if err != nil {
		t.Fatalf("ListOptions: %v", err)
	}
	for _, option := range options {
		if option.OptionID == "carrier-service-claim" {
			if option.EvidenceChannel != "shipping" {
				t.Fatalf("carrier evidence channel=%q, want shipping", option.EvidenceChannel)
			}
			return
		}
	}
	t.Fatal("carrier-service-claim option not returned")
}

func TestNormalOrderUsesDeterministicPathWithoutMutation(t *testing.T) {
	store := newTestCommerceStore(t)
	before, err := store.GetOrder(context.Background(), "ORD-1000")
	if err != nil {
		t.Fatal(err)
	}
	result, err := store.ProcessNormalOrder(context.Background(), "ORD-1000")
	if err != nil {
		t.Fatalf("ProcessNormalOrder: %v", err)
	}
	after, _ := store.GetOrder(context.Background(), "ORD-1000")
	if result.TerminalOutcome != "normal_order_confirmed" || !result.DeterministicPath || result.AgentInvoked || result.LLMInvoked {
		t.Fatalf("normal result = %+v", result)
	}
	if before != after || after.Version != 1 || after.FulfillmentStatus != "confirmed" || !after.InventoryReserved {
		t.Fatalf("normal path changed authoritative state: before=%+v after=%+v", before, after)
	}
	if _, err := store.ProcessNormalOrder(context.Background(), "ORD-1003"); err == nil {
		t.Fatal("exception order was accepted by the normal path")
	}
}

func TestElectronicsExceptionRejectsDay35Refund(t *testing.T) {
	store := newTestCommerceStore(t)
	decision := evaluateWithReceipts(t, store, "ORD-1002", "refund-merchandise", "target-general-return-window", "target-electronics-return-window")
	if decision.Eligible {
		t.Fatalf("day-35 electronics return must be ineligible: %+v", decision)
	}
	if _, err := store.Execute(context.Background(), decision.DecisionToken, "return-day35", "", ""); !errors.Is(err, errNotEligible) {
		t.Fatalf("ineligible decision executed: %v", err)
	}
}

func TestReplacementCommandIsIdempotentAndVerifiable(t *testing.T) {
	store := newTestCommerceStore(t)
	decision := evaluateWithReceipts(t, store, "ORD-1003", "same-sku-replacement", "demo-auto-recovery-bounds")
	if !decision.Eligible || decision.ApprovalRequired || !strings.HasPrefix(decision.DecisionToken, "automatic.") {
		t.Fatalf("expected bounded automatic replacement: %+v", decision)
	}
	key := "ORD-1003:same-sku-replacement:v1"
	first, err := store.Execute(context.Background(), decision.DecisionToken, key, "", "")
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	second, err := store.Execute(context.Background(), decision.DecisionToken, key, "", "")
	if err != nil {
		t.Fatalf("Execute replay: %v", err)
	}
	if first.CommandID != second.CommandID || !second.Replayed {
		t.Fatalf("idempotency mismatch: first=%+v second=%+v", first, second)
	}
	if _, err := store.Execute(context.Background(), "automatic.a-different-decision", key, "", ""); !errors.Is(err, errIdempotencyConflict) {
		t.Fatalf("expected idempotency conflict, got %v", err)
	}
	verification, err := store.Verify(context.Background(), "ORD-1003", first.CommandID)
	if err != nil || verification["verified"] != true || verification["fulfillment_status"] != "replacement_created" {
		t.Fatalf("Verify: result=%+v err=%v", verification, err)
	}
	order, _ := store.GetOrder(context.Background(), "ORD-1003")
	if order.InventoryAvailable {
		t.Fatal("replacement must reserve the available inventory")
	}
	if _, err := store.Notify(context.Background(), "ORD-1003", first.CommandID, "email", "Replacement created"); err != nil {
		t.Fatalf("Notify: %v", err)
	}
}

func TestHighValueReplacementRequiresVerifiedApproval(t *testing.T) {
	store := newTestCommerceStore(t)
	decision := evaluateWithReceipts(t, store, "ORD-1004", "same-sku-replacement", "demo-auto-recovery-bounds", "demo-approval-thresholds")
	if !decision.Eligible || !decision.ApprovalRequired || !strings.HasPrefix(decision.DecisionToken, "approval.") {
		t.Fatalf("expected eligible decision with approval: %+v", decision)
	}
	if _, err := store.Execute(context.Background(), decision.DecisionToken, "high-value", "", ""); !errors.Is(err, errApprovalRequired) {
		t.Fatalf("direct execution must fail without approval proof: %v", err)
	}
	checkpointID := "checkpoint-approved-123"
	proof := signTestApproval(checkpointID, decision.DecisionToken, "high-value")
	if _, err := store.Execute(context.Background(), decision.DecisionToken, "high-value", "different-checkpoint", proof); !errors.Is(err, errApprovalRequired) {
		t.Fatalf("approval proof must be bound to the checkpoint and decision: %v", err)
	}
	if _, err := store.Execute(context.Background(), decision.DecisionToken, "high-value-other", checkpointID, proof); !errors.Is(err, errApprovalRequired) {
		t.Fatalf("approval proof must be bound to the idempotency key: %v", err)
	}
	store.now = func() time.Time { return testNow.Add(20 * time.Minute) }
	if _, err := store.Execute(context.Background(), decision.DecisionToken, "high-value", checkpointID, proof); !errors.Is(err, errDecisionStale) {
		t.Fatalf("approval must not bypass decision-token expiry: %v", err)
	}
	store.now = func() time.Time { return testNow }
	approved, err := store.Execute(context.Background(), decision.DecisionToken, "high-value", checkpointID, proof)
	if err != nil {
		t.Fatalf("approved execution failed: %v", err)
	}
	if approved.ApprovalReference != checkpointID {
		t.Fatalf("approval_reference=%q, want %q", approved.ApprovalReference, checkpointID)
	}
}

func TestFailClosedPolicies(t *testing.T) {
	store := newTestCommerceStore(t)
	wait := evaluateWithReceipts(t, store, "ORD-1001", "wait-for-current-fulfillment", "ftc-delay-consent-or-refund")
	if wait.Eligible {
		t.Fatalf("wait must not be eligible without affirmative delay consent: %+v", wait)
	}
	carrier := evaluateWithReceipts(t, store, "ORD-1004", "carrier-service-claim", "fedex-guarantee-service-scope")
	if carrier.Eligible {
		t.Fatalf("incomplete FedEx dimensions must deny safely: %+v", carrier)
	}
	refund := evaluateWithReceipts(t, store, "ORD-1003", "refund-merchandise", "usps-priority-express-eligibility")
	if refund.Eligible {
		t.Fatalf("carrier postage policy must not authorize a merchandise refund: %+v", refund)
	}
	highValueWithoutThreshold := evaluateWithReceipts(t, store, "ORD-1004", "same-sku-replacement", "demo-auto-recovery-bounds")
	if highValueWithoutThreshold.Eligible || highValueWithoutThreshold.ApprovalRequired {
		t.Fatalf("approval classification must itself be evidence-grounded: %+v", highValueWithoutThreshold)
	}
	if highValueWithoutThreshold.Outcome != PolicyOutcomeInsufficientEvidence {
		t.Fatalf("missing approval evidence outcome = %q, want %q", highValueWithoutThreshold.Outcome, PolicyOutcomeInsufficientEvidence)
	}
}

func TestPolicyOutcomeClassifications(t *testing.T) {
	tests := []struct {
		name     string
		orderID  string
		optionID string
		sections []string
		outcome  PolicyOutcome
	}{
		{name: "deterministic denial", orderID: "ORD-1005", optionID: "same-sku-replacement", sections: []string{"demo-auto-recovery-bounds"}, outcome: PolicyOutcomeDenied},
		{name: "insufficient carrier facts", orderID: "ORD-1004", optionID: "carrier-service-claim", sections: []string{"fedex-guarantee-service-scope"}, outcome: PolicyOutcomeInsufficientEvidence},
		{name: "unresolved evidence conflict", orderID: "ORD-1006", optionID: "same-sku-replacement", sections: []string{"demo-replacement-conflict-allow", "demo-replacement-conflict-deny"}, outcome: PolicyOutcomeConflictingEvidence},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			store := newTestCommerceStore(t)
			decision := evaluateWithReceipts(t, store, test.orderID, test.optionID, test.sections...)
			if decision.Outcome != test.outcome || decision.Eligible || decision.ApprovalRequired {
				t.Fatalf("decision = %+v, want terminal outcome %q", decision, test.outcome)
			}
			if _, err := store.Execute(context.Background(), decision.DecisionToken, test.name, "", ""); !errors.Is(err, errNotEligible) {
				t.Fatalf("terminal decision executed: %v", err)
			}
		})
	}
}

func TestExecutionRejectsChangedPolicyRelease(t *testing.T) {
	store := newTestCommerceStore(t)
	decision := evaluateWithReceipts(t, store, "ORD-1004", "same-sku-replacement", "demo-auto-recovery-bounds", "demo-approval-thresholds")
	if _, err := store.ManageDemoState(context.Background(), demoStateRequest{Action: demoActionActivatePolicyRelease, ReleaseID: "commerce-us-2026-08-20-policy-change"}); err != nil {
		t.Fatalf("activate changed release: %v", err)
	}
	proof := signTestApproval("approved-policy-change", decision.DecisionToken, "policy-change")
	if _, err := store.Execute(context.Background(), decision.DecisionToken, "policy-change", "approved-policy-change", proof); !errors.Is(err, errDecisionStale) || !errors.Is(err, errPolicyBasisChanged) {
		t.Fatalf("old policy decision was not invalidated: %v", err)
	}
	store.now = func() time.Time { return testNow.Add(2 * time.Hour) }
	if _, err := store.Execute(context.Background(), decision.DecisionToken, "expired-policy-change", "approved-policy-change", signTestApproval("approved-policy-change", decision.DecisionToken, "expired-policy-change")); !errors.Is(err, errPolicyBasisChanged) {
		t.Fatalf("policy race must remain the primary replan reason after expiry: %v", err)
	}
}

func TestChangedPolicyStillRequiresFreshApprovalAboveNewThreshold(t *testing.T) {
	store := newTestCommerceStore(t)
	if _, err := store.ManageDemoState(context.Background(), demoStateRequest{Action: demoActionActivatePolicyRelease, ReleaseID: "commerce-us-2026-08-20-policy-change"}); err != nil {
		t.Fatalf("activate changed release: %v", err)
	}
	order, err := store.GetOrder(context.Background(), "ORD-1007")
	if err != nil {
		t.Fatal(err)
	}
	sections := []string{"demo-auto-recovery-bounds", "demo-approval-thresholds"}
	receipts := make([]string, 0, len(sections))
	for _, sectionID := range sections {
		receipts = append(receipts, signTestReceipt(t, evidenceReceiptClaims{
			Version: 1, SectionID: sectionID, ReleaseID: "commerce-us-2026-08-20-policy-change",
			ReleaseHash: "sha256:460d1f0d7f89d2a649c7a94103ef9ba48f2474fca7f84ad50533d9eec48c2159",
			TenantID:    order.TenantID, Jurisdiction: order.Jurisdiction, Channel: order.Channel, AsOf: testNow,
			IssuedAt: testNow.Add(-time.Minute), ExpiresAt: testNow.Add(time.Hour),
		}))
	}
	decision, err := store.Evaluate(
		context.Background(), order.OrderID, "same-sku-replacement", "commerce-us-2026-08-20-policy-change",
		"sha256:460d1f0d7f89d2a649c7a94103ef9ba48f2474fca7f84ad50533d9eec48c2159", testNow, sections, receipts,
	)
	if err != nil {
		t.Fatalf("evaluate changed policy: %v", err)
	}
	if decision.Outcome != PolicyOutcomeApprovalRequired || !decision.ApprovalRequired || !strings.HasPrefix(decision.DecisionToken, "approval.") {
		t.Fatalf("changed-policy decision did not require fresh approval: %+v", decision)
	}
}

func TestDemoStateMutationAndReset(t *testing.T) {
	store := newTestCommerceStore(t)
	decision := evaluateWithReceipts(t, store, "ORD-1004", "same-sku-replacement", "demo-auto-recovery-bounds", "demo-approval-thresholds")
	state, err := store.ManageDemoState(context.Background(), demoStateRequest{Action: demoActionSetInventoryUnavailable, OrderID: "ORD-1004", ExpectedVersion: 1})
	if err != nil || state.Order == nil || state.Order.Version != 2 || state.Order.InventoryAvailable {
		t.Fatalf("state mutation = %+v, err=%v", state, err)
	}
	proof := signTestApproval("approved-state-change", decision.DecisionToken, "state-change")
	if _, err := store.Execute(context.Background(), decision.DecisionToken, "state-change", "approved-state-change", proof); !errors.Is(err, errDecisionStale) || !errors.Is(err, errOrderStateChanged) {
		t.Fatalf("old state decision was not invalidated: %v", err)
	}
	store.now = func() time.Time { return testNow.Add(2 * time.Hour) }
	expiredProof := signTestApproval("approved-state-change", decision.DecisionToken, "expired-state-change")
	if _, err := store.Execute(context.Background(), decision.DecisionToken, "expired-state-change", "approved-state-change", expiredProof); !errors.Is(err, errOrderStateChanged) {
		t.Fatalf("state race must remain the primary replan reason after expiry: %v", err)
	}
	store.now = func() time.Time { return testNow }
	reset, err := store.ManageDemoState(context.Background(), demoStateRequest{Action: demoActionReset})
	if err != nil || reset.ActivePolicy.ReleaseID != "commerce-us-2026-08-20" {
		t.Fatalf("reset = %+v, err=%v", reset, err)
	}
	order, err := store.GetOrder(context.Background(), "ORD-1004")
	if err != nil || order.Version != 1 || !order.InventoryAvailable {
		t.Fatalf("reset order = %+v, err=%v", order, err)
	}
}

func TestActionSpecificRecoveryTransitions(t *testing.T) {
	tests := []struct {
		name               string
		orderID            string
		optionID           string
		sections           []string
		fulfillment        string
		payment            string
		carrierClaimStatus string
	}{
		{name: "cancel and refund", orderID: "ORD-1001", optionID: "cancel-and-refund", sections: []string{"ftc-delay-consent-or-refund"}, fulfillment: "cancelled", payment: "refunded"},
		{name: "missing merchandise refund", orderID: "ORD-1003", optionID: "refund-merchandise", sections: []string{"demo-missing-merchandise-refund"}, fulfillment: "resolved_refund", payment: "refunded"},
		{name: "USPS service claim", orderID: "ORD-1003", optionID: "carrier-service-claim", sections: []string{"usps-priority-express-eligibility", "usps-priority-express-claim-window"}, fulfillment: "missing_in_transit", payment: "paid", carrierClaimStatus: "filed"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			store := newTestCommerceStore(t)
			decision := evaluateWithReceipts(t, store, test.orderID, test.optionID, test.sections...)
			if !decision.Eligible || decision.ApprovalRequired {
				t.Fatalf("expected automatic eligible decision: %+v", decision)
			}
			result, err := store.Execute(context.Background(), decision.DecisionToken, test.name, "", "")
			if err != nil {
				t.Fatalf("Execute: %v", err)
			}
			verification, err := store.Verify(context.Background(), test.orderID, result.CommandID)
			if err != nil || verification["verified"] != true || verification["fulfillment_status"] != test.fulfillment || verification["payment_status"] != test.payment || verification["carrier_claim_status"] != test.carrierClaimStatus {
				t.Fatalf("unexpected postconditions: result=%+v err=%v", verification, err)
			}
		})
	}
}

func TestExecutionRejectsChangedOrderAndExpiredEvidence(t *testing.T) {
	store := newTestCommerceStore(t)
	decision := evaluateWithReceipts(t, store, "ORD-1003", "same-sku-replacement", "demo-auto-recovery-bounds")
	store.mu.Lock()
	order := store.orders["ORD-1003"]
	order.Version++
	store.orders["ORD-1003"] = order
	store.mu.Unlock()
	if _, err := store.Execute(context.Background(), decision.DecisionToken, "changed-order", "", ""); !errors.Is(err, errDecisionStale) {
		t.Fatalf("expected changed-order rejection, got %v", err)
	}

	store = newTestCommerceStore(t)
	decision = evaluateWithReceipts(t, store, "ORD-1003", "same-sku-replacement", "demo-auto-recovery-bounds")
	store.now = func() time.Time { return testNow.Add(2 * time.Hour) }
	if _, err := store.Execute(context.Background(), decision.DecisionToken, "expired-evidence", "", ""); !errors.Is(err, errDecisionStale) {
		t.Fatalf("expected expired evidence rejection, got %v", err)
	}
}

func TestPolicyDecisionRejectsUnverifiedEvidence(t *testing.T) {
	store := newTestCommerceStore(t)
	if _, err := store.Evaluate(context.Background(), "ORD-1003", "same-sku-replacement", "commerce-us-2026-08-20", "sha256:invented", testNow, []string{"demo-auto-recovery-bounds"}, []string{"invented"}); !errors.Is(err, errReleaseMismatch) {
		t.Fatalf("expected release mismatch, got %v", err)
	}
	if _, err := store.Evaluate(context.Background(), "ORD-1003", "same-sku-replacement", "commerce-us-2026-08-20", testReleaseHash, testNow, []string{"invented-section"}, []string{"invented"}); !errors.Is(err, errUnknownEvidence) {
		t.Fatalf("expected unknown evidence, got %v", err)
	}
	valid := signTestReceipt(t, evidenceReceiptClaims{Version: 1, SectionID: "demo-auto-recovery-bounds", ReleaseID: "commerce-us-2026-08-20", ReleaseHash: testReleaseHash, TenantID: "commerce-demo", Jurisdiction: "US", Channel: "online", AsOf: testNow, IssuedAt: testNow.Add(-time.Minute), ExpiresAt: testNow.Add(time.Hour)})
	tamperSuffix := "0"
	if strings.HasSuffix(valid, tamperSuffix) {
		tamperSuffix = "1"
	}
	tampered := valid[:len(valid)-1] + tamperSuffix
	if _, err := store.Evaluate(context.Background(), "ORD-1003", "same-sku-replacement", "commerce-us-2026-08-20", testReleaseHash, testNow, []string{"demo-auto-recovery-bounds"}, []string{tampered}); !errors.Is(err, errInvalidReceipt) {
		t.Fatalf("expected invalid receipt, got %v", err)
	}
	wrongScope := signTestReceipt(t, evidenceReceiptClaims{Version: 1, SectionID: "demo-auto-recovery-bounds", ReleaseID: "commerce-us-2026-08-20", ReleaseHash: testReleaseHash, TenantID: "another-tenant", Jurisdiction: "US", Channel: "online", AsOf: testNow, IssuedAt: testNow.Add(-time.Minute), ExpiresAt: testNow.Add(time.Hour)})
	if _, err := store.Evaluate(context.Background(), "ORD-1003", "same-sku-replacement", "commerce-us-2026-08-20", testReleaseHash, testNow, []string{"demo-auto-recovery-bounds"}, []string{wrongScope}); !errors.Is(err, errInvalidReceipt) {
		t.Fatalf("expected tenant-scope rejection, got %v", err)
	}
}

func signTestReceipt(t *testing.T, claims evidenceReceiptClaims) string {
	t.Helper()
	payload, err := json.Marshal(claims)
	if err != nil {
		t.Fatal(err)
	}
	mac := hmac.New(sha256.New, []byte(testControlKey))
	_, _ = mac.Write([]byte("evidence-v1|"))
	_, _ = mac.Write(payload)
	return base64.RawURLEncoding.EncodeToString(payload) + "." + hex.EncodeToString(mac.Sum(nil))
}

func signTestApproval(checkpointID, decisionToken, idempotencyKey string) string {
	mac := hmac.New(sha256.New, []byte(testControlKey))
	_, _ = mac.Write([]byte("approval-v1|" + checkpointID + "|" + decisionToken + "|" + idempotencyKey))
	return hex.EncodeToString(mac.Sum(nil))
}
