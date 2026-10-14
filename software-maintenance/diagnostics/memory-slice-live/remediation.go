package main

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/truvaagents/truva-g3/core"
	"github.com/truvaagents/truva-g3/orchestration"
	"github.com/truvaagents/truva-g3/orchestration/redisprovider"
	"github.com/truvaagents/truva-g3/telemetry"
	"go.opentelemetry.io/otel/trace"
)

// Operator-only verification, not a registered maintenance agent or workflow.
// The normal runtime discovers the deployed capability and dispatches actual
// reads. Fixture-model mode is explicitly not paid-model quality acceptance.
type remediationDiagnosticInput struct {
	References []map[string]interface{} `json:"references"`
}

type remediationReadGuard struct {
	service string
	inputs  []map[string]interface{}
}

func (g *remediationReadGuard) Name() string                 { return "diagnostic_read_only_authority" }
func (g *remediationReadGuard) RequireAfterPlanningSuccess() {}
func (g *remediationReadGuard) AfterPlanning(_ context.Context, _ *core.PipelineContext, value interface{}) (interface{}, error) {
	plan, ok := value.(*orchestration.RoutingPlan)
	if !ok || plan == nil {
		return nil, errors.New("diagnostic received an unknown plan type")
	}
	for _, step := range plan.Steps {
		if step.AgentName != g.service || step.Metadata["capability"] != "get_remediation_state" {
			return nil, errors.New("diagnostic authorizes only the discovered PR-state reader")
		}
		parameters, err := json.Marshal(step.Metadata["parameters"])
		if err != nil {
			return nil, err
		}
		var allowed bool
		for _, input := range g.inputs {
			candidate, err := json.Marshal(input)
			if err == nil && string(parameters) == string(candidate) {
				allowed = true
			}
		}
		if !allowed {
			return nil, errors.New("diagnostic plan changed the operator's exact read reference")
		}
	}
	return value, nil // Validate only; the diagnostic does not rewrite the plan.
}

type remediationDiagnosticAI struct {
	core.AIClient
	guard         *remediationReadGuard
	paid          bool
	planningSeen  bool
	synthesisSeen bool
}

func (client *remediationDiagnosticAI) GenerateResponse(ctx context.Context, prompt string, opts *core.AIOptions) (*core.AIResponse, error) {
	// Resource selection has its own contract. Normal planning/synthesis must
	// contain the published always-required, boundary-specific guidance.
	if strings.Contains(prompt, "Treat prior-case memory and publication receipts as lookup leads") {
		client.planningSeen = true
	}
	if strings.Contains(prompt, "Distinguish a current decision to link prior work") {
		client.synthesisSeen = true
	}
	if client.paid {
		return client.AIClient.GenerateResponse(ctx, prompt, opts)
	}
	content := `{"selected_resources":[]}`
	if strings.Contains(prompt, "Treat prior-case memory and publication receipts as lookup leads") {
		var steps []map[string]interface{}
		for index, input := range client.guard.inputs {
			steps = append(steps, map[string]interface{}{"step_id": fmt.Sprintf("step-%d", index+1), "agent_name": client.guard.service, "instruction": "Labelled fixture decision: read this operator-provided PR reference", "metadata": map[string]interface{}{"capability": "get_remediation_state", "parameters": input}})
		}
		encoded, err := json.Marshal(map[string]interface{}{"terminal": true, "steps": steps})
		if err != nil {
			return nil, err
		}
		content = string(encoded)
	} else if strings.Contains(prompt, "Distinguish a current decision to link prior work") {
		content = `{"status":"inconclusive","findings":[],"summary":"Labelled fixture synthesis; paid-model attribution is not exercised. Reads establish PR state only."}`
	}
	return &core.AIResponse{Content: content, Model: "labelled-local-fixture", Provider: "local-fixture"}, nil
}

