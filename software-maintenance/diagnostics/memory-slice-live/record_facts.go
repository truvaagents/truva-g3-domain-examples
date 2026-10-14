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
	"github.com/truvaagents/truva-g3/memory"
	"github.com/truvaagents/truva-g3/orchestration"
	"github.com/truvaagents/truva-g3/orchestration/redisprovider"
	"github.com/truvaagents/truva-g3/telemetry"
	"go.opentelemetry.io/otel/trace"
)

// The fixture provides model responses only. Discovery, HTTP dispatch, hooks,
// storage, request lifecycle and telemetry execute through production APIs.
type recordFactsAI struct {
	calls       int
	failureSeen bool
	historySeen bool
}

func (client *recordFactsAI) GenerateResponse(_ context.Context, prompt string, _ *core.AIOptions) (*core.AIResponse, error) {
	client.calls++
	var content string
	switch client.calls {
	case 1:
		content = `{"terminal":true,"steps":[{"step_id":"step-1","agent_name":"record-facts-fixture","instruction":"Read a labelled fixture document","metadata":{"capability":"fixture_read","parameters":{}}},{"step_id":"step-2","agent_name":"record-facts-fixture","instruction":"Observe a labelled input refusal","metadata":{"capability":"fixture_refuse","parameters":{}}},{"step_id":"step-3","agent_name":"record-facts-fixture","instruction":"Observe a labelled long error without a code","metadata":{"capability":"fixture_long_error","parameters":{}}}]}`
	case 2:
		if !strings.Contains(prompt, "<failure>") || !strings.Contains(prompt, "SYNTHETIC_INPUT_REFUSAL") || strings.Contains(prompt, "LONG_ERROR_FIXTURE") {
			return nil, errors.New("summary prompt lost the recorded failure facts")
		}
		client.failureSeen = true
		// Deliberately omit the refused step: the real recorder owns its fallback.
		content = `[{"step_id":"step-1","summary":"Read labelled fixture document fixture-read-doc","entities":[{"type":"document","id":"fixture-read-doc"}]}]`
	case 3:
		content = "Labelled recording diagnostic: one fixture read and one refused operation."
	case 4:
		if !strings.Contains(prompt, "summary_source") || !strings.Contains(prompt, "heuristic") || !strings.Contains(prompt, "step_end") || !strings.Contains(prompt, "SYNTHETIC_INPUT_REFUSAL") || !strings.Contains(prompt, "fixture-request-doc") || !strings.Contains(prompt, "batch_limit") || !strings.Contains(prompt, "[shortened]") {
			return nil, errors.New("recall prompt lost stored quality, failure, entity or time provenance")
		}
		client.historySeen = true
		content = `{"terminal":true,"steps":[]}`
	case 5:
		content = "Labelled recall diagnostic: historical evidence retained with provenance."
	default:
		return nil, errors.New("unexpected extra recording-fixture interaction")
	}
	return &core.AIResponse{Content: content, Provider: "local-fixture", Model: "labelled-record-facts-fixture"}, nil
}

