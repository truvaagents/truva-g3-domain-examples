package main

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/truvaagents/truva-g3-domain-examples/agentic-commerce/domain-knowledge-gateway/internal/knowledge"
	"github.com/truvaagents/truva-g3/core"
)

func TestGatewayCapabilitiesAreSchemaBacked(t *testing.T) {
	tool := newTestGateway(t)
	tool.registerCapabilities()
	expectedOutputs := map[string][]string{
		"search_evidence":         {"release", "candidates", "paths", "warnings"},
		"traverse_relations":      {"paths"},
		"read_exact_section":      {"release", "section", "source", "evidence_receipt"},
		"get_release_info":        {"release_id", "domain", "tenant_id", "published_at", "valid_from", "valid_to", "content_hash", "section_count", "source_count"},
		"manage_demo_domain_pack": {"action", "active_pack", "release", "available_packs"},
	}

	capabilities := tool.GetCapabilities()
	if len(capabilities) != len(expectedOutputs) {
		t.Fatalf("capability count = %d, want %d", len(capabilities), len(expectedOutputs))
	}
	for _, capability := range capabilities {
		if capability.InputSummary == nil || capability.OutputSummary == nil {
			t.Fatalf("capability %q is missing input or output schema metadata", capability.Name)
		}
		if capability.Endpoint != "/api/capabilities/"+capability.Name || capability.SchemaEndpoint != capability.Endpoint+"/schema" {
			t.Fatalf("capability %q has inconsistent endpoints: endpoint=%q schema=%q", capability.Name, capability.Endpoint, capability.SchemaEndpoint)
		}
		if capability.Name == "manage_demo_domain_pack" {
			if !capability.Internal {
				t.Fatal("manage_demo_domain_pack must be hidden from LLM discovery")
			}
		} else if capability.Internal {
			t.Fatalf("public tool capability %q must be discoverable", capability.Name)
		}
		assertGatewayFields(t, capability.Name, capability.OutputSummary, expectedOutputs[capability.Name])
	}

	search := capabilities[0]
	if containsGatewayField(search.OutputSummary.RequiredFields, "paths") || containsGatewayField(search.OutputSummary.RequiredFields, "warnings") {
		t.Fatal("omitempty search outputs must not be declared required")
	}
	if !containsGatewayField(search.OutputSummary.OptionalFields, "paths") || !containsGatewayField(search.OutputSummary.OptionalFields, "warnings") {
		t.Fatal("search paths and warnings must be declared optional")
	}
}

func TestGatewayHandlerNormalizesScopeAndSection(t *testing.T) {
	tool := newTestGateway(t)
	body := `{"section_id":" ftc-delay-consent-or-refund ","release_id":" commerce-us-2026-08-20 ","tenant_id":" commerce-demo ","jurisdiction":" US ","channel":" online ","as_of":" 2026-08-20T18:00:00Z "}`
	request := httptest.NewRequest(http.MethodPost, "/api/capabilities/read_exact_section", strings.NewReader(body))
	response := httptest.NewRecorder()

	tool.handleReadExactSection(response, request)

	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"section_id":"ftc-delay-consent-or-refund"`) {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
}

func TestMemoryStoreHonorsCanceledContext(t *testing.T) {
	pack, err := knowledge.LoadPack("../domain-packs/commerce-demo/pack.json")
	if err != nil {
		t.Fatalf("LoadPack: %v", err)
	}
	store, err := knowledge.NewMemoryStore(pack)
	if err != nil {
		t.Fatalf("NewMemoryStore: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	if _, err := store.GetReleaseInfo(ctx, pack.Release.ReleaseID); !errors.Is(err, context.Canceled) {
		t.Fatalf("GetReleaseInfo error = %v, want context.Canceled", err)
	}
	if _, err := store.ReadExact(ctx, knowledge.SectionRef{}); !errors.Is(err, context.Canceled) {
		t.Fatalf("ReadExact error = %v, want context.Canceled", err)
	}
}

func TestGatewayConfigurationRejectsInvalidPorts(t *testing.T) {
	t.Setenv("REDIS_URL", "redis://redis.example:6379")
	t.Setenv("CONTROL_PROOF_HMAC_KEY", gatewayTestKey)
	for _, port := range []string{"invalid", "0", "-1", "65536"} {
		t.Run(port, func(t *testing.T) {
			t.Setenv("PORT", port)
			if err := validateConfig(); err == nil {
				t.Fatalf("validateConfig accepted PORT=%q", port)
			}
		})
	}
	t.Setenv("PORT", "8396")
	if err := validateConfig(); err != nil || envPort(8396) != 8396 {
		t.Fatalf("reserved gateway port rejected: port=%d err=%v", envPort(8396), err)
	}
}

func TestGatewayRequestIDFallsBackToExecutorHeader(t *testing.T) {
	request := httptest.NewRequest(http.MethodPost, "/api/capabilities/search_evidence", nil)
	request.Header.Set("X-TruvaG3-Request-ID", "request-456")
	if requestID := gatewayRequestID(request.Context(), request); requestID != "request-456" {
		t.Fatalf("request ID = %q, want request-456", requestID)
	}
}

func assertGatewayFields(t *testing.T, capability string, summary *core.SchemaSummary, expected []string) {
	t.Helper()
	actual := make(map[string]struct{}, len(summary.RequiredFields)+len(summary.OptionalFields))
	for _, field := range append(append([]core.FieldHint(nil), summary.RequiredFields...), summary.OptionalFields...) {
		actual[field.Name] = struct{}{}
	}
	if len(actual) != len(expected) {
		t.Fatalf("capability %q declares %d output fields, want %d: %v", capability, len(actual), len(expected), actual)
	}
	for _, field := range expected {
		if _, found := actual[field]; !found {
			t.Errorf("capability %q does not declare output field %q", capability, field)
		}
	}
}

func containsGatewayField(fields []core.FieldHint, name string) bool {
	for _, field := range fields {
		if field.Name == name {
			return true
		}
	}
	return false
}
