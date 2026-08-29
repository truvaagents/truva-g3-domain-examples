package main

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"sync"

	"github.com/truvaagents/truva-g3/orchestration"
)

const (
	terminalOutcomeRecovered     = "recovered"
	terminalOutcomeApproval      = "approval_required"
	terminalOutcomeRejected      = "approval_rejected"
	terminalOutcomeAborted       = "approval_aborted"
	terminalOutcomeExpired       = "approval_expired"
	terminalOutcomeDenied        = "denied"
	terminalOutcomeInsufficient  = "insufficient_evidence"
	terminalOutcomeConflicting   = "conflicting_evidence"
	terminalOutcomePolicyChanged = "policy_changed_during_hitl"
	terminalOutcomeStateChanged  = "state_changed_during_hitl"
	terminalOutcomeBasisExpired  = "decision_basis_expired"
	terminalOutcomeNoMutation    = "no_mutation"
	terminalOutcomeUnverified    = "verification_required"
)

type recoveryOutcomeTracker struct {
	mu              sync.Mutex
	policyOutcome   string
	policyStepID    string
	commandExecuted bool
	verified        bool
	resumeGuard     string
	resumeReason    string
	order           *observedOrderAudit
	decision        *policyDecisionAudit
	command         *commandAudit
	verification    *verificationAudit
	notification    *notificationAudit
	evidence        map[string]evidenceAudit
	optionChannels  map[string]string
}

type observedOrderAudit struct {
	OrderID            string  `json:"order_id"`
	Channel            string  `json:"channel"`
	Version            int     `json:"version"`
	FulfillmentStatus  string  `json:"fulfillment_status"`
	PaymentStatus      string  `json:"payment_status"`
	ItemValueUSD       float64 `json:"item_value_usd"`
	InventoryAvailable bool    `json:"inventory_available"`
	FraudRisk          string  `json:"fraud_risk"`
	Restricted         bool    `json:"restricted"`
}

type policyDecisionAudit struct {
	OrderID            string   `json:"order_id"`
	OptionID           string   `json:"option_id"`
	Eligible           bool     `json:"eligible"`
	ApprovalRequired   bool     `json:"approval_required"`
	Outcome            string   `json:"outcome"`
	Reasons            []string `json:"reasons"`
	EvidenceSectionIDs []string `json:"evidence_section_ids"`
	ReleaseID          string   `json:"release_id"`
	ReleaseHash        string   `json:"release_hash"`
	OrderVersion       int      `json:"order_version"`
	ExpiresAt          string   `json:"expires_at"`
}

type commandAudit struct {
	CommandID         string `json:"command_id"`
	OrderID           string `json:"order_id"`
	OptionID          string `json:"option_id"`
	Status            string `json:"status"`
	OrderVersion      int    `json:"order_version"`
	Replayed          bool   `json:"replayed"`
	ApprovalReference string `json:"approval_reference,omitempty"`
}

type verificationAudit struct {
	OrderID            string `json:"order_id"`
	CommandID          string `json:"command_id"`
	Verified           bool   `json:"verified"`
	FulfillmentStatus  string `json:"fulfillment_status"`
	PaymentStatus      string `json:"payment_status"`
	CarrierClaimStatus string `json:"carrier_claim_status,omitempty"`
	OrderVersion       int    `json:"order_version"`
}

type notificationAudit struct {
	NotificationID string `json:"notification_id"`
	OrderID        string `json:"order_id"`
	CommandID      string `json:"command_id"`
	Channel        string `json:"channel"`
}

type evidenceAudit struct {
	SectionID   string `json:"section_id"`
	Title       string `json:"title"`
	SourceURL   string `json:"source_url"`
	ReleaseID   string `json:"release_id"`
	ReleaseHash string `json:"release_hash"`
}

func (tracker *recoveryOutcomeTracker) Restore(results map[string]*orchestration.StepResult) {
	for _, result := range results {
		if result != nil {
			tracker.Record(orchestration.RoutingStep{}, *result)
		}
	}
}

