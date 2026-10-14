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

const noWorkUnknownAgentPlan = `{"steps":[{"step_id":"step-1","agent_name":"unregistered-actuator","instruction":"Labelled unknown-agent diagnostic input","metadata":{"capability":"actuate","parameters":{}}}],"terminal":false}`

// The unknown-agent input and final answer are labelled fixtures. Only the
// no-work planning decision comes from OpenRouter, once per scenario. The
// provenance-only mode uses fixtures for all interactions and makes no AI calls.
type noWorkProbeClient struct {
	core.AIClient
	unknownAgent bool
	parseFixture bool
	exhausted    bool
	iterative    bool
	provenance   bool
	prompts      []string
	systems      []string
	requestID    string
	feedbackSeen bool
	calls        int
	paidCalls    int
}

func (client *noWorkProbeClient) GenerateResponse(ctx context.Context, prompt string, options *core.AIOptions) (*core.AIResponse, error) {
	client.calls++
	client.requestID = orchestration.GetRequestID(ctx)
	if client.provenance {
		client.prompts = append(client.prompts, prompt)
		client.systems = append(client.systems, options.SystemPrompt)
	}
	if client.unknownAgent && client.calls == 1 {
		return &core.AIResponse{Content: noWorkUnknownAgentPlan, Model: "labelled-local-fixture", Provider: "local-fixture"}, nil
	}
	if client.parseFixture && client.calls == 2 {
		return &core.AIResponse{Content: `{"steps":[`, Model: "labelled-local-fixture", Provider: "local-fixture"}, nil
	}
	if client.provenance {
		var content string
		switch client.calls {
		case 3:
			content = strings.ReplaceAll(noWorkUnknownAgentPlan, "unregistered-actuator", "another-unregistered-actuator")
		case 4:
			if !strings.Contains(prompt, "You used agent 'another-unregistered-actuator'") {
				return nil, errors.New("latest unavailable-agent feedback missing")
			}
			content = `{"terminal":true,"steps":[]}`
		case 5:
			content = "Labelled diagnostic synthesis: the catalog has no actuator capability."
		default:
			return nil, errors.New("unexpected extra provenance-fixture interaction")
		}
		return &core.AIResponse{Content: content, Model: "labelled-local-fixture", Provider: "local-fixture"}, nil
	}
	if client.exhausted {
		if client.calls == 4 {
			if !strings.Contains(prompt, "plan correction failed after 2 retries") || !strings.Contains(prompt, "execution_plan") || strings.Contains(prompt, "LLM hallucinated agent") {
				return nil, errors.New("phase regeneration lost the latest failure")
			}
			client.feedbackSeen = true
		} else if client.calls != 3 {
			return nil, errors.New("unexpected extra exhausted-fixture interaction")
		}
		return &core.AIResponse{Content: `{"execution_plan":{"status":"done"}}`, Model: "labelled-local-fixture", Provider: "local-fixture"}, nil
	}
	if client.paidCalls == 0 {
		if !strings.Contains(prompt, `If available agents cannot fulfill the request, return {"terminal": true, "steps": []}`) {
			return nil, errors.New("planning prompt lacks the explicit built-in no-work rule")
		}
		if client.parseFixture {
			if !strings.Contains(prompt, "<parse_error>") || !strings.Contains(prompt, "unexpected end of JSON input") || strings.Contains(prompt, "You used agent '") {
				return nil, errors.New("parse correction lacks current feedback or repeats stale hallucination feedback")
			}
			client.feedbackSeen = true
		}
		if client.iterative && (!strings.Contains(prompt, "<iterative_planning>") || strings.Contains(prompt, "For zero-work plans, explicitly set")) {
			return nil, errors.New("iterative guidance was omitted or redundant rule survived")
		}
		client.paidCalls++
		return client.AIClient.GenerateResponse(ctx, prompt, options)
	}
	wantCalls := 2
	if client.unknownAgent {
		wantCalls++
	}
	if client.parseFixture {
		wantCalls++
	}
	if client.calls != wantCalls || strings.Contains(prompt, "STRICT RULES FOR THIS RETRY") {
		return nil, errors.New("unexpected additional correction or planning interaction")
	}
	return &core.AIResponse{Content: "Labelled diagnostic synthesis: the catalog has no actuator capability.", Model: "labelled-local-fixture", Provider: "local-fixture"}, nil
}

