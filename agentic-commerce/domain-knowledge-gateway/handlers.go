package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/truvaagents/truva-g3-domain-examples/agentic-commerce/domain-knowledge-gateway/internal/knowledge"
	"github.com/truvaagents/truva-g3/core"
)

type searchEvidenceRequest struct {
	Query        string   `json:"query"`
	ReleaseID    string   `json:"release_id,omitempty"`
	TenantID     string   `json:"tenant_id,omitempty"`
	Jurisdiction string   `json:"jurisdiction,omitempty"`
	Channel      string   `json:"channel,omitempty"`
	AsOf         string   `json:"as_of,omitempty"`
	Tags         []string `json:"tags,omitempty"`
	Limit        int      `json:"limit,omitempty"`
}

type traverseRequest struct {
	ReleaseID     string   `json:"release_id,omitempty"`
	TenantID      string   `json:"tenant_id"`
	Jurisdiction  string   `json:"jurisdiction"`
	Channel       string   `json:"channel"`
	AsOf          string   `json:"as_of"`
	SectionIDs    []string `json:"section_ids"`
	RelationTypes []string `json:"relation_types,omitempty"`
	MaxDepth      int      `json:"max_depth,omitempty"`
	Limit         int      `json:"limit,omitempty"`
}

type exactSectionRequest struct {
	ReleaseID    string `json:"release_id,omitempty"`
	SectionID    string `json:"section_id"`
	TenantID     string `json:"tenant_id"`
	Jurisdiction string `json:"jurisdiction"`
	Channel      string `json:"channel"`
	AsOf         string `json:"as_of"`
}

type releaseRequest struct {
	ReleaseID string `json:"release_id,omitempty"`
}

func (t *GatewayTool) handleSearchEvidence(w http.ResponseWriter, r *http.Request) {
	observer := t.observeRequest(r, "search_evidence")
	defer observer.finish()
	var request searchEvidenceRequest
	if !decodeJSON(w, r, &request, observer) {
		return
	}
	normalizeGatewayStrings(&request.Query, &request.ReleaseID, &request.TenantID, &request.Jurisdiction, &request.Channel, &request.AsOf)
	request.Tags = compactGatewayList(request.Tags)
	if request.Query == "" || !requiredScope(request.TenantID, request.Jurisdiction, request.Channel, request.AsOf) {
		err := fmt.Errorf("query and all scope fields are required")
		observer.reject(err, "validation_error", map[string]any{"tenant_id": request.TenantID, "jurisdiction": request.Jurisdiction, "channel": request.Channel})
		sendError(w, http.StatusBadRequest, "MISSING_FIELD", "query, tenant_id, jurisdiction, channel, and as_of are required")
		return
	}
	if request.Limit < 0 || request.Limit > 20 {
		err := fmt.Errorf("limit %d is outside the supported range", request.Limit)
		observer.reject(err, "validation_error", map[string]any{"tenant_id": request.TenantID, "limit": request.Limit})
		sendError(w, http.StatusBadRequest, "INVALID_LIMIT", "limit must be between 1 and 20")
		return
	}
	var asOf time.Time
	var err error
	asOf, err = time.Parse(time.RFC3339, request.AsOf)
	if err != nil {
		observer.reject(err, "validation_error", map[string]any{"tenant_id": request.TenantID, "as_of": request.AsOf})
		sendError(w, http.StatusBadRequest, "INVALID_AS_OF", "as_of must be RFC3339")
		return
	}
	observer.validated(map[string]any{"tenant_id": request.TenantID, "jurisdiction": request.Jurisdiction, "channel": request.Channel, "release_id": request.ReleaseID, "query_length": len(request.Query), "tag_count": len(request.Tags), "limit": request.Limit})
	result, err := knowledge.HybridSearch(r.Context(), t.store, knowledge.CandidateSpec{
		Query: request.Query, ReleaseID: request.ReleaseID, TenantID: request.TenantID, Jurisdiction: request.Jurisdiction,
		Channel: request.Channel, AsOf: asOf, Tags: request.Tags, Limit: request.Limit,
	})
	if err != nil {
		t.handleStoreError(w, observer, err)
		return
	}
	observer.succeed(map[string]any{"release_id": result.Release.ReleaseID, "candidate_count": len(result.Candidates), "path_count": len(result.Paths), "warning_count": len(result.Warnings)})
	writeSuccess(w, result)
}

