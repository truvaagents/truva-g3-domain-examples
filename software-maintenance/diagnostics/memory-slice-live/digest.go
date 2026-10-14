package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/truvaagents/truva-g3/core"
	"github.com/truvaagents/truva-g3/memory"
	"github.com/truvaagents/truva-g3/orchestration"
	"github.com/truvaagents/truva-g3/telemetry"
	"go.opentelemetry.io/otel/trace"
)

// This exercises production hooks, not a substitute autonomous workflow.
func verifyDigestLive() (runErr error) {
	if os.Getenv("OPENROUTER_API_KEY") == "" {
		return errors.New("OpenRouter credential missing")
	}
	lock, err := os.OpenFile("/tmp/truvag3-digest-window-live.lock", os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return err
	}
	_ = lock.Close()
	cfg := telemetry.UseProfile(telemetry.ProfileDevelopment)
	cfg.ServiceName = "digest-window-live-diagnostic"
	if endpoint := os.Getenv("OTEL_EXPORTER_OTLP_ENDPOINT"); endpoint != "" {
		cfg.Endpoint = endpoint
	}
	if err := telemetry.Initialize(cfg); err != nil {
		return err
	}
	telemetry.EnableFrameworkIntegration(nil)
	defer func() {
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		runErr = errors.Join(runErr, telemetry.Shutdown(ctx))
	}()
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Minute)
	defer cancel()
	ctx, span := telemetry.GetTelemetryProvider().StartSpan(ctx, "diagnostic.digest_window")
	defer span.End()
	runID := fmt.Sprintf("digest-window-%d", time.Now().UnixNano())
	ctx = telemetry.WithBaggage(orchestration.WithRequestID(ctx, runID), "request_id", runID)
	emit("digest_run", map[string]any{"request_id": runID, "trace_id": trace.SpanContextFromContext(ctx).TraceID().String(), "max_paid_calls": 4})
	guard := &guardedTransport{next: http.DefaultTransport}
	defer func() { emit("provider_attempts", map[string]any{"count": guard.calls, "wire": guard.wire}) }()
	logger := core.NewBaseAgent("digest-window-live-diagnostic").Logger
	chain, err := diagnosticChain(guard, logger, telemetry.GetTelemetryProvider())
	if err != nil {
		return err
	}
	connection, err := core.ResolveRedisConnectionConfig(nil, os.LookupEnv)
	if err != nil {
		return err
	}
	client, err := core.NewRedisUniversalClient(connection)
	if err != nil {
		return err
	}
	defer func() { _ = client.Close() }()
	keys, err := core.NewRedisKeyspace(runID)
	if err != nil {
		return err
	}
	const diagnosticDomain = "labelled-digest-fixture"
	const agent = "synthetic-documents"
	episodic, err := memory.NewStreamEpisodicMemory(memory.WithEpisodicRedisClient(client), memory.WithEpisodicKeyspace(keys), memory.WithEpisodicDomain(diagnosticDomain), memory.WithEpisodicEventTTL(10*time.Minute))
	if err != nil {
		return err
	}
	cache, err := memory.NewRedisDigestCache(client, logger, memory.WithDigestCacheKeyspace(keys))
	if err != nil {
		return err
	}
	// Exact owned keys only. No shared memory records or diagnostic evidence are deleted.
	defer func() {
		cleanupCtx, stop := context.WithTimeout(context.Background(), 10*time.Second)
		defer stop()
		for _, key := range []string{
			keys.Plain("memory", diagnosticDomain, "digest"),
			keys.Plain("memory", diagnosticDomain, "events", "stream"),
			keys.Plain("memory", diagnosticDomain, "agent", agent),
			keys.Plain("memory", diagnosticDomain, "event", "one"),
			keys.Plain("memory", diagnosticDomain, "event", "two"),
			keys.Plain("memory", diagnosticDomain, "event", "late"),
		} {
			runErr = errors.Join(runErr, client.Del(cleanupCtx, key).Err())
		}
	}()
	compactor, err := orchestration.NewLLMActivityCompactor(chain, orchestration.WithActivityCompactorModel("fast"), orchestration.WithActivityCompactorLogger(logger), orchestration.WithActivityCompactorTelemetry(telemetry.GetTelemetryProvider()))
	if err != nil {
		return err
	}
	defer func() { _ = compactor.Shutdown(context.Background()) }()
	hook, err := orchestration.NewMemoryEnrichmentHook(episodic, nil, agent, diagnosticDomain, orchestration.WithActivityCompactor(compactor), orchestration.WithDigestCache(cache), orchestration.WithCompactionRawLimit(3), orchestration.WithCompactionMaxTokens(500))
	if err != nil {
		return err
	}
	base := time.Now().UTC().Add(-time.Minute)
	makeEvent := func(id, summary string) core.AgentEvent {
		return core.AgentEvent{EventID: id, AgentDomain: diagnosticDomain, AgentName: agent, RequestID: "synthetic-origin", Timestamp: base, Scope: core.ScopeSharedDomain, ActionType: "document_read", Outcome: "success", Summary: summary}
	}
	for _, event := range []core.AgentEvent{makeEvent("one", "Labelled fixture: read document A."), makeEvent("two", "Labelled fixture: read document B.")} {
		if err := episodic.RecordEvent(ctx, event); err != nil {
			return err
		}
	}
	check := func(label string, wantCalls, wantEvents int) error {
		pctx := &core.PipelineContext{Request: "Labelled synthetic memory check; no task or PR."}
		if _, err := hook.BeforePlanning(ctx, pctx); err != nil {
			return err
		}
		view, _ := pctx.Enrichments[core.EnrichmentRAGContext].(string)
		blob, err := cache.GetDigest(ctx, diagnosticDomain)
		if err != nil {
			return err
		}
		if len(blob) == 0 {
			emit("digest_unavailable", map[string]any{"label": label, "provider_calls": guard.calls, "raw_fallback": strings.Contains(view, "Historical domain events:")})
			return fmt.Errorf("%s: no usable digest was cached; inspect the correlated compaction/provider trace", label)
		}
		var record struct {
			Content string `json:"content"`
			Window  struct {
				Events   []string `json:"events"`
				Coverage string   `json:"coverage"`
			} `json:"window"`
		}
		if err := json.Unmarshal(blob, &record); err != nil {
			return fmt.Errorf("%s cached evidence: %w", label, err)
		}
		if guard.calls != wantCalls || len(record.Window.Events) != wantEvents || strings.TrimSpace(record.Content) == "" || !strings.Contains(view, "Coverage: bounded returned event window") {
			return fmt.Errorf("%s: calls=%d want=%d represented=%d want=%d", label, guard.calls, wantCalls, len(record.Window.Events), wantEvents)
		}
		emit("digest_checkpoint", map[string]any{"label": label, "provider_calls": guard.calls, "represented_events": len(record.Window.Events), "coverage": record.Window.Coverage, "digest": record.Content})
		return nil
	}
	if err := check("initial", 1, 2); err != nil {
		return err
	}
	if err := check("unchanged_zero_call", 1, 2); err != nil {
		return err
	}
	if err := episodic.RecordEvent(ctx, makeEvent("late", "Labelled late fixture with the same timestamp: document C was read.")); err != nil {
		return err
	}
	if err := check("late_same_timestamp", 2, 3); err != nil {
		return err
	}
	if err := check("saturated_unchanged_zero_call", 2, 3); err != nil {
		return err
	}
	corrected := makeEvent("one", "Corrected labelled fixture: document A read failed, not successful.")
	corrected.Outcome = "failure"
	if err := episodic.RecordEvent(ctx, corrected); err != nil {
		return err
	}
	return check("changed_content_rebaseline", 3, 3)
}