func verifyNoWorkLive(initialOnly, retryFeedback, debugProvenance bool) (runErr error) {
	if os.Getenv("OPENROUTER_API_KEY") == "" || os.Getenv("OPENAI_API_KEY") != "" {
		return errors.New("no-work diagnostic requires OpenRouter-only credentials")
	}
	lockPath := "/tmp/truvag3-v4-no-work-live.lock"
	if retryFeedback {
		lockPath = "/tmp/truvag3-v4-retry-feedback-live.lock"
	}
	if debugProvenance {
		lockPath = "/tmp/truvag3-v4-debug-provenance-live.lock"
	}
	lock, err := os.OpenFile(lockPath, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return err
	}
	if err := lock.Close(); err != nil {
		return err
	}
	cfg := telemetry.UseProfile(telemetry.ProfileDevelopment)
	cfg.ServiceName = "v4-no-work-kind-diagnostic"
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
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Minute)
	defer cancel()
	ctx, span := telemetry.GetTelemetryProvider().StartSpan(ctx, "diagnostic.no_work_planning")
	defer span.End()
	scenarios := []noWorkProbeClient{{}, {unknownAgent: true}}
	if initialOnly {
		scenarios = scenarios[:1]
	}
	if retryFeedback {
		scenarios = []noWorkProbeClient{{iterative: true}, {unknownAgent: true, parseFixture: true}, {unknownAgent: true, parseFixture: true, exhausted: true}}
	}
	if debugProvenance {
		scenarios = []noWorkProbeClient{{unknownAgent: true, parseFixture: true, provenance: true}}
	}
	maxPaidCalls := 0
	for _, scenario := range scenarios {
		if !scenario.exhausted && !scenario.provenance {
			maxPaidCalls++
		}
	}
	emit("no_work_started", map[string]any{"trace_id": trace.SpanContextFromContext(ctx).TraceID().String(), "max_paid_calls": maxPaidCalls})
	logger := core.NewBaseAgent("v4-no-work-kind-diagnostic").Logger
	guard := &guardedTransport{next: http.DefaultTransport, limit: maxPaidCalls}
	defer func() { emit("provider_attempts", map[string]any{"count": guard.calls, "wire": guard.wire}) }()
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
	keys, err := core.NewRedisKeyspace(fmt.Sprintf("v4-no-work-fixture-%d", time.Now().UnixNano()))
	if err != nil {
		return err
	}
	discovery, err := core.NewRedisDiscoveryWithClient(redis, keys, 5*time.Minute)
	if err != nil {
		return err
	}
	var dispatches atomic.Int32
	capabilities := []core.Capability{{Name: "read_marker", Endpoint: "/fixture", Type: core.CapabilityTool, Description: "Read a labelled marker; this fixture provides no actuator"}}
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("Content-Type", "application/json")
		if request.Method == http.MethodGet && request.URL.Path == "/api/capabilities" {
			_ = json.NewEncoder(writer).Encode(capabilities)
			return
		}
		if request.Method == http.MethodPost {
			dispatches.Add(1)
		}
		http.NotFound(writer, request)
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
	if err := discovery.Register(ctx, &core.ServiceRegistration{ID: "no-work-fixture", Name: "no-work-fixture", Address: host, Port: port, Type: core.ComponentTypeTool, Capabilities: capabilities}); err != nil {
		return err
	}
	backends, err := redisprovider.NewDefaultBackends(logger, redisprovider.WithDefaultBackendRoles(redisprovider.ClientRoleExecution, redisprovider.ClientRoleLLMDebug))
	if err != nil {
		return err
	}
	defer func() { _ = backends.Close() }()
	for _, scenario := range scenarios {
		client := &scenario
		client.AIClient = chain
		config := orchestration.NewDefaultOrchestratorConfig()
		config.Name, config.RequestIDPrefix = "v4-no-work-kind-diagnostic", "v4-no-work"
		config.EnableTieredResolution, config.CacheEnabled = false, false
		config.IterativePlanning.Enabled = client.iterative
		config.PlanParseRetryEnabled, config.PlanRefinementEnabled = false, false
		config.HallucinationRetryEnabled, config.HallucinationMaxRetries = true, 1
		if client.parseFixture {
			config.HallucinationMaxRetries = 2
		}
		if client.provenance {
			config.HallucinationMaxRetries = 3
		}
		config.PlanAIOptions = &orchestration.AIOptionsOverride{Model: orchestration.StringPtr("fast"), MaxTokens: orchestration.IntPtr(1000)}
		o, err := orchestration.CreateResolvedOrchestrator(config, orchestration.OrchestratorDependencies{Discovery: discovery, AIClient: client, Logger: logger, Telemetry: telemetry.GetTelemetryProvider()})
		if err != nil {
			return err
		}
		o.SetExecutionStore(backends.Backends().Execution())
		o.SetLLMDebugStore(backends.Backends().LLMDebug())
		if err := o.Start(ctx); err != nil {
			return errors.Join(err, o.Shutdown(ctx))
		}
		response, execution, requestErr := o.ProcessRequestWithExecution(ctx, "Labelled operator diagnostic: actuate a physical fixture. The catalog contains only a marker reader, with no actuator or authority to move objects. Report the capability limitation. No maintenance case, publication or external action is authorized.", nil)
		if err := o.Shutdown(ctx); err != nil {
			return errors.Join(requestErr, err)
		}
		if client.provenance {
			if requestErr != nil || response == nil || execution == nil || len(execution.Steps) != 0 || client.calls != 5 || client.paidCalls != 0 || dispatches.Load() != 0 {
				return fmt.Errorf("provenance scenario changed recovery: error=%v interactions=%d", requestErr, client.calls)
			}
			debug, err := backends.Backends().LLMDebug().GetRecord(ctx, response.RequestID)
			if err != nil {
				return err
			}
			if debug == nil {
				return errors.New("provenance debug record missing")
			}
			matched := 0
			seenAttempts := make(map[int]bool)
			for _, interaction := range debug.Interactions {
				if interaction.Type != "hallucination_detection" {
					continue
				}
				index, content := 0, noWorkUnknownAgentPlan
				if strings.Contains(interaction.Error, "another-unregistered-actuator") {
					index, content = 2, strings.ReplaceAll(noWorkUnknownAgentPlan, "unregistered-actuator", "another-unregistered-actuator")
				}
				if seenAttempts[index] || interaction.Prompt != client.prompts[index] || interaction.SystemPrompt != client.systems[index] || interaction.Response != content || interaction.Attempt != index+1 {
					return errors.New("stored rejection evidence does not match the actual attempt")
				}
				matched++
				seenAttempts[index] = true
			}
			if matched != 2 {
				return errors.New("expected exactly two rejection diagnostics")
			}
			record, err := backends.Backends().Execution().Get(ctx, response.RequestID)
			if err != nil {
				return err
			}
			if record == nil || record.Lifecycle == nil || record.Lifecycle.State != orchestration.RequestLifecycleCompleted {
				return errors.New("provenance scenario lacks completed lifecycle")
			}
			emit("rejection_provenance_verified", map[string]any{"request_id": response.RequestID, "matched_rejections": matched, "fixture_interactions": client.calls, "paid_calls": 0, "tool_dispatches": 0, "lifecycle": record.Lifecycle})
			continue
		}
		if client.exhausted {
			record, storeErr := backends.Backends().Execution().Get(ctx, client.requestID)
			if storeErr != nil {
				return storeErr
			}
			if requestErr == nil || !strings.Contains(requestErr.Error(), "unrecognized_plan_envelope") || strings.Contains(requestErr.Error(), "LLM hallucinated agent") ||
				response != nil || record == nil || record.Lifecycle == nil || record.Lifecycle.State != orchestration.RequestLifecycleFailed ||
				client.paidCalls != 0 || client.calls != 4 || !client.feedbackSeen || dispatches.Load() != 0 {
				return errors.New("exhausted retry failed to preserve the current cause or existing phase fallback")
			}
			emit("retry_feedback_exhaustion_verified", map[string]any{"request_id": client.requestID, "error": requestErr.Error(), "lifecycle": record.Lifecycle, "paid_calls": 0, "fixture_interactions": client.calls, "tool_dispatches": 0})
			continue
		}
		if requestErr != nil {
			return requestErr
		}
		if response == nil || execution == nil || len(execution.Steps) != 0 || dispatches.Load() != 0 || client.paidCalls != 1 {
			return errors.New("no-work diagnostic produced unexpected work or provider calls")
		}
		record, err := backends.Backends().Execution().Get(ctx, response.RequestID)
		if err != nil {
			return err
		}
		if record == nil || record.Plan == nil || record.Plan.Terminal == nil || !*record.Plan.Terminal || record.Plan.NeedsUserInput != nil || record.Lifecycle == nil || record.Lifecycle.State != orchestration.RequestLifecycleCompleted {
			return errors.New("no-work record lacks explicit completed terminal intent")
		}
		emit("no_work_verified", map[string]any{"request_id": response.RequestID, "unknown_agent_fixture": client.unknownAgent, "parse_fixture": client.parseFixture, "feedback_seen": client.feedbackSeen, "iterative": client.iterative, "paid_calls": client.paidCalls, "fixture_interactions": client.calls - client.paidCalls, "steps": 0, "lifecycle": record.Lifecycle})
	}
	if guard.calls != maxPaidCalls {
		return fmt.Errorf("provider attempt count mismatch: %d", guard.calls)
	}
	return nil
}
