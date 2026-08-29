package main

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/truvaagents/truva-g3/ai"
	"github.com/truvaagents/truva-g3/core"
	"github.com/truvaagents/truva-g3/orchestration"
	"github.com/truvaagents/truva-g3/telemetry"
)

type OrderOperationsAgent struct {
	*core.BaseAgent
	orchestrator *orchestration.AIOrchestrator
	hitl         *HITLInfrastructure
	taskStore    core.TaskStore
	controlKey   []byte
	planStates   *governedPlanStateStore
	mu           sync.RWMutex
}

func NewOrderOperationsAgent(controlKey string) (*OrderOperationsAgent, error) {
	if len(controlKey) < 32 {
		return nil, fmt.Errorf("CONTROL_PROOF_HMAC_KEY must contain at least 32 characters")
	}
	base := core.NewBaseAgent("order-operations-agent")
	chain, err := ai.NewChainClient(
		ai.WithChainTelemetry(telemetry.GetTelemetryProvider()),
		ai.WithChainLogger(base.Logger),
		ai.WithChainTimeout(240*time.Second),
	)
	if err != nil {
		client, singleErr := ai.NewClient(ai.WithTimeout(240 * time.Second))
		if singleErr != nil {
			return nil, fmt.Errorf("create AI client: chain=%v single=%w", err, singleErr)
		}
		base.AI = client
	} else {
		base.AI = chain
	}
	return &OrderOperationsAgent{BaseAgent: base, controlKey: []byte(controlKey), planStates: newGovernedPlanStateStore()}, nil
}

func (a *OrderOperationsAgent) SetTaskStore(store core.TaskStore) error {
	if store == nil {
		return fmt.Errorf("task store is required")
	}
	a.mu.Lock()
	a.taskStore = store
	a.mu.Unlock()
	return nil
}

func (a *OrderOperationsAgent) getTaskStore() core.TaskStore {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.taskStore
}

func (a *OrderOperationsAgent) ResolveOrchestratorConfig(skills orchestration.SkillRegistry) (*orchestration.OrchestratorConfig, error) {
	if skills == nil {
		return nil, fmt.Errorf("skill registry is required")
	}
	base := orchestration.NewDefaultOrchestratorConfig()
	base.RoutingMode = orchestration.ModeAutonomous
	base.SynthesisStrategy = orchestration.StrategyLLM
	base.MetricsEnabled = true
	base.EnableTelemetry = true
	base.RequestIDPrefix = "order-operations"
	base.PromptConfig = orchestration.PromptConfig{
		Domain: "order-operations",
		SystemInstructions: `<identity>
You are the governed order-operations coordinator for a capability demo. You operate only on synthetic orders through discovered tools.
</identity>
<control_contract>
1. Read current order context and bounded fulfillment options before retrieving policy evidence.
2. Search evidence using the order's tenant, jurisdiction, channel, and one event decision time. Use that identical scope and decision time for every exact decisive-section read; retain each signed evidence_receipt and never cite a section you did not read exactly.
3. Ask commerce-core to evaluate exactly one option using the release identity, decision_time, paired exact section IDs, and signed evidence receipts, even when scoped order facts appear incomplete. Its outcome, eligible, approval_required, and reasons fields are authoritative; never override or reinterpret them.
4. Do not execute an ineligible option. Execute only with the returned decision_token and a stable order-scoped idempotency_key.
5. Treat execute_recovery_command as sensitive and respect HITL interruption. Never invent approval_reference or approval_proof; the agent runtime injects them only during an approved resume. In one resumable plan, verify with a direct dependency on execution, then notify with direct dependencies on both execution and verification. Every templated output reference requires its source step as a direct dependency. Notify only after verified=true.
6. For denied, insufficient_evidence, or conflicting_evidence outcomes, do not mutate and report that exact terminal classification. If decisive evidence is stale or outside scope, abstain and explain what is missing.
7. In the final response separate observed order state, cited evidence, deterministic decision, approval state, command outcome, verification, and notification.
</control_contract>`,
	}
	resolved, err := orchestration.ResolveOrchestratorConfig(orchestration.ConfigResolution{
		Base:        base,
		Environment: orchestration.EnvironmentStrict,
		Options: []orchestration.OrchestratorOption{
			orchestration.WithSkills(orchestration.SkillConfig{Enabled: true, Bindings: []orchestration.SkillBinding{{Namespace: "order-operations", Name: "evidence-grounded-recovery", Version: "published", Activation: orchestration.SkillActivationAlways, Required: true}}}),
			orchestration.WithSkillRegistry(skills),
		},
	})
	if err != nil {
		return nil, fmt.Errorf("resolve orchestrator config: %w", err)
	}
	return resolved.Config, nil
}

func (a *OrderOperationsAgent) InitializeOrchestrator(ctx context.Context, discovery core.Discovery, hitl *HITLInfrastructure, config *orchestration.OrchestratorConfig) error {
	if discovery == nil {
		return fmt.Errorf("discovery is not available")
	}
	if config == nil {
		return fmt.Errorf("orchestrator config is required")
	}
	deps := orchestration.OrchestratorDependencies{
		Discovery: discovery, AIClient: a.AI, Logger: a.Logger,
		Telemetry: telemetry.GetTelemetryProvider(), EnableErrorAnalyzer: true,
		PipelineHooks: []core.PipelineHook{&governedOrderPlanHook{states: a.planStates}, &governedOrderResponseHook{}},
	}
	// ResolveOrchestratorConfig has already performed the single strict merge.
	// The compatibility constructor is intentional here because it also wires
	// the framework's environment-enabled execution and LLM debug stores.
	orch, err := orchestration.CreateOrchestrator(config, deps)
	if err != nil {
		return fmt.Errorf("create orchestrator: %w", err)
	}
	if hitl != nil {
		orch.SetInterruptController(hitl.Controller)
	}
	if err := orch.Start(ctx); err != nil {
		return fmt.Errorf("start orchestrator: %w", err)
	}
	a.mu.Lock()
	a.orchestrator = orch
	a.hitl = hitl
	a.mu.Unlock()
	return nil
}

func (a *OrderOperationsAgent) SetHITL(hitl *HITLInfrastructure) {
	a.mu.Lock()
	a.hitl = hitl
	a.mu.Unlock()
}

func (a *OrderOperationsAgent) getHITL() *HITLInfrastructure {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.hitl
}

func (a *OrderOperationsAgent) IsReady() bool {
	return a.getOrchestrator() != nil
}

func (a *OrderOperationsAgent) getOrchestrator() *orchestration.AIOrchestrator {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.orchestrator
}

func (a *OrderOperationsAgent) stopOrchestrator() {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.orchestrator != nil {
		a.orchestrator.Stop()
		a.orchestrator = nil
	}
}
