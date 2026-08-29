package main

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/truvaagents/truva-g3/core"
)

func TestCommerceCapabilitiesAreSchemaBacked(t *testing.T) {
	tool := &CommerceTool{BaseTool: core.NewTool("commerce-core")}
	tool.registerCapabilities()

	expectedOutputs := map[string][]string{
		"get_order_context": {
			"order_id", "tenant_id", "customer_tier", "seller", "category", "item_value_usd", "channel", "jurisdiction",
			"fulfillment_status", "payment_status", "carrier_claim_status", "promised_ship_at", "promised_delivery_at", "delivered_at",
			"return_requested_at", "carrier", "carrier_service", "tracking_number", "mailing_receipt", "mailed_at", "inventory_available",
			"inventory_reserved", "fraud_risk", "restricted", "restriction_reason", "carrier_payer_known", "carrier_lane_known",
			"carrier_ship_date_known", "carrier_exclusions_cleared", "consent_to_delay", "recovery", "version",
		},
		"process_normal_order": {
			"order_id", "status", "terminal_outcome", "fulfillment_status", "payment_status", "inventory_reserved",
			"order_version", "deterministic_path", "agent_invoked", "llm_invoked",
		},
		"list_fulfillment_options": {"options"},
		"evaluate_recovery_policy": {
			"decision_token", "order_id", "option_id", "eligible", "approval_required", "outcome", "reasons", "evidence_section_ids",
			"release_id", "release_hash", "order_version", "decision_time", "expires_at",
		},
		"execute_recovery_command": {"command_id", "idempotency_key", "order_id", "option_id", "status", "order_version", "replayed", "approval_reference"},
		"verify_order_outcome": {
			"order_id", "command_id", "verified", "fulfillment_status", "payment_status", "carrier_claim_status", "recovery", "order_version",
		},
		"send_synthetic_notification": {"notification_id", "order_id", "command_id", "channel", "message", "sent_at"},
		"manage_demo_state":           {"action", "active_policy_release", "available_policy_releases", "order_count", "order"},
	}

	capabilities := tool.GetCapabilities()
	if len(capabilities) != len(expectedOutputs) {
		t.Fatalf("capability count = %d, want %d", len(capabilities), len(expectedOutputs))
	}
	for _, capability := range capabilities {
		if capability.InputSummary == nil || capability.OutputSummary == nil {
			t.Fatalf("capability %q is missing input or output schema metadata", capability.Name)
		}
		if capability.Endpoint != "/api/capabilities/"+capability.Name || capability.SchemaEndpoint != capability.Endpoint+"/schema" {
			t.Fatalf("capability %q has inconsistent endpoints: endpoint=%q schema=%q", capability.Name, capability.Endpoint, capability.SchemaEndpoint)
		}
		if capability.Name == "manage_demo_state" || capability.Name == "process_normal_order" {
			if !capability.Internal {
				t.Fatalf("%s must be hidden from LLM discovery", capability.Name)
			}
		} else if capability.Internal {
			t.Fatalf("public tool capability %q must be discoverable", capability.Name)
		}
		assertCommerceFields(t, capability.Name, capability.OutputSummary, expectedOutputs[capability.Name])
	}
}

func TestCommerceHandlerNormalizesIdentifiers(t *testing.T) {
	tool := &CommerceTool{BaseTool: core.NewTool("commerce-core-test"), store: newTestCommerceStore(t)}
	request := httptest.NewRequest(http.MethodPost, "/api/capabilities/get_order_context", strings.NewReader(`{"order_id":"  ORD-1003  "}`))
	response := httptest.NewRecorder()

	tool.handleGetOrder(response, request)

	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"order_id":"ORD-1003"`) {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
}

func TestCommerceStoreHonorsCanceledContext(t *testing.T) {
	store := newTestCommerceStore(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	if _, err := store.GetOrder(ctx, "ORD-1003"); !errors.Is(err, context.Canceled) {
		t.Fatalf("GetOrder error = %v, want context.Canceled", err)
	}
	if _, err := store.ProcessNormalOrder(ctx, "ORD-1000"); !errors.Is(err, context.Canceled) {
		t.Fatalf("ProcessNormalOrder error = %v, want context.Canceled", err)
	}
	if _, err := store.ListOptions(ctx, "ORD-1003"); !errors.Is(err, context.Canceled) {
		t.Fatalf("ListOptions error = %v, want context.Canceled", err)
	}
	if _, err := store.Evaluate(ctx, "ORD-1003", "same-sku-replacement", "commerce-us-2026-08-20", testReleaseHash, testNow, nil, nil); !errors.Is(err, context.Canceled) {
		t.Fatalf("Evaluate error = %v, want context.Canceled", err)
	}
	if _, err := store.Execute(ctx, "decision", "key", "", ""); !errors.Is(err, context.Canceled) {
		t.Fatalf("Execute error = %v, want context.Canceled", err)
	}
	if _, err := store.Verify(ctx, "ORD-1003", "command"); !errors.Is(err, context.Canceled) {
		t.Fatalf("Verify error = %v, want context.Canceled", err)
	}
	if _, err := store.Notify(ctx, "ORD-1003", "command", "email", "message"); !errors.Is(err, context.Canceled) {
		t.Fatalf("Notify error = %v, want context.Canceled", err)
	}
}

func TestCommerceConfigurationRejectsInvalidPorts(t *testing.T) {
	t.Setenv("REDIS_URL", "redis://redis.example:6379")
	t.Setenv("CONTROL_PROOF_HMAC_KEY", testControlKey)
	for _, port := range []string{"invalid", "0", "-1", "65536"} {
		t.Run(port, func(t *testing.T) {
			t.Setenv("PORT", port)
			if err := validateCommerceConfig(); err == nil {
				t.Fatalf("validateCommerceConfig accepted PORT=%q", port)
			}
		})
	}
	t.Setenv("PORT", "8395")
	if err := validateCommerceConfig(); err != nil || commercePort() != 8395 {
		t.Fatalf("reserved commerce port rejected: port=%d err=%v", commercePort(), err)
	}
}

func TestCommerceRequestIDFallsBackToExecutorHeader(t *testing.T) {
	request := httptest.NewRequest(http.MethodPost, "/api/capabilities/get_order_context", nil)
	request.Header.Set("X-TruvaG3-Request-ID", "request-123")
	if requestID := requestIDFromRequest(request.Context(), request); requestID != "request-123" {
		t.Fatalf("request ID = %q, want request-123", requestID)
	}
}

func assertCommerceFields(t *testing.T, capability string, summary *core.SchemaSummary, expected []string) {
	t.Helper()
	actual := make(map[string]struct{}, len(summary.RequiredFields)+len(summary.OptionalFields))
	for _, field := range append(append([]core.FieldHint(nil), summary.RequiredFields...), summary.OptionalFields...) {
		actual[field.Name] = struct{}{}
	}
	if len(actual) != len(expected) {
		t.Fatalf("capability %q declares %d output fields, want %d: %v", capability, len(actual), len(expected), actual)
	}
	for _, field := range expected {
		if _, found := actual[field]; !found {
			t.Errorf("capability %q does not declare output field %q", capability, field)
		}
	}
}
