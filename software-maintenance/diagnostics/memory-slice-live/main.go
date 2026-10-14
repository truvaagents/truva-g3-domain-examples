// memory-slice-live is an operator diagnostic, not a registered agent or a
// maintenance workflow. It exercises production memory hooks using explicitly
// synthetic inputs, real Redis and at most four real OpenRouter requests.
package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/truvaagents/truva-g3/ai"
	_ "github.com/truvaagents/truva-g3/ai/providers/openai"
	"github.com/truvaagents/truva-g3/core"
	"github.com/truvaagents/truva-g3/memory"
	"github.com/truvaagents/truva-g3/orchestration"
	"github.com/truvaagents/truva-g3/orchestration/redisprovider"
	"github.com/truvaagents/truva-g3/telemetry"
	"go.opentelemetry.io/otel/trace"
)

const domain = "labelled-memory-slice-diagnostic"
const interpretation = `This is a labelled synthetic historical-memory diagnostic, not a maintenance execution. Read only the supplied historical view. Return a JSON object with origin_request_ids (unique origins of admitted event records), entity_ids (unique nonempty entity_id fields of admitted event records), and current_request_completed (whether the historical view proves this new request already completed). Read record fields, rather than IDs mentioned in summary prose. Preserve identifiers exactly. Omitted records are not evidence. Return the object directly.`

type fixture struct {
	Name  string                     `json:"name"`
	Steps []orchestration.StepResult `json:"steps"`
}

func fixtures() []fixture {
	var out []fixture
	for _, name := range []string{"documents", "maintenance"} {
		typ := "document"
		if name == "maintenance" {
			typ = "case"
		}
		var steps []orchestration.StepResult
		for i := range 3 {
			response := `{"count":3,"status":"available"}`
			if i < 2 {
				value := map[string]any{"label": "synthetic diagnostic fixture", "entities": []core.EntityRef{{Type: typ, ID: fmt.Sprintf("fixture-%s-%d-界", name, i)}}}
				if i == 1 {
					value["entities"] = append(value["entities"].([]core.EntityRef), core.EntityRef{Type: typ, ID: "fixture-" + name + "-linked"})
				}
				data, _ := json.Marshal(value)
				response = string(data)
			}
			steps = append(steps, orchestration.StepResult{StepID: fmt.Sprintf("step-%d", i), AgentName: "synthetic-fixture-source", Capability: "diagnostic_read", Instruction: "Summarize this labelled synthetic observation.", Response: response, Success: true})
		}
		out = append(out, fixture{Name: name, Steps: steps})
	}
	return out
}

func historicalFixtures() []core.AgentEvent {
	var events []core.AgentEvent
	for i := range 15 {
		events = append(events, core.AgentEvent{EventID: fmt.Sprintf("fixture-history-%d", i), RequestID: fmt.Sprintf("fixture-origin-%d", i%3), AgentName: "synthetic-history-source", AgentDomain: domain, ActionType: "diagnostic_read", Scope: core.ScopeSharedDomain, Outcome: "success", EntityType: "document", EntityID: fmt.Sprintf("fixture-doc-%d-界", i), Summary: strings.Repeat("Labelled synthetic historical observation; it establishes no current-request progress. ", 12)})
	}
	return events
}

type guardedTransport struct {
	next  http.RoundTripper
	calls int
	limit int
	wire  []map[string]any
}

func (g *guardedTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	limit := g.limit
	if limit == 0 {
		limit = 4
	}
	if req.URL.Scheme != "https" || req.URL.Host != "openrouter.ai" || req.URL.Path != "/api/v1/chat/completions" || g.calls >= limit {
		return nil, errors.New("diagnostic destination or attempt budget rejected")
	}
	body, err := io.ReadAll(io.LimitReader(req.Body, 1<<20))
	if err != nil {
		return nil, err
	}
	_ = req.Body.Close()
	var wire map[string]any
	if err := json.Unmarshal(body, &wire); err != nil {
		return nil, err
	}
	provider, _ := wire["provider"].(map[string]any)
	model, _ := wire["model"].(string)
	if (model != "openai/gpt-6-luna" && model != "deepseek/deepseek-v4.1-flash") || provider["zdr"] != true || provider["data_collection"] != "deny" {
		return nil, errors.New("diagnostic model or zero-retention policy rejected")
	}
	for _, field := range []string{"max_tokens", "max_completion_tokens"} {
		if n, ok := wire[field].(float64); ok && (n <= 0 || n > 10000) {
			return nil, errors.New("diagnostic output cap rejected")
		}
	}
	g.calls++
	g.wire = append(g.wire, map[string]any{"model": model, "max_tokens": wire["max_tokens"], "max_completion_tokens": wire["max_completion_tokens"], "provider": provider, "reasoning": wire["reasoning"]})
	req.Body = io.NopCloser(bytes.NewReader(body))
	return g.next.RoundTrip(req)
}

