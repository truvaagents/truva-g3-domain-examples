package main

import (
	"context"
	"net/http"
	"time"

	"github.com/truvaagents/truva-g3/core"
	"github.com/truvaagents/truva-g3/telemetry"
	"go.opentelemetry.io/otel/attribute"
)

type commerceRequestObserver struct {
	ctx       context.Context
	logger    core.Logger
	operation string
	requestID string
	started   time.Time
	status    string
	errorType string
}

func (t *CommerceTool) observeRequest(r *http.Request, operation string) *commerceRequestObserver {
	ctx := r.Context()
	requestID := requestIDFromRequest(ctx, r)
	observer := &commerceRequestObserver{
		ctx:       ctx,
		logger:    t.Logger,
		operation: operation,
		requestID: requestID,
		started:   time.Now(),
		status:    "failure",
	}
	telemetry.SetSpanAttributes(ctx,
		attribute.String("truvag3.tool.name", "commerce-core"),
		attribute.String("truvag3.capability", operation),
		attribute.String("request.id", requestID),
	)
	telemetry.AddSpanEvent(ctx, "request_received",
		attribute.String("operation", operation),
		attribute.String("method", r.Method),
		attribute.String("path", r.URL.Path),
		attribute.String("request_id", requestID),
	)
	if observer.logger != nil {
		observer.logger.InfoWithContext(ctx, "Processing Commerce Core request", map[string]any{
			"operation":  operation,
			"method":     r.Method,
			"path":       r.URL.Path,
			"request_id": requestID,
			"status":     "started",
		})
	}
	return observer
}

func (observer *commerceRequestObserver) validated(fields map[string]any) {
	fields = observer.fields(fields)
	fields["status"] = "validated"
	if observer.logger != nil {
		observer.logger.InfoWithContext(observer.ctx, "Commerce Core request validated", fields)
	}
	telemetry.AddSpanEvent(observer.ctx, "backend_query_started",
		attribute.String("operation", observer.operation),
		attribute.String("request_id", observer.requestID),
	)
}

func (observer *commerceRequestObserver) reject(err error, errorType string, fields map[string]any) {
	observer.recordError(err, errorType, fields, false)
}

func (observer *commerceRequestObserver) fail(err error, errorType string, fields map[string]any) {
	observer.recordError(err, errorType, fields, true)
}

func (observer *commerceRequestObserver) recordError(err error, errorType string, fields map[string]any, severe bool) {
	observer.errorType = errorType
	telemetry.RecordSpanError(observer.ctx, err)
	telemetry.AddSpanEvent(observer.ctx, "request_failed",
		attribute.String("operation", observer.operation),
		attribute.String("request_id", observer.requestID),
		attribute.String("error_type", errorType),
	)
	fields = observer.fields(fields)
	fields["status"] = "failure"
	fields["error_type"] = errorType
	fields["error"] = err.Error()
	fields["duration_ms"] = time.Since(observer.started).Milliseconds()
	if observer.logger == nil {
		return
	}
	if severe {
		observer.logger.ErrorWithContext(observer.ctx, "Commerce Core request failed", fields)
		return
	}
	observer.logger.WarnWithContext(observer.ctx, "Commerce Core request rejected", fields)
}

func (observer *commerceRequestObserver) succeed(fields map[string]any) {
	observer.status = "success"
	telemetry.AddSpanEvent(observer.ctx, "request_completed",
		attribute.String("operation", observer.operation),
		attribute.String("request_id", observer.requestID),
	)
	if len(fields) > 0 && observer.logger != nil {
		fields = observer.fields(fields)
		fields["status"] = "success"
		observer.logger.DebugWithContext(observer.ctx, "Commerce Core response prepared", fields)
	}
}

func (observer *commerceRequestObserver) finish() {
	durationMs := float64(time.Since(observer.started).Milliseconds())
	metricStatus := observer.status
	if metricStatus != "success" {
		metricStatus = "error"
	}
	telemetry.RecordToolCall("commerce-core", observer.operation, durationMs, metricStatus)
	telemetry.Counter("commerce_core.requests", "operation", observer.operation, "status", observer.status)
	telemetry.Histogram("commerce_core.request_duration_ms", durationMs, "operation", observer.operation, "status", observer.status)
	if observer.errorType != "" {
		telemetry.RecordToolCallError("commerce-core", observer.operation, observer.errorType)
	}
	if observer.logger != nil {
		observer.logger.InfoWithContext(observer.ctx, "Commerce Core request completed", map[string]any{
			"operation":   observer.operation,
			"request_id":  observer.requestID,
			"status":      observer.status,
			"duration_ms": int64(durationMs),
		})
	}
}

func (observer *commerceRequestObserver) fields(fields map[string]any) map[string]any {
	result := make(map[string]any, len(fields)+2)
	for key, value := range fields {
		result[key] = value
	}
	result["operation"] = observer.operation
	result["request_id"] = observer.requestID
	return result
}

func requestIDFromRequest(ctx context.Context, r *http.Request) string {
	if baggage := telemetry.GetBaggage(ctx); baggage != nil {
		if requestID := baggage["request_id"]; requestID != "" {
			return requestID
		}
	}
	return r.Header.Get("X-TruvaG3-Request-ID")
}
