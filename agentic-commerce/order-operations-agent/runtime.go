package main

import (
	"context"
	"fmt"
	"time"

	"github.com/truvaagents/truva-g3/core"
	"github.com/truvaagents/truva-g3/telemetry"
)

// orderOperationsRuntime makes orchestrator initialization and task
// consumption one framework-owned lifecycle. Framework.Run initializes
// BaseAgent.Discovery before starting this Runnable, and the worker cannot
// dequeue a task until the orchestrator catalog is ready.
type orderOperationsRuntime struct {
	logger     core.Logger
	initialize func(context.Context) error
	worker     core.Runnable
	stop       func()
}

func (runtime *orderOperationsRuntime) Start(ctx context.Context) error {
	if runtime == nil || runtime.initialize == nil || runtime.worker == nil {
		return fmt.Errorf("order operations runtime is not fully configured")
	}
	start := time.Now()
	runtime.logger.InfoWithContext(ctx, "Initializing order operations runtime", map[string]any{
		"operation": "agent_runtime_initialization",
		"status":    "starting",
	})
	if err := runtime.initialize(ctx); err != nil {
		telemetry.Counter("order_operations.runtime_initialization", "status", "failed")
		telemetry.Duration("order_operations.runtime_initialization_duration_ms", start, "status", "failed")
		runtime.logger.ErrorWithContext(ctx, "Order operations runtime initialization failed", map[string]any{
			"operation":   "agent_runtime_initialization",
			"status":      "failed",
			"duration_ms": time.Since(start).Milliseconds(),
			"error":       err.Error(),
		})
		return fmt.Errorf("initialize order operations runtime: %w", err)
	}
	if runtime.stop != nil {
		defer runtime.stop()
	}
	telemetry.Counter("order_operations.runtime_initialization", "status", "ready")
	telemetry.Duration("order_operations.runtime_initialization_duration_ms", start, "status", "ready")
	runtime.logger.InfoWithContext(ctx, "Order operations runtime ready; starting workers", map[string]any{
		"operation":   "agent_runtime_initialization",
		"status":      "ready",
		"duration_ms": time.Since(start).Milliseconds(),
	})
	if err := runtime.worker.Start(ctx); err != nil {
		return fmt.Errorf("run order recovery workers: %w", err)
	}
	return nil
}

var _ core.Runnable = (*orderOperationsRuntime)(nil)
