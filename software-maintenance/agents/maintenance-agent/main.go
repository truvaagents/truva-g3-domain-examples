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

	maintenanceconfig "github.com/truvaagents/truva-g3-domain-examples/software-maintenance/internal/config"
	"github.com/truvaagents/truva-g3/core"
	"github.com/truvaagents/truva-g3/memory"
	"github.com/truvaagents/truva-g3/orchestration"
	"github.com/truvaagents/truva-g3/orchestration/redisprovider"
	"github.com/truvaagents/truva-g3/telemetry"

	_ "github.com/truvaagents/truva-g3/ai/providers/anthropic"
	_ "github.com/truvaagents/truva-g3/ai/providers/gemini"
	_ "github.com/truvaagents/truva-g3/ai/providers/openai"
)

func main() {
	if err := runCoordinator(); err != nil {
		log.Fatalf("operation=component_exit error_type=component_failure: %v", err)
	}
}

func runCoordinator() error {
	investigationWorkerCount, err := maintenanceconfig.LoadInvestigationWorkers(os.LookupEnv)
	if err != nil {
		return fmt.Errorf("configuration error: %w", err)
	}
	core.SetCurrentComponentType(core.ComponentTypeAgent)
	initializeCoordinatorTelemetry(maintenanceAgentName)
	defer func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := telemetry.Shutdown(ctx); err != nil {
			log.Println("operation=telemetry_shutdown error_type=telemetry_shutdown: telemetry shutdown did not complete")
		}
	}()

	profile, err := loadCoordinatorProfile(os.Getenv("MAINTENANCE_AGENT_PROFILE_FILE"))
	if err != nil {
		return fmt.Errorf("configuration error: %w", err)
	}
	redisConnection, err := core.ResolveRedisConnectionConfig(nil, os.LookupEnv)
	if err != nil {
		return fmt.Errorf("redis configuration error: %w", err)
	}
	redisClient, err := core.NewRedisUniversalClient(redisConnection)
	if err != nil {
		return fmt.Errorf("create application Redis client: %w", err)
	}
	defer func() { _ = redisClient.Close() }()
	sharedKeyspace, err := core.NewRedisKeyspace(os.Getenv("TRUVAG3_REDIS_NAMESPACE"))
	if err != nil {
		return fmt.Errorf("redis namespace error: %w", err)
	}
	if profile.Collection.PrivateRedisDeployment == sharedKeyspace.Deployment() {
		return errors.New("private collection Redis deployment must differ from the shared discovery/task deployment")
	}
	cases, err := newCaseStore(redisClient, sharedKeyspace)
	if err != nil {
		return fmt.Errorf("create case store: %w", err)
	}

	bootstrapLogger := core.NewBaseAgent(maintenanceAgentName).Logger
	sharedBackends, err := redisprovider.NewDefaultBackends(bootstrapLogger,
		redisprovider.WithDefaultBackendRoles(
			redisprovider.ClientRoleExecution, redisprovider.ClientRoleLLMDebug,
			redisprovider.ClientRoleSkills, redisprovider.ClientRoleScheduling,
		),
		redisprovider.WithDefaultBackendProviderOptions(
			redisprovider.WithDeployment(sharedKeyspace.Deployment()),
			redisprovider.WithTaskQueueScope(maintenanceAgentName),
		),
	)
	if err != nil {
		return fmt.Errorf("create shared orchestration backends: %w", err)
	}
	defer func() { _ = sharedBackends.Close() }()
	sharedTaskRequirements, err := orchestration.RequirementsForFeatures(nil,
		orchestration.BackendFeatureTaskStorage, orchestration.BackendFeatureTaskQueue,
	)
	if err != nil {
		return fmt.Errorf("resolve shared task backend requirements: %w", err)
	}
	if err := sharedBackends.Backends().ValidateFor(sharedTaskRequirements); err != nil {
		return fmt.Errorf("validate shared task backends: %w", err)
	}
	privateBackends, err := redisprovider.NewDefaultBackends(bootstrapLogger,
		redisprovider.WithDefaultBackendRoles(redisprovider.ClientRoleScheduling),
		redisprovider.WithDefaultBackendProviderOptions(
			redisprovider.WithDeployment(profile.Collection.PrivateRedisDeployment),
			redisprovider.WithTaskQueueScope(maintenanceAgentName+"-collection"),
		),
	)
	if err != nil {
		return fmt.Errorf("create private collection backends: %w", err)
	}
	defer func() { _ = privateBackends.Close() }()
	privateRequirements, err := orchestration.RequirementsForFeatures(nil,
		orchestration.BackendFeatureSchedulerProducer,
		orchestration.BackendFeatureTaskStorage,
		orchestration.BackendFeatureTaskQueue,
	)
	if err != nil {
		return fmt.Errorf("resolve private collection backend requirements: %w", err)
	}
	if err := privateBackends.Backends().ValidateFor(privateRequirements); err != nil {
		return fmt.Errorf("validate private collection backends: %w", err)
	}

	agent, err := newMaintenanceAgent(profile, cases, sharedBackends.Backends().TaskQueue(), sharedBackends.Backends().Tasks())
	if err != nil {
		return fmt.Errorf("configuration error: %w", err)
	}
	port, err := coordinatorPort()
	if err != nil {
		return fmt.Errorf("configuration error: %w", err)
	}
	taskAPI := orchestration.NewTaskAPIHandler(sharedBackends.Backends().TaskQueue(), sharedBackends.Backends().Tasks(), agent.Logger)
	if err := agent.registerHTTPHandlers(taskAPI); err != nil {
		return fmt.Errorf("register coordinator HTTP handlers: %w", err)
	}
	framework, err := core.NewFramework(agent,
		core.WithName(maintenanceAgentName), core.WithPort(port), core.WithNamespace(os.Getenv("NAMESPACE")),
		core.WithRedisConnection(redisConnection), core.WithDiscovery(true, "redis"),
		core.WithDevelopmentMode(os.Getenv("DEV_MODE") == "true"), core.WithCORSDefaults(),
		core.WithMiddleware(telemetry.TracingMiddlewareWithConfig(maintenanceAgentName, &telemetry.TracingMiddlewareConfig{
			ExcludedPaths: []string{"/health", "/ready", "/live", "/metrics", "/api/capabilities", "/maintenance/health"},
			RequestFilter: func(request *http.Request) bool { return request.URL.Query().Get("poll") != "true" },
		})),
	)
	if err != nil {
		return fmt.Errorf("create framework: %w", err)
	}

	memoryBackends, err := memory.NewSharedBackends(redisClient, agent.Logger,
		memory.WithAgentName(maintenanceAgentName), memory.WithDomain("software-maintenance"),
		memory.WithRedisDeployment(sharedKeyspace.Deployment()), memory.WithKnowledgeDisabled(),
	)
	if err != nil {
		return fmt.Errorf("create required shared memory backends: %w", err)
	}
	defer memoryBackends.Close()
	memoryDeps := memoryBackends.ToDeps()
	if agent.limits.Diagnostic {
		coordinator, err := memory.NewAtomicLockCoordinator(
			memory.WithCoordinatorRedisClient(redisClient),
			memory.WithCoordinatorDomain("software-maintenance"),
			memory.WithCoordinatorKeyspace(sharedKeyspace),
			memory.WithCoordinatorLogger(agent.Logger),
			memory.WithInvestigationTTL(agent.limits.InvestigationClaim),
		)
		if err != nil {
			return fmt.Errorf("create diagnostic claim coordinator: %w", err)
		}
		memoryDeps.Coordinator = coordinator
	}
	memoryHooks, activityCoordinator, err := orchestration.BuildMemoryHooks(memoryDeps, agent.AI, agent.Logger,
		orchestration.WithBuilderRecordRequestEntityExtractor(orchestration.NoOpEntityExtractor{}),
	)
	if err != nil {
		return fmt.Errorf("configure memory hooks: %w", err)
	}
	if len(memoryHooks) == 0 {
		return errors.New("required cross-execution memory hooks could not be built")
	}

	dispatcher := &collectionDispatcher{
		queue: privateBackends.Backends().TaskQueue(), targetAgent: maintenanceAgentName,
		coordinatorProfile: profile.CoordinatorProfileID, coordinatorSHA256: profile.ProfileSHA256,
		serviceProfile: profile.ServiceProfileID,
	}
	scheduler, err := orchestration.NewScheduler(orchestration.SchedulerDeps{
		ScheduleStore: privateBackends.Backends().Schedules(), TaskDispatcher: dispatcher,
		TaskStore: privateBackends.Backends().Tasks(), Lock: privateBackends.Backends().Lock(), Logger: agent.Logger,
	})
	if err != nil {
		return fmt.Errorf("create collection scheduler: %w", err)
	}
	collectionWorkers := orchestration.NewTaskWorkerPool(
		privateBackends.Backends().TaskQueue(), privateBackends.Backends().Tasks(),
		&orchestration.TaskWorkerConfig{WorkerCount: 1, DequeueTimeout: 15 * time.Second, ShutdownTimeout: 30 * time.Second, DefaultTaskTimeout: 2 * time.Minute, Logger: agent.Logger},
	)
	if err := collectionWorkers.RegisterHandler(core.ScheduledTaskType, agent.handleCollectionTask); err != nil {
		return fmt.Errorf("register collection handler: %w", err)
	}
	workerTaskStore, err := newCancellationPreservingTaskStore(sharedBackends.Backends().Tasks())
	if err != nil {
		return fmt.Errorf("create cancellation-preserving task store: %w", err)
	}
	investigationWorkers := orchestration.NewTaskWorkerPool(
		sharedBackends.Backends().TaskQueue(), workerTaskStore,
		&orchestration.TaskWorkerConfig{WorkerCount: investigationWorkerCount, DequeueTimeout: 15 * time.Second, ShutdownTimeout: 60 * time.Second, DefaultTaskTimeout: agent.limits.Task, Logger: agent.Logger},
	)
	if err := investigationWorkers.RegisterHandler(investigationTaskType, agent.handleInvestigationTask); err != nil {
		return fmt.Errorf("register investigation handler: %w", err)
	}
	startupCtx, startupCancel := context.WithTimeout(context.Background(), 10*time.Second)
	if err := ensureCollectionSchedule(startupCtx, privateBackends.Backends().Schedules(), profile, agent.now()); err != nil {
		startupCancel()
		return fmt.Errorf("reconcile collection schedule: %w", err)
	}
	startupCancel()
	for _, runnable := range sharedBackends.Backends().Runnables() {
		framework.RegisterRunnable(runnable)
	}
	for _, runnable := range privateBackends.Backends().Runnables() {
		framework.RegisterRunnable(runnable)
	}
	framework.RegisterRunnable(scheduler)
	framework.RegisterRunnable(collectionWorkers)
	framework.RegisterRunnable(investigationWorkers)

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	agent.runtimeInit = func(ctx context.Context) error {
		if err := agent.InitializeOrchestrator(ctx, agent.Discovery, sharedBackends.Backends(), memoryHooks, activityCoordinator); err != nil {
			return fmt.Errorf("initialize orchestrator: %w", err)
		}
		agent.Logger.InfoWithContext(ctx, "Coordinator orchestrator initialized", map[string]interface{}{"operation": "orchestrator_initialize", "service": maintenanceAgentName})
		return nil
	}

	defer func() {
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		if err := agent.shutdownOrchestrator(shutdownCtx); err != nil {
			agent.Logger.Warn("Coordinator orchestrator shutdown failed", map[string]interface{}{"operation": "orchestrator_shutdown", "error": "orchestrator shutdown error", "error_type": "orchestrator_shutdown"})
		}
	}()
	agent.Logger.Info("Maintenance Agent starting", map[string]interface{}{
		"operation": "startup", "service": maintenanceAgentName, "port": port,
		"collection_enabled":          profile.Collection.Enabled,
		"collection_redis_deployment": profile.Collection.PrivateRedisDeployment,
		"investigation_workers":       investigationWorkerCount,
	})
	return framework.Run(ctx)
}

func coordinatorPort() (int, error) {
	port := 8398
	if value := os.Getenv("PORT"); value != "" {
		parsed, err := strconv.Atoi(value)
		if err != nil || parsed < 1 || parsed > 65535 {
			return 0, errors.New("PORT must be an integer from 1 through 65535")
		}
		port = parsed
	}
	return port, nil
}

func initializeCoordinatorTelemetry(serviceName string) {
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
func (agent *maintenanceAgent) Initialize(ctx context.Context) error {
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
