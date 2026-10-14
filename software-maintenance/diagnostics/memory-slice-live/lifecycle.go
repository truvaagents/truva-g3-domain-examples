package main

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"time"

	"github.com/truvaagents/truva-g3/core"
	"github.com/truvaagents/truva-g3/memory"
	"github.com/truvaagents/truva-g3/orchestration"
	"github.com/truvaagents/truva-g3/orchestration/redisprovider"
	"github.com/truvaagents/truva-g3/telemetry"
	"go.opentelemetry.io/otel/trace"
)

// An explicitly labelled observation gate, not an autonomous maintenance
// workflow. It pauses delivery after a real model answer to check TTL renewal
// and request-vs-phase status, without altering the plan or model response.
type lifecycleObservationGate struct {
	activity core.ActivityCoordinator
	store    orchestration.ExecutionStore
}

func (*lifecycleObservationGate) Name() string { return "labelled-live-observation-gate" }
func (h *lifecycleObservationGate) AfterSynthesis(ctx context.Context, _ *core.PipelineContext, response string) (string, error) {
	id := orchestration.GetRequestID(ctx)
	emit("lifecycle_observation_window", map[string]any{"request_id": id, "seconds": 45, "synthetic_delay": true})
	for i := 0; i < 9; i++ {
		select {
		case <-time.After(5 * time.Second):
		case <-ctx.Done():
			return "", ctx.Err()
		}
		signals, err := h.activity.GetDomainActivities(ctx, "software-maintenance")
		if err != nil {
			return "", err
		}
		found := false
		for _, s := range signals {
			if s.RequestID == id {
				found = true
				if s.Status != "synthesizing" {
					return "", fmt.Errorf("activity regressed to %s", s.Status)
				}
			}
		}
		if !found {
			return "", errors.New("activity expired during delivery gate")
		}
		record, err := h.store.Get(ctx, id)
		if err != nil {
			return "", err
		}
		if record.Lifecycle == nil || record.Lifecycle.State != orchestration.RequestLifecycleRunning {
			return "", errors.New("intermediate record claims terminal request")
		}
		emit("lifecycle_live_sample", map[string]any{"request_id": id, "sample": i + 1, "activity_status": "synthesizing", "lifecycle": record.Lifecycle, "record_revision": record.RecordRevision})
	}
	return response, nil
}

