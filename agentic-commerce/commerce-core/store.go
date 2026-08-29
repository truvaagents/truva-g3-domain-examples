package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

var (
	errOrderNotFound       = errors.New("order not found")
	errDecisionNotFound    = errors.New("policy decision not found")
	errDecisionStale       = errors.New("policy decision is stale")
	errPolicyBasisChanged  = errors.New("policy basis changed")
	errOrderStateChanged   = errors.New("order state changed")
	errDecisionExpired     = errors.New("decision or evidence expired")
	errNotEligible         = errors.New("recovery option is not eligible")
	errReleaseMismatch     = errors.New("evidence release does not match the pinned policy release")
	errUnknownEvidence     = errors.New("evidence section is not in the pinned policy release")
	errInvalidReceipt      = errors.New("evidence receipt is invalid")
	errApprovalRequired    = errors.New("verified human approval is required")
	errIdempotencyConflict = errors.New("idempotency key was already used for a different decision")
)

type CommerceStore struct {
	mu                sync.RWMutex
	orders            map[string]Order
	seedOrders        map[string]Order
	decisions         map[string]PolicyDecision
	commands          map[string]CommandResult
	notifications     map[string]Notification
	policyRelease     PolicyRelease
	policyReleases    map[string]PolicyRelease
	baselineReleaseID string
	sectionIDs        map[string]struct{}
	controlKey        []byte
	now               func() time.Time
}

func LoadCommerceStore(path, controlKey string) (*CommerceStore, error) {
	if len(controlKey) < 32 {
		return nil, fmt.Errorf("CONTROL_PROOF_HMAC_KEY must contain at least 32 characters")
	}
	cleanPath := filepath.Clean(path)
	if filepath.Base(cleanPath) != "orders.json" {
		return nil, fmt.Errorf("commerce orders path must name orders.json")
	}
	dataRoot, err := os.OpenRoot(filepath.Dir(cleanPath))
	if err != nil {
		return nil, fmt.Errorf("open commerce data root: %w", err)
	}
	defer func() { _ = dataRoot.Close() }()
	data, err := dataRoot.ReadFile("orders.json")
	if err != nil {
		return nil, fmt.Errorf("read orders: %w", err)
	}
	var orders []Order
	if err := decodeStrictJSON(data, &orders); err != nil {
		return nil, fmt.Errorf("decode orders: %w", err)
	}
	policyRelease, err := loadPolicyRelease(dataRoot, "policy-release.json")
	if err != nil {
		return nil, err
	}
	policyReleases := map[string]PolicyRelease{policyRelease.ReleaseID: policyRelease}
	if changed, changedErr := loadPolicyRelease(dataRoot, "policy-release-changed.json"); changedErr == nil {
		policyReleases[changed.ReleaseID] = changed
	} else if !errors.Is(changedErr, os.ErrNotExist) {
		return nil, changedErr
	}
	sectionIDs := policySectionSet(policyRelease)
	store := &CommerceStore{
		orders:            make(map[string]Order, len(orders)),
		seedOrders:        make(map[string]Order, len(orders)),
		decisions:         map[string]PolicyDecision{},
		commands:          map[string]CommandResult{},
		notifications:     map[string]Notification{},
		policyRelease:     policyRelease,
		policyReleases:    policyReleases,
		baselineReleaseID: policyRelease.ReleaseID,
		sectionIDs:        sectionIDs,
		controlKey:        []byte(controlKey),
		now:               func() time.Time { return time.Now().UTC() },
	}
	for _, order := range orders {
		if order.OrderID == "" || order.TenantID == "" || order.Jurisdiction == "" || order.Channel == "" || order.Version < 1 || order.PaymentStatus == "" {
			return nil, fmt.Errorf("order identity, scope, payment_status, and positive version are required")
		}
		if order.TrackingNumber != "" && order.MailedAt == nil {
			return nil, fmt.Errorf("tracked order %q requires mailed_at", order.OrderID)
		}
		if _, duplicate := store.orders[order.OrderID]; duplicate {
			return nil, fmt.Errorf("duplicate order_id %q", order.OrderID)
		}
		store.orders[order.OrderID] = order
		store.seedOrders[order.OrderID] = order
	}
	return store, nil
}

