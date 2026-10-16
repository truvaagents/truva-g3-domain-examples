package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"os/signal"
	"strconv"
	"syscall"
	"time"

	"github.com/truvaagents/truva-g3-domain-examples/software-maintenance/internal/serviceevidence"
	"github.com/truvaagents/truva-g3/core"
	"github.com/truvaagents/truva-g3/telemetry"
)

const toolName = serviceevidence.ToolName

func main() {
	if err := runEvidence(); err != nil {
		log.Fatalf("operation=component_exit error_type=component_failure: %v", err)
	}
}

func runEvidence() error {
	core.SetCurrentComponentType(core.ComponentTypeTool)
	initializeTelemetry(toolName)
	defer func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := telemetry.Shutdown(ctx); err != nil {
			log.Println("operation=telemetry_shutdown error_type=telemetry_shutdown: telemetry shutdown did not complete")
		}
	}()
	if _, err := core.ResolveRedisConnectionConfig(nil, os.LookupEnv); err != nil {
		return fmt.Errorf("configuration error: %w", err)
	}
	port, err := configuredPort()
	if err != nil {
		return fmt.Errorf("configuration error: %w", err)
	}
	tool, err := serviceevidence.NewFromEnvironment()
	if err != nil {
		return fmt.Errorf("configuration error: %w", err)
	}
	defer tool.Close()

	framework, err := core.NewFramework(tool,
		core.WithName(toolName),
		core.WithPort(port),
		core.WithNamespace(os.Getenv("NAMESPACE")),
		core.WithDiscovery(true, "redis"),
		core.WithDevelopmentMode(os.Getenv("DEV_MODE") == "true"),
		core.WithMiddleware(telemetry.TracingMiddlewareWithConfig(toolName, &telemetry.TracingMiddlewareConfig{
			ExcludedPaths: []string{"/health", "/ready", "/metrics"},
		})),
	)
	if err != nil {
		return fmt.Errorf("create framework: %w", err)
	}

	tool.Logger.Info("Maintenance Evidence Tool starting", map[string]interface{}{
		"operation": "startup", "service": toolName, "port": port, "profile_count": tool.ProfileCount(),
	})
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	return framework.Run(ctx)
}

func configuredPort() (int, error) {
	port := 8397
	if value := os.Getenv("PORT"); value != "" {
		parsed, err := strconv.Atoi(value)
		if err != nil || parsed < 1 || parsed > 65535 {
			return 0, fmt.Errorf("PORT must be an integer from 1 through 65535")
		}
		port = parsed
	}
	return port, nil
}

func initializeTelemetry(serviceName string) {
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
