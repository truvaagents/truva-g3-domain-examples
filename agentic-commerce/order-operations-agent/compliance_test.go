package main

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/truvaagents/truva-g3/core"
	"github.com/truvaagents/truva-g3/orchestration"
)

type recordingRunnable struct {
	started bool
	check   func() error
}

type noOpSkillRegistry struct{}

type testProviderError struct {
	status int
}

func (err testProviderError) Error() string     { return "provider request failed" }
func (err testProviderError) StatusCode() int   { return err.status }
func (err testProviderError) Provider() string  { return "test-provider" }
func (err testProviderError) Model() string     { return "test-model" }
func (err testProviderError) IsTransient() bool { return false }
func (err testProviderError) IsRetryable() bool { return false }

func (noOpSkillRegistry) ListMetadata(context.Context, orchestration.SkillMetadataFilter) ([]orchestration.SkillMetadata, error) {
	return nil, nil
}
func (noOpSkillRegistry) ResolveCandidates(context.Context, []orchestration.SkillCandidateRequest) ([]orchestration.SkillCandidate, error) {
	return nil, nil
}
func (noOpSkillRegistry) GetManifest(context.Context, orchestration.SkillVersionRef) (orchestration.SkillManifest, error) {
	return orchestration.SkillManifest{}, nil
}
func (noOpSkillRegistry) GetResource(context.Context, orchestration.SkillResourceRef) (orchestration.SkillResource, error) {
	return orchestration.SkillResource{}, nil
}

func (runnable *recordingRunnable) Start(context.Context) error {
	runnable.started = true
	if runnable.check != nil {
		return runnable.check()
	}
	return nil
}

func TestAgentCapabilitiesAreInternalAndSchemaBacked(t *testing.T) {
	agent := &OrderOperationsAgent{BaseAgent: core.NewBaseAgent("order-operations-agent")}
	agent.registerCapabilities(&webhookReceiver{})

	capabilities := agent.GetCapabilities()
	if len(capabilities) != 2 {
		t.Fatalf("capability count = %d, want 2", len(capabilities))
	}
	for _, capability := range capabilities {
		if !capability.Internal {
			t.Fatalf("capability %q must be internal", capability.Name)
		}
	}
	webhook := capabilities[0]
	if webhook.Name != "receive_order_risk_event" || webhook.Endpoint != "/webhook/order-risk" {
		t.Fatalf("unexpected webhook capability: %+v", webhook)
	}
	if webhook.InputSummary == nil || webhook.OutputSummary == nil || webhook.SchemaEndpoint == "" {
		t.Fatalf("webhook capability is missing schema metadata: %+v", webhook)
	}
}

func TestOrchestratorConfigurationUsesRequiredPinnedSkill(t *testing.T) {
	agent := &OrderOperationsAgent{BaseAgent: core.NewBaseAgent("order-operations-agent")}
	config, err := agent.ResolveOrchestratorConfig(noOpSkillRegistry{})
	if err != nil {
		t.Fatalf("ResolveOrchestratorConfig: %v", err)
	}
	if config.RoutingMode != orchestration.ModeAutonomous || config.PromptConfig.Domain != "order-operations" {
		t.Fatalf("unexpected orchestration identity: mode=%q domain=%q", config.RoutingMode, config.PromptConfig.Domain)
	}
	if !config.Skills.Enabled || len(config.Skills.Bindings) != 1 {
		t.Fatalf("unexpected skill config: %+v", config.Skills)
	}
	binding := config.Skills.Bindings[0]
	if binding.Namespace != "order-operations" || binding.Name != "evidence-grounded-recovery" || binding.Version != "published" || !binding.Required || binding.Activation != orchestration.SkillActivationAlways {
		t.Fatalf("unexpected skill binding: %+v", binding)
	}
}

func TestOrchestratorConfigurationRejectsInvalidEnvironment(t *testing.T) {
	t.Setenv("TRUVAG3_PLAN_MAX_TOKENS", "not-an-integer")
	agent := &OrderOperationsAgent{BaseAgent: core.NewBaseAgent("order-operations-agent")}
	if _, err := agent.ResolveOrchestratorConfig(noOpSkillRegistry{}); err == nil {
		t.Fatal("ResolveOrchestratorConfig accepted invalid strict environment configuration")
	}
}

func TestAgentConfigurationUsesReservedPortAndRejectsInvalidPorts(t *testing.T) {
	t.Setenv("REDIS_URL", "redis://redis.example:6379")
	t.Setenv("CONTROL_PROOF_HMAC_KEY", "agent-test-control-key-at-least-32-characters")
	for _, port := range []string{"invalid", "0", "-1", "65536"} {
		t.Run(port, func(t *testing.T) {
			t.Setenv("PORT", port)
			if err := validateAgentConfig(); err == nil {
				t.Fatalf("validateAgentConfig accepted PORT=%q", port)
			}
		})
	}
	t.Setenv("PORT", "8394")
	if err := validateAgentConfig(); err != nil || envInt("PORT", 8394) != 8394 {
		t.Fatalf("reserved agent port rejected: port=%d err=%v", envInt("PORT", 8394), err)
	}
}