func (t *GatewayTool) handleTraverseRelations(w http.ResponseWriter, r *http.Request) {
	observer := t.observeRequest(r, "traverse_relations")
	defer observer.finish()
	var request traverseRequest
	if !decodeJSON(w, r, &request, observer) {
		return
	}
	normalizeGatewayStrings(&request.ReleaseID, &request.TenantID, &request.Jurisdiction, &request.Channel, &request.AsOf)
	request.SectionIDs = normalizeGatewayList(request.SectionIDs)
	request.RelationTypes = compactGatewayList(request.RelationTypes)
	if len(request.SectionIDs) == 0 || hasEmptyGatewayValue(request.SectionIDs) {
		err := fmt.Errorf("section_ids must contain non-empty identifiers")
		observer.reject(err, "validation_error", map[string]any{"tenant_id": request.TenantID})
		sendError(w, http.StatusBadRequest, "MISSING_FIELD", "section_ids must contain at least one identifier")
		return
	}
	if !requiredScope(request.TenantID, request.Jurisdiction, request.Channel, request.AsOf) {
		err := fmt.Errorf("all traversal scope fields are required")
		observer.reject(err, "validation_error", map[string]any{"tenant_id": request.TenantID, "jurisdiction": request.Jurisdiction, "channel": request.Channel})
		sendError(w, http.StatusBadRequest, "MISSING_FIELD", "tenant_id, jurisdiction, channel, and as_of are required")
		return
	}
	if request.MaxDepth < 0 || request.MaxDepth > 3 || request.Limit < 0 || request.Limit > 20 {
		err := fmt.Errorf("traversal bounds are outside the supported range")
		observer.reject(err, "validation_error", map[string]any{"tenant_id": request.TenantID, "max_depth": request.MaxDepth, "limit": request.Limit})
		sendError(w, http.StatusBadRequest, "INVALID_BOUNDS", "max_depth must be 1-3 and limit must be 1-20 when provided")
		return
	}
	asOf, err := time.Parse(time.RFC3339, request.AsOf)
	if err != nil {
		observer.reject(err, "validation_error", map[string]any{"tenant_id": request.TenantID, "as_of": request.AsOf})
		sendError(w, http.StatusBadRequest, "INVALID_AS_OF", "as_of must be RFC3339")
		return
	}
	observer.validated(map[string]any{"tenant_id": request.TenantID, "jurisdiction": request.Jurisdiction, "channel": request.Channel, "release_id": request.ReleaseID, "section_count": len(request.SectionIDs), "relation_type_count": len(request.RelationTypes), "max_depth": request.MaxDepth, "limit": request.Limit})
	paths, err := t.store.Traverse(r.Context(), knowledge.TraversalSpec{ReleaseID: request.ReleaseID, TenantID: request.TenantID, Jurisdiction: request.Jurisdiction, Channel: request.Channel, AsOf: asOf, SectionIDs: request.SectionIDs, RelationTypes: request.RelationTypes, MaxDepth: request.MaxDepth, Limit: request.Limit})
	if err != nil {
		t.handleStoreError(w, observer, err)
		return
	}
	observer.succeed(map[string]any{"path_count": len(paths), "release_id": request.ReleaseID})
	writeSuccess(w, map[string]any{"paths": paths})
}

func (t *GatewayTool) handleReadExactSection(w http.ResponseWriter, r *http.Request) {
	observer := t.observeRequest(r, "read_exact_section")
	defer observer.finish()
	var request exactSectionRequest
	if !decodeJSON(w, r, &request, observer) {
		return
	}
	normalizeGatewayStrings(&request.ReleaseID, &request.SectionID, &request.TenantID, &request.Jurisdiction, &request.Channel, &request.AsOf)
	if request.SectionID == "" || !requiredScope(request.TenantID, request.Jurisdiction, request.Channel, request.AsOf) {
		err := fmt.Errorf("section identity and all scope fields are required")
		observer.reject(err, "validation_error", map[string]any{"section_id": request.SectionID, "tenant_id": request.TenantID, "jurisdiction": request.Jurisdiction, "channel": request.Channel})
		sendError(w, http.StatusBadRequest, "MISSING_FIELD", "section_id, tenant_id, jurisdiction, channel, and as_of are required")
		return
	}
	asOf, err := time.Parse(time.RFC3339, request.AsOf)
	if err != nil {
		observer.reject(err, "validation_error", map[string]any{"section_id": request.SectionID, "tenant_id": request.TenantID, "as_of": request.AsOf})
		sendError(w, http.StatusBadRequest, "INVALID_AS_OF", "as_of must be RFC3339")
		return
	}
	observer.validated(map[string]any{"section_id": request.SectionID, "tenant_id": request.TenantID, "jurisdiction": request.Jurisdiction, "channel": request.Channel, "release_id": request.ReleaseID})
	section, err := t.store.ReadExact(r.Context(), knowledge.SectionRef{ReleaseID: request.ReleaseID, SectionID: request.SectionID, TenantID: request.TenantID, Jurisdiction: request.Jurisdiction, Channel: request.Channel, AsOf: asOf})
	if err != nil {
		t.handleStoreError(w, observer, err)
		return
	}
	issuedAt := time.Now().UTC()
	receipt, err := issueEvidenceReceipt(t.controlKey, evidenceReceiptClaims{Version: 1, SectionID: section.Section.SectionID, ReleaseID: section.Release.ReleaseID, ReleaseHash: section.Release.ContentHash, TenantID: request.TenantID, Jurisdiction: request.Jurisdiction, Channel: request.Channel, AsOf: asOf, IssuedAt: issuedAt, ExpiresAt: issuedAt.Add(30 * time.Minute)})
	if err != nil {
		t.handleStoreError(w, observer, err)
		return
	}
	observer.succeed(map[string]any{"section_id": section.Section.SectionID, "release_id": section.Release.ReleaseID, "source_id": section.Source.SourceID})
	writeSuccess(w, map[string]any{"release": section.Release, "section": section.Section, "source": section.Source, "evidence_receipt": receipt})
}

