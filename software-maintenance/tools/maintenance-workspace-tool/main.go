package main

import (
	"context"
	"errors"
	"fmt"
	"log"
	"os"
	"os/signal"
	"strconv"
	"syscall"
	"time"

	"github.com/truvaagents/truva-g3/core"
	"github.com/truvaagents/truva-g3/telemetry"
)

func main() {
	if err := runWorkspace(); err != nil {
		log.Fatalf("operation=component_exit error_type=component_failure: %v", err)
	}
}

func runWorkspace() error {
	core.SetCurrentComponentType(core.ComponentTypeTool)
	initializeWorkspaceTelemetry(workspaceToolName)
	defer func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := telemetry.Shutdown(ctx); err != nil {
			log.Println("operation=telemetry_shutdown error_type=telemetry_shutdown: telemetry shutdown did not complete")
		}
	}()

	tool, err := newMaintenanceWorkspaceToolFromEnvironment()
	if err != nil {
		return fmt.Errorf("configuration error: %w", err)
	}
	defer func() { _ = tool.redisClient.Close() }()
	port, err := workspacePort()
	if err != nil {
		return fmt.Errorf("configuration error: %w", err)
	}
	framework, err := core.NewFramework(tool,
		core.WithName(workspaceToolName), core.WithPort(port), core.WithNamespace(os.Getenv("NAMESPACE")),
		core.WithDiscovery(true, "redis"), core.WithDevelopmentMode(os.Getenv("DEV_MODE") == "true"),
		core.WithMiddleware(telemetry.TracingMiddlewareWithConfig(workspaceToolName, &telemetry.TracingMiddlewareConfig{ExcludedPaths: []string{"/health", "/ready", "/metrics"}})),
	)
	if err != nil {
		return fmt.Errorf("create framework: %w", err)
	}
	// Reconciliation can make three bounded Kubernetes reads plus store writes.
	// Its 90s delivery margin is separate from the optional polling window.
	if tool.maxJobWait > 0 && tool.Config.HTTP.WriteTimeout > 0 && tool.Config.HTTP.WriteTimeout <= tool.maxJobWait+90*time.Second {
		return errors.New("HTTP write timeout must exceed maximum Job wait plus 90s reconciliation/response margin")
	}
	tool.Logger.Info("Maintenance Workspace Tool starting", map[string]interface{}{"operation": "startup", "service": workspaceToolName, "port": port, "profile_count": len(tool.profiles.profiles)})
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	return framework.Run(ctx)
}

func workspacePort() (int, error) {
	port := 8401
	if value := os.Getenv("PORT"); value != "" {
		parsed, err := strconv.Atoi(value)
		if err != nil || parsed < 1 || parsed > 65535 {
			return 0, errors.New("PORT must be an integer from 1 through 65535")
		}
		port = parsed
	}
	return port, nil
}

func initializeWorkspaceTelemetry(serviceName string) {
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
