package serviceevidence

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/truvaagents/truva-g3/core"
)

const testToken = "coordinator-token-0000000000000000"

func TestAttemptPromotionEvidencePreservesPresence(t *testing.T) {
	for _, raw := range []string{`{}`, `{"requested_promotion_code":""}`, `{"requested_promotion_code":"WELCOME100"}`} {
		t.Run(raw, func(t *testing.T) {
			var receipt upstreamReceipt
			if err := json.Unmarshal([]byte(raw), &receipt); err != nil {
				t.Fatal(err)
			}
			receipt.ObservationID, receipt.OrderID = "observation-1", "order-1"
			receipt.HTTPStatus = http.StatusOK
			tool := newTestTool(t, &fakeEvidenceClient{receipt: receipt})
			response := invoke(t, tool.handleGetServiceEvidence, `{"profile_id":"retail-order-service","observation_id":"observation-1"}`, testToken)
			if response.Code != http.StatusOK {
				t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
			}
			var envelope struct {
				Data struct {
					Observation map[string]json.RawMessage `json:"observation"`
				} `json:"data"`
			}
			if err := json.Unmarshal(response.Body.Bytes(), &envelope); err != nil {
				t.Fatal(err)
			}
			value, present := envelope.Data.Observation["requested_promotion_code"]
			if present != (receipt.RequestedPromotionCode != nil) {
				t.Fatal("attempt promotion presence changed")
			}
			if present {
				var actual string
				if err := json.Unmarshal(value, &actual); err != nil || actual != *receipt.RequestedPromotionCode {
					t.Fatalf("attempt promotion changed: %s", value)
				}
			}
		})
	}
}

type fakeEvidenceClient struct {
	feed       upstreamFeed
	receipt    upstreamReceipt
	order      upstreamOrder
	builds     []buildInfo
	products   []product
	err        error
	buildCalls int
	listCalls  int
}

func (f *fakeEvidenceClient) listObservations(context.Context, serviceProfile, time.Time, time.Time, int) (upstreamFeed, error) {
	f.listCalls++
	return f.feed, f.err
}

func (f *fakeEvidenceClient) getEvidence(context.Context, serviceProfile, string) (upstreamReceipt, error) {
	return f.receipt, f.err
}

func (f *fakeEvidenceClient) getOutcome(context.Context, serviceProfile, string) (upstreamOrder, error) {
	return f.order, f.err
}

func (f *fakeEvidenceClient) getBuildInfo(context.Context, serviceProfile) (buildInfo, error) {
	if f.err != nil {
		return buildInfo{}, f.err
	}
	index := f.buildCalls
	f.buildCalls++
	if index >= len(f.builds) {
		index = len(f.builds) - 1
	}
	return f.builds[index], nil
}

func (f *fakeEvidenceClient) getCatalog(context.Context, serviceProfile) ([]product, error) {
	return f.products, f.err
}

func TestToolRegistersSixDiscoverableCapabilitiesWithSchemas(t *testing.T) {
	t.Parallel()
	tool := newTestTool(t, &fakeEvidenceClient{})
	capabilities := tool.GetCapabilities()
	if len(capabilities) != 6 {
		t.Fatalf("capability count = %d", len(capabilities))
	}
	for _, capability := range capabilities {
		if capability.Internal {
			t.Fatalf("capability %q is unexpectedly internal", capability.Name)
		}
		if capability.InputSummary == nil || capability.OutputSummary == nil || capability.SchemaEndpoint == "" {
			t.Fatalf("capability %q has incomplete discovery schema", capability.Name)
		}
	}
}