func loadPolicyRelease(root *os.Root, name string) (PolicyRelease, error) {
	data, err := root.ReadFile(name)
	if err != nil {
		return PolicyRelease{}, fmt.Errorf("read policy release %s: %w", name, err)
	}
	var release PolicyRelease
	if err := decodeStrictJSON(data, &release); err != nil {
		return PolicyRelease{}, fmt.Errorf("decode policy release %s: %w", name, err)
	}
	if release.ReleaseID == "" || release.ContentHash == "" || len(release.SectionIDs) == 0 {
		return PolicyRelease{}, fmt.Errorf("policy release %s identity and section_ids are required", name)
	}
	if release.Rules.RefundApprovalOverUSD <= 0 || release.Rules.ReplacementApprovalOverUSD <= 0 {
		return PolicyRelease{}, fmt.Errorf("policy release %s approval thresholds must be positive", name)
	}
	seen := make(map[string]struct{}, len(release.SectionIDs))
	for _, sectionID := range release.SectionIDs {
		if sectionID == "" {
			return PolicyRelease{}, fmt.Errorf("policy release %s contains an empty section_id", name)
		}
		if _, duplicate := seen[sectionID]; duplicate {
			return PolicyRelease{}, fmt.Errorf("policy release %s contains duplicate section_id %q", name, sectionID)
		}
		seen[sectionID] = struct{}{}
	}
	return release, nil
}

func policySectionSet(release PolicyRelease) map[string]struct{} {
	sections := make(map[string]struct{}, len(release.SectionIDs))
	for _, sectionID := range release.SectionIDs {
		sections[sectionID] = struct{}{}
	}
	return sections
}

func decodeStrictJSON(data []byte, target any) error {
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return err
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return fmt.Errorf("expected exactly one JSON value")
	}
	return nil
}

