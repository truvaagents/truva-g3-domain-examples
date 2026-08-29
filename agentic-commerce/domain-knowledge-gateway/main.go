package main

import (
	"context"
	"errors"
	"fmt"
	"log"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/truvaagents/truva-g3-domain-examples/agentic-commerce/domain-knowledge-gateway/internal/knowledge"
	"github.com/truvaagents/truva-g3/core"
	"github.com/truvaagents/truva-g3/telemetry"
)

func main() {
	os.Exit(runGateway())
}

func runGateway() int {
	if err := validateConfig(); err != nil {
		log.Printf("configuration error: %v", err)
		return 1
	}
	tool, err := NewGatewayTool()
	if err != nil {
		log.Printf("create domain knowledge gateway: %v", err)
		return 1
	}
	initTelemetry(tool, "domain-knowledge-gateway")
	defer func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := telemetry.Shutdown(ctx); err != nil {
			tool.Logger.Warn("Knowledge-gateway telemetry shutdown failed", map[string]any{"operation": "telemetry_shutdown", "status": "failure", "error": err.Error()})
		}
	}()
	defer func() {
		if err := tool.Close(); err != nil {
			tool.Logger.Warn("Knowledge store shutdown failed", map[string]any{"operation": "knowledge_store_shutdown", "status": "failure", "error": err.Error()})
		}
	}()
	port := envPort(8396)
	framework, err := core.NewFramework(tool,
		core.WithName("domain-knowledge-gateway"), core.WithPort(port), core.WithNamespace(os.Getenv("NAMESPACE")),
		core.WithRedisURL(os.Getenv("REDIS_URL")), core.WithDiscovery(true, "redis"),
		core.WithCORSDefaults(), core.WithDevelopmentMode(os.Getenv("DEV_MODE") == "true"),
		core.WithMiddleware(telemetry.TracingMiddlewareWithConfig("domain-knowledge-gateway", &telemetry.TracingMiddlewareConfig{ExcludedPaths: []string{"/health", "/ready", "/metrics", "/api/capabilities"}})),
	)
	if err != nil {
		tool.Logger.Error("Knowledge-gateway framework creation failed", map[string]any{"operation": "framework_create", "status": "failure", "error": err.Error()})
		return 1
	}
	tool.Logger.Info("Domain Knowledge Gateway starting", map[string]any{"operation": "tool_startup", "status": "starting", "port": port, "store_provider": knowledge.StoreProviderName(), "capabilities": 4})
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	if err := framework.Run(ctx); err != nil && !errors.Is(err, context.Canceled) {
		return logGatewayFailure(tool, "framework_run", err)
	}
	return 0
}

func logGatewayFailure(tool *GatewayTool, operation string, err error) int {
	tool.Logger.Error("Domain Knowledge Gateway stopped", map[string]any{"operation": operation, "status": "failure", "error": err.Error()})
	return 1
}

func validateConfig() error {
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

func envPort(fallback int) int {
	if raw := os.Getenv("PORT"); raw != "" {
		if value, err := strconv.Atoi(raw); err == nil {
			return value
		}
	}
	return fallback
}

func initTelemetry(tool *GatewayTool, serviceName string) {
	profile := telemetry.ProfileDevelopment
	if os.Getenv("APP_ENV") == "production" {
		profile = telemetry.ProfileProduction
	}
	config := telemetry.UseProfile(profile)
	config.ServiceName = serviceName
	if endpoint := os.Getenv("OTEL_EXPORTER_OTLP_ENDPOINT"); endpoint != "" {
		config.Endpoint = endpoint
	}
	if err := telemetry.Initialize(config); err != nil {
		tool.Logger.Warn("Knowledge-gateway telemetry disabled", map[string]any{"operation": "telemetry_initialize", "status": "disabled", "error": err.Error()})
		return
	}
	telemetry.EnableFrameworkIntegration(nil)
}
