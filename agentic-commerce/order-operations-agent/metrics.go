package main

import "github.com/truvaagents/truva-g3/telemetry"

func init() {
	telemetry.DeclareMetrics("order-operations-agent", telemetry.ModuleConfig{
		Metrics: []telemetry.MetricDefinition{
			{Name: "order_operations.events_received", Type: "counter", Help: "Order-risk webhook events by validation status", Labels: []string{"status"}},
			{Name: "order_operations.events_deduplicated", Type: "counter", Help: "Duplicate order-risk events by outcome", Labels: []string{"outcome"}},
			{Name: "order_operations.events_enqueued", Type: "counter", Help: "Order-risk events queued for recovery", Labels: []string{"retry"}},
			{Name: "order_operations.recoveries", Type: "counter", Help: "Order recovery orchestrations by outcome", Labels: []string{"status"}},
			{Name: "order_operations.recovery_duration_ms", Type: "histogram", Help: "Order recovery orchestration duration", Labels: []string{"status"}, Unit: "milliseconds", Buckets: []float64{1000, 5000, 10000, 30000, 60000, 120000, 300000}},
			{Name: "order_operations.hitl_resume", Type: "counter", Help: "HITL resume attempts by outcome", Labels: []string{"status"}},
			{Name: "order_operations.hitl_resume_duration_ms", Type: "histogram", Help: "HITL resume duration", Labels: []string{"status"}, Unit: "milliseconds", Buckets: []float64{100, 500, 1000, 5000, 10000, 30000, 120000}},
			{Name: "order_operations.runtime_initialization", Type: "counter", Help: "Agent runtime initialization attempts by outcome", Labels: []string{"status"}},
			{Name: "order_operations.runtime_initialization_duration_ms", Type: "histogram", Help: "Agent runtime initialization duration", Labels: []string{"status"}, Unit: "milliseconds", Buckets: []float64{10, 50, 100, 500, 1000, 5000, 30000}},
		},
	})
}
