package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/truvaagents/truva-g3/core"
)

type orderRequest struct {
	OrderID string `json:"order_id"`
}
type evaluateRequest struct {
	OrderID            string   `json:"order_id"`
	OptionID           string   `json:"option_id"`
	ReleaseID          string   `json:"release_id"`
	ReleaseHash        string   `json:"release_hash"`
	EvidenceSectionIDs []string `json:"evidence_section_ids"`
	EvidenceReceipts   []string `json:"evidence_receipts"`
	DecisionTime       string   `json:"decision_time"`
}
type executeRequest struct {
	DecisionToken     string `json:"decision_token"`
	IdempotencyKey    string `json:"idempotency_key"`
	ApprovalReference string `json:"approval_reference,omitempty"`
	ApprovalProof     string `json:"approval_proof,omitempty"`
}
type verifyRequest struct {
	OrderID   string `json:"order_id"`
	CommandID string `json:"command_id"`
}
type notifyRequest struct {
	OrderID   string `json:"order_id"`
	CommandID string `json:"command_id"`
	Channel   string `json:"channel"`
	Message   string `json:"message"`
}

func (t *CommerceTool) handleGetOrder(w http.ResponseWriter, r *http.Request) {
	observer := t.observeRequest(r, "get_order_context")
	defer observer.finish()
	var request orderRequest
	if !decodeCommerceJSON(w, r, &request, observer) {
		return
	}
	normalizeCommerceStrings(&request.OrderID)
	if !requireFields(w, observer, request.OrderID) {
		return
	}
	observer.validated(map[string]any{"order_id": request.OrderID})
	order, err := t.store.GetOrder(r.Context(), request.OrderID)
	if err != nil {
		t.handleCommerceError(w, observer, err)
		return
	}
	observer.succeed(map[string]any{"order_id": request.OrderID, "order_version": order.Version})
	writeCommerceSuccess(w, order)
}

func (t *CommerceTool) handleProcessNormalOrder(w http.ResponseWriter, r *http.Request) {
	observer := t.observeRequest(r, "process_normal_order")
	defer observer.finish()
	var request orderRequest
	if !decodeCommerceJSON(w, r, &request, observer) {
		return
	}
	normalizeCommerceStrings(&request.OrderID)
	if !requireFields(w, observer, request.OrderID) {
		return
	}
	observer.validated(map[string]any{"order_id": request.OrderID, "execution_path": "deterministic"})
	result, err := t.store.ProcessNormalOrder(r.Context(), request.OrderID)
	if err != nil {
		t.handleCommerceError(w, observer, err)
		return
	}
	observer.succeed(map[string]any{
		"order_id": request.OrderID, "terminal_outcome": result.TerminalOutcome,
		"agent_invoked": result.AgentInvoked, "llm_invoked": result.LLMInvoked,
	})
	writeCommerceSuccess(w, result)
}

func (t *CommerceTool) handleListOptions(w http.ResponseWriter, r *http.Request) {
	observer := t.observeRequest(r, "list_fulfillment_options")
	defer observer.finish()
	var request orderRequest
	if !decodeCommerceJSON(w, r, &request, observer) {
		return
	}
	normalizeCommerceStrings(&request.OrderID)
	if !requireFields(w, observer, request.OrderID) {
		return
	}
	observer.validated(map[string]any{"order_id": request.OrderID})
	options, err := t.store.ListOptions(r.Context(), request.OrderID)
	if err != nil {
		t.handleCommerceError(w, observer, err)
		return
	}
	observer.succeed(map[string]any{"order_id": request.OrderID, "option_count": len(options)})
	writeCommerceSuccess(w, map[string]any{"options": options})
}