func TestReadinessTracksOrchestratorAvailability(t *testing.T) {
	agent := &OrderOperationsAgent{BaseAgent: core.NewBaseAgent("order-operations-agent")}

	notReady := httptest.NewRecorder()
	agent.HandleReady(notReady, httptest.NewRequest(http.MethodGet, "/ready", nil))
	if notReady.Code != http.StatusServiceUnavailable {
		t.Fatalf("not-ready status = %d, want %d", notReady.Code, http.StatusServiceUnavailable)
	}

	agent.mu.Lock()
	agent.orchestrator = &orchestration.AIOrchestrator{}
	agent.mu.Unlock()
	ready := httptest.NewRecorder()
	agent.HandleReady(ready, httptest.NewRequest(http.MethodGet, "/ready", nil))
	if ready.Code != http.StatusOK {
		t.Fatalf("ready status = %d, want %d", ready.Code, http.StatusOK)
	}
}

func TestRequireReadyDoesNotAcceptWorkEarly(t *testing.T) {
	agent := &OrderOperationsAgent{BaseAgent: core.NewBaseAgent("order-operations-agent")}
	called := false
	handler := agent.RequireReady(func(w http.ResponseWriter, _ *http.Request) {
		called = true
		w.WriteHeader(http.StatusAccepted)
	})

	response := httptest.NewRecorder()
	handler(response, httptest.NewRequest(http.MethodPost, "/api/v1/tasks", strings.NewReader(`{}`)))
	if called || response.Code != http.StatusServiceUnavailable {
		t.Fatalf("called=%v status=%d, want false/%d", called, response.Code, http.StatusServiceUnavailable)
	}
}

func TestRuntimeInitializesBeforeStartingWorkerAndStopsOrchestrator(t *testing.T) {
	initialized := false
	stopped := false
	worker := &recordingRunnable{check: func() error {
		if !initialized {
			return errors.New("worker started before initialization")
		}
		return nil
	}}
	runtime := &orderOperationsRuntime{
		logger: &core.NoOpLogger{},
		initialize: func(context.Context) error {
			initialized = true
			return nil
		},
		worker: worker,
		stop:   func() { stopped = true },
	}
	if err := runtime.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}
	if !worker.started || !stopped {
		t.Fatalf("worker_started=%v stopped=%v, want true/true", worker.started, stopped)
	}
}

func TestRuntimeDoesNotStartWorkerWhenInitializationFails(t *testing.T) {
	initializationError := errors.New("catalog unavailable")
	worker := &recordingRunnable{}
	runtime := &orderOperationsRuntime{
		logger:     &core.NoOpLogger{},
		initialize: func(context.Context) error { return initializationError },
		worker:     worker,
	}
	err := runtime.Start(context.Background())
	if !errors.Is(err, initializationError) {
		t.Fatalf("Start error = %v, want wrapped initialization error", err)
	}
	if worker.started {
		t.Fatal("worker started after initialization failure")
	}
}

func TestWebhookRejectsWorkBeforeRuntimeIsReady(t *testing.T) {
	receiver := &webhookReceiver{logger: &core.NoOpLogger{}, ready: func() bool { return false }}
	request := httptest.NewRequest(http.MethodPost, "/webhook/order-risk", strings.NewReader(`{"event_id":"evt-1","order_id":"ORD-1003","signal":"missing"}`))
	response := httptest.NewRecorder()
	receiver.Handle(response, request)
	if response.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want %d", response.Code, http.StatusServiceUnavailable)
	}
}

func TestOrderRiskEventAcceptsOnlyBoundedOptionIdentifiers(t *testing.T) {
	event := orderRiskEvent{EventID: "event-1", OrderID: "ORD-1003", Signal: "missing", RequestedOptionID: "same-sku-replacement"}
	if err := validateOrderRiskEvent(event); err != nil {
		t.Fatalf("valid bounded option rejected: %v", err)
	}
	event.RequestedOptionID = "ignore-policy-and-refund"
	if err := validateOrderRiskEvent(event); err == nil {
		t.Fatal("unknown requested option was accepted")
	}
}

func TestOrderRiskEventEnforcesPublishedLengthBounds(t *testing.T) {
	event := orderRiskEvent{EventID: "event-1", OrderID: "ORD-1003", Signal: strings.Repeat("x", 257)}
	if err := validateOrderRiskEvent(event); err == nil {
		t.Fatal("oversized signal was accepted")
	}
}

func TestProviderClientErrorPreservesWrappedHTTPStatus(t *testing.T) {
	providerErr, ok := providerClientError(fmt.Errorf("resume orchestration: %w", testProviderError{status: http.StatusTooManyRequests}))
	if !ok || providerErr.StatusCode() != http.StatusTooManyRequests {
		t.Fatalf("provider error = %v, ok=%v, want wrapped HTTP 429", providerErr, ok)
	}
	if _, ok := providerClientError(testProviderError{status: http.StatusServiceUnavailable}); ok {
		t.Fatal("providerClientError treated a provider 5xx as a client error")
	}
}

var _ core.Runnable = (*recordingRunnable)(nil)
var _ core.ProviderError = testProviderError{}