func (t *GatewayTool) handleGetReleaseInfo(w http.ResponseWriter, r *http.Request) {
	observer := t.observeRequest(r, "get_release_info")
	defer observer.finish()
	var request releaseRequest
	if !decodeJSON(w, r, &request, observer) {
		return
	}
	normalizeGatewayStrings(&request.ReleaseID)
	observer.validated(map[string]any{"release_id": request.ReleaseID})
	release, err := t.store.GetReleaseInfo(r.Context(), request.ReleaseID)
	if err != nil {
		t.handleStoreError(w, observer, err)
		return
	}
	observer.succeed(map[string]any{"release_id": release.ReleaseID, "section_count": release.SectionCount, "source_count": release.SourceCount})
	writeSuccess(w, release)
}

func (t *GatewayTool) handleStoreError(w http.ResponseWriter, observer *gatewayRequestObserver, err error) {
	status := http.StatusServiceUnavailable
	code := "STORE_ERROR"
	message := "knowledge store unavailable"
	errorType := "backend_error"
	switch {
	case errors.Is(err, knowledge.ErrNotFound):
		status, code, message, errorType = http.StatusNotFound, "NOT_FOUND", "knowledge record not found", "not_found"
	case errors.Is(err, knowledge.ErrReleaseMismatch):
		status, code, message, errorType = http.StatusConflict, "RELEASE_MISMATCH", err.Error(), "release_conflict"
	case errors.Is(err, knowledge.ErrReleaseInactive):
		status, code, message, errorType = http.StatusConflict, "RELEASE_INACTIVE", err.Error(), "release_conflict"
	case errors.Is(err, knowledge.ErrInvalidScope):
		status, code, message, errorType = http.StatusBadRequest, "INVALID_SCOPE", err.Error(), "validation_error"
	}
	if status >= 500 {
		observer.fail(err, errorType, nil)
	} else {
		observer.reject(err, errorType, nil)
	}
	sendError(w, status, code, message)
}

func decodeJSON(w http.ResponseWriter, r *http.Request, target any, observer *gatewayRequestObserver) bool {
	if r.Method != http.MethodPost {
		err := fmt.Errorf("method %s is not allowed", r.Method)
		observer.reject(err, "method_error", nil)
		sendError(w, http.StatusMethodNotAllowed, "METHOD_NOT_ALLOWED", "use POST")
		return false
	}
	r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		observer.fail(err, "decode_error", nil)
		sendError(w, http.StatusBadRequest, "INVALID_REQUEST", fmt.Sprintf("invalid JSON request: %v", err))
		return false
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		err = fmt.Errorf("request body must contain exactly one JSON object")
		observer.fail(err, "decode_error", nil)
		sendError(w, http.StatusBadRequest, "INVALID_REQUEST", "request body must contain exactly one JSON object")
		return false
	}
	return true
}

func normalizeGatewayStrings(values ...*string) {
	for _, value := range values {
		*value = strings.TrimSpace(*value)
	}
}

func normalizeGatewayList(values []string) []string {
	for index := range values {
		values[index] = strings.TrimSpace(values[index])
	}
	return values
}

func compactGatewayList(values []string) []string {
	result := make([]string, 0, len(values))
	for _, value := range values {
		if value = strings.TrimSpace(value); value != "" {
			result = append(result, value)
		}
	}
	return result
}

func hasEmptyGatewayValue(values []string) bool {
	for _, value := range values {
		if value == "" {
			return true
		}
	}
	return false
}

func requiredScope(values ...string) bool {
	for _, value := range values {
		if strings.TrimSpace(value) == "" {
			return false
		}
	}
	return true
}

func writeSuccess(w http.ResponseWriter, data any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(core.ToolResponse{Success: true, Data: data})
}

func sendError(w http.ResponseWriter, status int, code, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(core.ToolResponse{Success: false, Error: &core.ToolError{Code: code, Message: message, Retryable: status >= 500}})
}