func verifyRecordFactsLive() (runErr error) {
	lock, err := os.OpenFile("/tmp/truvag3-v4-record-facts-review-live.lock", os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return fmt.Errorf("one-run guard: %w", err)
	}
	_ = lock.Close()
	cfg := telemetry.UseProfile(telemetry.ProfileDevelopment)
	cfg.ServiceName = "v4-record-facts-kind-diagnostic"
	if endpoint := os.Getenv("OTEL_EXPORTER_OTLP_ENDPOINT"); endpoint != "" {
		cfg.Endpoint = endpoint
	}
	if err := telemetry.Initialize(cfg); err != nil {
		return err
	}
	telemetry.EnableFrameworkIntegration(nil)
	defer func() {
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		runErr = errors.Join(runErr, telemetry.Shutdown(shutdownCtx))
	}()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	ctx, span := telemetry.GetTelemetryProvider().StartSpan(ctx, "diagnostic.memory_record_facts")
	defer span.End()
	logger := core.NewBaseAgent("v4-record-facts-kind-diagnostic").Logger
	connection, err := core.ResolveRedisConnectionConfig(nil, os.LookupEnv)
	if err != nil {
		return err
	}
	redis, err := core.NewRedisUniversalClient(connection)
	if err != nil {
		return err
	}
	defer func() { _ = redis.Close() }()
	runID := fmt.Sprintf("v4-record-facts-%d", time.Now().UnixNano())
	keys, err := core.NewRedisKeyspace(runID)
	if err != nil {
		return err
	}
	backends, err := memory.NewSharedBackends(redis, logger, memory.WithAgentName("record-facts-diagnostic"), memory.WithDomain("documents"), memory.WithRedisDeployment(runID), memory.WithKnowledgeDisabled())
	if err != nil {
		return err
	}
	defer backends.Close()
	deps := backends.ToDeps()
	discovery, err := core.NewRedisDiscoveryWithClient(redis, keys, 5*time.Minute)
	if err != nil {
		return err
	}
	var dispatches atomic.Int32
	capabilities := []core.Capability{
		{Name: "fixture_read", Endpoint: "/read", Type: core.CapabilityTool, Description: "Read a labelled local fixture"},
		{Name: "fixture_refuse", Endpoint: "/refuse", Type: core.CapabilityTool, Description: "Return a labelled non-retryable input refusal"},
		{Name: "fixture_long_error", Endpoint: "/long-error", Type: core.CapabilityTool, Description: "Return a labelled long error without a structured code"},
	}
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("Content-Type", "application/json")
		if request.Method == http.MethodGet && request.URL.Path == "/api/capabilities" {
			_ = json.NewEncoder(writer).Encode(capabilities)
			return
		}
		if request.Method != http.MethodPost {
			http.NotFound(writer, request)
			return
		}
		dispatches.Add(1)
		if request.URL.Path == "/long-error" {
			writer.WriteHeader(http.StatusBadRequest)
			_ = json.NewEncoder(writer).Encode(map[string]any{"success": false, "error": map[string]any{"message": "LONG_ERROR_FIXTURE " + strings.Repeat("界🙂 multiline\n", 1000), "retryable": false}})
			return
		}
		if request.URL.Path == "/refuse" {
			writer.WriteHeader(http.StatusBadRequest)
			_ = json.NewEncoder(writer).Encode(map[string]any{"success": false, "error": core.ToolError{Code: "SYNTHETIC_INPUT_REFUSAL", Message: "Labelled diagnostic refusal", Retryable: false}})
			return
		}
		_ = json.NewEncoder(writer).Encode(map[string]any{"success": true, "data": map[string]string{"document_id": "fixture-read-doc"}})
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
	if err := discovery.Register(ctx, &core.ServiceRegistration{ID: "record-facts-fixture", Name: "record-facts-fixture", Type: core.ComponentTypeTool, Address: host, Port: port, Capabilities: capabilities}); err != nil {
		return err
	}
	stores, err := redisprovider.NewDefaultBackends(logger, redisprovider.WithDefaultBackendRoles(redisprovider.ClientRoleExecution, redisprovider.ClientRoleLLMDebug))
	if err != nil {
		return err
	}
	defer func() { _ = stores.Close() }()
	client := &recordFactsAI{}
	summarizer, err := orchestration.NewLLMEventSummarizer(client, orchestration.WithSummarizerMaxStepsPerBatch(2), orchestration.WithSummarizerTelemetry(telemetry.GetTelemetryProvider()), orchestration.WithSummarizerLogger(logger))
	if err != nil {
		return err
	}
	enrich, err := orchestration.NewMemoryEnrichmentHook(deps.Episodic, deps.Coordinator, "record-facts-diagnostic", "documents", orchestration.WithEnrichmentLogger(logger))
	if err != nil {
		return err
	}
	record, err := orchestration.NewMemoryRecordHook(deps.Episodic, deps.Coordinator, "record-facts-diagnostic", "documents",
		orchestration.WithEventSummarizer(summarizer), orchestration.WithRecordEntityExtractor(orchestration.LLMEntityExtractor{}),
		orchestration.WithRecordRequestEntityExtractor(orchestration.NoOpEntityExtractor{}), orchestration.WithRecordLogger(logger))
	if err != nil {
		return err
	}
	config := orchestration.NewDefaultOrchestratorConfig()
	config.Name, config.RequestIDPrefix = "v4-record-facts-kind-diagnostic", "v4-record-facts"
	config.EnableTieredResolution, config.CacheEnabled = false, false
	config.IterativePlanning.Enabled = false
	config.PlanParseRetryEnabled, config.PlanRefinementEnabled, config.HallucinationRetryEnabled = false, false, false
	o, err := orchestration.CreateResolvedOrchestrator(config, orchestration.OrchestratorDependencies{Discovery: discovery, AIClient: client, Logger: logger,
		Telemetry: telemetry.GetTelemetryProvider(), PipelineHooks: []core.PipelineHook{enrich, record}})
	if err != nil {
		return err
	}
	o.SetExecutionStore(stores.Backends().Execution())
	o.SetLLMDebugStore(stores.Backends().LLMDebug())
	defer func() { runErr = errors.Join(runErr, o.Shutdown(ctx)) }()
	if err := o.Start(ctx); err != nil {
		return err
	}
	response, result, err := o.ProcessRequestWithExecution(ctx, "Labelled diagnostic: record one read and one refused operation against local fixture services.", map[string]interface{}{"entity_type": "document", "entity_id": "fixture-request-doc"})
	if err != nil || response == nil || result == nil || len(result.Steps) != 3 {
		return fmt.Errorf("recording request failed: %v", err)
	}
	events, err := deps.Episodic.QueryRecentEvents(ctx, "documents", time.Now().Add(-time.Hour), 10)
	if err != nil || len(events) != 3 {
		return fmt.Errorf("expected three stored events, got %d: %v", len(events), err)
	}
	failed, model := 0, 0
	for _, event := range events {
		if event.EntityType != "document" || event.EntityID != "fixture-request-doc" || event.Metadata["event_time_basis"] != "step_end" {
			return errors.New("stored event lost explicit request entities or occurrence-time basis")
		}
		recordedAt, err := time.Parse(time.RFC3339Nano, event.Metadata["recorded_at"])
		if err != nil || recordedAt.Before(event.Timestamp) {
			return errors.New("recording time is not separate from valid occurrence time")
		}
		matched := false
		for _, step := range result.Steps {
			if event.ActionType == step.Capability && event.Timestamp.Equal(step.EndTime) {
				matched = true
			}
		}
		if !matched {
			return errors.New("stored occurrence time differs from the actual executed step")
		}
		switch event.Outcome {
		case "failure":
			failed++
			if event.ActionType == "fixture_long_error" {
				if event.Metadata["summary_source"] != "heuristic" || event.Metadata["summary_fallback_reason"] != "batch_limit" || event.Metadata["summary_cause_shortened"] != "true" || !strings.HasSuffix(event.Summary, " [shortened]") || len(event.Summary) > 400 {
					return errors.New("long failure lost its bound, marker or batch-exclusion attribution")
				}
				continue
			}
			if event.Metadata["summary_source"] != "heuristic" || event.Metadata["summary_fallback_reason"] != "missing_summary" || !strings.Contains(event.Summary, "SYNTHETIC_INPUT_REFUSAL") {
				return errors.New("refused operation lacks truthful fallback provenance")
			}
		case "success":
			model++
			if event.Metadata["summary_source"] != "model" || len(event.Entities) != 2 {
				return errors.New("model summary or distinct step entities lost")
			}
		}
	}
	var rawLongError bool
	for _, step := range result.Steps {
		if step.Capability == "fixture_long_error" && len(step.Error) > 10000 && strings.Contains(step.Error, "LONG_ERROR_FIXTURE") {
			rawLongError = true
		}
	}
	if !rawLongError {
		return errors.New("source long error was not preserved in execution evidence")
	}
	active, err := deps.Coordinator.GetActiveInvestigations(ctx)
	if err != nil || len(active) != 0 {
		return errors.New("execution-owned investigation claim survived terminal cleanup")
	}
	recall, recallResult, err := o.ProcessRequestWithExecution(ctx, "Labelled diagnostic: inspect previously recorded fixture evidence without dispatching new work.", map[string]interface{}{"entity_type": "document", "entity_id": "fixture-request-doc"})
	if err != nil || recall == nil || recallResult == nil || len(recallResult.Steps) != 0 || !client.failureSeen || !client.historySeen || client.calls != 5 || dispatches.Load() != 3 || failed != 2 || model != 1 {
		return fmt.Errorf("recall/provenance verification failed: %v interactions=%d dispatches=%d", err, client.calls, dispatches.Load())
	}
	emit("record_facts_verified", map[string]any{"request_id": response.RequestID, "recall_request_id": recall.RequestID,
		"trace_id": trace.SpanContextFromContext(ctx).TraceID().String(), "memory_deployment": runID, "events": events,
		"paid_calls": 0, "fixture_interactions": client.calls, "tool_dispatches": dispatches.Load(), "investigation_claims_after_first_request": len(active),
		"raw_long_error_preserved": rawLongError,
		"qualification":            "Labelled local responses; actual public harness, discovery, dispatch, hooks, Redis, execution/debug persistence and OTLP. No maintenance case or PR action."})
	return nil
}
