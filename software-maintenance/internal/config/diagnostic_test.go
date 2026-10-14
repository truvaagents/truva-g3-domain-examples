package config

import (
	"os"
	"strings"
	"testing"
	"time"

	"github.com/truvaagents/truva-g3/orchestration"
)

func TestInvestigationWorkersConfiguration(t *testing.T) {
	for _, tc := range []struct {
		value   string
		present bool
		want    int
	}{
		{"", false, 1}, {"1", true, 1}, {"3", true, 3}, {"100", true, 100},
		{"", true, 0}, {"0", true, 0}, {"-1", true, 0}, {"101", true, 0},
		{"1.5", true, 0}, {"three", true, 0}, {" 3 ", true, 0},
		{"999999999999999999999999", true, 0},
	} {
		got, err := LoadInvestigationWorkers(func(key string) (string, bool) {
			if key != "MAINTENANCE_INVESTIGATION_WORKERS" {
				t.Fatalf("unexpected setting %s", key)
			}
			return tc.value, tc.present
		})
		if got != tc.want || (err != nil) != (tc.want == 0) {
			t.Fatalf("%+v: got %d, %v", tc, got, err)
		}
	}
}

func TestExecutionLimitsOptIn(t *testing.T) {
	for _, tc := range []struct {
		profile                            string
		task, coordinator, delegate, claim time.Duration
		invalid                            bool
	}{
		{"", 20 * time.Minute, 5 * time.Minute, 4 * time.Minute, 30 * time.Minute, false},
		{"expanded-budget", 40 * time.Minute, 10 * time.Minute, 8 * time.Minute, 60 * time.Minute, false},
		{"typo", 0, 0, 0, 0, true},
	} {
		t.Run(tc.profile, func(t *testing.T) {
			got, err := LoadExecutionLimits(func(string) (string, bool) { return tc.profile, tc.profile != "" })
			if (err != nil) != tc.invalid {
				t.Fatal(err)
			}
			if got.Task != tc.task || got.CoordinatorAI != tc.coordinator || got.DelegateAI != tc.delegate || got.InvestigationClaim != tc.claim {
				t.Fatalf("limits=%+v", got)
			}
		})
	}
}

func TestExpandedProfileResolvesThroughFramework(t *testing.T) {
	data, err := os.ReadFile("../../profiles/diagnostics/expanded-budget.env")
	if err != nil {
		t.Fatal(err)
	}
	values := map[string]string{}
	for _, line := range strings.Split(string(data), "\n") {
		if key, value, ok := strings.Cut(line, "="); ok && !strings.HasPrefix(key, "#") {
			values[key] = value
		}
	}
	resolved, err := orchestration.ResolveOrchestratorConfig(orchestration.ConfigResolution{
		Base: orchestration.NewDefaultOrchestratorConfig(), Environment: orchestration.EnvironmentStrict,
		LookupEnv: func(key string) (string, bool) { v, ok := values[key]; return v, ok },
		Options: []orchestration.OrchestratorOption{
			orchestration.WithSkills(orchestration.SkillConfig{Enabled: true}),
			orchestration.WithSkillActivationAIOptions(&orchestration.AIOptionsOverride{Model: orchestration.StringPtr("fast")}),
			orchestration.WithSkillResourceAIOptions(&orchestration.AIOptionsOverride{Model: orchestration.StringPtr("fast")}),
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	c := resolved.Config
	if *c.PlanAIOptions.MaxTokens != 128000 || *c.SynthesisAIOptions.MaxTokens != 30000 || c.Skills.Limits.ResolutionMaxTokens != 5000 || values["TRUVAG3_SHARED_MEMORY_EVENT_SUMMARIZER_MAX_TOKENS"] != "10000" {
		t.Fatal("token precedence changed")
	}
	if c.ExecutionOptions.StepTimeout != 35*time.Minute || c.ExecutionOptions.TotalTimeout != 40*time.Minute || c.IterativePlanning.PhaseTimeout != 40*time.Minute || c.IterativePlanning.MaxPhases != 25 {
		t.Fatal("timeout/phase projection changed")
	}
}
