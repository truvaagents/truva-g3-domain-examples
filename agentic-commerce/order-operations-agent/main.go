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
	"strings"
	"syscall"
	"time"

	"github.com/redis/go-redis/v9"
	"github.com/truvaagents/truva-g3/core"
	"github.com/truvaagents/truva-g3/orchestration"
	"github.com/truvaagents/truva-g3/telemetry"

	_ "github.com/truvaagents/truva-g3/ai/providers/anthropic"
	_ "github.com/truvaagents/truva-g3/ai/providers/openai"
)

func main() {
	os.Exit(run())
}

func run() int {
	if err := validateAgentConfig(); err != nil {
		log.Printf("configuration error: %v", err)
		return 1
	}
	core.SetCurrentComponentType(core.ComponentTypeAgent)
	initAgentTelemetry()
	defer func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = telemetry.Shutdown(ctx)
	}()
	redisOptions, err := redis.ParseURL(os.Getenv("REDIS_URL"))
	if err != nil {
		log.Printf("parse REDIS_URL: %v", err)
		return 1
	}
	redisClient := redis.NewClient(core.ApplyRedisClientDefaults(redisOptions))
	defer func() { _ = redisClient.Close() }()
	pingCtx, pingCancel := context.WithTimeout(context.Background(), 5*time.Second)
	if err := redisClient.Ping(pingCtx).Err(); err != nil {
		pingCancel()
		log.Printf("connect Redis: %v", err)
		return 1
	}
	pingCancel()

	agent, err := NewOrderOperationsAgent(os.Getenv("CONTROL_PROOF_HMAC_KEY"))
	if err != nil {
		log.Printf("create agent: %v", err)
		return 1
	}
	port := envInt("PORT", 8394)
	framework, err := core.NewFramework(agent,
		core.WithName("order-operations-agent"), core.WithPort(port), core.WithNamespace(os.Getenv("NAMESPACE")),
		core.WithRedisURL(os.Getenv("REDIS_URL")), core.WithDiscovery(true, "redis"), core.WithCORSDefaults(),
		core.WithDevelopmentMode(os.Getenv("DEV_MODE") == "true"),
		core.WithMiddleware(telemetry.TracingMiddlewareWithConfig("order-operations-agent", &telemetry.TracingMiddlewareConfig{ExcludedPaths: []string{"/health", "/ready", "/metrics", "/api/capabilities"}})),
	)
	if err != nil {
		agent.Logger.Error("Order Operations Agent framework creation failed", map[string]any{"operation": "framework_create", "status": "failure", "error": err.Error()})
		return 1
	}

	queueConfig := orchestration.DefaultRedisTaskQueueConfig()
	queueConfig.Logger = agent.Logger
	taskQueue := orchestration.NewRedisTaskQueue(redisClient, &queueConfig)
	storeConfig := orchestration.DefaultRedisTaskStoreConfig()
	storeConfig.Logger = agent.Logger
	taskStore := orchestration.NewRedisTaskStore(redisClient, &storeConfig)
	if err := agent.SetTaskStore(taskStore); err != nil {
		return logAgentFailure(agent, "configure_task_store", err)
	}
	workerConfig := orchestration.DefaultTaskWorkerConfig()
	workerConfig.WorkerCount = envInt("WORKER_COUNT", 2)
	workerConfig.DefaultTaskTimeout = 10 * time.Minute
	workerConfig.ShutdownTimeout = 30 * time.Second
	workerPool := orchestration.NewTaskWorkerPool(taskQueue, taskStore, &workerConfig)
	workerPool.SetLogger(agent.Logger)
	if err := workerPool.RegisterHandler("order_recovery", agent.HandleOrderRecovery); err != nil {
		return logAgentFailure(agent, "register_order_recovery_handler", err)
	}
	skillRegistry, skillClients, err := newSkillRegistry(agent.Logger)
	if err != nil {
		return logAgentFailure(agent, "create_skill_registry", err)
	}
	defer func() { _ = skillClients.Close() }()
	orchestratorConfig, err := agent.ResolveOrchestratorConfig(skillRegistry)
	if err != nil {
		return logAgentFailure(agent, "resolve_orchestrator_configuration", err)
	}

	receiver := &webhookReceiver{redis: redisClient, queue: taskQueue, store: taskStore, logger: agent.Logger, ready: agent.IsReady}
	agent.registerCapabilities(receiver)
	if err := agent.HandleFunc("/ready", agent.HandleReady); err != nil {
		return logAgentFailure(agent, "register_readiness_endpoint", err)
	}
	taskAPI := orchestration.NewTaskAPIHandler(taskQueue, taskStore, agent.Logger)
	if err := agent.HandleFunc("/api/v1/tasks", agent.RequireReady(taskAPI.HandleSubmit)); err != nil {
		return logAgentFailure(agent, "register_task_submit", err)
	}
	if err := agent.HandleFunc("/api/v1/tasks/", func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/cancel") && r.Method == http.MethodPost {
			taskAPI.HandleCancel(w, r)
			return
		}
		if r.Method == http.MethodGet {
			taskAPI.HandleGetTask(w, r)
			return
		}
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}); err != nil {
		return logAgentFailure(agent, "register_task_status", err)
	}

	hitlConfig := orchestratorConfig.HITL
	var hitl *HITLInfrastructure
	if hitlConfig.Enabled {
		hitl, err = SetupHITL(agent.Logger, hitlConfig, agent.HandleHITLExpiry)
		if err != nil {
			return logAgentFailure(agent, "setup_hitl", err)
		}
		defer hitl.Close()
		agent.SetHITL(hitl)
		handler := orchestration.NewHITLHandler(
			newHITLCommandProjectingController(
				hitl.Controller,
				hitl.CheckpointStore,
				agent.HandleHITLCommandProcessed,
			),
			hitl.CheckpointStore,
			orchestration.WithHITLHandlerLogger(agent.Logger),
		)
		if err := agent.HandleFunc("/hitl/checkpoints", handler.HandleListCheckpoints); err != nil {
			return logAgentFailure(agent, "register_hitl_list", err)
		}
		if err := agent.HandleFunc("/hitl/checkpoints/", handler.HandleGetCheckpoint); err != nil {
			return logAgentFailure(agent, "register_hitl_detail", err)
		}
		if err := agent.HandleFunc("/hitl/command", handler.HandleCommand); err != nil {
			return logAgentFailure(agent, "register_hitl_command", err)
		}
		if err := agent.HandleFunc("/hitl/resume/", agent.HandleHITLResume); err != nil {
			return logAgentFailure(agent, "register_hitl_resume", err)
		}
		if err := agent.HandleFunc("/internal/hitl-webhook", func(w http.ResponseWriter, r *http.Request) {
			if r.Method != http.MethodPost {
				http.Error(w, "use POST", http.StatusMethodNotAllowed)
				return
			}
			writeAgentJSON(w, http.StatusOK, map[string]string{"status": "received"})
		}); err != nil {
			return logAgentFailure(agent, "register_hitl_webhook", err)
		}
	}
	runtime := &orderOperationsRuntime{
		logger: agent.Logger,
		initialize: func(ctx context.Context) error {
			return agent.InitializeOrchestrator(ctx, agent.Discovery, hitl, orchestratorConfig)
		},
		worker: workerPool,
		stop:   agent.stopOrchestrator,
	}
	framework.RegisterRunnable(runtime)
	if hitl != nil && hitl.ExpiryProcessor != nil {
		framework.RegisterRunnable(hitl.ExpiryProcessor)
	}

	agent.Logger.Info("Order Operations Agent starting", map[string]any{"operation": "agent_startup", "status": "starting", "port": port, "workers": workerConfig.WorkerCount, "hitl_enabled": hitlConfig.Enabled})
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	if err := framework.Run(ctx); err != nil && !errors.Is(err, context.Canceled) {
		return logAgentFailure(agent, "framework_run", err)
	}
	return 0
}

