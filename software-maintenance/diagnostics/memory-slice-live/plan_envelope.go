package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	"github.com/truvaagents/truva-g3/core"
	"github.com/truvaagents/truva-g3/orchestration"
	"github.com/truvaagents/truva-g3/orchestration/redisprovider"
	"github.com/truvaagents/truva-g3/telemetry"
	"go.opentelemetry.io/otel/trace"
)

// The first responses are labelled synthetic inputs to the real decoder, not
// claims about provider output or maintenance execution. Remaining calls use
// the actual configured provider through the four-request recording guard.
type envelopeFixtureClient struct {
	core.AIClient
	continuation bool
	rejectDeps   bool
	calls        int
	feedbackSeen bool
}

func (client *envelopeFixtureClient) GenerateResponse(ctx context.Context, prompt string, options *core.AIOptions) (*core.AIResponse, error) {
	client.calls++
	if client.rejectDeps {
		return &core.AIResponse{Content: `{"steps":[{"step_id":"step-1","agent_name":"envelope-fixture","instruction":"Labelled rejected dependency fixture","depends_on":["step-not-present"],"metadata":{"capability":"fixture_read","parameters":{}}}],"terminal":true}`, Model: "labelled-local-fixture", Provider: "local-fixture"}, nil
	}
	if client.continuation && client.calls == 1 {
		return &core.AIResponse{Content: `{"plan_id":"labelled-integration-fixture","steps":[{"step_id":"step-1","agent_name":"envelope-fixture","instruction":"Read the labelled synthetic observation","metadata":{"capability":"fixture_read","parameters":{}}}],"terminal":false}`, Model: "labelled-local-fixture", Provider: "local-fixture"}, nil
	}
	if (!client.continuation && client.calls == 1) || (client.continuation && client.calls == 2) {
		invalid := `{"needs_user_input":{"execution_plan":"labelled diagnostic fixture"}}`
		if client.continuation {
			invalid = `{"needs_user_input":{"question":" \t\n"}}`
		}
		return &core.AIResponse{Content: invalid, Model: "labelled-local-fixture", Provider: "local-fixture"}, nil
	}
	if strings.Contains(prompt, "invalid_clarification_request") && strings.Contains(prompt, "nonblank question") {
		client.feedbackSeen = true
	}
	return client.AIClient.GenerateResponse(ctx, prompt, options)
}