func verifyRemediationStateLive(paid bool) (runErr error) {
	data, err := os.ReadFile("/tmp/v4-remediation-references.json")
	if err != nil {
		return err
	}
	var input remediationDiagnosticInput
	if len(data) > 16<<10 || json.Unmarshal(data, &input) != nil || len(input.References) != 2 {
		return errors.New("diagnostic requires two bounded operator read references")
	}
	lock, err := os.OpenFile("/tmp/truvag3-v4-remediation-"+fmt.Sprint(paid)+".lock", os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return err
	}
	if err := lock.Close(); err != nil {
		return err
	}
	cfg := telemetry.UseProfile(telemetry.ProfileDevelopment)
	cfg.ServiceName = "v4-remediation-state-diagnostic"
	if endpoint := os.Getenv("OTEL_EXPORTER_OTLP_ENDPOINT"); endpoint != "" {
		cfg.Endpoint = endpoint
	}
	if err := telemetry.Initialize(cfg); err != nil {
		return err
	}
	telemetry.EnableFrameworkIntegration(nil)
	defer func() {
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		runErr = errors.Join(runErr, telemetry.Shutdown(ctx))
	}()
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Minute)
	defer cancel()
	ctx, span := telemetry.GetTelemetryProvider().StartSpan(ctx, "diagnostic.remediation_state")
	defer span.End()
	logger := core.NewBaseAgent("v4-remediation-state-diagnostic").Logger
	connection, err := core.ResolveRedisConnectionConfig(nil, os.LookupEnv)
	if err != nil {
		return err
	}
	redis, err := core.NewRedisUniversalClient(connection)
	if err != nil {
		return err
	}
	defer func() { _ = redis.Close() }()
	keys, err := core.NewRedisKeyspace(os.Getenv("TRUVAG3_REDIS_NAMESPACE"))
	if err != nil {
		return err
	}
	discovery, err := core.NewRedisDiscoveryWithClient(redis, keys, 5*time.Minute)
	if err != nil {
		return err
	}
	services, err := discovery.FindByCapability(ctx, "get_remediation_state")
	if err != nil || len(services) != 1 {
		return fmt.Errorf("expected exactly one live PR-state reader: %d %v", len(services), err)
	}
	guard := &remediationReadGuard{service: services[0].Name, inputs: input.References}
	transport := &guardedTransport{next: http.DefaultTransport, limit: 4}
	client := &remediationDiagnosticAI{guard: guard, paid: paid}
	if paid {
		chain, err := diagnosticChain(transport, logger, telemetry.GetTelemetryProvider())
		if err != nil {
			return err
		}
		client.AIClient = chain
	}
	backends, err := redisprovider.NewDefaultBackends(logger, redisprovider.WithDefaultBackendRoles(redisprovider.ClientRoleExecution, redisprovider.ClientRoleLLMDebug, redisprovider.ClientRoleSkills))
	if err != nil {
		return err
	}
	defer func() { _ = backends.Close() }()
	config := orchestration.NewDefaultOrchestratorConfig()
	config.Name, config.RequestIDPrefix = "v4-remediation-state-diagnostic", "v4-remediation"
	config.EnableTieredResolution, config.CacheEnabled = false, false
	config.IterativePlanning.Enabled = false
	config.PlanParseRetryEnabled, config.PlanRefinementEnabled, config.HallucinationRetryEnabled = false, false, false
	config.ExecutionOptions.TotalTimeout = 7 * time.Minute
	config.PlanAIOptions = &orchestration.AIOptionsOverride{Model: orchestration.StringPtr("default"), MaxTokens: orchestration.IntPtr(5000)}
	config.SynthesisAIOptions = &orchestration.AIOptionsOverride{Model: orchestration.StringPtr("default"), MaxTokens: orchestration.IntPtr(3000), ResponseFormat: orchestration.StringPtr("json")}
	// Diagnostic-only read eligibility uses the public exclusion control. The
	// live catalog is still built by ordinary registry discovery, not a wrapper.
	all, err := discovery.Discover(ctx, core.DiscoveryFilter{})
	if err != nil {
		return err
	}
	for _, service := range all {
		for _, capability := range service.Capabilities {
			if capability.Name != "get_remediation_state" {
				config.ExcludedCapabilities = append(config.ExcludedCapabilities, capability.Name)
			}
		}
	}
	config.Skills = orchestration.SkillConfig{Enabled: true, Bindings: []orchestration.SkillBinding{
		{Namespace: "software-maintenance", Name: "remediation-coordination", Activation: orchestration.SkillActivationAlways, Required: true},
		{Namespace: "software-maintenance", Name: "coordinator-result-contract", Activation: orchestration.SkillActivationAlways, Required: true},
	}}
	config.SkillRegistry = backends.Backends().SkillRegistry()
	o, err := orchestration.CreateResolvedOrchestrator(config, orchestration.OrchestratorDependencies{Discovery: discovery, AIClient: client, Logger: logger, Telemetry: telemetry.GetTelemetryProvider(), PipelineHooks: []core.PipelineHook{guard}})
	if err != nil {
		return err
	}
	o.SetExecutionStore(backends.Backends().Execution())
	o.SetLLMDebugStore(backends.Backends().LLMDebug())
	defer func() { runErr = errors.Join(runErr, o.Shutdown(ctx)) }()
	if err := o.Start(ctx); err != nil {
		return err
	}
	token := os.Getenv("MAINTENANCE_PUBLICATION_COORDINATOR_TOKEN")
	if len(token) < 32 {
		return errors.New("diagnostic requires the existing publication reader credential")
	}
	ctx = orchestration.WithOAuthToken(ctx, token)
	objective := "Read-only operator diagnostic, not a maintenance case: freshly inspect both provided PR references using their authorized profile. The receipt reference is historical work from its original case, not work done for this new request. Report observed PR states and exact provenance, with observation times. Distinguish open/closed/merged from production resolution and defect applicability, which are not established by this read. Select only the read capability; no publication, case admission, merge, closure or deployment is authorized. References: " + string(data)
	response, execution, err := o.ProcessRequestWithExecution(ctx, objective, nil)
	if err != nil || response == nil || execution == nil {
		return fmt.Errorf("PR-state diagnostic failed: %v", err)
	}
	if !client.planningSeen || !client.synthesisSeen || len(execution.Steps) != 2 {
		return fmt.Errorf("diagnostic required guidance/read coverage: planning=%t synthesis=%t steps=%d (expected 2)", client.planningSeen, client.synthesisSeen, len(execution.Steps))
	}
	seen := make(map[string]bool)
	for _, step := range execution.Steps {
		if !step.Success || step.Capability != "get_remediation_state" {
			return errors.New("diagnostic did not complete both read operations")
		}
		parameters, err := json.Marshal(step.Parameters)
		if err != nil || seen[string(parameters)] {
			return errors.New("diagnostic repeated a read instead of covering both references")
		}
		seen[string(parameters)] = true
		var observation struct {
			Success bool `json:"success"`
			Data    struct {
				ObservedAt          time.Time `json:"observed_at"`
				State               string    `json:"state"`
				OriginCaseID        string    `json:"origin_case_id"`
				OriginPublicationID string    `json:"origin_publication_id"`
			} `json:"data"`
		}
		if json.Unmarshal([]byte(step.Response), &observation) != nil || !observation.Success || observation.Data.ObservedAt.IsZero() || observation.Data.State == "" {
			return errors.New("diagnostic read lacks a real timestamped observation")
		}
		if publication, ok := step.Parameters["publication_id"].(string); ok && (observation.Data.OriginPublicationID != publication || observation.Data.OriginCaseID != step.Parameters["origin_case_id"]) {
			return errors.New("historical publication lost its original provenance")
		}
	}
	// Successful reads do not establish the model's final claims. A separate
	// recorded assessment must review the exact final response before paid-model
	// attribution can pass. Fixture responses are ineligible for that acceptance.
	assessment := "not_exercised_fixture"
	if paid {
		assessment = "pending_recorded_review"
	}
	emit("remediation_reads_verified", map[string]interface{}{"request_id": response.RequestID, "trace_id": trace.SpanContextFromContext(ctx).TraceID().String(), "paid_calls": transport.calls, "wire": transport.wire, "planning_guidance_seen": client.planningSeen, "synthesis_guidance_seen": client.synthesisSeen, "execution": execution, "response": response, "response_sha256": fmt.Sprintf("%x", sha256.Sum256([]byte(response.Response))), "model_assessment": assessment, "model_judgment_verified": false, "no_gitHub_writes": true, "model_mode": map[bool]string{true: "paid OpenRouter", false: "labelled local fixture"}[paid]})
	return nil
}