func (s *CommerceStore) GetOrder(ctx context.Context, orderID string) (Order, error) {
	if err := ctx.Err(); err != nil {
		return Order{}, err
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	order, ok := s.orders[orderID]
	if !ok {
		return Order{}, errOrderNotFound
	}
	return order, nil
}

// ProcessNormalOrder proves the straight-through commerce path without an
// agent or AI provider. The seed state is already authoritative, so this
// operation validates and reports it without manufacturing a second mutation.
func (s *CommerceStore) ProcessNormalOrder(ctx context.Context, orderID string) (NormalOrderResult, error) {
	order, err := s.GetOrder(ctx, orderID)
	if err != nil {
		return NormalOrderResult{}, err
	}
	if order.FulfillmentStatus != "confirmed" || order.PaymentStatus != "paid" || !order.InventoryReserved {
		return NormalOrderResult{}, fmt.Errorf("order is not eligible for the deterministic normal path: fulfillment must be confirmed, payment paid, and inventory reserved")
	}
	return NormalOrderResult{
		OrderID: order.OrderID, Status: "completed", TerminalOutcome: "normal_order_confirmed",
		FulfillmentStatus: order.FulfillmentStatus, PaymentStatus: order.PaymentStatus,
		InventoryReserved: order.InventoryReserved, OrderVersion: order.Version,
		DeterministicPath: true, AgentInvoked: false, LLMInvoked: false,
	}, nil
}

func (s *CommerceStore) ListOptions(ctx context.Context, orderID string) ([]FulfillmentOption, error) {
	order, err := s.GetOrder(ctx, orderID)
	if err != nil {
		return nil, err
	}
	options := make([]FulfillmentOption, 0, 5)
	if order.FulfillmentStatus == "late_unshipped" && order.ConsentToDelay != nil && *order.ConsentToDelay {
		options = append(options, FulfillmentOption{OptionID: "wait-for-current-fulfillment", Action: "wait", Description: "Leave the current fulfillment unchanged after recorded customer consent", Preconditions: []string{"recorded customer consent", "applicable policy evidence"}})
	}
	if order.FulfillmentStatus == "backordered" || order.FulfillmentStatus == "late_unshipped" {
		options = append(options, FulfillmentOption{OptionID: "cancel-and-refund", Action: "refund", AmountUSD: order.ItemValueUSD, Description: "Cancel unshipped merchandise and refund the order", Preconditions: []string{"applicable policy evidence", "current order version"}})
	}
	if order.FulfillmentStatus == "missing_in_transit" || order.FulfillmentStatus == "delivered" {
		options = append(options, FulfillmentOption{OptionID: "refund-merchandise", Action: "refund", AmountUSD: order.ItemValueUSD, Description: "Refund merchandise value", Preconditions: []string{"applicable policy evidence", "current order version"}})
	}
	if order.InventoryAvailable && (order.FulfillmentStatus == "missing_in_transit" || order.FulfillmentStatus == "late_unshipped") {
		options = append(options, FulfillmentOption{OptionID: "same-sku-replacement", Action: "replacement", Description: "Create a replacement fulfillment for the same SKU and unchanged destination", Preconditions: []string{"inventory available", "unchanged destination", "low fraud risk"}})
	}
	if order.TrackingNumber != "" {
		options = append(options, FulfillmentOption{OptionID: "carrier-service-claim", Action: "carrier_claim", Description: "Record a carrier service-failure claim", Preconditions: []string{"carrier-specific eligibility", "tracking evidence"}, EvidenceChannel: "shipping"})
	}
	sort.Slice(options, func(i, j int) bool { return options[i].OptionID < options[j].OptionID })
	return options, nil
}

func (s *CommerceStore) Evaluate(ctx context.Context, orderID, optionID, releaseID, releaseHash string, decisionTime time.Time, evidence, receipts []string) (PolicyDecision, error) {
	if err := ctx.Err(); err != nil {
		return PolicyDecision{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	order, ok := s.orders[orderID]
	if !ok {
		return PolicyDecision{}, errOrderNotFound
	}
	if releaseID != s.policyRelease.ReleaseID || releaseHash != s.policyRelease.ContentHash {
		return PolicyDecision{}, fmt.Errorf("%w: expected %s at %s", errReleaseMismatch, s.policyRelease.ReleaseID, s.policyRelease.ContentHash)
	}
	if decisionTime.IsZero() || decisionTime.After(s.now().Add(time.Minute)) {
		return PolicyDecision{}, fmt.Errorf("decision_time must be a current or past RFC3339 timestamp")
	}
	evidenceSet, err := s.validateEvidence(order, releaseID, releaseHash, decisionTime, evidence, receipts, s.now())
	if err != nil {
		return PolicyDecision{}, err
	}
	evaluation, err := evaluatePolicy(order, optionID, decisionTime, evidenceSet, s.policyRelease.Rules)
	if err != nil {
		return PolicyDecision{}, err
	}
	expires := s.now().Add(15 * time.Minute)
	token := decisionToken(order, optionID, releaseID, releaseHash, decisionTime, evidence, expires, evaluation.ApprovalRequired)
	decision := PolicyDecision{
		DecisionToken: token, OrderID: orderID, OptionID: optionID, Eligible: evaluation.Eligible,
		ApprovalRequired: evaluation.ApprovalRequired, Outcome: evaluation.Outcome, Reasons: evaluation.Reasons, EvidenceSectionIDs: append([]string(nil), evidence...),
		ReleaseID: releaseID, ReleaseHash: releaseHash, OrderVersion: order.Version, DecisionTime: decisionTime,
		ExpiresAt: expires, EvidenceReceipts: append([]string(nil), receipts...),
	}
	s.decisions[token] = decision
	return decision, nil
}

func (s *CommerceStore) Execute(ctx context.Context, decisionTokenValue, idempotencyKey, approvalReference, approvalProof string) (CommandResult, error) {
	if err := ctx.Err(); err != nil {
		return CommandResult{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if strings.TrimSpace(idempotencyKey) == "" {
		return CommandResult{}, fmt.Errorf("idempotency_key is required")
	}
	if existing, ok := s.commands[idempotencyKey]; ok {
		if existing.DecisionToken != decisionTokenValue {
			return CommandResult{}, errIdempotencyConflict
		}
		existing.Replayed = true
		return existing, nil
	}
	decision, ok := s.decisions[decisionTokenValue]
	if !ok {
		return CommandResult{}, errDecisionNotFound
	}
	if !decision.Eligible {
		return CommandResult{}, errNotEligible
	}
	now := s.now()
	if decision.ReleaseID != s.policyRelease.ReleaseID || decision.ReleaseHash != s.policyRelease.ContentHash {
		return CommandResult{}, fmt.Errorf("%w: %w: active policy release changed", errDecisionStale, errPolicyBasisChanged)
	}
	if decision.ApprovalRequired {
		if !verifyApprovalProof(s.controlKey, approvalReference, decisionTokenValue, idempotencyKey, approvalProof) {
			return CommandResult{}, errApprovalRequired
		}
	}
	order := s.orders[decision.OrderID]
	if order.Version != decision.OrderVersion {
		return CommandResult{}, fmt.Errorf("%w: %w: order version changed", errDecisionStale, errOrderStateChanged)
	}
	// Report a concrete policy or state race ahead of generic time expiry when
	// more than one fail-closed guard is true. Expiry is still enforced before
	// evidence revalidation or mutation, including for approval-class decisions.
	// This gives operators a stable, actionable replan reason even when an LLM
	// phase consumed most of the short-lived demo decision window.
	if !now.Before(decision.ExpiresAt) {
		return CommandResult{}, fmt.Errorf("%w: %w: decision token expired", errDecisionStale, errDecisionExpired)
	}
	evidenceSet, err := s.validateEvidence(order, decision.ReleaseID, decision.ReleaseHash, decision.DecisionTime, decision.EvidenceSectionIDs, decision.EvidenceReceipts, now)
	if err != nil {
		return CommandResult{}, fmt.Errorf("%w: %w: %v", errDecisionStale, errDecisionExpired, err)
	}
	evaluation, err := evaluatePolicy(order, decision.OptionID, decision.DecisionTime, evidenceSet, s.policyRelease.Rules)
	if err != nil || !evaluation.Eligible || evaluation.ApprovalRequired != decision.ApprovalRequired || evaluation.Outcome != decision.Outcome {
		return CommandResult{}, fmt.Errorf("%w: %w: policy outcome changed", errDecisionStale, errPolicyBasisChanged)
	}
	commandID := shortHash("command|" + idempotencyKey + "|" + decisionTokenValue)
	expectedFulfillment, expectedPayment, expectedClaim, err := applyRecovery(&order, decision.OptionID)
	if err != nil {
		return CommandResult{}, err
	}
	order.Version++
	order.Recovery = &RecoveryState{
		CommandID: commandID, OptionID: decision.OptionID, Status: "completed", EvidenceRelease: decision.ReleaseID,
		ExecutedAt: now, ExpectedFulfillmentStatus: expectedFulfillment, ExpectedPaymentStatus: expectedPayment,
		ExpectedCarrierClaimStatus: expectedClaim,
	}
	s.orders[order.OrderID] = order
	result := CommandResult{
		CommandID: commandID, IdempotencyKey: idempotencyKey, OrderID: order.OrderID,
		OptionID: decision.OptionID, Status: "completed", OrderVersion: order.Version,
		ApprovalReference: approvalReference, DecisionToken: decisionTokenValue,
	}
	s.commands[idempotencyKey] = result
	return result, nil
}

func (s *CommerceStore) Verify(ctx context.Context, orderID, commandID string) (map[string]any, error) {
	order, err := s.GetOrder(ctx, orderID)
	if err != nil {
		return nil, err
	}
	verified := recoveryPostconditionsHold(order, commandID)
	return map[string]any{
		"order_id": orderID, "command_id": commandID, "verified": verified,
		"fulfillment_status": order.FulfillmentStatus, "payment_status": order.PaymentStatus,
		"carrier_claim_status": order.CarrierClaimStatus, "recovery": order.Recovery, "order_version": order.Version,
	}, nil
}

func (s *CommerceStore) Notify(ctx context.Context, orderID, commandID, channel, message string) (Notification, error) {
	if err := ctx.Err(); err != nil {
		return Notification{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	order, ok := s.orders[orderID]
	if !ok {
		return Notification{}, errOrderNotFound
	}
	if !recoveryPostconditionsHold(order, commandID) {
		return Notification{}, fmt.Errorf("command postconditions must hold before notification")
	}
	notification := Notification{NotificationID: shortHash("notification|" + orderID + "|" + commandID + "|" + channel), OrderID: orderID, CommandID: commandID, Channel: channel, Message: message, SentAt: s.now()}
	if existing, ok := s.notifications[notification.NotificationID]; ok {
		return existing, nil
	}
	s.notifications[notification.NotificationID] = notification
	return notification, nil
}

func (s *CommerceStore) validateEvidence(order Order, releaseID, releaseHash string, decisionTime time.Time, evidence, receipts []string, now time.Time) (map[string]struct{}, error) {
	if len(evidence) == 0 || len(receipts) != len(evidence) {
		return nil, fmt.Errorf("%w: every evidence section needs one exact-read receipt", errInvalidReceipt)
	}
	evidenceSet := make(map[string]struct{}, len(evidence))
	receiptSections := make(map[string]struct{}, len(receipts))
	for _, sectionID := range evidence {
		if _, known := s.sectionIDs[sectionID]; !known {
			return nil, fmt.Errorf("%w: %s", errUnknownEvidence, sectionID)
		}
		if _, duplicate := evidenceSet[sectionID]; duplicate {
			return nil, fmt.Errorf("duplicate evidence section %q", sectionID)
		}
		evidenceSet[sectionID] = struct{}{}
	}
	for _, receipt := range receipts {
		claims, err := verifyEvidenceReceipt(s.controlKey, receipt, now)
		if err != nil {
			return nil, fmt.Errorf("%w: %v", errInvalidReceipt, err)
		}
		if claims.ReleaseID != releaseID || claims.ReleaseHash != releaseHash || claims.TenantID != order.TenantID || !strings.EqualFold(claims.Jurisdiction, order.Jurisdiction) || !orderAllowsKnowledgeChannel(order, claims.Channel) || !claims.AsOf.Equal(decisionTime) {
			return nil, fmt.Errorf("%w: receipt scope does not match the decision and order", errInvalidReceipt)
		}
		if _, expected := evidenceSet[claims.SectionID]; !expected {
			return nil, fmt.Errorf("%w: receipt is for an uncited section", errInvalidReceipt)
		}
		if _, duplicate := receiptSections[claims.SectionID]; duplicate {
			return nil, fmt.Errorf("%w: duplicate receipt for %s", errInvalidReceipt, claims.SectionID)
		}
		receiptSections[claims.SectionID] = struct{}{}
	}
	if len(receiptSections) != len(evidenceSet) {
		return nil, fmt.Errorf("%w: receipts do not cover all cited sections", errInvalidReceipt)
	}
	return evidenceSet, nil
}

func orderAllowsKnowledgeChannel(order Order, evidenceChannel string) bool {
	return strings.EqualFold(evidenceChannel, "all") || strings.EqualFold(evidenceChannel, order.Channel) || (order.Carrier != "" && strings.EqualFold(evidenceChannel, "shipping"))
}

type policyEvaluation struct {
	Outcome          PolicyOutcome
	Eligible         bool
	ApprovalRequired bool
	Reasons          []string
}

func evaluatePolicy(order Order, optionID string, decisionTime time.Time, evidence map[string]struct{}, rules PolicyRules) (policyEvaluation, error) {
	if optionID == "same-sku-replacement" && hasEvidence(evidence, "demo-replacement-conflict-allow") && hasEvidence(evidence, "demo-replacement-conflict-deny") {
		return policyEvaluation{
			Outcome: PolicyOutcomeConflictingEvidence,
			Reasons: []string{"equally applicable replacement rules conflict and no reviewed precedence rule resolves them"},
		}, nil
	}
	eligible := false
	outcome := PolicyOutcomeDenied
	reasons := make([]string, 0, 3)
	switch optionID {
	case "wait-for-current-fulfillment":
		if !hasEvidence(evidence, "ftc-delay-consent-or-refund") {
			outcome = PolicyOutcomeInsufficientEvidence
			reasons = append(reasons, "the decisive delay consent policy was not provided")
			break
		}
		eligible = order.FulfillmentStatus == "late_unshipped" && decisionTime.After(order.PromisedShipAt) && order.ConsentToDelay != nil && *order.ConsentToDelay
		reasons = append(reasons, ternary(eligible, "recorded customer consent and delay-policy evidence permit waiting", "waiting requires late-unshipped state, explicit consent, and decisive delay-policy evidence"))
	case "cancel-and-refund":
		if !hasEvidence(evidence, "ftc-delay-consent-or-refund") {
			outcome = PolicyOutcomeInsufficientEvidence
			reasons = append(reasons, "the decisive shipment-delay policy was not provided")
			break
		}
		eligible = (order.FulfillmentStatus == "backordered" || order.FulfillmentStatus == "late_unshipped") && decisionTime.After(order.PromisedShipAt)
		reasons = append(reasons, ternary(eligible, "missed shipment promise is supported by consent-or-refund evidence", "order state or decisive delay evidence is missing"))
	case "same-sku-replacement":
		if !hasEvidence(evidence, "demo-auto-recovery-bounds") && !hasEvidence(evidence, "demo-replacement-conflict-allow") {
			outcome = PolicyOutcomeInsufficientEvidence
			reasons = append(reasons, "the decisive replacement authorization was not provided")
			break
		}
		eligible = order.InventoryAvailable && order.FraudRisk == "low" && !order.Restricted
		reasons = append(reasons, ternary(eligible, "replacement preconditions and enterprise policy evidence are satisfied", "replacement preconditions or decisive evidence are not satisfied"))
		if order.Restricted && order.RestrictionReason != "" {
			reasons = append(reasons, "restricted merchandise blocks replacement: "+order.RestrictionReason)
		}
	case "carrier-service-claim":
		if strings.EqualFold(order.Carrier, "FedEx") {
			if !hasEvidence(evidence, "fedex-guarantee-service-scope") {
				outcome = PolicyOutcomeInsufficientEvidence
				reasons = append(reasons, "the carrier service-scope evidence was not provided")
				break
			}
			if !order.CarrierPayerKnown || !order.CarrierLaneKnown || !order.CarrierShipDateKnown || !order.CarrierExclusionsCleared {
				outcome = PolicyOutcomeInsufficientEvidence
				reasons = append(reasons, "payer, lane, ship-date, or exclusion facts required by the FedEx service guarantee are missing")
				break
			}
		}
		if !hasEvidence(evidence, "usps-priority-express-eligibility") || !hasEvidence(evidence, "usps-priority-express-claim-window") {
			outcome = PolicyOutcomeInsufficientEvidence
			reasons = append(reasons, "carrier eligibility and claim-window evidence are both required")
			break
		}
		claimAge := time.Duration(-1)
		if order.MailedAt != nil {
			claimAge = decisionTime.Sub(*order.MailedAt)
		}
		eligible = strings.EqualFold(order.Carrier, "USPS") && strings.EqualFold(order.CarrierService, "Priority Mail Express") && order.TrackingNumber != "" && order.MailingReceipt && claimAge >= 48*time.Hour && claimAge < 31*24*time.Hour
		if strings.EqualFold(order.Carrier, "FedEx") {
			reasons = append(reasons, "the bundled FedEx source does not establish payer, lane, ship-date, and exclusion facts; deny safely")
		} else {
			reasons = append(reasons, ternary(eligible, "USPS service, tracking, receipt, mailing date, eligibility, and 2-to-30-day claim-window facts are all present", "carrier-specific eligibility, mailing date, or claim-window facts are incomplete"))
		}
	case "refund-merchandise":
		if order.FulfillmentStatus == "missing_in_transit" && !hasEvidence(evidence, "demo-missing-merchandise-refund") {
			outcome = PolicyOutcomeInsufficientEvidence
			reasons = append(reasons, "missing-merchandise refund authority was not provided")
			break
		}
		eligible = refundSupported(order, evidence)
		reasons = append(reasons, ternary(eligible, "order state and seller or loss policy evidence support a refund", "seller-specific return or missing-merchandise evidence does not support a refund"))
	default:
		return policyEvaluation{}, fmt.Errorf("unknown option_id %q", optionID)
	}
	if !eligible {
		return policyEvaluation{Outcome: outcome, Reasons: reasons}, nil
	}
	approvalRequired := (strings.Contains(optionID, "refund") && order.ItemValueUSD > rules.RefundApprovalOverUSD) || (optionID == "same-sku-replacement" && order.ItemValueUSD > rules.ReplacementApprovalOverUSD) || order.Restricted
	if approvalRequired {
		if !hasEvidence(evidence, "demo-approval-thresholds") {
			outcome = PolicyOutcomeInsufficientEvidence
			reasons = append(reasons, "approval threshold evidence is required before proposing a human approval")
			return policyEvaluation{Outcome: outcome, Reasons: reasons}, nil
		} else {
			outcome = PolicyOutcomeApprovalRequired
			reasons = append(reasons, "enterprise monetary or restricted-item threshold requires human approval")
		}
	} else {
		outcome = PolicyOutcomeAllowed
	}
	return policyEvaluation{Outcome: outcome, Eligible: true, ApprovalRequired: approvalRequired, Reasons: reasons}, nil
}

func refundSupported(order Order, evidence map[string]struct{}) bool {
	if order.FulfillmentStatus == "missing_in_transit" {
		return hasEvidence(evidence, "demo-missing-merchandise-refund")
	}
	if order.FulfillmentStatus != "delivered" || order.DeliveredAt == nil || order.ReturnRequestedAt == nil {
		return false
	}
	days := int(order.ReturnRequestedAt.Sub(*order.DeliveredAt).Hours() / 24)
	if days < 0 {
		return false
	}
	seller := strings.ToLower(order.Seller)
	if order.Category == "electronics" {
		return (seller == "target" && days <= 30 && hasEvidence(evidence, "target-electronics-return-window")) || (seller == "walmart" && days <= 30 && hasEvidence(evidence, "walmart-electronics-marketplace-exceptions"))
	}
	return (seller == "target" && days <= 90 && hasEvidence(evidence, "target-general-return-window")) || (seller == "walmart" && days <= 90 && hasEvidence(evidence, "walmart-general-return-window"))
}

func applyRecovery(order *Order, optionID string) (string, string, string, error) {
	switch optionID {
	case "wait-for-current-fulfillment":
		return order.FulfillmentStatus, order.PaymentStatus, order.CarrierClaimStatus, nil
	case "cancel-and-refund":
		order.FulfillmentStatus = "cancelled"
		order.PaymentStatus = "refunded"
	case "refund-merchandise":
		order.FulfillmentStatus = "resolved_refund"
		order.PaymentStatus = "refunded"
	case "same-sku-replacement":
		order.FulfillmentStatus = "replacement_created"
		order.InventoryAvailable = false
	case "carrier-service-claim":
		order.CarrierClaimStatus = "filed"
	default:
		return "", "", "", fmt.Errorf("unknown option_id %q", optionID)
	}
	return order.FulfillmentStatus, order.PaymentStatus, order.CarrierClaimStatus, nil
}

func recoveryPostconditionsHold(order Order, commandID string) bool {
	return order.Recovery != nil && order.Recovery.CommandID == commandID && order.Recovery.Status == "completed" && order.FulfillmentStatus == order.Recovery.ExpectedFulfillmentStatus && order.PaymentStatus == order.Recovery.ExpectedPaymentStatus && order.CarrierClaimStatus == order.Recovery.ExpectedCarrierClaimStatus
}

func hasEvidence(evidence map[string]struct{}, sectionID string) bool {
	_, ok := evidence[sectionID]
	return ok
}

func ternary(condition bool, yes, no string) string {
	if condition {
		return yes
	}
	return no
}

func decisionToken(order Order, optionID, releaseID, releaseHash string, decisionTime time.Time, evidence []string, expires time.Time, approvalRequired bool) string {
	copyEvidence := append([]string(nil), evidence...)
	sort.Strings(copyEvidence)
	prefix := "automatic."
	if approvalRequired {
		prefix = "approval."
	}
	return prefix + shortHash(fmt.Sprintf("%s|%d|%s|%s|%s|%s|%s|%d", order.OrderID, order.Version, optionID, releaseID, releaseHash, decisionTime.Format(time.RFC3339Nano), strings.Join(copyEvidence, ","), expires.Unix()))
}

func shortHash(value string) string {
	sum := sha256.Sum256([]byte(value))
	return hex.EncodeToString(sum[:16])
}
