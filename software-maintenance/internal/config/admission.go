package config

import (
	"fmt"
	"regexp"
	"strconv"
	"time"
)

// AdmissionLimits bound application ingress, independently of harness limits.
// RunID is operator-owned and must change explicitly to re-arm an exhausted run.
type AdmissionLimits struct {
	Enabled      bool          `json:"enabled"`
	RunID        string        `json:"run_id"`
	MaxCases     int           `json:"max_cases"`
	MaxPending   int           `json:"max_pending"`
	MaxPerWindow int           `json:"max_per_window"`
	FailureQuota int           `json:"failure_quota"`
	SuccessQuota int           `json:"success_quota"`
	RunDuration  time.Duration `json:"run_duration"`
	Window       time.Duration `json:"window"`
	Cooldown     time.Duration `json:"cooldown"`
}

func LoadAdmissionLimits(collectionEnabled bool, lookup func(string) (string, bool)) (AdmissionLimits, error) {
	limits := AdmissionLimits{Enabled: collectionEnabled, RunID: "default", MaxCases: 2, MaxPending: 1,
		MaxPerWindow: 2, FailureQuota: 1, SuccessQuota: 1, RunDuration: 2 * time.Hour,
		Window: 5 * time.Minute, Cooldown: time.Minute}
	if raw, ok := lookup("MAINTENANCE_ADMISSION_ENABLED"); ok {
		value, err := strconv.ParseBool(raw)
		if err != nil || collectionEnabled && !value {
			return limits, fmt.Errorf("MAINTENANCE_ADMISSION_ENABLED requires a boolean; enabled collection requires admission controls")
		}
		limits.Enabled = value
	}
	if raw, ok := lookup("MAINTENANCE_ADMISSION_RUN_ID"); ok {
		if !regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,62}$`).MatchString(raw) {
			return limits, fmt.Errorf("MAINTENANCE_ADMISSION_RUN_ID requires a lowercase slug of at most 63 bytes")
		}
		limits.RunID = raw
	}
	for _, setting := range []struct {
		key   string
		value *int
		max   int
	}{
		{"MAINTENANCE_ADMISSION_MAX_CASES", &limits.MaxCases, 1000},
		{"MAINTENANCE_ADMISSION_MAX_PENDING", &limits.MaxPending, 100},
		{"MAINTENANCE_ADMISSION_MAX_PER_WINDOW", &limits.MaxPerWindow, 1000},
		{"MAINTENANCE_ADMISSION_FAILURE_QUOTA", &limits.FailureQuota, 1000},
		{"MAINTENANCE_ADMISSION_SUCCESS_QUOTA", &limits.SuccessQuota, 1000},
	} {
		if raw, ok := lookup(setting.key); ok {
			value, err := strconv.Atoi(raw)
			if err != nil || value < 1 || value > setting.max {
				return limits, fmt.Errorf("%s requires an integer in [1, %d]", setting.key, setting.max)
			}
			*setting.value = value
		}
	}
	for _, setting := range []struct {
		key      string
		value    *time.Duration
		min, max time.Duration
	}{
		{"MAINTENANCE_ADMISSION_RUN_DURATION", &limits.RunDuration, time.Minute, 24 * time.Hour},
		{"MAINTENANCE_ADMISSION_WINDOW", &limits.Window, time.Minute, time.Hour},
		{"MAINTENANCE_ADMISSION_COOLDOWN", &limits.Cooldown, time.Second, time.Hour},
	} {
		if raw, ok := lookup(setting.key); ok {
			value, err := time.ParseDuration(raw)
			if err != nil || value < setting.min || value > setting.max {
				return limits, fmt.Errorf("%s requires a duration in [%s, %s]", setting.key, setting.min, setting.max)
			}
			*setting.value = value
		}
	}
	return limits, nil
}
