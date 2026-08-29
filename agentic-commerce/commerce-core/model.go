package main

import "time"

type PolicyRelease struct {
	ReleaseID   string      `json:"release_id"`
	ContentHash string      `json:"content_hash"`
	SectionIDs  []string    `json:"section_ids"`
	Rules       PolicyRules `json:"rules"`
}

type PolicyRules struct {
	RefundApprovalOverUSD      float64 `json:"refund_approval_over_usd"`
	ReplacementApprovalOverUSD float64 `json:"replacement_approval_over_usd"`
}

type PolicyOutcome string

const (
	PolicyOutcomeAllowed              PolicyOutcome = "allowed"
	PolicyOutcomeApprovalRequired     PolicyOutcome = "approval_required"
	PolicyOutcomeDenied               PolicyOutcome = "denied"
	PolicyOutcomeInsufficientEvidence PolicyOutcome = "insufficient_evidence"
	PolicyOutcomeConflictingEvidence  PolicyOutcome = "conflicting_evidence"
)

type Order struct {
	OrderID                  string         `json:"order_id"`
	TenantID                 string         `json:"tenant_id"`
	CustomerTier             string         `json:"customer_tier"`
	Seller                   string         `json:"seller"`
	Category                 string         `json:"category"`
	ItemValueUSD             float64        `json:"item_value_usd"`
	Channel                  string         `json:"channel"`
	Jurisdiction             string         `json:"jurisdiction"`
	FulfillmentStatus        string         `json:"fulfillment_status"`
	PaymentStatus            string         `json:"payment_status"`
	CarrierClaimStatus       string         `json:"carrier_claim_status,omitempty"`
	PromisedShipAt           time.Time      `json:"promised_ship_at"`
	PromisedDeliveryAt       time.Time      `json:"promised_delivery_at"`
	DeliveredAt              *time.Time     `json:"delivered_at,omitempty"`
	ReturnRequestedAt        *time.Time     `json:"return_requested_at,omitempty"`
	Carrier                  string         `json:"carrier"`
	CarrierService           string         `json:"carrier_service"`
	TrackingNumber           string         `json:"tracking_number,omitempty"`
	MailingReceipt           bool           `json:"mailing_receipt"`
	MailedAt                 *time.Time     `json:"mailed_at,omitempty"`
	InventoryAvailable       bool           `json:"inventory_available"`
	InventoryReserved        bool           `json:"inventory_reserved"`
	FraudRisk                string         `json:"fraud_risk"`
	Restricted               bool           `json:"restricted"`
	RestrictionReason        string         `json:"restriction_reason,omitempty"`
	CarrierPayerKnown        bool           `json:"carrier_payer_known"`
	CarrierLaneKnown         bool           `json:"carrier_lane_known"`
	CarrierShipDateKnown     bool           `json:"carrier_ship_date_known"`
	CarrierExclusionsCleared bool           `json:"carrier_exclusions_cleared"`
	ConsentToDelay           *bool          `json:"consent_to_delay,omitempty"`
	Recovery                 *RecoveryState `json:"recovery,omitempty"`
	Version                  int            `json:"version"`
}

type RecoveryState struct {
	CommandID                  string    `json:"command_id"`
	OptionID                   string    `json:"option_id"`
	Status                     string    `json:"status"`
	EvidenceRelease            string    `json:"evidence_release"`
	ExecutedAt                 time.Time `json:"executed_at"`
	ExpectedFulfillmentStatus  string    `json:"expected_fulfillment_status"`
	ExpectedPaymentStatus      string    `json:"expected_payment_status"`
	ExpectedCarrierClaimStatus string    `json:"expected_carrier_claim_status,omitempty"`
}

type FulfillmentOption struct {
	OptionID        string   `json:"option_id"`
	Action          string   `json:"action"`
	AmountUSD       float64  `json:"amount_usd,omitempty"`
	Description     string   `json:"description"`
	Preconditions   []string `json:"preconditions"`
	EvidenceChannel string   `json:"evidence_channel,omitempty"`
}

type PolicyDecision struct {
	DecisionToken      string        `json:"decision_token"`
	OrderID            string        `json:"order_id"`
	OptionID           string        `json:"option_id"`
	Eligible           bool          `json:"eligible"`
	ApprovalRequired   bool          `json:"approval_required"`
	Outcome            PolicyOutcome `json:"outcome"`
	Reasons            []string      `json:"reasons"`
	EvidenceSectionIDs []string      `json:"evidence_section_ids"`
	ReleaseID          string        `json:"release_id"`
	ReleaseHash        string        `json:"release_hash"`
	OrderVersion       int           `json:"order_version"`
	DecisionTime       time.Time     `json:"decision_time"`
	ExpiresAt          time.Time     `json:"expires_at"`
	EvidenceReceipts   []string      `json:"-"`
}

type CommandResult struct {
	CommandID         string `json:"command_id"`
	IdempotencyKey    string `json:"idempotency_key"`
	OrderID           string `json:"order_id"`
	OptionID          string `json:"option_id"`
	Status            string `json:"status"`
	OrderVersion      int    `json:"order_version"`
	Replayed          bool   `json:"replayed"`
	ApprovalReference string `json:"approval_reference,omitempty"`
	DecisionToken     string `json:"-"`
}

type NormalOrderResult struct {
	OrderID           string `json:"order_id"`
	Status            string `json:"status"`
	TerminalOutcome   string `json:"terminal_outcome"`
	FulfillmentStatus string `json:"fulfillment_status"`
	PaymentStatus     string `json:"payment_status"`
	InventoryReserved bool   `json:"inventory_reserved"`
	OrderVersion      int    `json:"order_version"`
	DeterministicPath bool   `json:"deterministic_path"`
	AgentInvoked      bool   `json:"agent_invoked"`
	LLMInvoked        bool   `json:"llm_invoked"`
}

type Notification struct {
	NotificationID string    `json:"notification_id"`
	OrderID        string    `json:"order_id"`
	CommandID      string    `json:"command_id"`
	Channel        string    `json:"channel"`
	Message        string    `json:"message"`
	SentAt         time.Time `json:"sent_at"`
}
