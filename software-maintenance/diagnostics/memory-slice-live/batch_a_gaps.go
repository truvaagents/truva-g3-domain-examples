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

// These inputs are labelled operator fixtures. They exercise production
// decoding, dispatch, interruption and storage; they are not autonomous plans
// or claims about live provider responses. No external AI client is installed.
type gapFixtureClient struct {
	calls     int
	requestID string
	cancel    context.CancelFunc
	tool      bool
}

func (client *gapFixtureClient) GenerateResponse(ctx context.Context, _ string, _ *core.AIOptions) (*core.AIResponse, error) {
	client.calls++
	client.requestID = orchestration.GetRequestID(ctx)
	if client.cancel != nil {
		client.cancel()
		<-ctx.Done()
		return nil, ctx.Err()
	}
	if client.calls > 2 {
		return nil, errors.New("unexpected extra fixture call: correction or retry path ran")
	}
	body := "Labelled diagnostic observed the non-retryable HTTP response."
	if client.calls == 1 {
		capability := "fixture_delegate"
		if client.tool {
			capability = "fixture_tool"
		}
		body = fmt.Sprintf(`{"steps":[{"step_id":"step-1","agent_name":"batch-a-gap-fixture","instruction":"Replay the labelled historical non-retryable response","metadata":{"capability":%q,"parameters":{}}}],"terminal":true}`, capability)
	}
	return &core.AIResponse{Content: body, Model: "labelled-local-fixture", Provider: "local-fixture"}, nil
}

