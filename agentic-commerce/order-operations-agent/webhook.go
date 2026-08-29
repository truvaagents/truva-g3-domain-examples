package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/redis/go-redis/v9"
	"github.com/truvaagents/truva-g3/core"
	"github.com/truvaagents/truva-g3/telemetry"
)

type orderRiskEvent struct {
	EventID           string `json:"event_id"`
	OrderID           string `json:"order_id"`
	Signal            string `json:"signal"`
	RequestedOutcome  string `json:"requested_outcome,omitempty"`
	RequestedOptionID string `json:"requested_option_id,omitempty"`
	DecisionTime      string `json:"decision_time,omitempty"`
}

var supportedRecoveryOptions = map[string]struct{}{
	"wait-for-current-fulfillment": {},
	"cancel-and-refund":            {},
	"refund-merchandise":           {},
	"same-sku-replacement":         {},
	"carrier-service-claim":        {},
}

type webhookReceiver struct {
	redis  *redis.Client
	queue  core.TaskQueue
	store  core.TaskStore
	logger core.Logger
	ready  func() bool
}

func (receiver *webhookReceiver) Handle(w http.ResponseWriter, r *http.Request) {
	start := time.Now()
	if r.Method != http.MethodPost {
		receiver.recordRejected(r, start, "invalid_method", fmt.Errorf("method %s is not allowed", r.Method))
		http.Error(w, "use POST", http.StatusMethodNotAllowed)
		return
	}
	if receiver.ready != nil && !receiver.ready() {
		receiver.recordRejected(r, start, "not_ready", fmt.Errorf("orchestrator is not ready"))
		writeAgentJSON(w, http.StatusServiceUnavailable, map[string]any{"error": "orchestrator is not ready", "status": "not_ready"})
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	var event orderRiskEvent
	if err := decoder.Decode(&event); err != nil {
		receiver.recordRejected(r, start, "invalid", err)
		http.Error(w, "invalid event: "+err.Error(), http.StatusBadRequest)
		return
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		receiver.recordRejected(r, start, "invalid", fmt.Errorf("request body must contain exactly one JSON object"))
		http.Error(w, "request body must contain exactly one JSON object", http.StatusBadRequest)
		return
	}
	event.EventID = strings.TrimSpace(event.EventID)
	event.OrderID = strings.TrimSpace(event.OrderID)
	event.Signal = strings.TrimSpace(event.Signal)
	event.RequestedOutcome = strings.TrimSpace(event.RequestedOutcome)
	event.RequestedOptionID = strings.TrimSpace(event.RequestedOptionID)
	event.DecisionTime = strings.TrimSpace(event.DecisionTime)
	if err := validateOrderRiskEvent(event); err != nil {
		receiver.recordRejected(r, start, "invalid", err)
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if event.DecisionTime == "" {
		event.DecisionTime = time.Now().UTC().Format(time.RFC3339)
	} else if _, err := time.Parse(time.RFC3339, event.DecisionTime); err != nil {
		receiver.recordRejected(r, start, "invalid", err)
		http.Error(w, "decision_time must be RFC3339", http.StatusBadRequest)
		return
	}
	dedupKey := "truvag3:commerce:event:" + event.EventID
	accepted, err := receiver.redis.SetNX(r.Context(), dedupKey, event.OrderID, 24*time.Hour).Result()
	if err != nil {
		receiver.recordRejected(r, start, "dependency_error", err)
		http.Error(w, "dedup store unavailable", http.StatusServiceUnavailable)
		return
	}
	if !accepted {
		receiver.retryFailedTask(w, r, event, start)
		return
	}
	receiver.createAndEnqueue(w, r, event, dedupKey, false, start)
}

func (receiver *webhookReceiver) retryFailedTask(w http.ResponseWriter, r *http.Request, event orderRiskEvent, start time.Time) {
	taskID := "order-event-" + event.EventID
	task, err := receiver.store.Get(r.Context(), taskID)
	if err != nil {
		receiver.recordRejected(r, start, "dependency_error", err)
		http.Error(w, "dedup task lookup unavailable", http.StatusServiceUnavailable)
		return
	}
	if task.Status != core.TaskStatusFailed {
		telemetry.Counter("order_operations.events_received", "status", "duplicate")
		telemetry.Counter("order_operations.events_deduplicated", "outcome", "active_or_completed")
		receiver.logger.InfoWithContext(r.Context(), "Duplicate order-risk event was not requeued", map[string]any{
			"operation": "receive_order_risk_event", "status": "duplicate", "event_id": event.EventID,
			"order_id": event.OrderID, "task_id": taskID, "task_status": task.Status, "duration_ms": time.Since(start).Milliseconds(),
		})
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusConflict)
		_ = json.NewEncoder(w).Encode(map[string]any{"status": "duplicate", "event_id": event.EventID, "task_id": taskID, "task_status": task.Status})
		return
	}
	retryKey := "truvag3:commerce:event-retry:" + event.EventID
	locked, err := receiver.redis.SetNX(r.Context(), retryKey, "1", time.Minute).Result()
	if err != nil {
		receiver.recordRejected(r, start, "dependency_error", err)
		http.Error(w, "retry lock unavailable", http.StatusServiceUnavailable)
		return
	}
	if !locked {
		telemetry.Counter("order_operations.events_received", "status", "duplicate")
		telemetry.Counter("order_operations.events_deduplicated", "outcome", "retry_in_progress")
		http.Error(w, "event retry is already in progress", http.StatusConflict)
		return
	}
	defer func() { _ = receiver.redis.Del(r.Context(), retryKey).Err() }()
	if task.Input == nil {
		task.Input = map[string]any{}
	}
	if result, ok := task.Result.(map[string]any); ok && result["status"] == "replan_required" {
		// A retry after stale policy/state rejection is a new decision boundary.
		// Preserve the event identity for deduplication, but do not reuse the old
		// as-of time that made the invalidated policy basis applicable.
		task.Input["decision_time"] = time.Now().UTC().Format(time.RFC3339)
	}
	task.Status = core.TaskStatusQueued
	task.Error = nil
	task.Result = nil
	task.Progress = nil
	task.StartedAt = nil
	task.CompletedAt = nil
	if err := receiver.store.Update(r.Context(), task); err != nil {
		receiver.recordRejected(r, start, "dependency_error", err)
		http.Error(w, "reset failed task: "+err.Error(), http.StatusInternalServerError)
		return
	}
	if err := receiver.queue.Enqueue(r.Context(), task); err != nil {
		task.Status = core.TaskStatusFailed
		_ = receiver.store.Update(r.Context(), task)
		receiver.recordRejected(r, start, "dependency_error", err)
		http.Error(w, "enqueue retry: "+err.Error(), http.StatusServiceUnavailable)
		return
	}
	telemetry.Counter("order_operations.events_deduplicated", "outcome", "requeued")
	receiver.writeAccepted(w, r, event, task, true, start)
}

func validateOrderRiskEvent(event orderRiskEvent) error {
	if event.EventID == "" || event.OrderID == "" || event.Signal == "" {
		return fmt.Errorf("event_id, order_id, and signal are required")
	}
	if len(event.EventID) > 128 || len(event.OrderID) > 128 || len(event.Signal) > 256 || len(event.RequestedOutcome) > 256 {
		return fmt.Errorf("event_id/order_id must be at most 128 characters and signal/requested_outcome at most 256 characters")
	}
	if event.RequestedOptionID != "" {
		if _, supported := supportedRecoveryOptions[event.RequestedOptionID]; !supported {
			return fmt.Errorf("requested_option_id is not a supported bounded recovery option")
		}
	}
	return nil
}

func (receiver *webhookReceiver) createAndEnqueue(w http.ResponseWriter, r *http.Request, event orderRiskEvent, dedupKey string, retry bool, start time.Time) {
	task := core.NewTask("order-event-"+event.EventID, "order_recovery", map[string]any{
		"event_id": event.EventID, "order_id": event.OrderID, "signal": event.Signal,
		"requested_outcome": event.RequestedOutcome, "requested_option_id": event.RequestedOptionID, "decision_time": event.DecisionTime,
	})
	trace := telemetry.GetTraceContext(r.Context())
	task.SetTraceContext(trace.TraceID, trace.SpanID)
	if err := receiver.store.Create(r.Context(), task); err != nil {
		_ = receiver.redis.Del(r.Context(), dedupKey).Err()
		receiver.recordRejected(r, start, "dependency_error", err)
		http.Error(w, "create task: "+err.Error(), http.StatusInternalServerError)
		return
	}
	if err := receiver.queue.Enqueue(r.Context(), task); err != nil {
		_ = receiver.store.Delete(r.Context(), task.ID)
		_ = receiver.redis.Del(r.Context(), dedupKey).Err()
		receiver.recordRejected(r, start, "dependency_error", err)
		http.Error(w, "enqueue task: "+err.Error(), http.StatusServiceUnavailable)
		return
	}
	receiver.writeAccepted(w, r, event, task, retry, start)
}

func (receiver *webhookReceiver) writeAccepted(w http.ResponseWriter, r *http.Request, event orderRiskEvent, task *core.Task, retry bool, start time.Time) {
	telemetry.Counter("order_operations.events_received", "status", "accepted")
	telemetry.Counter("order_operations.events_enqueued", "retry", strconv.FormatBool(retry))
	receiver.logger.InfoWithContext(r.Context(), "Order risk event accepted", map[string]any{
		"operation": "receive_order_risk_event", "status": "accepted", "event_id": event.EventID,
		"order_id": event.OrderID, "task_id": task.ID, "retry": retry, "duration_ms": time.Since(start).Milliseconds(),
	})
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusAccepted)
	_ = json.NewEncoder(w).Encode(map[string]any{"status": "accepted", "retry": retry, "task_id": task.ID, "status_url": fmt.Sprintf("/api/v1/tasks/%s", task.ID)})
}

func (receiver *webhookReceiver) recordRejected(r *http.Request, start time.Time, status string, err error) {
	telemetry.Counter("order_operations.events_received", "status", status)
	telemetry.RecordSpanError(r.Context(), err)
	receiver.logger.WarnWithContext(r.Context(), "Order risk event rejected", map[string]any{
		"operation": "receive_order_risk_event", "status": status,
		"duration_ms": time.Since(start).Milliseconds(), "error": err.Error(),
	})
}