func verifyPlanEnvelopeLive() (runErr error) {
	if os.Getenv("OPENROUTER_API_KEY") == "" || os.Getenv("OPENAI_API_KEY") != "" {
		return errors.New("diagnostic requires OpenRouter-only credentials")
	}
	lock, err := os.OpenFile("/tmp/truvag3-v4-clarification-live.lock", os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return err
	}
	_ = lock.Close()
	cfg := telemetry.UseProfile(telemetry.ProfileDevelopment)
	cfg.ServiceName = "v4-envelope-kind-diagnostic"
	if endpoint := os.Getenv("OTEL_EXPORTER_OTLP_ENDPOINT"); endpoint != "" {
		cfg.Endpoint = endpoint
	}
	if err := telemetry.Initialize(cfg); err != nil {
		return err
	}
	defer func() {
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		runErr = errors.Join(runErr, telemetry.Shutdown(ctx))
	}()
	ctx, cancel := context.WithTimeout(context.Background(), 6*time.Minute)
	defer cancel()
	ctx, span := telemetry.GetTelemetryProvider().StartSpan(ctx, "diagnostic.plan_envelope_kind")
	defer span.End()
	emit("envelope_probe_started", map[string]any{"trace_id": trace.SpanContextFromContext(ctx).TraceID().String(), "max_paid_calls": 4, "injected_inputs": "labelled empty/blank clarification fixtures"})
	guard := &guardedTransport{next: http.DefaultTransport}
	defer func() { emit("provider_attempts", map[string]any{"count": guard.calls, "wire": guard.wire}) }()
	logger := core.NewBaseAgent("v4-envelope-kind-diagnostic").Logger
	chain, err := diagnosticChain(guard, logger, telemetry.GetTelemetryProvider())
	if err != nil {
		return err
	}
	connection, err := core.ResolveRedisConnectionConfig(nil, os.LookupEnv)
	if err != nil {
		return err
	}
	redis, err := core.NewRedisUniversalClient(connection)
	if err != nil {
		return err
	}
	defer func() { _ = redis.Close() }()
	keys, err := core.NewRedisKeyspace(fmt.Sprintf("v4-envelope-diagnostic-%d", time.Now().UnixNano()))
	if err != nil {
		return err
	}
	discovery, err := core.NewRedisDiscoveryWithClient(redis, keys, 8*time.Minute)
	if err != nil {
		return err
	}
	var dispatches atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if request.Method == http.MethodGet && request.URL.Path == "/api/capabilities" {
			_ = json.NewEncoder(w).Encode([]map[string]string{{"name": "fixture_read", "endpoint": "/fixture", "description": "Read a labelled synthetic observation"}})
			return
		}
		if request.Method != http.MethodPost || request.URL.Path != "/fixture" {
			http.NotFound(w, request)
			return
		}
		dispatches.Add(1)
		_ = json.NewEncoder(w).Encode(map[string]any{"data": map[string]any{"diagnostic_fixture": true, "observed_value": 5}})
	}))
	defer server.Close()
	host, portText, err := net.SplitHostPort(strings.TrimPrefix(server.URL, "http://"))
	if err != nil {
		return err
	}
	port, err := strconv.Atoi(portText)
	if err != nil {
		return err
	}
	if err := discovery.Register(ctx, &core.ServiceRegistration{ID: "envelope-fixture", Name: "envelope-fixture", Address: host, Port: port, Type: core.ComponentTypeTool, Capabilities: []core.Capability{{Name: "fixture_read", Description: "Read the labelled synthetic fixture observation", Endpoint: "/fixture"}}}); err != nil {
		return err
	}
	debug, err := redisprovider.NewDefaultBackends(logger, redisprovider.WithDefaultBackendRoles(redisprovider.ClientRoleExecution, redisprovider.ClientRoleLLMDebug))
	if err != nil {
		return err
	}
	defer func() { _ = debug.Close() }()
	for _, scenario := range []struct{ continuation, rejectDeps bool }{{}, {continuation: true}, {rejectDeps: true}} {
		client := &envelopeFixtureClient{AIClient: chain, continuation: scenario.continuation, rejectDeps: scenario.rejectDeps}
		config := orchestration.NewDefaultOrchestratorConfig()
		config.Name = "v4-envelope-kind-diagnostic"
		config.PlanAIOptions = &orchestration.AIOptionsOverride{Model: orchestration.StringPtr("fast"), MaxTokens: orchestration.IntPtr(1000)}
		config.SynthesisAIOptions = &orchestration.AIOptionsOverride{Model: orchestration.StringPtr("fast"), MaxTokens: orchestration.IntPtr(500)}
		config.EnableTieredResolution, config.CacheEnabled = false, false
		config.IterativePlanning.Enabled = true
		config.IterativePlanning.MaxPhases = 3
		config.IterativePlanning.MaxValidationRounds = 1
		o, err := orchestration.CreateResolvedOrchestrator(config, orchestration.OrchestratorDependencies{Discovery: discovery, AIClient: client, Logger: logger, Telemetry: telemetry.GetTelemetryProvider()})
		if err != nil {
			return err
		}
		o.SetExecutionStore(debug.Backends().Execution())
		o.SetLLMDebugStore(debug.Backends().LLMDebug())
		if err := o.Start(ctx); err != nil {
			return errors.Join(err, o.Shutdown(ctx))
		}
		response, execution, requestErr := o.ProcessRequestWithExecution(ctx, "Labelled synthetic operator integration diagnostic. The fixture observation is 5. Current evidence is sufficient; return an explicit terminal plan with empty steps, then answer with the observed value. The injected local-fixture responses test wire validation; there is no maintenance case or external action to perform.", nil)
		shutdownErr := o.Shutdown(ctx)
		if scenario.rejectDeps {
			if requestErr == nil || shutdownErr != nil || response != nil || execution == nil || execution.Success || len(execution.Steps) != 0 || execution.Metadata["termination_reason"] != "plan_validation_exhausted" {
				return errors.New("labelled invalid-dependency fixture did not fail closed without dispatch")
			}
			requestID, _ := execution.Metadata[orchestration.MetadataKeyExecutionRequestID].(string)
			record, err := debug.Backends().Execution().Get(ctx, requestID)
			if err != nil {
				return fmt.Errorf("failed validation execution readback: %w", err)
			}
			if record == nil || record.Lifecycle == nil || record.Lifecycle.State != orchestration.RequestLifecycleFailed || record.Result == nil || record.Result.Metadata["termination_reason"] != "plan_validation_exhausted" {
				return errors.New("failed validation execution readback did not preserve lifecycle and reason")
			}
			emit("validation_failure_verified", map[string]any{"request_id": requestID, "injected_inputs": "labelled invalid-dependency fixture; no provider calls", "lifecycle": record.Lifecycle, "termination_reason": record.Result.Metadata["termination_reason"], "steps": len(execution.Steps), "paid_calls_so_far": guard.calls})
			continue
		}
		if requestErr != nil || shutdownErr != nil {
			return errors.Join(requestErr, shutdownErr)
		}
		if response == nil || execution == nil || !client.feedbackSeen {
			return errors.New("missing accepted response or actionable envelope feedback")
		}
		record, err := debug.Backends().Execution().Get(ctx, response.RequestID)
		if err != nil {
			return fmt.Errorf("terminal execution readback failed: %w", err)
		}
		if record == nil || record.Lifecycle == nil || record.Lifecycle.State != orchestration.RequestLifecycleCompleted {
			return errors.New("terminal execution readback did not show completed lifecycle")
		}
		want := 0
		if scenario.continuation {
			want = 1
		}
		if len(execution.Steps) != want || (want == 1 && !execution.Steps[0].Success) {
			return errors.New("rejected envelope or terminal plan produced unexpected work")
		}
		emit("envelope_verified", map[string]any{"request_id": response.RequestID, "continuation": scenario.continuation, "actionable_feedback": client.feedbackSeen, "steps": len(execution.Steps), "lifecycle": record.Lifecycle, "paid_calls_so_far": guard.calls})
	}
	if dispatches.Load() != 1 || guard.calls != 4 {
		return fmt.Errorf("dispatch/paid-call totals differ: dispatches=%d paid=%d", dispatches.Load(), guard.calls)
	}
	return nil
}
