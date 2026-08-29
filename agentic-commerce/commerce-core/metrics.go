package main

import "github.com/truvaagents/truva-g3/telemetry"

func init() {
	telemetry.DeclareMetrics("commerce-core", telemetry.ModuleConfig{
		Metrics: []telemetry.MetricDefinition{
			{Name: "commerce_core.requests", Type: "counter", Help: "Commerce Core capability requests by operation and status", Labels: []string{"operation", "status"}},
			{Name: "commerce_core.request_duration_ms", Type: "histogram", Help: "Commerce Core capability latency", Unit: "milliseconds", Labels: []string{"operation", "status"}, Buckets: []float64{1, 5, 10, 25, 50, 100, 250, 500, 1000}},
		},
	})
}
