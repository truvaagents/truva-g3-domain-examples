package main

import (
	"net/http"
	"time"

	"github.com/truvaagents/truva-g3/core"
)

func (a *OrderOperationsAgent) registerCapabilities(receiver *webhookReceiver) {
	a.RegisterCapability(core.Capability{
		Name: "receive_order_risk_event",
		Description: "Accepts a synthetic order-risk event and queues evidence-grounded recovery. " +
			"Required: event_id, order_id, signal. Optional: requested_outcome, requested_option_id, decision_time.",
		Endpoint:    "/webhook/order-risk",
		InputTypes:  []string{"json"},
		OutputTypes: []string{"json"},
		Handler:     receiver.Handle,
		Internal:    true,
		InputSummary: &core.SchemaSummary{
			RequiredFields: []core.FieldHint{
				{Name: "event_id", Type: "string", Example: "evt-1003-missing", Description: "Stable upstream event identity used for deduplication"},
				{Name: "order_id", Type: "string", Example: "ORD-1003", Description: "Synthetic order identifier"},
				{Name: "signal", Type: "string", Example: "missing_in_transit", Description: "Observed fulfillment risk signal"},
			},
			OptionalFields: []core.FieldHint{
				{Name: "requested_outcome", Type: "string", Example: "refund", Description: "Customer or operator requested outcome"},
				{Name: "requested_option_id", Type: "string", Example: "same-sku-replacement", Description: "Optional operator constraint; when present the agent evaluates only this bounded option and must not substitute another"},
				{Name: "decision_time", Type: "string", Example: "2026-08-20T18:00:00Z", Description: "RFC3339 policy decision time; defaults to receipt time"},
			},
		},
		OutputSummary: &core.SchemaSummary{
			RequiredFields: []core.FieldHint{
				{Name: "status", Type: "string", Example: "accepted", Description: "Submission status"},
				{Name: "task_id", Type: "string", Example: "order-event-evt-1003-missing", Description: "Stable asynchronous task identity"},
				{Name: "status_url", Type: "string", Example: "/api/v1/tasks/order-event-evt-1003-missing", Description: "Polling endpoint for task progress and result"},
			},
			OptionalFields: []core.FieldHint{
				{Name: "retry", Type: "boolean", Example: "false", Description: "Whether a previously failed event was safely requeued"},
			},
		},
	})

	a.RegisterCapability(core.Capability{
		Name:        "health",
		Description: "Reports agent, discovery, AI provider, and orchestrator health.",
		Endpoint:    "/health",
		OutputTypes: []string{"json"},
		Handler:     a.HandleHealth,
		Internal:    true,
	})
}

func (a *OrderOperationsAgent) HandleHealth(w http.ResponseWriter, r *http.Request) {
	status := "healthy"
	health := map[string]any{
		"status":      status,
		"agent":       a.Name,
		"timestamp":   time.Now().UTC().Format(time.RFC3339),
		"ai_provider": "connected",
	}
	if a.AI == nil {
		health["ai_provider"] = "not_configured"
		status = "degraded"
	}
	if a.Discovery == nil {
		health["discovery"] = "initializing"
		status = "degraded"
	} else if _, err := a.Discovery.Discover(r.Context(), core.DiscoveryFilter{}); err != nil {
		health["discovery"] = "unavailable"
		status = "degraded"
		a.Logger.WarnWithContext(r.Context(), "Agent health check could not reach discovery", map[string]any{
			"operation": "health_check",
			"status":    "degraded",
			"error":     err.Error(),
		})
	} else {
		health["discovery"] = "healthy"
	}
	if a.IsReady() {
		health["orchestrator"] = "active"
	} else {
		health["orchestrator"] = "initializing"
	}
	health["status"] = status
	statusCode := http.StatusOK
	if status == "degraded" {
		statusCode = http.StatusServiceUnavailable
	}
	writeAgentJSON(w, statusCode, health)
}

func (a *OrderOperationsAgent) HandleReady(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "use GET", http.StatusMethodNotAllowed)
		return
	}
	if !a.IsReady() {
		writeAgentJSON(w, http.StatusServiceUnavailable, map[string]any{"status": "not_ready", "orchestrator": "initializing"})
		return
	}
	writeAgentJSON(w, http.StatusOK, map[string]any{"status": "ready", "orchestrator": "active"})
}

func (a *OrderOperationsAgent) RequireReady(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !a.IsReady() {
			writeAgentJSON(w, http.StatusServiceUnavailable, map[string]any{"error": "orchestrator is not ready", "status": "not_ready"})
			return
		}
		next(w, r)
	}
}