func verifyLifecycleLive() (runErr error) {
	if os.Getenv("OPENROUTER_API_KEY") == "" {
		return errors.New("OpenRouter credential missing")
	}
	lock, err := os.OpenFile("/tmp/truvag3-lifecycle-live-v2.lock", os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return fmt.Errorf("one-run guard: %w", err)
	}
	_ = lock.Close()
	for k, v := range map[string]string{"OPENROUTER_BASE_URL": "https://openrouter.ai/api/v1", "TRUVAG3_OPENROUTER_MODEL_DEFAULT": "openai/gpt-6-luna", "TRUVAG3_OPENROUTER_MODEL_FAST": "openai/gpt-6-luna"} {
		if err := os.Setenv(k, v); err != nil {
			return err
		}
	}
	cfg := telemetry.UseProfile(telemetry.ProfileDevelopment)
	cfg.ServiceName = "lifecycle-live-diagnostic"
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
	ctx, span := telemetry.GetTelemetryProvider().StartSpan(ctx, "diagnostic.request_lifecycle")
	defer span.End()
	emit("lifecycle_run", map[string]any{"trace_id": trace.SpanContextFromContext(ctx).TraceID().String(), "max_paid_calls": 4, "openai_key_configured": os.Getenv("OPENAI_API_KEY") != ""})
	g := &guardedTransport{next: http.DefaultTransport}
	defer func() { emit("provider_attempts", map[string]any{"count": g.calls, "wire": g.wire}) }()
	logger := core.NewBaseAgent("lifecycle-live-diagnostic").Logger
	chain, err := diagnosticChain(g, logger, telemetry.GetTelemetryProvider())
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
	// Use real discovery in an isolated empty registry: the harness requires
	// discovery even for a terminal zero-step plan. This prevents the diagnostic
	// from discovering or dispatching unrelated production/example capabilities.
	discoveryKeys, err := core.NewRedisKeyspace(fmt.Sprintf("lifecycle-diagnostic-%d", time.Now().UnixNano()))
	if err != nil {
		return err
	}
	discovery, err := core.NewRedisDiscoveryWithClient(client, discoveryKeys, time.Minute)
	if err != nil {
		return err
	}
	keyspace, err := core.NewRedisKeyspace(os.Getenv("TRUVAG3_REDIS_NAMESPACE"))
	if err != nil {
		return err
	}
	activity, err := memory.NewRedisActivityCoordinator(client, "software-maintenance", memory.WithActivityCoordinatorKeyspace(keyspace))
	if err != nil {
		return err
	}
	debug, err := redisprovider.NewDefaultBackends(logger, redisprovider.WithDefaultBackendRoles(redisprovider.ClientRoleExecution, redisprovider.ClientRoleLLMDebug))
	if err != nil {
		return err
	}
	defer func() { _ = debug.Close() }()
	announcement, err := orchestration.NewActivityAnnouncementHook(activity, "lifecycle-live-diagnostic", "software-maintenance", 1, orchestration.WithAnnouncementSignalTTL(6*time.Second))
	if err != nil {
		return err
	}
	config := orchestration.NewDefaultOrchestratorConfig()
	config.Name = "lifecycle-live-diagnostic"
	config.PlanMaxTokens, config.SynthesisMaxTokens = 2000, 1000
	config.PlanParseRetryEnabled = false
	config.HallucinationRetryEnabled = false
	config.EnableTieredResolution = false
	config.IterativePlanning.Enabled = false
	config.CacheEnabled = false
	config.ExecutionOptions.TotalTimeout = 3 * time.Minute
	o, err := orchestration.CreateResolvedOrchestrator(config, orchestration.OrchestratorDependencies{Discovery: discovery, AIClient: chain, Logger: logger, Telemetry: telemetry.GetTelemetryProvider(), ActivityCoordinator: activity, PipelineHooks: []core.PipelineHook{announcement, &lifecycleObservationGate{activity: activity, store: debug.Backends().Execution()}}})
	if err != nil {
		return err
	}
	o.SetExecutionStore(debug.Backends().Execution())
	o.SetLLMDebugStore(debug.Backends().LLMDebug())
	defer func() {
		stopCtx, stop := context.WithTimeout(context.Background(), 15*time.Second)
		defer stop()
		runErr = errors.Join(runErr, o.Shutdown(stopCtx))
	}()
	response, execution, err := o.ProcessRequestWithExecution(ctx, "Labelled operator lifecycle diagnostic: what is two plus three? Answer in one sentence. No tools or external actions are needed.", nil)
	if err != nil {
		return err
	}
	if err := o.Shutdown(ctx); err != nil {
		return err
	}
	record, err := debug.Backends().Execution().Get(ctx, response.RequestID)
	if err != nil {
		return err
	}
	if record.Lifecycle == nil || record.Lifecycle.State != orchestration.RequestLifecycleCompleted || record.RecordRevision < 2 {
		return errors.New("final lifecycle missing")
	}
	if execution == nil || execution.Metadata[orchestration.MetadataKeyExecutionRequestID] != response.RequestID || record.Result == nil || record.Result.Metadata[orchestration.MetadataKeyExecutionRequestID] != response.RequestID {
		return errors.New("returned/stored terminal execution identity mismatch")
	}
	signals, err := activity.GetDomainActivities(ctx, "software-maintenance")
	if err != nil {
		return err
	}
	for _, s := range signals {
		if s.RequestID == response.RequestID {
			return errors.New("activity remained after completion")
		}
	}
	found := map[string]bool{}
	for _, hook := range record.PipelineHooks {
		for _, effect := range hook.Effects {
			found[effect.EffectID] = effect.Status == core.PipelineHookEffectSucceeded
		}
	}
	if !found["activity_signal_renewal"] || !found["activity_signal_cleanup"] {
		return errors.New("terminal renewal/cleanup evidence missing")
	}
	emit("lifecycle_verified", map[string]any{"request_id": response.RequestID, "lifecycle": record.Lifecycle, "record_revision": record.RecordRevision, "response": response.Response, "activity_removed": true, "renewal_and_cleanup_recorded": true, "terminal_correlation_verified": true})
	return nil
}