func (t *CommerceTool) handleEvaluate(w http.ResponseWriter, r *http.Request) {
	observer := t.observeRequest(r, "evaluate_recovery_policy")
	defer observer.finish()
	var request evaluateRequest
	if !decodeCommerceJSON(w, r, &request, observer) {
		return
	}
	normalizeCommerceStrings(&request.OrderID, &request.OptionID, &request.ReleaseID, &request.ReleaseHash, &request.DecisionTime)
	request.EvidenceSectionIDs = normalizeCommerceList(request.EvidenceSectionIDs)
	request.EvidenceReceipts = normalizeCommerceList(request.EvidenceReceipts)
	if !requireFields(w, observer, request.OrderID, request.OptionID, request.ReleaseID, request.ReleaseHash, request.DecisionTime) {
		return
	}
	if len(request.EvidenceSectionIDs) == 0 || hasEmptyCommerceValue(request.EvidenceSectionIDs) {
		err := fmt.Errorf("evidence_section_ids must contain non-empty values")
		observer.reject(err, "validation_error", map[string]any{"order_id": request.OrderID, "option_id": request.OptionID})
		sendCommerceError(w, http.StatusBadRequest, "MISSING_FIELD", "evidence_section_ids is required")
		return
	}
	if len(request.EvidenceReceipts) != len(request.EvidenceSectionIDs) || hasEmptyCommerceValue(request.EvidenceReceipts) {
		err := fmt.Errorf("one non-empty evidence receipt is required for every evidence section")
		observer.reject(err, "validation_error", map[string]any{"order_id": request.OrderID, "option_id": request.OptionID})
		sendCommerceError(w, http.StatusBadRequest, "MISSING_FIELD", "one evidence_receipt is required for every evidence_section_id")
		return
	}
	decisionTime, err := time.Parse(time.RFC3339, request.DecisionTime)
	if err != nil {
		observer.reject(err, "validation_error", map[string]any{"order_id": request.OrderID, "option_id": request.OptionID})
		sendCommerceError(w, http.StatusBadRequest, "INVALID_DECISION_TIME", "decision_time must be RFC3339")
		return
	}
	observer.validated(map[string]any{"order_id": request.OrderID, "option_id": request.OptionID, "release_id": request.ReleaseID, "evidence_count": len(request.EvidenceSectionIDs), "decision_time": request.DecisionTime})
	decision, err := t.store.Evaluate(r.Context(), request.OrderID, request.OptionID, request.ReleaseID, request.ReleaseHash, decisionTime, request.EvidenceSectionIDs, request.EvidenceReceipts)
	if err != nil {
		t.handleCommerceError(w, observer, err)
		return
	}
	observer.succeed(map[string]any{"order_id": request.OrderID, "option_id": request.OptionID, "eligible": decision.Eligible, "approval_required": decision.ApprovalRequired})
	writeCommerceSuccess(w, decision)
}

func (t *CommerceTool) handleExecute(w http.ResponseWriter, r *http.Request) {
	observer := t.observeRequest(r, "execute_recovery_command")
	defer observer.finish()
	var request executeRequest
	if !decodeCommerceJSON(w, r, &request, observer) {
		return
	}
	normalizeCommerceStrings(&request.DecisionToken, &request.IdempotencyKey, &request.ApprovalReference, &request.ApprovalProof)
	if !requireFields(w, observer, request.DecisionToken, request.IdempotencyKey) {
		return
	}
	observer.validated(map[string]any{"idempotency_key": request.IdempotencyKey, "approval_supplied": request.ApprovalReference != "" && request.ApprovalProof != ""})
	result, err := t.store.Execute(r.Context(), request.DecisionToken, request.IdempotencyKey, request.ApprovalReference, request.ApprovalProof)
	if err != nil {
		t.handleCommerceError(w, observer, err)
		return
	}
	observer.succeed(map[string]any{"order_id": result.OrderID, "option_id": result.OptionID, "command_id": result.CommandID, "replayed": result.Replayed})
	writeCommerceSuccess(w, result)
}

func (t *CommerceTool) handleVerify(w http.ResponseWriter, r *http.Request) {
	observer := t.observeRequest(r, "verify_order_outcome")
	defer observer.finish()
	var request verifyRequest
	if !decodeCommerceJSON(w, r, &request, observer) {
		return
	}
	normalizeCommerceStrings(&request.OrderID, &request.CommandID)
	if !requireFields(w, observer, request.OrderID, request.CommandID) {
		return
	}
	observer.validated(map[string]any{"order_id": request.OrderID, "command_id": request.CommandID})
	result, err := t.store.Verify(r.Context(), request.OrderID, request.CommandID)
	if err != nil {
		t.handleCommerceError(w, observer, err)
		return
	}
	observer.succeed(map[string]any{"order_id": request.OrderID, "command_id": request.CommandID, "verified": result["verified"]})
	writeCommerceSuccess(w, result)
}

func (t *CommerceTool) handleNotify(w http.ResponseWriter, r *http.Request) {
	observer := t.observeRequest(r, "send_synthetic_notification")
	defer observer.finish()
	var request notifyRequest
	if !decodeCommerceJSON(w, r, &request, observer) {
		return
	}
	normalizeCommerceStrings(&request.OrderID, &request.CommandID, &request.Channel, &request.Message)
	if !requireFields(w, observer, request.OrderID, request.CommandID, request.Channel, request.Message) {
		return
	}
	observer.validated(map[string]any{"order_id": request.OrderID, "command_id": request.CommandID, "channel": request.Channel})
	notification, err := t.store.Notify(r.Context(), request.OrderID, request.CommandID, request.Channel, request.Message)
	if err != nil {
		t.handleCommerceError(w, observer, err)
		return
	}
	observer.succeed(map[string]any{"order_id": request.OrderID, "command_id": request.CommandID, "notification_id": notification.NotificationID, "channel": request.Channel})
	writeCommerceSuccess(w, notification)
}

