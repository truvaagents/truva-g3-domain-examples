package main

import (
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/truvaagents/truva-g3/core"
	"github.com/truvaagents/truva-g3/orchestration"
)

type HITLInfrastructure struct {
	CheckpointStore *orchestration.RedisCheckpointStore
	CommandStore    *orchestration.RedisCommandStore
	Controller      *orchestration.DefaultInterruptController
	ExpiryProcessor core.Runnable
}

func SetupHITL(logger core.Logger, config orchestration.HITLConfig, onExpiry orchestration.ExpiryCallback) (*HITLInfrastructure, error) {
	redisURL := os.Getenv("REDIS_URL")
	checkpointStore, err := orchestration.NewRedisCheckpointStore(orchestration.WithCheckpointRedisURL(redisURL), orchestration.WithCheckpointStoreLogger(logger))
	if err != nil {
		return nil, fmt.Errorf("checkpoint store: %w", err)
	}
	commandStore, err := orchestration.NewRedisCommandStore(orchestration.WithCommandStoreRedisURL(redisURL), orchestration.WithCommandStoreLogger(logger))
	if err != nil {
		_ = checkpointStore.Close()
		return nil, fmt.Errorf("command store: %w", err)
	}
	webhookURL := os.Getenv("TRUVAG3_HITL_WEBHOOK_URL")
	if webhookURL == "" {
		webhookURL = fmt.Sprintf("http://localhost:%s/internal/hitl-webhook", os.Getenv("PORT"))
	}
	handler := orchestration.NewWebhookInterruptHandler(webhookURL, commandStore, orchestration.WithHandlerLogger(logger))
	policy := newDecisionAwarePolicy(config, logger)
	controller := orchestration.NewInterruptController(policy, checkpointStore, handler, orchestration.WithControllerLogger(logger), orchestration.WithControllerCommandStore(commandStore))
	expiryConfig := orchestration.ExpiryProcessorConfigFromEnv()
	processor, err := orchestration.NewCheckpointExpiryProcessor(checkpointStore, checkpointStore, func(ctx context.Context, checkpoint *orchestration.ExecutionCheckpoint, action orchestration.CommandType) {
		logger.InfoWithContext(ctx, "HITL checkpoint expired", map[string]any{
			"operation": "hitl_checkpoint_expiry", "status": string(checkpoint.Status),
			"checkpoint_id": checkpoint.CheckpointID, "action": string(action),
		})
		if onExpiry != nil {
			onExpiry(ctx, checkpoint, action)
		}
	}, expiryConfig, orchestration.WithCheckpointExpiryLogger(logger))
	if err != nil {
		_ = commandStore.Close()
		_ = checkpointStore.Close()
		return nil, fmt.Errorf("expiry processor: %w", err)
	}
	return &HITLInfrastructure{CheckpointStore: checkpointStore, CommandStore: commandStore, Controller: controller, ExpiryProcessor: processor}, nil
}

// decisionAwarePolicy keeps a single mutation capability while making HITL
// depend on Commerce Core's deterministic decision. Decision tokens are opaque
// hashes with a non-secret control prefix; unknown/missing prefixes fail closed.
// The tool remains the authority for eligibility, while this policy owns the
// human-approval interruption before dispatch.
type decisionAwarePolicy struct {
	base       *orchestration.RuleBasedPolicy
	timeout    orchestration.HITLConfig
	logger     core.Logger
	capability string
}

func newDecisionAwarePolicy(config orchestration.HITLConfig, logger core.Logger) *decisionAwarePolicy {
	baseConfig := config
	baseConfig.SensitiveCapabilities = withoutCapability(baseConfig.SensitiveCapabilities, "execute_recovery_command")
	baseConfig.StepSensitiveCapabilities = withoutCapability(baseConfig.StepSensitiveCapabilities, "execute_recovery_command")
	return &decisionAwarePolicy{
		base:    orchestration.NewRuleBasedPolicy(baseConfig, orchestration.WithPolicyLogger(logger)),
		timeout: config, logger: logger, capability: "execute_recovery_command",
	}
}

func (p *decisionAwarePolicy) ShouldApprovePlan(ctx context.Context, plan *orchestration.RoutingPlan) (*orchestration.InterruptDecision, error) {
	return p.base.ShouldApprovePlan(ctx, plan)
}

