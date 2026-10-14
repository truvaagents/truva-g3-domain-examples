// Package agentfailure owns the maintenance application's disclosure boundary.
package agentfailure

import (
	"context"
	"encoding/json"

	"github.com/truvaagents/truva-g3/orchestration"
)

// Context attributes application projections to the child execution, not the
// incoming parent's request. Missing or conflicting evidence remains unknown.
func Context(ctx context.Context, response *orchestration.OrchestratorResponse, execution *orchestration.ExecutionResult) context.Context {
	var responseID, executionID string
	if response != nil {
		responseID = response.RequestID
	}
	if execution != nil {
		executionID, _ = execution.Metadata[orchestration.MetadataKeyExecutionRequestID].(string)
	}
	requestID := responseID
	if requestID == "" {
		requestID = executionID
	} else if executionID != "" && executionID != requestID {
		requestID = ""
	}
	return orchestration.WithRequestID(ctx, requestID)
}

// Project is used only after a maintenance caller/profile has been authorized.
// The case caller receives bounded recorded facts, separately from model prose
// and authoritative receipts. These facts grant no repair/publication authority.
func Project(ctx context.Context, execution *orchestration.ExecutionResult) map[string]json.RawMessage {
	if execution == nil {
		return nil
	}
	views, err := orchestration.ProjectFailedOperationEvidence(ctx, execution.Steps, orchestration.FailedOperationViewConfig{})
	if err != nil {
		return map[string]json.RawMessage{"projection": json.RawMessage(`{"view_status":"unavailable"}`)}
	}
	return views
}

// Details adapts the same bounded facts to the existing string-valued tool
// error envelope. JSON encoding is intentional; consumers treat it as data.
func Details(ctx context.Context, execution *orchestration.ExecutionResult) map[string]string {
	views := Project(ctx, execution)
	if len(views) == 0 {
		return nil
	}
	data, err := json.Marshal(views)
	if err != nil {
		return map[string]string{"failed_operations": "unavailable"}
	}
	return map[string]string{"failed_operations": string(data)}
}