func TestGetServiceProfileRequiresAuthenticationAndReturnsReadiness(t *testing.T) {
	t.Parallel()
	tool := newTestTool(t, &fakeEvidenceClient{})

	unauthorized := invoke(t, tool.handleGetServiceProfile, `{"profile_id":"retail-order-service"}`, "")
	if unauthorized.Code != http.StatusUnauthorized {
		t.Fatalf("unauthorized status = %d", unauthorized.Code)
	}

	authorized := invoke(t, tool.handleGetServiceProfile, `{"profile_id":"retail-order-service"}`, testToken)
	if authorized.Code != http.StatusOK {
		t.Fatalf("authorized status = %d, body = %s", authorized.Code, authorized.Body.String())
	}
	var envelope struct {
		Success bool `json:"success"`
		Data    struct {
			AssessmentReady    bool                `json:"assessment_ready"`
			ProfileVersion     string              `json:"profile_version"`
			ContractReferences []contractReference `json:"contract_references"`
		} `json:"data"`
	}
	if err := json.Unmarshal(authorized.Body.Bytes(), &envelope); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if !envelope.Success || envelope.Data.AssessmentReady || !strings.HasPrefix(envelope.Data.ProfileVersion, "sha256:") {
		t.Fatalf("unexpected response: %+v", envelope)
	}
	if envelope.Data.ContractReferences == nil {
		t.Fatal("contract_references must be an empty array, not null")
	}
}

func TestProfileAuthorizationIsIndependentFromAuthentication(t *testing.T) {
	t.Parallel()
	profile := validTestProfile()
	if err := validateProfile(&profile); err != nil {
		t.Fatalf("validateProfile() error = %v", err)
	}
	profile.version = "sha256:" + strings.Repeat("f", 64)
	auth, err := newAuthorizer(map[string]string{
		"maintenance-agent":        testToken,
		"maintenance-review-agent": "review-token-000000000000000000000",
	})
	if err != nil {
		t.Fatalf("newAuthorizer() error = %v", err)
	}
	tool := newMaintenanceEvidenceTool(core.NewTool(toolName), &profileStore{profiles: map[string]serviceProfile{profile.ProfileID: profile}}, auth, &fakeEvidenceClient{})
	response := invoke(t, tool.handleGetServiceProfile, `{"profile_id":"retail-order-service"}`, "review-token-000000000000000000000")
	if response.Code != http.StatusForbidden {
		t.Fatalf("status = %d, body = %s", response.Code, response.Body.String())
	}
}

func TestObservationWindowIsRejectedBeforeUpstreamCall(t *testing.T) {
	t.Parallel()
	client := &fakeEvidenceClient{}
	tool := newTestTool(t, client)
	response := invoke(t, tool.handleListServiceObservations, `{"profile_id":"retail-order-service","start":"2026-09-17T15:00:00Z","end":"2026-09-17T15:20:00Z","limit":50}`, testToken)
	if response.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, body = %s", response.Code, response.Body.String())
	}
	if client.listCalls != 0 {
		t.Fatalf("upstream list calls = %d, want 0", client.listCalls)
	}
}

func TestGetServiceEvidenceKeepsServingAndOrderRevisionDistinct(t *testing.T) {
	t.Parallel()
	client := &fakeEvidenceClient{receipt: upstreamReceipt{
		ObservationID: "observation-1", OrderID: "order-1", Operation: "create_order",
		At:            time.Date(2026, 9, 17, 15, 1, 0, 0, time.UTC),
		Revision:      buildRevision("a"),
		OrderRevision: buildRevision("b"),
		HTTPStatus:    http.StatusOK,
		SKU:           "COFFEE-250", UnitPrice: 1000, Quantity: 1, Currency: "USD",
	}}
	tool := newTestTool(t, client)
	response := invoke(t, tool.handleGetServiceEvidence, `{"profile_id":"retail-order-service","observation_id":"observation-1"}`, testToken)
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", response.Code, response.Body.String())
	}
	var envelope struct {
		Data struct {
			Observation struct {
				ServingRevision string `json:"serving_revision"`
				OrderRevision   string `json:"order_revision"`
			} `json:"observation"`
			OutcomeReference *outcomeReference `json:"outcome_reference"`
		} `json:"data"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &envelope); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if envelope.Data.Observation.ServingRevision == envelope.Data.Observation.OrderRevision || envelope.Data.OutcomeReference == nil {
		t.Fatalf("revision or outcome mapping was lost: %+v", envelope.Data)
	}
}

func TestRejectedEvidenceDoesNotExposeOutcomeReference(t *testing.T) {
	t.Parallel()
	client := &fakeEvidenceClient{receipt: upstreamReceipt{
		ObservationID: "observation-2", OrderID: "order-1", Operation: "create_order",
		At: time.Date(2026, 9, 17, 15, 1, 0, 0, time.UTC), Revision: buildRevision("a"),
		HTTPStatus: http.StatusConflict, ErrorCode: "order_id_conflict",
	}}
	tool := newTestTool(t, client)
	response := invoke(t, tool.handleGetServiceEvidence, `{"profile_id":"retail-order-service","observation_id":"observation-2"}`, testToken)
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", response.Code, response.Body.String())
	}
	var envelope struct {
		Data struct {
			OutcomeReference *outcomeReference `json:"outcome_reference"`
			Limitations      []string          `json:"limitations"`
		} `json:"data"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &envelope); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if envelope.Data.OutcomeReference != nil || len(envelope.Data.Limitations) == 0 {
		t.Fatalf("rejected evidence inherited an outcome: %+v", envelope.Data)
	}
}

