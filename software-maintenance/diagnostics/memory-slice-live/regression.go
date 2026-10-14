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
	"github.com/truvaagents/truva-g3/telemetry"
	"go.opentelemetry.io/otel/trace"
)

// These are controlled fixtures, not provider calls or autonomous plans. The
// diagnostic does not publish a fake agent execution or LLM debug record.
type fixtureSummaryAI struct{ body string }

func (f fixtureSummaryAI) GenerateResponse(context.Context, string, *core.AIOptions) (*core.AIResponse, error) {
	return &core.AIResponse{Content: f.body, Model: "labelled-fixture-replay", Provider: "local-fixture"}, nil
}

type inclusionLogger struct {
	core.Logger
	warnings atomic.Int32
}

func (l *inclusionLogger) WarnWithContext(ctx context.Context, msg string, fields map[string]interface{}) {
	if fields["operation"] == "template_dependency_auto_include" {
		l.warnings.Add(1)
	}
	l.Logger.WarnWithContext(ctx, msg, fields)
}

func verifyRegressions() error {
	cfg := telemetry.UseProfile(telemetry.ProfileDevelopment)
	cfg.ServiceName = "post-memory-regression-diagnostic"
	if endpoint := os.Getenv("OTEL_EXPORTER_OTLP_ENDPOINT"); endpoint != "" {
		cfg.Endpoint = endpoint
	}
	if err := telemetry.Initialize(cfg); err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	ctx, span := telemetry.GetTelemetryProvider().StartSpan(ctx, "diagnostic.fixture_regressions")
	span.SetAttribute("diagnostic.fixture_ai", true)
	emit("regression_trace", map[string]any{"trace_id": trace.SpanContextFromContext(ctx).TraceID().String(), "paid_calls": 0})
	err := runRegressionFixtures(ctx)
	if err != nil {
		telemetry.RecordSpanError(ctx, err)
	}
	span.End()
	shutdownCtx, stop := context.WithTimeout(context.Background(), 20*time.Second)
	defer stop()
	return errors.Join(err, telemetry.Shutdown(shutdownCtx))
}

func runRegressionFixtures(ctx context.Context) error {
	if err := verifyTemplateInclusions(ctx); err != nil {
		return err
	}
	if err := verifyLegacyOwnershipCorrelation(ctx); err != nil {
		return err
	}
	return verifySummaryEnvelopes(ctx)
}

func verifyTemplateInclusions(ctx context.Context) error {
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		var input map[string]interface{}
		if json.NewDecoder(r.Body).Decode(&input) != nil || input["value"] != "exact-界" {
			http.Error(w, "fixture received incorrect bound input", http.StatusBadRequest)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]interface{}{"data": input})
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
	catalog := orchestration.NewAgentCatalogWithFixtures(map[string]*orchestration.AgentInfo{
		"fixture": {
			Registration: &core.ServiceRegistration{ID: "fixture", Name: "fixture", Type: core.ComponentTypeTool, Address: host, Port: port},
			Capabilities: []orchestration.EnhancedCapability{{Name: "echo", Endpoint: "/echo", Parameters: []orchestration.Parameter{{Name: "value", Type: "string", Required: true}}}},
		},
	})
	for _, declared := range []bool{true, false} {
		logger := &inclusionLogger{Logger: &core.NoOpLogger{}}
		executor := orchestration.NewSmartExecutor(catalog)
		executor.SetLogger(logger)
		step := orchestration.RoutingStep{StepID: "step-2", AgentName: "fixture", Metadata: map[string]interface{}{
			"capability": "echo", "parameters": map[string]interface{}{"value": "{{step-1.response.value}}"},
		}}
		reason := "undeclared_reference"
		if declared {
			step.ImplicitDeps = []string{"step-1"}
			reason = "declared_prior_phase"
		}
		requestID := fmt.Sprintf("correlation-fixture-%s-%d", reason, time.Now().UnixNano())
		callCtx := orchestration.WithCompletedSteps(orchestration.WithRequestID(ctx, requestID), map[string]*orchestration.StepResult{
			"step-1": {StepID: "step-1", Success: true, Response: `{"value":"exact-界"}`},
		})
		result, err := executor.Execute(callCtx, &orchestration.RoutingPlan{PlanID: "labelled-fixture-" + reason, Steps: []orchestration.RoutingStep{step}})
		if err != nil || result == nil || !result.Success || len(result.Steps) != 1 {
			return fmt.Errorf("fixture executor failed: %v %+v", err, result)
		}
		data, _ := json.Marshal(result.Steps[0].Metadata["template_auto_includes"])
		var inclusions []struct{ Reason string }
		if json.Unmarshal(data, &inclusions) != nil || len(inclusions) != 1 || inclusions[0].Reason != reason || (logger.warnings.Load() == 0) != declared {
			return fmt.Errorf("inclusion evidence mismatch: %s warnings=%d", data, logger.warnings.Load())
		}
		emit("inclusion_verified", map[string]any{"request_id": requestID, "reason": reason, "warnings": logger.warnings.Load(), "metadata": result.Steps[0].Metadata})
	}
	if calls.Load() != 2 {
		return fmt.Errorf("expected two fixture HTTP dispatches, got %d", calls.Load())
	}
	return nil
}

// Hide the optional lease interface to exercise the documented legacy adapter
// contract against actual, isolated Redis state. This is an operator fixture,
// not a new application coordinator or a claim of token-safe ownership.
type legacyCoordinatorDiagnostic struct{ core.InvestigationCoordinator }