func logAgentFailure(agent *OrderOperationsAgent, operation string, err error) int {
	agent.Logger.Error("Order Operations Agent stopped", map[string]any{
		"operation": operation,
		"status":    "failed",
		"error":     err.Error(),
	})
	return 1
}

func validateAgentConfig() error {
	redisURL := os.Getenv("REDIS_URL")
	if redisURL == "" || (!strings.HasPrefix(redisURL, "redis://") && !strings.HasPrefix(redisURL, "rediss://")) {
		return fmt.Errorf("REDIS_URL must use redis:// or rediss://")
	}
	if len(os.Getenv("CONTROL_PROOF_HMAC_KEY")) < 32 {
		return fmt.Errorf("CONTROL_PROOF_HMAC_KEY must contain at least 32 characters")
	}
	if raw := os.Getenv("PORT"); raw != "" {
		port, err := strconv.Atoi(raw)
		if err != nil || port < 1 || port > 65535 {
			return fmt.Errorf("PORT must be an integer between 1 and 65535")
		}
	}
	return nil
}

func envInt(name string, fallback int) int {
	value, err := strconv.Atoi(os.Getenv(name))
	if err != nil || value <= 0 {
		return fallback
	}
	return value
}

func initAgentTelemetry() {
	profile := telemetry.ProfileDevelopment
	if os.Getenv("APP_ENV") == "production" {
		profile = telemetry.ProfileProduction
	}
	config := telemetry.UseProfile(profile)
	config.ServiceName = "order-operations-agent"
	if endpoint := os.Getenv("OTEL_EXPORTER_OTLP_ENDPOINT"); endpoint != "" {
		config.Endpoint = endpoint
	}
	if err := telemetry.Initialize(config); err != nil {
		log.Printf("telemetry disabled: %v", err)
		return
	}
	telemetry.EnableFrameworkIntegration(nil)
}