func verifyBatchAGaps() (runErr error) {
	lock, err := os.OpenFile("/tmp/truvag3-v4-batch-a-gaps.lock", os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return err
	}
	if err := lock.Close(); err != nil {
		return err
	}
	// The file re-encodes the captured JSON payload from the previously verified
	// real reviewer failure. Its values are checked before replay; original HTTP
	// whitespace is not claimed. The server transports this fixture verbatim.
	body, err := os.ReadFile("/tmp/v4-nonretryable-child-response.json")
	if err != nil {
		return err
	}
	var envelope struct {
		Success bool `json:"success"`
		Error   struct {
			Code      string `json:"code"`
			Retryable *bool  `json:"retryable"`
		} `json:"error"`
	}
	if json.Unmarshal(body, &envelope) != nil || envelope.Success || envelope.Error.Code != "AGENT_GUIDANCE_BUDGET_EXCEEDED" || envelope.Error.Retryable == nil || *envelope.Error.Retryable {
		return errors.New("historical child response does not declare the reviewed non-retryable policy")
	}
	cfg := telemetry.UseProfile(telemetry.ProfileDevelopment)
	cfg.ServiceName = "v4-batch-a-gap-diagnostic"
	if endpoint := os.Getenv("OTEL_EXPORTER_OTLP_ENDPOINT"); endpoint != "" {
		cfg.Endpoint = endpoint
	}
	if err := telemetry.Initialize(cfg); err != nil {
		return err
	}
	defer func() {
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		runErr = errors.Join(runErr, telemetry.Shutdown(shutdownCtx))
	}()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	ctx, span := telemetry.GetTelemetryProvider().StartSpan(ctx, "diagnostic.batch_a_gaps")
	defer span.End()
	emit("batch_a_gaps_started", map[string]any{"trace_id": trace.SpanContextFromContext(ctx).TraceID().String(), "paid_calls": 0, "fixture_source": "saved real reviewer HTTP 500 response; labelled local plan inputs"})
	logger := core.NewBaseAgent("v4-batch-a-gap-diagnostic").Logger
	connection, err := core.ResolveRedisConnectionConfig(nil, os.LookupEnv)
	if err != nil {
		return err
	}
	redis, err := core.NewRedisUniversalClient(connection)
	if err != nil {
		return err
	}
	defer func() { _ = redis.Close() }()
	keys, err := core.NewRedisKeyspace(fmt.Sprintf("v4-gap-fixture-%d", time.Now().UnixNano()))
	if err != nil {
		return err
	}
	discovery, err := core.NewRedisDiscoveryWithClient(redis, keys, 5*time.Minute)
	if err != nil {
		return err
	}
	var dispatches atomic.Int32
	capabilities := []core.Capability{
		{Name: "fixture_delegate", Endpoint: "/fixture", Type: core.CapabilityOrchestrator, Description: "Replay a labelled historical agent failure"},
		{Name: "fixture_tool", Endpoint: "/fixture", Type: core.CapabilityTool, Description: "Replay the same labelled non-retryable failure with a tool retry budget"},
	}
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("Content-Type", "application/json")
		if request.Method == http.MethodGet && request.URL.Path == "/api/capabilities" {
			_ = json.NewEncoder(writer).Encode(capabilities)
			return
		}
		if request.Method != http.MethodPost || request.URL.Path != "/fixture" {
			http.NotFound(writer, request)
			return
		}
		dispatches.Add(1)
		writer.WriteHeader(http.StatusInternalServerError)
		_, _ = writer.Write(body)
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
	if err := discovery.Register(ctx, &core.ServiceRegistration{ID: "batch-a-gap-fixture", Name: "batch-a-gap-fixture", Type: core.ComponentTypeAgent, Address: host, Port: port, Capabilities: capabilities}); err != nil {
		return err
	}
	backends, err := redisprovider.NewDefaultBackends(logger, redisprovider.WithDefaultBackendRoles(redisprovider.ClientRoleExecution, redisprovider.ClientRoleLLMDebug, redisprovider.ClientRoleHITL))
	if err != nil {
		return err
	}
	defer func() { _ = backends.Close() }()
	for _, scenario := range []string{"non_resend_tool", "non_resend", "cancelled", "suspended"} {
		config := orchestration.NewDefaultOrchestratorConfig()
		config.Name, config.RequestIDPrefix = "v4-batch-a-gap-diagnostic", "v4-gap-"+scenario
		config.EnableTieredResolution, config.CacheEnabled = false, false
		config.PlanParseRetryEnabled, config.HallucinationRetryEnabled = false, false
		config.PlanRefinementEnabled = false
		config.IterativePlanning.MaxPhases = 1
		config.ExecutionOptions.RetryAttempts = 3
		config.ExecutionOptions.TotalTimeout = time.Minute
		config.ExecutionOptions.StepTimeout = 15 * time.Second
		client := &gapFixtureClient{tool: scenario == "non_resend_tool"}
		dispatchesBefore := dispatches.Load()
		callCtx, cancelRequest := context.WithCancel(ctx)
		if scenario == "cancelled" {
			client.cancel = cancelRequest
		}
		if scenario == "suspended" {
			config.HITL.Enabled, config.HITL.RequirePlanApproval = true, true
		}
		o, err := orchestration.CreateResolvedOrchestrator(config, orchestration.OrchestratorDependencies{Discovery: discovery, AIClient: client, Logger: logger, Telemetry: telemetry.GetTelemetryProvider(), EnableErrorAnalyzer: true})
		if err != nil {
			cancelRequest()
			return err
		}
		o.SetExecutionStore(backends.Backends().Execution())
		o.SetLLMDebugStore(backends.Backends().LLMDebug())
		if scenario == "suspended" {
			o.SetInterruptController(orchestration.NewInterruptController(orchestration.NewRuleBasedPolicy(config.HITL), backends.Backends().Checkpoints(), nil, orchestration.WithControllerLogger(logger)))
		}
		if err := o.Start(ctx); err != nil {
			cancelRequest()
			return errors.Join(err, o.Shutdown(ctx))
		}
		response, execution, requestErr := o.ProcessRequestWithExecution(callCtx, "Labelled operator regression fixture; no maintenance case or external action is authorized.", nil)
		cancelRequest()
		if err := o.Shutdown(ctx); err != nil {
			return err
		}
		record, err := backends.Backends().Execution().Get(ctx, client.requestID)
		if err != nil || record == nil || record.Lifecycle == nil {
			return fmt.Errorf("%s terminal record missing: %w", scenario, err)
		}
		switch scenario {
		case "non_resend", "non_resend_tool":
			if requestErr != nil || response == nil || execution == nil || len(execution.Steps) != 1 || execution.Steps[0].Success || dispatches.Load()-dispatchesBefore != 1 || client.calls != 2 {
				return fmt.Errorf("non-resend fixture mismatch: error=%v dispatches=%d fixture_calls=%d", requestErr, dispatches.Load(), client.calls)
			}
		case "cancelled":
			if !errors.Is(requestErr, context.Canceled) || record.Lifecycle.State != orchestration.RequestLifecycleCancelled || dispatches.Load() != dispatchesBefore {
				return fmt.Errorf("cancelled fixture mismatch: error=%v lifecycle=%s", requestErr, record.Lifecycle.State)
			}
		case "suspended":
			checkpoint := orchestration.GetCheckpoint(requestErr)
			if !orchestration.IsInterrupted(requestErr) || record.Lifecycle.State != orchestration.RequestLifecycleSuspended || checkpoint == nil || dispatches.Load() != dispatchesBefore {
				return fmt.Errorf("suspended fixture mismatch: error=%v lifecycle=%s", requestErr, record.Lifecycle.State)
			}
			stored, err := backends.Backends().Checkpoints().LoadCheckpoint(ctx, checkpoint.CheckpointID)
			if err != nil || stored == nil || stored.RequestID != client.requestID || stored.Status != orchestration.CheckpointStatusPending {
				return fmt.Errorf("suspension checkpoint missing or inconsistent: %w", err)
			}
			emit("suspension_checkpoint_verified", map[string]any{"request_id": client.requestID, "checkpoint_id": stored.CheckpointID, "checkpoint_status": stored.Status})
		}
		emit("batch_a_gap_verified", map[string]any{"scenario": scenario, "request_id": client.requestID, "lifecycle": record.Lifecycle, "http_dispatches": dispatches.Load() - dispatchesBefore, "fixture_ai_calls": client.calls, "paid_calls": 0})
	}
	return nil
}