func emit(kind string, value any) {
	_ = json.NewEncoder(os.Stdout).Encode(map[string]any{"diagnostic": kind, "data": value})
}

func run() error {
	live := flag.Bool("live", false, "Run once in Kind; maximum four paid calls, no retries")
	shutdownOnly := flag.Bool("shutdown-only", false, "Verify real telemetry export/shutdown in Kind without AI or Redis calls")
	regressionOnly := flag.Bool("regression-only", false, "Run labelled dependency/parser fixture replay with real HTTP, Redis and OTLP; no paid AI")
	leasesOnly := flag.Bool("leases-only", false, "Verify investigation ownership and managed cleanup against real Redis in Kind; no AI")
	lifecycleLive := flag.Bool("lifecycle-live", false, "Run one real planning/synthesis lifecycle diagnostic in Kind, at most four OpenRouter calls")
	digestLive := flag.Bool("digest-live", false, "Verify bounded digest reuse and late events using real Redis/OpenRouter; at most four calls")
	planEnvelopeLive := flag.Bool("plan-envelope-live", false, "Labelled model-envelope fixtures followed by real correction/synthesis in Kind; at most four paid calls")
	batchAGaps := flag.Bool("batch-a-gaps", false, "Labelled non-retryable HTTP replay, cancellation and real HITL suspension in Kind; no paid AI")
	noWorkLive := flag.Bool("no-work-live", false, "Verify explicit no-work planning and agent-name retry in Kind; at most two paid OpenRouter requests")
	noWorkInitialOnly := flag.Bool("no-work-initial-only", false, "With no-work-live, verify only initial planning with at most one paid request")
	noWorkRetryFeedback := flag.Bool("no-work-retry-feedback", false, "With no-work-live, verify iterative guidance and latest retry feedback; at most two paid requests")
	noWorkDebugProvenance := flag.Bool("no-work-debug-provenance", false, "With no-work-live, verify rejection records against labelled attempts; no paid AI")
	recordFactsLive := flag.Bool("record-facts-live", false, "Verify memory failure facts, request entities and occurrence times with real harness/Redis in Kind; no paid AI")
	remediationLive := flag.Bool("remediation-state-live", false, "Read existing PR states through normal discovery/harness in Kind; no GitHub writes")
	remediationPaid := flag.Bool("remediation-state-ai", false, "With remediation-state-live, use at most four authorized paid OpenRouter calls")
	remediationCapture := flag.String("remediation-review-capture", "", "Saved remediation_reads_verified JSON record to assess; no AI or network calls")
	remediationReview := flag.String("remediation-review-file", "", "Recorded assessment of the exact final model response")
	approved := flag.String("approved-fixture-sha256", "", "Digest printed by offline preflight")
	flag.Parse()
	if *remediationCapture != "" || *remediationReview != "" {
		conflict := *remediationCapture == "" || *remediationReview == ""
		flag.Visit(func(f *flag.Flag) {
			if f.Name != "remediation-review-capture" && f.Name != "remediation-review-file" {
				conflict = true
			}
		})
		if conflict {
			return errors.New("remediation assessment requires both files and excludes execution modes")
		}
		return verifyRecordedRemediationAssessment(*remediationCapture, *remediationReview)
	}
	if *remediationPaid && !*remediationLive {
		return errors.New("remediation-state-ai requires remediation-state-live")
	}
	if *remediationLive {
		var conflict bool
		flag.Visit(func(f *flag.Flag) {
			if f.Name != "remediation-state-live" && f.Name != "remediation-state-ai" {
				conflict = true
			}
		})
		if conflict || os.Getenv("KUBERNETES_SERVICE_HOST") == "" {
			return errors.New("remediation-state-live requires Kind and excludes other modes")
		}
		return verifyRemediationStateLive(*remediationPaid)
	}
	if *recordFactsLive {
		if *live || *shutdownOnly || *regressionOnly || *leasesOnly || *lifecycleLive || *digestLive || *planEnvelopeLive || *batchAGaps || *noWorkLive || *noWorkInitialOnly || *noWorkRetryFeedback || *noWorkDebugProvenance || os.Getenv("KUBERNETES_SERVICE_HOST") == "" {
			return errors.New("record-facts-live requires Kind and excludes other modes")
		}
		return verifyRecordFactsLive()
	}
	if (*noWorkInitialOnly || *noWorkRetryFeedback || *noWorkDebugProvenance) && !*noWorkLive {
		return errors.New("no-work scenario options require no-work-live")
	}
	if (*noWorkInitialOnly && *noWorkRetryFeedback) || (*noWorkDebugProvenance && (*noWorkInitialOnly || *noWorkRetryFeedback)) {
		return errors.New("no-work scenario options are exclusive")
	}
	if *noWorkLive {
		if *live || *shutdownOnly || *regressionOnly || *leasesOnly || *lifecycleLive || *digestLive || *planEnvelopeLive || *batchAGaps || os.Getenv("KUBERNETES_SERVICE_HOST") == "" {
			return errors.New("no-work-live requires Kind and excludes other modes")
		}
		return verifyNoWorkLive(*noWorkInitialOnly, *noWorkRetryFeedback, *noWorkDebugProvenance)
	}
	if *batchAGaps {
		if *live || *shutdownOnly || *regressionOnly || *leasesOnly || *lifecycleLive || *digestLive || *planEnvelopeLive || os.Getenv("KUBERNETES_SERVICE_HOST") == "" {
			return errors.New("batch-a-gaps requires Kind and excludes other modes")
		}
		return verifyBatchAGaps()
	}
	if *planEnvelopeLive {
		if *live || *shutdownOnly || *regressionOnly || *leasesOnly || *lifecycleLive || *digestLive || os.Getenv("KUBERNETES_SERVICE_HOST") == "" {
			return errors.New("plan-envelope-live requires Kind and excludes other modes")
		}
		return verifyPlanEnvelopeLive()
	}
	if *digestLive {
		if *live || *shutdownOnly || *regressionOnly || *leasesOnly || *lifecycleLive || os.Getenv("KUBERNETES_SERVICE_HOST") == "" {
			return errors.New("digest-live requires Kind and excludes other modes")
		}
		return verifyDigestLive()
	}
	if *lifecycleLive {
		if *live || *shutdownOnly || *regressionOnly || *leasesOnly || os.Getenv("KUBERNETES_SERVICE_HOST") == "" {
			return errors.New("lifecycle-live requires Kind and excludes other modes")
		}
		return verifyLifecycleLive()
	}
	if *leasesOnly {
		if *live || *shutdownOnly || *regressionOnly || os.Getenv("KUBERNETES_SERVICE_HOST") == "" {
			return errors.New("leases-only requires Kind and excludes other modes")
		}
		return verifyLeases()
	}
	if *regressionOnly {
		if *live || *shutdownOnly || os.Getenv("KUBERNETES_SERVICE_HOST") == "" {
			return errors.New("regression-only requires Kind and excludes other live modes")
		}
		return verifyRegressions()
	}
	if *shutdownOnly {
		if *live || os.Getenv("KUBERNETES_SERVICE_HOST") == "" {
			return errors.New("shutdown-only requires Kind and excludes live AI mode")
		}
		return verifyShutdown()
	}
	data, _ := json.Marshal(struct {
		Fixtures       []fixture
		History        []core.AgentEvent
		Interpretation string
	}{fixtures(), historicalFixtures(), interpretation})
	digest := fmt.Sprintf("%x", sha256.Sum256(data))
	if !*live {
		emit("preflight", map[string]any{"fixture_sha256": digest, "fixtures": fixtures(), "history": historicalFixtures(), "interpretation": interpretation, "max_provider_attempts": 4})
		return nil
	}
	if *approved != digest || os.Getenv("KUBERNETES_SERVICE_HOST") == "" || os.Getenv("OPENROUTER_API_KEY") == "" {
		return errors.New("live requires Kind, OpenRouter credential and explicit matching fixture approval")
	}
	lock, err := os.OpenFile("/tmp/truvag3-memory-slice-live.lock", os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return fmt.Errorf("one-run guard: %w", err)
	}
	_ = lock.Close()
	for k, v := range map[string]string{"OPENROUTER_BASE_URL": "https://openrouter.ai/api/v1", "TRUVAG3_OPENROUTER_MODEL_FAST": "openai/gpt-6-luna", "TRUVAG3_OPENROUTER_MODEL_DEFAULT": "deepseek/deepseek-v4.1-flash"} {
		if err := os.Setenv(k, v); err != nil {
			return err
		}
	}
	g := &guardedTransport{next: http.DefaultTransport}
	defer func() {
		emit("provider_attempts", map[string]any{"count": g.calls, "wire": g.wire})
	}()
	cfg := telemetry.UseProfile(telemetry.ProfileDevelopment)
	cfg.ServiceName = "memory-slice-live"
	if endpoint := os.Getenv("OTEL_EXPORTER_OTLP_ENDPOINT"); endpoint != "" {
		cfg.Endpoint = endpoint
	}
	if err := telemetry.Initialize(cfg); err != nil {
		return err
	}
	telemetry.EnableFrameworkIntegration(nil)
	defer func() {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_ = telemetry.Shutdown(ctx)
	}()
	logger := core.NewBaseAgent("memory-slice-live").Logger
	chain, err := diagnosticChain(g, logger, telemetry.GetTelemetryProvider())
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
	runID := fmt.Sprintf("memory-slice-%d", time.Now().UnixNano())
	backends, err := memory.NewSharedBackends(redis, logger, memory.WithAgentName("memory-slice-live"), memory.WithDomain(domain), memory.WithRedisDeployment(runID), memory.WithKnowledgeDisabled())
	if err != nil {
		return err
	}
	defer backends.Close()
	debug, err := redisprovider.NewDefaultBackends(logger, redisprovider.WithDefaultBackendRoles(redisprovider.ClientRoleLLMDebug))
	if err != nil {
		return err
	}
	defer func() { _ = debug.Close() }()
	store := debug.Backends().LLMDebug()
	ctx, cancel := context.WithTimeout(context.Background(), 12*time.Minute)
	defer cancel()
	ctx, span := telemetry.GetTelemetryProvider().StartSpan(ctx, "diagnostic.memory_slices")
	defer span.End()
	emit("run", map[string]any{"request_prefix": runID, "memory_deployment": runID, "domain": domain, "trace_id": trace.SpanContextFromContext(ctx).TraceID().String(), "fixture_sha256": digest})
	deps := backends.ToDeps()
	for _, f := range fixtures() {
		requestID := runID + "-" + f.Name
		callCtx := telemetry.WithBaggage(orchestration.WithRequestID(ctx, requestID), "request_id", requestID)
		s, err := orchestration.NewLLMEventSummarizer(chain, orchestration.WithSummarizerModel("fast"), orchestration.WithSummarizerMaxTokens(10000), orchestration.WithSummarizerLogger(logger), orchestration.WithSummarizerTelemetry(telemetry.GetTelemetryProvider()))
		if err != nil {
			return err
		}
		s.SetLLMDebugStore(store)
		hook, err := orchestration.NewMemoryRecordHook(deps.Episodic, nil, "memory-slice-live", domain, orchestration.WithEventSummarizer(s), orchestration.WithRecordEntityExtractor(orchestration.LLMEntityExtractor{}))
		if err != nil {
			return err
		}
		if err := hook.AfterExecution(callCtx, &core.PipelineContext{}, &orchestration.ExecutionResult{Steps: f.Steps, Success: true}); err != nil {
			return err
		}
		if err := s.Shutdown(ctx); err != nil {
			return err
		}
		record, err := store.GetRecord(ctx, requestID)
		if err != nil {
			return err
		}
		if len(record.Interactions) != 1 {
			return errors.New("unexpected summary interaction count")
		}
		var entries []struct {
			StepID   string            `json:"step_id"`
			Entities *[]core.EntityRef `json:"entities"`
		}
		if err := json.Unmarshal([]byte(record.Interactions[0].Response), &entries); err != nil {
			return fmt.Errorf("summary field audit: %w", err)
		}
		if len(entries) != 3 {
			return errors.New("incomplete summary array")
		}
		seen := make(map[string]bool)
		for _, entry := range entries {
			if entry.Entities == nil {
				return errors.New("missing or null entities field")
			}
			var expected []core.EntityRef
			found := false
			for _, step := range f.Steps {
				if step.StepID == entry.StepID {
					var source struct {
						Entities []core.EntityRef `json:"entities"`
					}
					if err := json.Unmarshal([]byte(step.Response), &source); err != nil {
						return err
					}
					expected, found = source.Entities, true
				}
			}
			if !found || seen[entry.StepID] || len(*entry.Entities) != len(expected) {
				return fmt.Errorf("invalid step or entity count: %s", entry.StepID)
			}
			seen[entry.StepID] = true
			actual := make(map[core.EntityRef]bool)
			for _, entity := range *entry.Entities {
				actual[entity] = true
			}
			for _, entity := range expected {
				if !actual[entity] {
					return fmt.Errorf("source entity not copied exactly for %s", entry.StepID)
				}
			}
			for _, entity := range *entry.Entities {
				history, err := deps.Episodic.QueryEntityHistory(ctx, domain, entity.Type, entity.ID, time.Now().Add(-time.Hour))
				if err != nil {
					return err
				}
				if len(history) == 0 || history[0].RequestID != requestID {
					return errors.New("entity-indexed durable readback missing")
				}
			}
		}
		emit("summary_readback", map[string]any{"request_id": requestID, "entries": entries})
	}
	// Clearly labelled synthetic history stresses the real default byte budget;
	// no production event or timestamp is rewritten and no shared domain is used.
	for _, event := range historicalFixtures() {
		if err := deps.Episodic.RecordEvent(ctx, event); err != nil {
			return err
		}
	}
	for _, limit := range []int{1, 20} {
		requestID := fmt.Sprintf("%s-view-%d", runID, limit)
		callCtx := telemetry.WithBaggage(orchestration.WithRequestID(ctx, requestID), "request_id", requestID)
		hook, err := orchestration.NewMemoryEnrichmentHook(deps.Episodic, nil, "memory-slice-live", domain, orchestration.WithEnrichmentRecentEventsLimit(limit), orchestration.WithEnrichmentLogger(logger))
		if err != nil {
			return err
		}
		pctx := &core.PipelineContext{}
		if _, err := hook.BeforePlanning(callCtx, pctx); err != nil {
			return err
		}
		view, _ := pctx.Enrichments[core.EnrichmentRAGContext].(string)
		if view == "" || len(view) > 8000 || !strings.HasPrefix(view, "Memory scope:") {
			return errors.New("invalid memory view bounds or scope")
		}
		origins, entities := map[string]bool{}, map[string]bool{}
		for _, line := range strings.Split(view, "\n") {
			if !strings.HasPrefix(line, "- ") {
				continue
			}
			var entry struct {
				Event    []string `json:"event"`
				Origin   string   `json:"origin_request_id"`
				EntityID string   `json:"entity_id"`
			}
			if err := json.Unmarshal([]byte(line[2:]), &entry); err != nil {
				return errors.New("partial JSON in memory view")
			}
			if len(entry.Event) > 0 {
				if len(entry.Event) != 6 {
					return errors.New("invalid tuple")
				}
				entry.Origin = entry.Event[1]
			}
			if !strings.HasPrefix(entry.Origin, "fixture-origin-") {
				return errors.New("unreviewed origin in diagnostic prompt")
			}
			origins[entry.Origin] = true
			if entry.EntityID != "" {
				entities[entry.EntityID] = true
			}
		}
		if (limit == 20) != strings.Contains(view, "Event tuple (event-detail/v1)") {
			return errors.New("unexpected dense/sparse format")
		}
		prompt := interpretation + "\nCurrent request: " + requestID + "\n<historical_memory>\n" + view + "\n</historical_memory>"
		started := time.Now()
		response, err := chain.GenerateResponse(callCtx, prompt, &core.AIOptions{Model: "default", MaxTokens: 3000, ResponseFormat: "json"})
		if err != nil {
			return err
		}
		if err := store.RecordInteraction(callCtx, requestID, orchestration.LLMInteraction{Type: "agent_llm_call", Timestamp: started, DurationMs: time.Since(started).Milliseconds(), Prompt: prompt, Response: response.Content, MaxTokens: 3000, Model: response.Model, Success: true, SourceComponent: "memory-slice-live", CallDescription: "Labelled diagnostic memory interpretation; not planner execution", PromptTokens: response.Usage.PromptTokens, CompletionTokens: response.Usage.CompletionTokens, TotalTokens: response.Usage.TotalTokens}); err != nil {
			return err
		}
		var answer struct {
			Origins  []string `json:"origin_request_ids"`
			Entities []string `json:"entity_ids"`
			Complete *bool    `json:"current_request_completed"`
		}
		if err := json.Unmarshal([]byte(response.Content), &answer); err != nil {
			return err
		}
		if answer.Complete == nil || *answer.Complete || !sameSet(answer.Origins, origins) || !sameSet(answer.Entities, entities) {
			emit("interpretation_mismatch", map[string]any{"answer": answer, "expected_origins": origins, "expected_entities": entities})
			return errors.New("historical interpretation did not match admitted evidence")
		}
		emit("view_verified", map[string]any{"request_id": requestID, "limit": limit, "bytes": len(view), "view": view, "answer": answer})
	}
	return nil
}