func (tracker *recoveryOutcomeTracker) Record(step orchestration.RoutingStep, result orchestration.StepResult) {
	capability := result.Capability
	if capability == "" {
		capability, _ = step.Metadata["capability"].(string)
	}
	if !result.Success {
		if capability == "execute_recovery_command" {
			tracker.recordResumeGuard(result.Error)
		}
		return
	}
	var envelope struct {
		Success bool            `json:"success"`
		Data    json.RawMessage `json:"data"`
	}
	if err := json.Unmarshal([]byte(result.Response), &envelope); err != nil || !envelope.Success {
		return
	}
	tracker.mu.Lock()
	defer tracker.mu.Unlock()
	switch capability {
	case "get_order_context":
		var order observedOrderAudit
		if json.Unmarshal(envelope.Data, &order) == nil && order.OrderID != "" {
			tracker.order = &order
		}
	case "list_fulfillment_options":
		var optionList struct {
			Options []struct {
				OptionID        string `json:"option_id"`
				EvidenceChannel string `json:"evidence_channel"`
			} `json:"options"`
		}
		if json.Unmarshal(envelope.Data, &optionList) == nil {
			if tracker.optionChannels == nil {
				tracker.optionChannels = make(map[string]string)
			}
			for _, option := range optionList.Options {
				if option.OptionID != "" && option.EvidenceChannel != "" {
					tracker.optionChannels[option.OptionID] = option.EvidenceChannel
				}
			}
		}
	case "read_exact_section":
		var exact struct {
			Release struct {
				ReleaseID   string `json:"release_id"`
				ContentHash string `json:"content_hash"`
			} `json:"release"`
			Section struct {
				SectionID string `json:"section_id"`
				Title     string `json:"title"`
			} `json:"section"`
			Source struct {
				URL string `json:"url"`
			} `json:"source"`
		}
		if json.Unmarshal(envelope.Data, &exact) == nil && exact.Section.SectionID != "" {
			if tracker.evidence == nil {
				tracker.evidence = make(map[string]evidenceAudit)
			}
			tracker.evidence[exact.Section.SectionID] = evidenceAudit{
				SectionID: exact.Section.SectionID, Title: exact.Section.Title, SourceURL: exact.Source.URL,
				ReleaseID: exact.Release.ReleaseID, ReleaseHash: exact.Release.ContentHash,
			}
		}
	case "evaluate_recovery_policy":
		var decision policyDecisionAudit
		if json.Unmarshal(envelope.Data, &decision) == nil && decision.Outcome != "" {
			tracker.policyOutcome = decision.Outcome
			tracker.policyStepID = result.StepID
			if tracker.policyStepID == "" {
				tracker.policyStepID = step.StepID
			}
			tracker.decision = &decision
		}
	case "execute_recovery_command":
		// A successful tool envelope means the mutation may have happened even
		// when a malformed response omits its audit fields. Fail closed as an
		// unverified execution rather than misclassifying it as no mutation.
		tracker.commandExecuted = true
		var command commandAudit
		if json.Unmarshal(envelope.Data, &command) == nil && command.CommandID != "" {
			tracker.command = &command
		}
	case "verify_order_outcome":
		var verification verificationAudit
		if json.Unmarshal(envelope.Data, &verification) == nil {
			tracker.verified = verification.Verified
			tracker.verification = &verification
		}
	case "send_synthetic_notification":
		var notification notificationAudit
		if json.Unmarshal(envelope.Data, &notification) == nil && notification.NotificationID != "" {
			tracker.notification = &notification
		}
	}
}

type recoveryExecutionBasis struct {
	PolicyStepID string
	Decision     policyDecisionAudit
	Channel      string
}

func (tracker *recoveryOutcomeTracker) ExecutionBasis() (recoveryExecutionBasis, bool) {
	tracker.mu.Lock()
	defer tracker.mu.Unlock()
	if tracker.decision == nil || tracker.policyStepID == "" {
		return recoveryExecutionBasis{}, false
	}
	basis := recoveryExecutionBasis{PolicyStepID: tracker.policyStepID, Decision: *tracker.decision, Channel: "online"}
	if tracker.order != nil && tracker.order.Channel != "" {
		basis.Channel = tracker.order.Channel
	}
	return basis, true
}