func (p *decisionAwarePolicy) ShouldApproveBeforeStep(ctx context.Context, step orchestration.RoutingStep, plan *orchestration.RoutingPlan) (*orchestration.InterruptDecision, error) {
	capability, _ := step.Metadata["capability"].(string)
	if capability != p.capability {
		return p.base.ShouldApproveBeforeStep(ctx, step, plan)
	}
	decisionToken, _ := orchestration.GetResolvedParams(ctx)["decision_token"].(string)
	if strings.HasPrefix(decisionToken, "automatic.") {
		return &orchestration.InterruptDecision{ShouldInterrupt: false}, nil
	}
	if err := validateRecoveryCompletionPlan(step, plan); err != nil {
		return nil, err
	}
	message := "Recovery decision requires human approval"
	if !strings.HasPrefix(decisionToken, "approval.") {
		message = "Recovery decision token is missing or unrecognized; approval required as a fail-safe"
	}
	defaultAction := p.timeout.DefaultAction
	if defaultAction == "" {
		defaultAction = orchestration.CommandReject
	}
	return &orchestration.InterruptDecision{
		ShouldInterrupt: true, Reason: orchestration.ReasonSensitiveOperation, Message: message,
		Priority: orchestration.PriorityHigh, Timeout: p.timeout.DefaultTimeout, DefaultAction: defaultAction,
		Metadata: map[string]any{"capability": capability, "decision_token_class": strings.SplitN(decisionToken, ".", 2)[0]},
	}, nil
}

func validateRecoveryCompletionPlan(executeStep orchestration.RoutingStep, plan *orchestration.RoutingPlan) error {
	if plan == nil {
		return fmt.Errorf("approval recovery plan is missing")
	}
	executeIndex := -1
	verifyIndex := -1
	verifyStepID := ""
	for index, step := range plan.Steps {
		if step.StepID == executeStep.StepID {
			executeIndex = index
			break
		}
	}
	if executeIndex < 0 {
		return fmt.Errorf("approval recovery plan does not contain its execute step")
	}
	for index := executeIndex + 1; index < len(plan.Steps); index++ {
		step := plan.Steps[index]
		capability, _ := step.Metadata["capability"].(string)
		if capability == "verify_order_outcome" && containsDependency(step.DependsOn, executeStep.StepID) {
			verifyIndex = index
			verifyStepID = step.StepID
			break
		}
	}
	if verifyIndex < 0 {
		return fmt.Errorf("approval recovery plan must verify the command in the same resumable plan")
	}
	for index := verifyIndex + 1; index < len(plan.Steps); index++ {
		step := plan.Steps[index]
		capability, _ := step.Metadata["capability"].(string)
		if capability == "send_synthetic_notification" &&
			containsDependency(step.DependsOn, executeStep.StepID) &&
			containsDependency(step.DependsOn, verifyStepID) {
			return nil
		}
	}
	return fmt.Errorf("approval recovery plan notification must depend directly on both execution and verification")
}

func containsDependency(dependencies []string, expected string) bool {
	for _, dependency := range dependencies {
		if dependency == expected {
			return true
		}
	}
	return false
}

func (p *decisionAwarePolicy) ShouldApproveAfterStep(ctx context.Context, step orchestration.RoutingStep, result *orchestration.StepResult) (*orchestration.InterruptDecision, error) {
	return p.base.ShouldApproveAfterStep(ctx, step, result)
}

func (p *decisionAwarePolicy) ShouldEscalateError(ctx context.Context, step orchestration.RoutingStep, err error, attempts int) (*orchestration.InterruptDecision, error) {
	return p.base.ShouldEscalateError(ctx, step, err, attempts)
}

func withoutCapability(values []string, removed string) []string {
	filtered := make([]string, 0, len(values))
	for _, value := range values {
		if value != removed {
			filtered = append(filtered, value)
		}
	}
	return filtered
}

func (h *HITLInfrastructure) Close() {
	if h == nil {
		return
	}
	if h.CommandStore != nil {
		_ = h.CommandStore.Close()
	}
	if h.CheckpointStore != nil {
		_ = h.CheckpointStore.Close()
	}
}
