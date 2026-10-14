package main

import (
	"context"
	"errors"
	"testing"

	"github.com/truvaagents/truva-g3/core"
)

// An invalid orchestrator configuration must fail framework initialization,
// rather than leaving a listening process with no usable harness.
func TestRuntimeInitializationFailureStopsStartup(t *testing.T) {
	want := errors.New("invalid harness configuration")
	called := false
	agent := &maintenanceRepairAgent{BaseAgent: core.NewBaseAgent(repairAgentName), runtimeInit: func(context.Context) error {
		called = true
		return want
	}}
	_, err := core.NewFramework(agent, core.WithDiscovery(false, "redis"))
	if err != nil {
		t.Fatal(err)
	}
	if err := agent.Initialize(t.Context()); !errors.Is(err, want) || !called {
		t.Fatalf("initialization failure lost: called=%v error=%v", called, err)
	}
}

func TestCancelledInitializationSkipsRuntime(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	agent := &maintenanceRepairAgent{BaseAgent: core.NewBaseAgent(repairAgentName), runtimeInit: func(context.Context) error {
		t.Fatal("runtime initialized after cancellation")
		return nil
	}}
	if err := agent.Initialize(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancellation lost: %v", err)
	}
}
