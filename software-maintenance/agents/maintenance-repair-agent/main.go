package main

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"syscall"
	"time"

	"github.com/truvaagents/truva-g3-domain-examples/software-maintenance/internal/toolruntime"
	"github.com/truvaagents/truva-g3/core"
	"github.com/truvaagents/truva-g3/orchestration/redisprovider"
	"github.com/truvaagents/truva-g3/telemetry"

	_ "github.com/truvaagents/truva-g3/ai/providers/anthropic"
	_ "github.com/truvaagents/truva-g3/ai/providers/gemini"
	_ "github.com/truvaagents/truva-g3/ai/providers/openai"
)

func main() {
	if err := runRepair(); err != nil {
		log.Fatalf("operation=component_exit error_type=component_failure: %v", err)
	}
}

func runRepair() error {
	core.SetCurrentComponentType(core.ComponentTypeAgent)
	initializeRepairTelemetry(repairAgentName)
	defer func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := telemetry.Shutdown(ctx); err != nil {
			log.Println("operation=telemetry_shutdown error_type=telemetry_shutdown: telemetry shutdown did not complete")
		}
	}()

	agent, err := newMaintenanceRepairAgentFromEnvironment()
	if err != nil {
		return fmt.Errorf("configuration error: %w", err)
	}
	redisConnection, err := core.ResolveRedisConnectionConfig(nil, os.LookupEnv)
	if err != nil {
		return fmt.Errorf("redis configuration error: %w", err)
	}
	port, err := repairPort()
	if err != nil {
		return fmt.Errorf("configuration error: %w", err)
	}
	framework, err := core.NewFramework(agent,
		core.WithName(repairAgentName), core.WithPort(port), core.WithNamespace(os.Getenv("NAMESPACE")),
		core.WithRedisConnection(redisConnection), core.WithDiscovery(true, "redis"),
		core.WithDevelopmentMode(os.Getenv("DEV_MODE") == "true"),
		core.WithMiddleware(telemetry.TracingMiddlewareWithConfig(repairAgentName, &telemetry.TracingMiddlewareConfig{
			ExcludedPaths: []string{"/health", "/ready", "/live", "/metrics", "/api/capabilities", "/repair/health"},
			RequestFilter: func(request *http.Request) bool { return request.URL.Query().Get("poll") != "true" },
		})),
	)
	if err != nil {
		return fmt.Errorf("create framework: %w", err)
	}
	backends, err := redisprovider.NewDefaultBackends(agent.Logger,
		redisprovider.WithDefaultBackendRoles(redisprovider.ClientRoleExecution, redisprovider.ClientRoleLLMDebug, redisprovider.ClientRoleSkills),
	)
	if err != nil {
		return fmt.Errorf("create orchestration backends: %w", err)
	}
	defer func() { _ = backends.Close() }()
	for _, runnable := range backends.Backends().Runnables() {
		framework.RegisterRunnable(runnable)
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	agent.runtimeInit = func(ctx context.Context) error {
		if err := agent.InitializeOrchestrator(ctx, agent.Discovery, backends.Backends()); err != nil {
			return fmt.Errorf("initialize orchestrator: %w", err)
		}
		agent.Logger.InfoWithContext(ctx, "Repair orchestrator initialized", map[string]interface{}{"operation": "orchestrator_initialize", "service": repairAgentName})
		return nil
	}

	defer func() {
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		if err := agent.shutdownOrchestrator(shutdownCtx); err != nil {
			agent.Logger.Warn("Repair orchestrator shutdown failed", map[string]interface{}{"operation": "orchestrator_shutdown", "error": "orchestrator shutdown error", "error_type": "orchestrator_shutdown"})
		}
	}()
	agent.Logger.Info("Maintenance Repair Agent starting", map[string]interface{}{"operation": "startup", "service": repairAgentName, "port": port})
	return toolruntime.Run(ctx, framework.Run, agent.Stop, agent.Config.HTTP.ShutdownTimeout)
}

func repairPort() (int, error) {
	port := 8400
	if value := os.Getenv("PORT"); value != "" {
		parsed, err := strconv.Atoi(value)
		if err != nil || parsed < 1 || parsed > 65535 {
			return 0, errors.New("PORT must be an integer from 1 through 65535")
		}
		port = parsed
	}
	return port, nil
}

func initializeRepairTelemetry(serviceName string) {
	profile := telemetry.ProfileDevelopment
	switch os.Getenv("APP_ENV") {
	case "production", "prod":
		profile = telemetry.ProfileProduction
	case "staging", "stage", "qa":
		profile = telemetry.ProfileStaging
	}
	config := telemetry.UseProfile(profile)
	config.ServiceName = serviceName
	if endpoint := os.Getenv("OTEL_EXPORTER_OTLP_ENDPOINT"); endpoint != "" {
		config.Endpoint = endpoint
	}
	if err := telemetry.Initialize(config); err != nil {
		fmt.Fprintln(os.Stderr, "operation=telemetry_initialize error_type=export_unavailable: telemetry initialization failed; continuing without export")
		return
	}
	telemetry.EnableFrameworkIntegration(nil)
}

// Initialize runs after framework configuration and before serving or starting
// workers. Discovery is ready here; no polling goroutine races shutdown.
func (agent *maintenanceRepairAgent) Initialize(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := agent.BaseAgent.Initialize(ctx); err != nil {
		return err
	}
	if agent.runtimeInit != nil {
		return agent.runtimeInit(ctx)
	}
	return nil
}