func (tracker *recoveryOutcomeTracker) OrderChannel() (string, bool) {
	tracker.mu.Lock()
	defer tracker.mu.Unlock()
	if tracker.order == nil || tracker.order.Channel == "" {
		return "", false
	}
	return tracker.order.Channel, true
}

func (tracker *recoveryOutcomeTracker) EvidenceChannel(optionID string) (string, bool) {
	tracker.mu.Lock()
	defer tracker.mu.Unlock()
	channel := tracker.optionChannels[optionID]
	return channel, channel != ""
}

func (tracker *recoveryOutcomeTracker) recordResumeGuard(detail string) {
	terminal := classifyResumeGuard(detail)
	if terminal == "" {
		return
	}
	tracker.mu.Lock()
	defer tracker.mu.Unlock()
	tracker.resumeGuard = terminal
	tracker.resumeReason = detail
}

func classifyResumeGuard(detail string) string {
	switch {
	case strings.Contains(detail, "POLICY_BASIS_CHANGED"):
		return terminalOutcomePolicyChanged
	case strings.Contains(detail, "ORDER_STATE_CHANGED"):
		return terminalOutcomeStateChanged
	case strings.Contains(detail, "DECISION_BASIS_EXPIRED"):
		return terminalOutcomeBasisExpired
	default:
		return ""
	}
}

func (tracker *recoveryOutcomeTracker) Result() (terminalOutcome, policyOutcome string) {
	tracker.mu.Lock()
	defer tracker.mu.Unlock()
	if tracker.resumeGuard != "" {
		return tracker.resumeGuard, tracker.policyOutcome
	}
	if tracker.commandExecuted {
		if tracker.verified {
			return terminalOutcomeRecovered, tracker.policyOutcome
		}
		return terminalOutcomeUnverified, tracker.policyOutcome
	}
	switch tracker.policyOutcome {
	case "approval_required":
		return terminalOutcomeApproval, tracker.policyOutcome
	case "denied":
		return terminalOutcomeDenied, tracker.policyOutcome
	case "insufficient_evidence":
		return terminalOutcomeInsufficient, tracker.policyOutcome
	case "conflicting_evidence":
		return terminalOutcomeConflicting, tracker.policyOutcome
	default:
		return terminalOutcomeNoMutation, tracker.policyOutcome
	}
}

func incompleteGovernedOutcomeError(terminalOutcome, policyOutcome string) error {
	if terminalOutcome == terminalOutcomeUnverified {
		return fmt.Errorf("recovery command executed without successful postcondition verification")
	}
	switch policyOutcome {
	case "allowed":
		if terminalOutcome != terminalOutcomeRecovered {
			return fmt.Errorf("allowed recovery plan ended without verified command execution")
		}
	case "approval_required":
		return fmt.Errorf("approval-required recovery plan ended without a governed HITL checkpoint")
	}
	return nil
}

func incompleteApprovedResumeOutcomeError(terminalOutcome, policyOutcome string) error {
	if policyOutcome != "approval_required" {
		return fmt.Errorf("approved resume is missing its approval-required policy decision")
	}
	if terminalOutcome != terminalOutcomeRecovered {
		return fmt.Errorf("approved recovery did not complete with successful postcondition verification")
	}
	return nil
}

func isSafeStaleBasisOutcome(terminalOutcome string) bool {
	switch terminalOutcome {
	case terminalOutcomePolicyChanged, terminalOutcomeStateChanged, terminalOutcomeBasisExpired:
		return true
	default:
		return false
	}
}

func (tracker *recoveryOutcomeTracker) ResumeReason() string {
	tracker.mu.Lock()
	defer tracker.mu.Unlock()
	return tracker.resumeReason
}

func (tracker *recoveryOutcomeTracker) ApprovalReference() string {
	tracker.mu.Lock()
	defer tracker.mu.Unlock()
	if tracker.command == nil {
		return ""
	}
	return tracker.command.ApprovalReference
}

