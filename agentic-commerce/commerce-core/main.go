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

	"github.com/truvaagents/truva-g3/core"
	"github.com/truvaagents/truva-g3/telemetry"
)

func main() {
	os.Exit(runCommerce())
}

func runCommerce() int {
	if err := validateCommerceConfig(); err != nil {
		log.Printf("configuration error: %v", err)
		return 1
	}
	tool, err := NewCommerceTool()
	if err != nil {
		log.Printf("create commerce core: %v", err)
		return 1
	}
	initCommerceTelemetry(tool)
	defer func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := telemetry.Shutdown(ctx); err != nil {
			tool.Logger.Warn("Commerce Core telemetry shutdown failed", map[string]any{"operation": "telemetry_shutdown", "status": "failure", "error": err.Error()})
		}
	}()
	port := commercePort()
	framework, err := core.NewFramework(tool,
		core.WithName("commerce-core"), core.WithPort(port), core.WithNamespace(os.Getenv("NAMESPACE")), core.WithRedisURL(os.Getenv("REDIS_URL")), core.WithDiscovery(true, "redis"),
		core.WithCORSDefaults(), core.WithDevelopmentMode(os.Getenv("DEV_MODE") == "true"),
		core.WithMiddleware(telemetry.TracingMiddlewareWithConfig("commerce-core", &telemetry.TracingMiddlewareConfig{ExcludedPaths: []string{"/health", "/ready", "/metrics", "/api/capabilities"}})),
	)
	if err != nil {
		tool.Logger.Error("Commerce Core framework creation failed", map[string]any{"operation": "framework_create", "status": "failure", "error": err.Error()})
		return 1
	}
	tool.Logger.Info("Commerce Core starting", map[string]any{"operation": "tool_startup", "status": "starting", "port": port, "capabilities": 6, "data_mode": "synthetic"})
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	if err := framework.Run(ctx); err != nil && !errors.Is(err, context.Canceled) {
		return logCommerceFailure(tool, "framework_run", err)
	}
	return 0
}

func logCommerceFailure(tool *CommerceTool, operation string, err error) int {
	tool.Logger.Error("Commerce Core stopped", map[string]any{"operation": operation, "status": "failure", "error": err.Error()})
	return 1
}

func validateCommerceConfig() error {
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

func commercePort() int {
	if value, err := strconv.Atoi(os.Getenv("PORT")); err == nil && value > 0 {
		return value
	}
	return 8395
}

func initCommerceTelemetry(tool *CommerceTool) {
	profile := telemetry.ProfileDevelopment
	if os.Getenv("APP_ENV") == "production" {
		profile = telemetry.ProfileProduction
	}
	config := telemetry.UseProfile(profile)
	config.ServiceName = "commerce-core"
	if endpoint := os.Getenv("OTEL_EXPORTER_OTLP_ENDPOINT"); endpoint != "" {
		config.Endpoint = endpoint
	}
	if err := telemetry.Initialize(config); err != nil {
		tool.Logger.Warn("Commerce Core telemetry disabled", map[string]any{"operation": "telemetry_initialize", "status": "disabled", "error": err.Error()})
		return
	}
	telemetry.EnableFrameworkIntegration(nil)
}
