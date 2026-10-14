package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"net/http"

	"github.com/truvaagents/truva-g3-domain-examples/software-maintenance/internal/strictjson"
	"github.com/truvaagents/truva-g3/core"
)

// Tool capabilities receive raw parameter objects, not agent data envelopes.
func decodeAssessmentRequest(r *http.Request, limit int64, dst any) error {
	if r.Body == nil {
		return errors.New("request body required")
	}
	data, err := io.ReadAll(io.LimitReader(r.Body, limit+1))
	if err != nil || int64(len(data)) > limit {
		return errors.New("request exceeds body limit or cannot be read")
	}
	trimmed := bytes.TrimSpace(data)
	if len(trimmed) == 0 || trimmed[0] != '{' {
		return errors.New("request must be one JSON object")
	}
	return strictjson.Decode(data, dst)
}
func writeAssessmentSuccess(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(core.ToolResponse{Success: true, Data: value})
}
func writeAssessmentError(w http.ResponseWriter, status int, code, message string, category core.ErrorCategory, retryable bool) {
	failure := &core.ToolError{Code: code, Message: message, Category: category, Retryable: retryable}
	if observer, ok := w.(interface{ recordToolError(*core.ToolError) }); ok {
		observer.recordToolError(failure)
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(core.ToolResponse{Success: false, Error: failure})
}
