package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/truvaagents/truva-g3/core"
	"github.com/truvaagents/truva-g3/memory"
	"github.com/truvaagents/truva-g3/orchestration"
	"github.com/truvaagents/truva-g3/orchestration/redisprovider"
	"github.com/truvaagents/truva-g3/telemetry"
	"go.opentelemetry.io/otel/trace"
)

type leaseDiagnosticEnd struct{}

func (leaseDiagnosticEnd) Name() string { return "labelled-lease-diagnostic" }
func (leaseDiagnosticEnd) BeforePlanning(ctx context.Context, _ *core.PipelineContext) (*core.PipelineShortCircuit, error) {
	emit("no_ai_activity_window", map[string]any{"request_id": orchestration.GetRequestID(ctx), "seconds": 30, "labelled_diagnostic": true})
	select {
	case <-time.After(30 * time.Second):
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	return &core.PipelineShortCircuit{Response: "Labelled lifecycle diagnostic completed without AI or tools", Source: "operator diagnostic"}, nil
}

func verifyLeases() (runErr error) {
	cfg := telemetry.UseProfile(telemetry.ProfileDevelopment)
	cfg.ServiceName = "investigation-lifecycle-diagnostic"
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
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	ctx, span := telemetry.GetTelemetryProvider().StartSpan(ctx, "diagnostic.investigation_lifecycle")
	defer span.End()
	runID := fmt.Sprintf("lease-check-%d", time.Now().UnixNano())
	emit("lease_run", map[string]any{"run": runID, "trace_id": trace.SpanContextFromContext(ctx).TraceID().String(), "paid_calls": 0})
	connection, err := core.ResolveRedisConnectionConfig(nil, os.LookupEnv)
	if err != nil {
		return err
	}
	client, err := core.NewRedisUniversalClient(connection)
	if err != nil {
		return err
	}
	defer func() { _ = client.Close() }()
	keyspace, err := core.NewRedisKeyspace(runID)
	if err != nil {
		return err
	}
	coordinator, err := memory.NewAtomicLockCoordinator(memory.WithCoordinatorRedisClient(client), memory.WithCoordinatorKeyspace(keyspace), memory.WithCoordinatorDomain("documents"))
	if err != nil {
		return err
	}
	request := core.InvestigationLeaseRequest{AgentName: "diagnostic-agent", EntityID: "synthetic-document", AcquisitionID: "first-segment", TTL: 20 * time.Millisecond}
	old, _, err := coordinator.AcquireInvestigation(ctx, request)
	if err != nil || old == nil {
		return fmt.Errorf("initial lease: %w", err)
	}
	select {
	case <-time.After(40 * time.Millisecond):
	case <-ctx.Done():
		return ctx.Err()
	}
	request.AcquisitionID, request.TTL = "next-segment", time.Minute
	newer, _, err := coordinator.AcquireInvestigation(ctx, request)
	if err != nil || newer == nil {
		return fmt.Errorf("reacquisition: %w", err)
	}
	if outcome, err := coordinator.ReleaseInvestigationLease(ctx, *old); err != nil || outcome != core.InvestigationNotOwned {
		return fmt.Errorf("old cleanup was not rejected: %s %v", outcome, err)
	}
	if err := coordinator.ReleaseInvestigation(ctx, request.AgentName, request.EntityID); err != nil {
		return err
	}
	active, err := coordinator.GetActiveInvestigations(ctx)
	if err != nil || active[request.EntityID] != request.AgentName {
		return errors.New("legacy release removed current ownership")
	}
	if outcome, err := coordinator.ReleaseInvestigationLease(ctx, *newer); err != nil || outcome != core.InvestigationReleased {
		return fmt.Errorf("exact release: %s %v", outcome, err)
	}
	logger := core.NewBaseAgent("investigation-lifecycle-diagnostic").Logger
	backends, err := memory.NewSharedBackends(client, logger, memory.WithDomain("documents"), memory.WithRedisDeployment(runID), memory.WithKnowledgeDisabled())
	if err != nil {
		return err
	}
	defer backends.Close()
	hook, err := orchestration.NewMemoryEnrichmentHook(backends.ToDeps().Episodic, coordinator, "diagnostic-agent", "documents")
	if err != nil {
		return err
	}
	debug, err := redisprovider.NewDefaultBackends(logger, redisprovider.WithDefaultBackendRoles(redisprovider.ClientRoleExecution))
	if err != nil {
		return err
	}
	defer func() { _ = debug.Close() }()
	activityKeys, err := core.NewRedisKeyspace(os.Getenv("TRUVAG3_REDIS_NAMESPACE"))
	if err != nil {
		return err
	}
	activity, err := memory.NewRedisActivityCoordinator(client, "software-maintenance", memory.WithActivityCoordinatorKeyspace(activityKeys))
	if err != nil {
		return err
	}
	announcement, err := orchestration.NewActivityAnnouncementHook(activity, "investigation-lifecycle-diagnostic", "software-maintenance", 1, orchestration.WithAnnouncementSignalTTL(6*time.Second))
	if err != nil {
		return err
	}
	config := orchestration.NewDefaultOrchestratorConfig()
	config.Name = "investigation-lifecycle-diagnostic"
	o, err := orchestration.CreateResolvedOrchestrator(config, orchestration.OrchestratorDependencies{Logger: logger, Telemetry: telemetry.GetTelemetryProvider(), PipelineHooks: []core.PipelineHook{announcement, hook, leaseDiagnosticEnd{}}})
	if err != nil {
		return err
	}
	defer func() {
		stopCtx, stop := context.WithTimeout(context.Background(), 10*time.Second)
		defer stop()
		runErr = errors.Join(runErr, o.Shutdown(stopCtx))
	}()
	o.SetExecutionStore(debug.Backends().Execution())
	response, err := o.ProcessRequest(ctx, "Labelled synthetic investigation cleanup check", map[string]interface{}{"entity_type": "document", "entity_id": "synthetic-document"})
	if err != nil {
		return err
	}
	if err := o.Shutdown(ctx); err != nil {
		return err
	}
	active, err = coordinator.GetActiveInvestigations(ctx)
	if err != nil || len(active) != 0 {
		return errors.New("managed request left investigation active")
	}
	record, err := debug.Backends().Execution().Get(ctx, response.RequestID)
	if err != nil {
		return err
	}
	found := false
	for _, hook := range record.PipelineHooks {
		for _, effect := range hook.Effects {
			if strings.HasPrefix(effect.EffectID, "investigation_lease_cleanup:") {
				found = effect.Status == core.PipelineHookEffectSucceeded && strings.Contains(string(effect.Data), `"outcome":"released"`)
			}
		}
	}
	if !found {
		return errors.New("terminal debug record lacks observed cleanup")
	}
	signals, err := activity.GetDomainActivities(ctx, "software-maintenance")
	if err != nil {
		return err
	}
	for _, signal := range signals {
		if signal.RequestID == response.RequestID {
			return errors.New("diagnostic activity remained after completion")
		}
	}
	emit("lease_verified", map[string]any{"request_id": response.RequestID, "stale_release_rejected": true, "legacy_release_safe": true, "cleanup_observed": true, "active_count": len(active), "paid_calls": 0})
	return nil
}
