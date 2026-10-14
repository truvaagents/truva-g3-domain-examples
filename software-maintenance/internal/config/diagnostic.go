// Package config owns example-specific diagnostic configuration.
package config

import (
	"fmt"
	"strconv"
	"time"
)

const (
	DefaultInvestigationWorkers = 1
	// This example admits at most 100 pending cases. Bound accidental worker
	// allocations as well; the shared admission limit still controls active work.
	MaximumInvestigationWorkers = 100
)

// LoadInvestigationWorkers configures this pod's existing framework worker pool.
// Absent configuration preserves one worker; explicit invalid values fail startup.
func LoadInvestigationWorkers(lookup func(string) (string, bool)) (int, error) {
	value, present := lookup("MAINTENANCE_INVESTIGATION_WORKERS")
	if !present {
		return DefaultInvestigationWorkers, nil
	}
	workers, err := strconv.Atoi(value)
	if err != nil || workers < 1 || workers > MaximumInvestigationWorkers {
		return 0, fmt.Errorf("MAINTENANCE_INVESTIGATION_WORKERS must be an integer between 1 and %d", MaximumInvestigationWorkers)
	}
	return workers, nil
}

// ExecutionLimits are construction-time bounds, independent of harness policy.
type ExecutionLimits struct {
	Task, CoordinatorAI, DelegateAI, InvestigationClaim time.Duration
	Diagnostic                                          bool
}

// LoadExecutionLimits leaves normal defaults unchanged unless explicitly opted in.
func LoadExecutionLimits(lookup func(string) (string, bool)) (ExecutionLimits, error) {
	profile, _ := lookup("MAINTENANCE_DIAGNOSTIC_PROFILE")
	switch profile {
	case "":
		return ExecutionLimits{Task: 20 * time.Minute, CoordinatorAI: 5 * time.Minute, DelegateAI: 4 * time.Minute, InvestigationClaim: 30 * time.Minute}, nil
	case "expanded-budget":
		return ExecutionLimits{Task: 40 * time.Minute, CoordinatorAI: 10 * time.Minute, DelegateAI: 8 * time.Minute, InvestigationClaim: 60 * time.Minute, Diagnostic: true}, nil
	default:
		return ExecutionLimits{}, fmt.Errorf("MAINTENANCE_DIAGNOSTIC_PROFILE must be empty or expanded-budget")
	}
}
