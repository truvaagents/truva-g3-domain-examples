package main

import "github.com/truvaagents/truva-g3/telemetry"

func init() {
	telemetry.DeclareMetrics("domain-knowledge-gateway", telemetry.ModuleConfig{
		Metrics: []telemetry.MetricDefinition{
			{Name: "domain_knowledge_gateway.requests", Type: "counter", Help: "Knowledge-gateway capability requests by operation and status", Labels: []string{"operation", "status"}},
			{Name: "domain_knowledge_gateway.request_duration_ms", Type: "histogram", Help: "Knowledge-gateway capability latency", Unit: "milliseconds", Labels: []string{"operation", "status"}, Buckets: []float64{1, 5, 10, 25, 50, 100, 250, 500, 1000, 5000}},
		},
	})
}
