package main

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/truvaagents/truva-g3-domain-examples/agentic-commerce/domain-knowledge-gateway/internal/knowledge"
	"github.com/truvaagents/truva-g3/core"
)

const gatewayTestKey = "gateway-test-key-that-is-at-least-32-characters"

func newTestGateway(t *testing.T) *GatewayTool {
	t.Helper()
	pack, err := knowledge.LoadPack("../domain-packs/commerce-demo/pack.json")
	if err != nil {
		t.Fatalf("LoadPack: %v", err)
	}
	store, err := knowledge.NewMemoryStore(pack)
	if err != nil {
		t.Fatalf("NewMemoryStore: %v", err)
	}
	return &GatewayTool{BaseTool: core.NewTool("domain-knowledge-gateway-test"), store: &switchableKnowledgeStore{active: store, activePack: "commerce-demo"}, controlKey: []byte(gatewayTestKey)}
}

func TestReadExactSectionReturnsVerifiableReceipt(t *testing.T) {
	tool := newTestGateway(t)
	body := `{"section_id":"ftc-delay-consent-or-refund","release_id":"commerce-us-2026-08-20","tenant_id":"commerce-demo","jurisdiction":"US","channel":"online","as_of":"2026-08-20T18:00:00Z"}`
	request := httptest.NewRequest(http.MethodPost, "/api/capabilities/read_exact_section", strings.NewReader(body))
	response := httptest.NewRecorder()
	tool.handleReadExactSection(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
	var envelope struct {
		Success bool `json:"success"`
		Data    struct {
			EvidenceReceipt string `json:"evidence_receipt"`
		} `json:"data"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &envelope); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if !envelope.Success || envelope.Data.EvidenceReceipt == "" {
		t.Fatalf("missing evidence receipt: %s", response.Body.String())
	}
	parts := strings.Split(envelope.Data.EvidenceReceipt, ".")
	if len(parts) != 2 {
		t.Fatalf("invalid receipt shape")
	}
	payload, err := base64.RawURLEncoding.DecodeString(parts[0])
	if err != nil {
		t.Fatalf("decode payload: %v", err)
	}
	signature, err := hex.DecodeString(parts[1])
	if err != nil {
		t.Fatalf("decode signature: %v", err)
	}
	mac := hmac.New(sha256.New, []byte(gatewayTestKey))
	_, _ = mac.Write([]byte("evidence-v1|"))
	_, _ = mac.Write(payload)
	if !hmac.Equal(signature, mac.Sum(nil)) {
		t.Fatal("gateway returned an invalid evidence receipt signature")
	}
	var claims evidenceReceiptClaims
	if err := json.Unmarshal(payload, &claims); err != nil {
		t.Fatalf("decode claims: %v", err)
	}
	if claims.SectionID != "ftc-delay-consent-or-refund" || claims.TenantID != "commerce-demo" || claims.Channel != "online" {
		t.Fatalf("receipt claims do not bind the exact read: %+v", claims)
	}
}

func TestGatewayHandlersFailClosedOnMissingScopeAndTrailingJSON(t *testing.T) {
	tool := newTestGateway(t)
	request := httptest.NewRequest(http.MethodPost, "/api/capabilities/search_evidence", strings.NewReader(`{"query":"refund"}`))
	response := httptest.NewRecorder()
	tool.handleSearchEvidence(response, request)
	if response.Code != http.StatusBadRequest {
		t.Fatalf("missing scope status=%d body=%s", response.Code, response.Body.String())
	}
	request = httptest.NewRequest(http.MethodPost, "/api/capabilities/read_exact_section", strings.NewReader(`{"section_id":"ftc-delay-consent-or-refund","tenant_id":"commerce-demo","jurisdiction":"US","channel":"online","as_of":"2026-08-20T18:00:00Z"} {}`))
	response = httptest.NewRecorder()
	tool.handleReadExactSection(response, request)
	if response.Code != http.StatusBadRequest {
		t.Fatalf("trailing JSON status=%d body=%s", response.Code, response.Body.String())
	}
}