// verifyShutdown emits a real span before flushing. It does not construct an AI
// client, read credentials, initialize Redis or create an execution/debug record.
func verifyShutdown() error {
	cfg := telemetry.UseProfile(telemetry.ProfileDevelopment)
	cfg.ServiceName = "telemetry-shutdown-diagnostic"
	if endpoint := os.Getenv("OTEL_EXPORTER_OTLP_ENDPOINT"); endpoint != "" {
		cfg.Endpoint = endpoint
	}
	if err := telemetry.Initialize(cfg); err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	ctx, span := telemetry.GetTelemetryProvider().StartSpan(ctx, "diagnostic.telemetry_shutdown")
	emit("shutdown_trace", map[string]any{"trace_id": trace.SpanContextFromContext(ctx).TraceID().String(), "paid_calls": 0})
	telemetry.Counter("diagnostic.shutdown_check", "module", "telemetry")
	span.End()
	for range 2 {
		if err := telemetry.Shutdown(ctx); err != nil {
			return err
		}
		if telemetry.GetRegistry() != nil || telemetry.GetTelemetryProvider() != nil || telemetry.GetHealth().Initialized || core.GetGlobalMetricsRegistry() != nil {
			return errors.New("telemetry remains initialized after shutdown")
		}
		telemetry.Counter("diagnostic.after_shutdown")
		telemetry.Gauge("diagnostic.after_shutdown_gauge", 1)
	}
	// Existing initialization is once-only, including after successful shutdown.
	if err := telemetry.Initialize(cfg); err != nil || telemetry.GetRegistry() != nil {
		return errors.New("initialize unexpectedly restarted telemetry after shutdown")
	}
	emit("shutdown_verified", map[string]any{"repeated_shutdown": true, "post_shutdown_noops": true, "reinitialization_disabled": true, "paid_calls": 0})
	return nil
}

// The legacy provider factory owns a separate HTTP transport and does not use
// http.DefaultTransport. Use the supported request-aware injection seam so the
// guard intercepts actual AI requests without affecting OTLP infrastructure.
func diagnosticChain(g *guardedTransport, logger core.Logger, provider core.Telemetry) (*ai.ChainClient, error) {
	return ai.NewChain(ai.ProviderEntry("diagnostic-openrouter", "openai.openrouter",
		ai.WithReasoningEffort("none"), ai.WithMaxRetries(0), ai.WithTimeout(180*time.Second),
		ai.WithTelemetry(provider), ai.WithLogger(logger), ai.WithHTTPClient(&http.Client{Transport: g}),
	))
}

func sameSet(values []string, want map[string]bool) bool {
	got := map[string]bool{}
	for _, v := range values {
		got[v] = true
	}
	if len(got) != len(want) {
		return false
	}
	for v := range got {
		if !want[v] {
			return false
		}
	}
	return true
}
func main() {
	if err := run(); err != nil {
		emit("failed", err.Error())
		os.Exit(1)
	}
	emit("complete", true)
}
