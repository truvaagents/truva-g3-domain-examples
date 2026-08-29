package main

import (
	"os"

	"github.com/truvaagents/truva-g3/core"
)

type CommerceTool struct {
	*core.BaseTool
	store *CommerceStore
}

func NewCommerceTool() (*CommerceTool, error) {
	path := os.Getenv("COMMERCE_ORDERS_PATH")
	if path == "" {
		path = "/app/data/orders.json"
	}
	store, err := LoadCommerceStore(path, os.Getenv("CONTROL_PROOF_HMAC_KEY"))
	if err != nil {
		return nil, err
	}
	tool := &CommerceTool{BaseTool: core.NewTool("commerce-core"), store: store}
	tool.registerCapabilities()
	return tool, nil
}

func (t *CommerceTool) registerCapabilities() {
	t.RegisterCapability(core.Capability{
		Name:        "get_order_context",
		Description: "Reads one synthetic order's current seller, category, value, jurisdiction, fulfillment state, promise dates, carrier evidence, risk controls, and version. Use first for any order recovery request. Required: order_id.",
		InputTypes:  []string{"json"}, OutputTypes: []string{"json"}, Handler: t.handleGetOrder,
		InputSummary: &core.SchemaSummary{RequiredFields: []core.FieldHint{{Name: "order_id", Type: "string", Example: "ORD-1003", Description: "Synthetic demo order identifier"}}},
		OutputSummary: &core.SchemaSummary{
			RequiredFields: []core.FieldHint{
				{Name: "order_id", Type: "string", Description: "Order identifier"},
				{Name: "tenant_id", Type: "string", Description: "Tenant knowledge scope"},
				{Name: "customer_tier", Type: "string", Description: "Synthetic customer tier"},
				{Name: "seller", Type: "string", Description: "Seller of record"},
				{Name: "category", Type: "string", Description: "Merchandise category"},
				{Name: "item_value_usd", Type: "number", Description: "Synthetic merchandise value"},
				{Name: "channel", Type: "string", Description: "Knowledge scope"},
				{Name: "jurisdiction", Type: "string", Description: "Knowledge scope"},
				{Name: "fulfillment_status", Type: "string", Description: "Current state used for recovery eligibility"},
				{Name: "payment_status", Type: "string", Description: "Current payment state"},
				{Name: "promised_ship_at", Type: "string", Description: "Promised ship timestamp"},
				{Name: "promised_delivery_at", Type: "string", Description: "Promised delivery timestamp"},
				{Name: "carrier", Type: "string", Description: "Fulfillment carrier"},
				{Name: "carrier_service", Type: "string", Description: "Carrier service level"},
				{Name: "mailing_receipt", Type: "boolean", Description: "Whether mailing evidence exists"},
				{Name: "inventory_available", Type: "boolean", Description: "Replacement inventory availability"},
				{Name: "inventory_reserved", Type: "boolean", Description: "Whether replacement inventory is already reserved"},
				{Name: "fraud_risk", Type: "string", Description: "Synthetic fraud-risk classification"},
				{Name: "restricted", Type: "boolean", Description: "Whether automatic recovery is restricted"},
				{Name: "carrier_payer_known", Type: "boolean", Description: "Whether the carrier payer fact is available"},
				{Name: "carrier_lane_known", Type: "boolean", Description: "Whether origin and destination lane facts are available"},
				{Name: "carrier_ship_date_known", Type: "boolean", Description: "Whether the carrier acceptance date is available"},
				{Name: "carrier_exclusions_cleared", Type: "boolean", Description: "Whether applicable carrier exclusions were checked and cleared"},
				{Name: "version", Type: "integer", Description: "Optimistic command version"},
			},
			OptionalFields: []core.FieldHint{
				{Name: "carrier_claim_status", Type: "string", Description: "Current carrier-claim state"},
				{Name: "delivered_at", Type: "string", Description: "Delivery timestamp when available"},
				{Name: "return_requested_at", Type: "string", Description: "Return-request timestamp when available"},
				{Name: "tracking_number", Type: "string", Description: "Carrier tracking identifier when available"},
				{Name: "mailed_at", Type: "string", Description: "Carrier acceptance timestamp when available"},
				{Name: "restriction_reason", Type: "string", Description: "Deterministic restriction reason when applicable"},
				{Name: "consent_to_delay", Type: "boolean", Description: "Recorded customer consent when available"},
				{Name: "recovery", Type: "object", Description: "Completed recovery state when present"},
			},
		},
	})
	t.RegisterCapability(core.Capability{
		Name:        "process_normal_order",
		Description: "Development-only straight-through proof that validates an already confirmed, paid, inventory-reserved synthetic order without invoking the agent or an LLM. Hidden from LLM planning.",
		Internal:    true,
		InputTypes:  []string{"json"}, OutputTypes: []string{"json"}, Handler: t.handleProcessNormalOrder,
		InputSummary: &core.SchemaSummary{RequiredFields: []core.FieldHint{{Name: "order_id", Type: "string", Example: "ORD-1000", Description: "Synthetic normal order identifier"}}},
		OutputSummary: &core.SchemaSummary{RequiredFields: []core.FieldHint{
			{Name: "order_id", Type: "string", Description: "Order identifier"},
			{Name: "status", Type: "string", Description: "Deterministic processing status"},
			{Name: "terminal_outcome", Type: "string", Description: "normal_order_confirmed when straight-through invariants hold"},
			{Name: "fulfillment_status", Type: "string", Description: "Authoritative fulfillment state"},
			{Name: "payment_status", Type: "string", Description: "Authoritative payment state"},
			{Name: "inventory_reserved", Type: "boolean", Description: "Authoritative reservation state"},
			{Name: "order_version", Type: "integer", Description: "Observed optimistic version"},
			{Name: "deterministic_path", Type: "boolean", Description: "True for this non-agent route"},
			{Name: "agent_invoked", Type: "boolean", Description: "Always false"},
			{Name: "llm_invoked", Type: "boolean", Description: "Always false"},
		}},
	})
	t.RegisterCapability(core.Capability{
		Name:        "list_fulfillment_options",
		Description: "Lists bounded recovery options derived only from current synthetic order state. These are candidates, not policy authorization; call evaluate_recovery_policy with exact evidence before execution.",
		InputTypes:  []string{"json"}, OutputTypes: []string{"json"}, Handler: t.handleListOptions,
		InputSummary:  &core.SchemaSummary{RequiredFields: []core.FieldHint{{Name: "order_id", Type: "string", Example: "ORD-1003", Description: "Order to inspect"}}},
		OutputSummary: &core.SchemaSummary{RequiredFields: []core.FieldHint{{Name: "options", Type: "array", Description: "Option IDs, actions, amounts, descriptions, preconditions, and any authoritative evidence-channel scope"}}},
	})
	t.RegisterCapability(core.Capability{
		Name:        "evaluate_recovery_policy",
		Description: "Deterministically evaluates one bounded option against current order state and cryptographically verified exact-read evidence from one immutable knowledge release. Returns an authoritative outcome (allowed, approval_required, denied, insufficient_evidence, or conflicting_evidence), reasons, and a short-lived decision_token. Call this even when scoped order facts appear incomplete so the terminal classification is recorded; the LLM must not override the result.",
		InputTypes:  []string{"json"}, OutputTypes: []string{"json"}, Handler: t.handleEvaluate,
		InputSummary: &core.SchemaSummary{RequiredFields: []core.FieldHint{
			{Name: "order_id", Type: "string", Example: "ORD-1003", Description: "Order identifier"},
			{Name: "option_id", Type: "string", Example: "same-sku-replacement", Description: "Option returned by list_fulfillment_options"},
			{Name: "release_id", Type: "string", Example: "commerce-us-2026-08-20", Description: "Immutable evidence release"},
			{Name: "release_hash", Type: "string", Example: "sha256:...", Description: "Release content hash"},
			{Name: "evidence_section_ids", Type: "array", Example: `["usps-priority-express-eligibility","demo-auto-recovery-bounds"]`, Description: "Exact cited evidence IDs"},
			{Name: "evidence_receipts", Type: "array", Example: `["receipt-from-read-exact-section"]`, Description: "Signed exact-read receipts paired with the evidence IDs"},
			{Name: "decision_time", Type: "string", Example: "2026-08-20T18:00:00Z", Description: "RFC3339 time used for every scoped evidence read"},
		}},
		OutputSummary: &core.SchemaSummary{RequiredFields: []core.FieldHint{
			{Name: "decision_token", Type: "string", Description: "Short-lived authorization bound to order version and evidence"},
			{Name: "order_id", Type: "string", Description: "Evaluated order identifier"},
			{Name: "option_id", Type: "string", Description: "Evaluated recovery option"},
			{Name: "eligible", Type: "boolean", Description: "Deterministic eligibility"},
			{Name: "approval_required", Type: "boolean", Description: "Whether agent HITL must approve execution"},
			{Name: "outcome", Type: "string", Description: "Deterministic policy outcome: allowed, approval_required, denied, insufficient_evidence, or conflicting_evidence"},
			{Name: "reasons", Type: "array", Description: "Auditable decision reasons"},
			{Name: "evidence_section_ids", Type: "array", Description: "Evidence sections used by the decision"},
			{Name: "release_id", Type: "string", Description: "Pinned evidence release"},
			{Name: "release_hash", Type: "string", Description: "Pinned evidence-release hash"},
			{Name: "order_version", Type: "integer", Description: "Order version bound into the decision"},
			{Name: "decision_time", Type: "string", Description: "Decision timestamp used for evidence scope"},
			{Name: "expires_at", Type: "string", Description: "Automatic decision-token expiration"},
		}},
	})
	t.RegisterCapability(core.Capability{
		Name:        "execute_recovery_command",
		Description: "SENSITIVE MUTATION. Revalidates evidence, policy, and order version before executing one eligible decision exactly once. Approval-class decisions additionally require approval_reference and approval_proof injected by the agent only after its HITL checkpoint is approved.",
		InputTypes:  []string{"json"}, OutputTypes: []string{"json"}, Handler: t.handleExecute,
		InputSummary: &core.SchemaSummary{RequiredFields: []core.FieldHint{{Name: "decision_token", Type: "string", Description: "Token from evaluate_recovery_policy"}, {Name: "idempotency_key", Type: "string", Example: "ORD-1003:same-sku-replacement:v1", Description: "Stable retry key"}}, OptionalFields: []core.FieldHint{{Name: "approval_reference", Type: "string", Description: "Approved checkpoint ID; required for approval-class tokens"}, {Name: "approval_proof", Type: "string", Description: "Agent-generated proof; required for approval-class tokens"}}},
		OutputSummary: &core.SchemaSummary{RequiredFields: []core.FieldHint{
			{Name: "command_id", Type: "string", Description: "Synthetic command reference"},
			{Name: "idempotency_key", Type: "string", Description: "Stable retry key used for the command"},
			{Name: "order_id", Type: "string", Description: "Mutated order identifier"},
			{Name: "option_id", Type: "string", Description: "Executed recovery option"},
			{Name: "status", Type: "string", Description: "Command state"},
			{Name: "order_version", Type: "integer", Description: "Post-command order version"},
			{Name: "replayed", Type: "boolean", Description: "True when an idempotent retry returned the original result"},
		}, OptionalFields: []core.FieldHint{{Name: "approval_reference", Type: "string", Description: "Non-secret approved HITL checkpoint reference for an approval-class command"}}},
	})
	t.RegisterCapability(core.Capability{
		Name:        "verify_order_outcome",
		Description: "Verifies action-specific fulfillment, payment, inventory, or carrier-claim postconditions for one command. Use after execute_recovery_command and do not claim success unless verified=true.",
		InputTypes:  []string{"json"}, OutputTypes: []string{"json"}, Handler: t.handleVerify,
		InputSummary: &core.SchemaSummary{RequiredFields: []core.FieldHint{{Name: "order_id", Type: "string", Description: "Order identifier"}, {Name: "command_id", Type: "string", Description: "Command reference to verify"}}},
		OutputSummary: &core.SchemaSummary{RequiredFields: []core.FieldHint{
			{Name: "order_id", Type: "string", Description: "Verified order identifier"},
			{Name: "command_id", Type: "string", Description: "Verified command reference"},
			{Name: "verified", Type: "boolean", Description: "Whether expected order state is present"},
			{Name: "fulfillment_status", Type: "string", Description: "Observed fulfillment state"},
			{Name: "payment_status", Type: "string", Description: "Observed payment state"},
			{Name: "carrier_claim_status", Type: "string", Description: "Observed carrier-claim state"},
			{Name: "recovery", Type: "object", Description: "Recorded recovery state"},
			{Name: "order_version", Type: "integer", Description: "Observed order version"},
		}},
	})
	t.RegisterCapability(core.Capability{
		Name:        "send_synthetic_notification",
		Description: "Records a synthetic customer notification only after the referenced recovery command is complete on the order. Returns an auditable notification ID; no external message is sent.",
		InputTypes:  []string{"json"}, OutputTypes: []string{"json"}, Handler: t.handleNotify,
		InputSummary: &core.SchemaSummary{RequiredFields: []core.FieldHint{{Name: "order_id", Type: "string", Description: "Order identifier"}, {Name: "command_id", Type: "string", Description: "Verified command reference"}, {Name: "channel", Type: "string", Example: "email", Description: "Synthetic delivery channel"}, {Name: "message", Type: "string", Description: "Customer-safe status including recovery and timing"}}},
		OutputSummary: &core.SchemaSummary{RequiredFields: []core.FieldHint{
			{Name: "notification_id", Type: "string", Description: "Synthetic audit reference"},
			{Name: "order_id", Type: "string", Description: "Notified order identifier"},
			{Name: "command_id", Type: "string", Description: "Completed command reference"},
			{Name: "channel", Type: "string", Description: "Synthetic delivery channel"},
			{Name: "message", Type: "string", Description: "Recorded customer-safe message"},
			{Name: "sent_at", Type: "string", Description: "Recorded timestamp"},
		}},
	})
	t.RegisterCapability(core.Capability{
		Name:        "manage_demo_state",
		Description: "Development-only deterministic control for resetting synthetic state, changing one order version, or activating an allow-listed policy release. Hidden from LLM planning.",
		Internal:    true,
		InputTypes:  []string{"json"}, OutputTypes: []string{"json"}, Handler: t.handleManageDemoState,
		InputSummary: &core.SchemaSummary{RequiredFields: []core.FieldHint{
			{Name: "action", Type: "string", Example: "status", Description: "status, reset, set_inventory_unavailable, or activate_policy_release"},
		}, OptionalFields: []core.FieldHint{
			{Name: "order_id", Type: "string", Example: "ORD-1004", Description: "Synthetic order for status or mutation"},
			{Name: "expected_version", Type: "integer", Example: "1", Description: "Required optimistic version for a state mutation"},
			{Name: "release_id", Type: "string", Example: "commerce-us-2026-08-20", Description: "Allow-listed policy release to activate"},
		}},
		OutputSummary: &core.SchemaSummary{RequiredFields: []core.FieldHint{
			{Name: "action", Type: "string", Description: "Applied control action"},
			{Name: "active_policy_release", Type: "object", Description: "Current deterministic policy release"},
			{Name: "available_policy_releases", Type: "array", Description: "Allow-listed release identifiers"},
			{Name: "order_count", Type: "integer", Description: "Loaded synthetic order count"},
		}, OptionalFields: []core.FieldHint{{Name: "order", Type: "object", Description: "Selected order after the control action"}}},
	})
}