func verifyLegacyOwnershipCorrelation(ctx context.Context) error {
	connection, err := core.ResolveRedisConnectionConfig(nil, os.LookupEnv)
	if err != nil {
		return err
	}
	client, err := core.NewRedisUniversalClient(connection)
	if err != nil {
		return err
	}
	defer func() { _ = client.Close() }()
	runID := fmt.Sprintf("legacy-correlation-fixture-%d", time.Now().UnixNano())
	keyspace, err := core.NewRedisKeyspace(runID)
	if err != nil {
		return err
	}
	coordinator, err := memory.NewAtomicLockCoordinator(memory.WithCoordinatorRedisClient(client), memory.WithCoordinatorKeyspace(keyspace), memory.WithCoordinatorDomain("documents"))
	if err != nil {
		return err
	}
	callCtx := orchestration.WithRequestID(ctx, runID)
	const agent, entity = "correlation-fixture", "synthetic-document"
	defer func() { _ = coordinator.ReleaseInvestigation(callCtx, agent, entity) }()
	hook, err := orchestration.NewMemoryEnrichmentHook(&core.NoOpEpisodicMemory{}, legacyCoordinatorDiagnostic{coordinator}, agent, "documents")
	if err != nil {
		return err
	}
	_, err = hook.BeforePlanning(callCtx, &core.PipelineContext{Request: "Labelled synthetic legacy ownership check", Metadata: map[string]interface{}{"entity_type": "document", "entity_id": entity}})
	if err != nil {
		return err
	}
	active, err := coordinator.GetActiveInvestigations(callCtx)
	if err != nil || active[entity] != agent {
		return errors.New("legacy diagnostic claim was not visible in Redis")
	}
	if err := coordinator.ReleaseInvestigation(callCtx, agent, entity); err != nil {
		return err
	}
	active, err = coordinator.GetActiveInvestigations(callCtx)
	if err != nil || len(active) != 0 {
		return errors.New("legacy diagnostic claim was not released")
	}
	emit("legacy_correlation_verified", map[string]any{"request_id": runID, "claimed_and_released": true, "paid_calls": 0})
	return nil
}

func verifySummaryEnvelopes(ctx context.Context) error {
	connection, err := core.ResolveRedisConnectionConfig(nil, os.LookupEnv)
	if err != nil {
		return err
	}
	redis, err := core.NewRedisUniversalClient(connection)
	if err != nil {
		return err
	}
	defer func() { _ = redis.Close() }()
	runID := fmt.Sprintf("envelope-fixture-%d", time.Now().UnixNano())
	backends, err := memory.NewSharedBackends(redis, &core.NoOpLogger{}, memory.WithAgentName("fixture-replay"), memory.WithDomain(domain), memory.WithRedisDeployment(runID), memory.WithKnowledgeDisabled())
	if err != nil {
		return err
	}
	defer backends.Close()
	entry := `{"step_id":"one","summary":"[fixture] retained **界**","entities":[]}`
	array := "[" + entry + `,{"step_id":"two","summary":"[fixture] second fact","entities":[]}]`
	for _, tc := range []struct {
		name, body string
		retained   int
	}{
		{"prefix", "Results: " + array, 2},
		{"suffix", array + " Done.", 2},
		{"both", "Results: " + array + " Done.", 2},
		{"fence", "```json\n" + array + "\n```", 2},
		{"truncated", "[" + entry + `,{"step_id":"two","summary":"cut`, 1},
		{"ambiguous", array + " []", 0},
	} {
		requestID := runID + "-" + tc.name
		callCtx := telemetry.WithBaggage(orchestration.WithRequestID(ctx, requestID), "request_id", requestID)
		s, err := orchestration.NewLLMEventSummarizer(fixtureSummaryAI{body: tc.body}, orchestration.WithSummarizerModel("labelled-fixture-replay"), orchestration.WithSummarizerTelemetry(telemetry.GetTelemetryProvider()))
		if err != nil {
			return err
		}
		hook, err := orchestration.NewMemoryRecordHook(backends.ToDeps().Episodic, nil, "fixture-replay", domain, orchestration.WithEventSummarizer(s))
		if err != nil {
			return err
		}
		err = hook.AfterExecution(callCtx, &core.PipelineContext{}, &orchestration.ExecutionResult{Success: true, Steps: []orchestration.StepResult{
			{StepID: "one", Success: true, Capability: "fixture_read"}, {StepID: "two", Success: true, Capability: "fixture_read"},
		}})
		closeErr := s.Shutdown(ctx)
		if err != nil || closeErr != nil {
			return errors.Join(err, closeErr)
		}
		events, err := backends.ToDeps().Episodic.QueryRecentEvents(ctx, domain, time.Now().Add(-time.Hour), 100)
		if err != nil {
			return err
		}
		count, retained := 0, 0
		for _, event := range events {
			if event.RequestID == requestID {
				count++
				if event.Summary == "[fixture] retained **界**" || event.Summary == "[fixture] second fact" {
					retained++
				}
			}
		}
		if count != 2 || retained != tc.retained {
			return fmt.Errorf("%s durable coverage mismatch: count=%d retained=%d", tc.name, count, retained)
		}
		emit("envelope_verified", map[string]any{"case": tc.name, "request_id": requestID, "durable_events": count, "retained": retained, "fallback": count - retained, "paid_calls": 0})
	}
	return nil
}
