// Package observation contains application-owned, payload-free log helpers.
package observation

import (
	"context"
	"net/http"

	"github.com/truvaagents/truva-g3/core"
	"github.com/truvaagents/truva-g3/telemetry"
)

// RequestID prefers propagated execution identity over the local HTTP identity.
func RequestID(request *http.Request) string {
	if id := ContextRequestID(request.Context()); id != "" {
		return id
	}
	return request.Header.Get("X-TruvaG3-Request-ID")
}

// ContextRequestID also serves task/collection handlers with no HTTP request.
func ContextRequestID(ctx context.Context) string {
	if id := telemetry.GetBaggage(ctx)["request_id"]; id != "" {
		return id
	}
	return core.GetRequestID(ctx)
}

// Received deliberately excludes credentials, request bodies and source bytes.
func Received(logger core.Logger, request *http.Request, operation string) {
	telemetry.AddSpanEvent(request.Context(), "maintenance.request.received")
	if logger != nil {
		logger.InfoWithContext(request.Context(), "Maintenance operation received", map[string]interface{}{
			"operation": operation, "request_id": RequestID(request), "method": request.Method,
		})
	}
}