func (t *CommerceTool) handleCommerceError(w http.ResponseWriter, observer *commerceRequestObserver, err error) {
	status := http.StatusBadRequest
	code := "INVALID_OPERATION"
	message := err.Error()
	errorType := "business_error"
	switch {
	case errors.Is(err, errOrderNotFound):
		status, code, message, errorType = http.StatusNotFound, "ORDER_NOT_FOUND", "order not found", "not_found"
	case errors.Is(err, errDecisionNotFound):
		status, code, message, errorType = http.StatusNotFound, "DECISION_NOT_FOUND", "policy decision not found", "not_found"
	case errors.Is(err, errPolicyBasisChanged):
		status, code, errorType = http.StatusConflict, "POLICY_BASIS_CHANGED", "policy_conflict"
	case errors.Is(err, errOrderStateChanged):
		status, code, errorType = http.StatusConflict, "ORDER_STATE_CHANGED", "state_conflict"
	case errors.Is(err, errDecisionExpired):
		status, code, errorType = http.StatusConflict, "DECISION_BASIS_EXPIRED", "evidence_expired"
	case errors.Is(err, errDecisionStale):
		status, code, errorType = http.StatusConflict, "DECISION_STALE", "state_conflict"
	case errors.Is(err, errReleaseMismatch):
		status, code, errorType = http.StatusConflict, "RELEASE_MISMATCH", "release_conflict"
	case errors.Is(err, errUnknownEvidence):
		status, code, errorType = http.StatusBadRequest, "UNKNOWN_EVIDENCE", "validation_error"
	case errors.Is(err, errInvalidReceipt):
		status, code, errorType = http.StatusBadRequest, "INVALID_EVIDENCE_RECEIPT", "validation_error"
	case errors.Is(err, errApprovalRequired):
		status, code, errorType = http.StatusForbidden, "APPROVAL_REQUIRED", "authorization_error"
	case errors.Is(err, errIdempotencyConflict):
		status, code, errorType = http.StatusConflict, "IDEMPOTENCY_CONFLICT", "state_conflict"
	case errors.Is(err, errNotEligible):
		status, code, errorType = http.StatusConflict, "NOT_ELIGIBLE", "policy_denial"
	}
	observer.reject(err, errorType, nil)
	sendCommerceError(w, status, code, message)
}

func requireFields(w http.ResponseWriter, observer *commerceRequestObserver, values ...string) bool {
	for _, value := range values {
		if strings.TrimSpace(value) == "" {
			err := fmt.Errorf("a documented required field is empty")
			observer.reject(err, "validation_error", nil)
			sendCommerceError(w, http.StatusBadRequest, "MISSING_FIELD", "all documented required fields must be non-empty")
			return false
		}
	}
	return true
}

func decodeCommerceJSON(w http.ResponseWriter, r *http.Request, target any, observer *commerceRequestObserver) bool {
	if r.Method != http.MethodPost {
		err := fmt.Errorf("method %s is not allowed", r.Method)
		observer.reject(err, "method_error", nil)
		sendCommerceError(w, http.StatusMethodNotAllowed, "METHOD_NOT_ALLOWED", "use POST")
		return false
	}
	r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		observer.fail(err, "decode_error", nil)
		sendCommerceError(w, http.StatusBadRequest, "INVALID_REQUEST", fmt.Sprintf("invalid JSON request: %v", err))
		return false
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		err = fmt.Errorf("request body must contain exactly one JSON object")
		observer.fail(err, "decode_error", nil)
		sendCommerceError(w, http.StatusBadRequest, "INVALID_REQUEST", "request body must contain exactly one JSON object")
		return false
	}
	return true
}

func normalizeCommerceStrings(values ...*string) {
	for _, value := range values {
		*value = strings.TrimSpace(*value)
	}
}

func normalizeCommerceList(values []string) []string {
	for index := range values {
		values[index] = strings.TrimSpace(values[index])
	}
	return values
}

func hasEmptyCommerceValue(values []string) bool {
	for _, value := range values {
		if value == "" {
			return true
		}
	}
	return false
}

func writeCommerceSuccess(w http.ResponseWriter, data any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(core.ToolResponse{Success: true, Data: data})
}
func sendCommerceError(w http.ResponseWriter, status int, code, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(core.ToolResponse{Success: false, Error: &core.ToolError{Code: code, Message: message, Retryable: status >= 500}})
}