func (tracker *recoveryOutcomeTracker) AuditSummary() map[string]any {
	tracker.mu.Lock()
	defer tracker.mu.Unlock()
	summary := map[string]any{
		"command_executed": tracker.commandExecuted,
		"verified":         tracker.verified,
	}
	if tracker.order != nil {
		summary["observed_order"] = *tracker.order
	}
	if tracker.decision != nil {
		summary["decision"] = *tracker.decision
	}
	if tracker.command != nil {
		summary["command"] = *tracker.command
	}
	if tracker.verification != nil {
		summary["verification"] = *tracker.verification
	}
	if tracker.notification != nil {
		summary["notification"] = *tracker.notification
	}
	if tracker.resumeGuard != "" {
		summary["resume_guard"] = map[string]string{"outcome": tracker.resumeGuard, "reason": tracker.resumeReason}
	}
	if len(tracker.evidence) > 0 {
		ids := make([]string, 0, len(tracker.evidence))
		for sectionID := range tracker.evidence {
			ids = append(ids, sectionID)
		}
		sort.Strings(ids)
		evidence := make([]evidenceAudit, 0, len(ids))
		for _, sectionID := range ids {
			evidence = append(evidence, tracker.evidence[sectionID])
		}
		summary["evidence"] = evidence
	}
	return summary
}

func (tracker *recoveryOutcomeTracker) DeterministicResponse(terminalOutcome, policyOutcome, checkpointID string) string {
	tracker.mu.Lock()
	defer tracker.mu.Unlock()
	var response strings.Builder
	fmt.Fprintf(&response, "Governed order recovery outcome: %s.", terminalOutcome)
	if policyOutcome != "" {
		fmt.Fprintf(&response, " Commerce Core policy outcome: %s.", policyOutcome)
	}
	if tracker.decision != nil {
		fmt.Fprintf(&response, " Order %s option %s was evaluated at order version %d against release %s (%s); eligible=%t, approval_required=%t.",
			tracker.decision.OrderID, tracker.decision.OptionID, tracker.decision.OrderVersion,
			tracker.decision.ReleaseID, tracker.decision.ReleaseHash, tracker.decision.Eligible, tracker.decision.ApprovalRequired)
		if len(tracker.decision.Reasons) > 0 {
			fmt.Fprintf(&response, " Reasons: %s.", strings.Join(tracker.decision.Reasons, "; "))
		}
		if len(tracker.decision.EvidenceSectionIDs) > 0 {
			fmt.Fprintf(&response, " Exact evidence sections: %s.", strings.Join(tracker.decision.EvidenceSectionIDs, ", "))
		}
	}
	if checkpointID == "" && tracker.command != nil {
		checkpointID = tracker.command.ApprovalReference
	}
	if checkpointID != "" {
		fmt.Fprintf(&response, " Approval checkpoint: %s.", checkpointID)
	}
	if tracker.command != nil {
		fmt.Fprintf(&response, " Command %s completed for %s at order version %d (replayed=%t).",
			tracker.command.CommandID, tracker.command.OptionID, tracker.command.OrderVersion, tracker.command.Replayed)
	} else {
		response.WriteString(" No recovery command was executed.")
	}
	if tracker.verification != nil {
		fmt.Fprintf(&response, " Verification for command %s returned verified=%t with fulfillment_status=%s, payment_status=%s, order_version=%d.",
			tracker.verification.CommandID, tracker.verification.Verified, tracker.verification.FulfillmentStatus,
			tracker.verification.PaymentStatus, tracker.verification.OrderVersion)
	}
	if tracker.notification != nil {
		fmt.Fprintf(&response, " Synthetic notification %s was recorded on channel %s.", tracker.notification.NotificationID, tracker.notification.Channel)
	}
	if tracker.resumeGuard != "" {
		fmt.Fprintf(&response, " The approved basis was rejected by the %s guard; resubmit the same event to replan from current state.", tracker.resumeGuard)
	}
	return response.String()
}