func TestRuntimeConfigurationRejectsRevisionChange(t *testing.T) {
	t.Parallel()
	client := &fakeEvidenceClient{
		builds:   []buildInfo{{Revision: buildRevision("a")}, {Revision: buildRevision("b")}},
		products: []product{{SKU: "COFFEE-250", Name: "Coffee", UnitPrice: 1000, Currency: "USD", MaxOrderQuantity: 100}},
	}
	tool := newTestTool(t, client)
	response := invoke(t, tool.handleGetRuntimeConfiguration, `{"profile_id":"retail-order-service"}`, testToken)
	if response.Code != http.StatusConflict {
		t.Fatalf("status = %d, body = %s", response.Code, response.Body.String())
	}
	var envelope core.ToolResponse
	if err := json.Unmarshal(response.Body.Bytes(), &envelope); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if envelope.Success || envelope.Error == nil || envelope.Error.Code != "RELEASE_CHANGED" || !envelope.Error.Retryable {
		t.Fatalf("unexpected error envelope: %+v", envelope)
	}
}

func TestUpstreamNotFoundUsesNonSuccessHTTPStatus(t *testing.T) {
	t.Parallel()
	tool := newTestTool(t, &fakeEvidenceClient{err: &upstreamFailure{kind: failureNotFound}})
	response := invoke(t, tool.handleGetBusinessOutcome, `{"profile_id":"retail-order-service","outcome_id":"missing-order"}`, testToken)
	if response.Code != http.StatusNotFound {
		t.Fatalf("status = %d, body = %s", response.Code, response.Body.String())
	}
	var envelope core.ToolResponse
	if err := json.Unmarshal(response.Body.Bytes(), &envelope); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if envelope.Success || envelope.Error == nil || envelope.Error.Code != "OUTCOME_NOT_FOUND" {
		t.Fatalf("unexpected error envelope: %+v", envelope)
	}
}

func newTestTool(t *testing.T, client evidenceClient) *maintenanceEvidenceTool {
	t.Helper()
	profile := validTestProfile()
	if err := validateProfile(&profile); err != nil {
		t.Fatalf("validateProfile() error = %v", err)
	}
	profile.version = "sha256:" + strings.Repeat("f", 64)
	auth, err := newAuthorizer(map[string]string{"maintenance-agent": testToken})
	if err != nil {
		t.Fatalf("newAuthorizer() error = %v", err)
	}
	tool := newMaintenanceEvidenceTool(core.NewTool(toolName), &profileStore{profiles: map[string]serviceProfile{profile.ProfileID: profile}}, auth, client)
	tool.now = func() time.Time { return time.Date(2026, 9, 17, 15, 5, 0, 0, time.UTC) }
	return tool
}

func invoke(t *testing.T, handler http.HandlerFunc, body, token string) *httptest.ResponseRecorder {
	t.Helper()
	request := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(body))
	if token != "" {
		request.Header.Set("Authorization", "Bearer "+token)
	}
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)
	return recorder
}

func buildRevision(character string) string {
	return "build-" + strings.Repeat(character, 64)
}
