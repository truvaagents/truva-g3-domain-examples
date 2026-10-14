package config

import "testing"

func TestAdmissionDefaultsAndInvalidExplicitSettings(t *testing.T) {
	lookup := func(string) (string, bool) { return "", false }
	limits, err := LoadAdmissionLimits(true, lookup)
	if err != nil || !limits.Enabled || limits.MaxCases != 2 || limits.MaxPending != 1 {
		t.Fatalf("unsafe defaults: %+v %v", limits, err)
	}
	for key, value := range map[string]string{
		"MAINTENANCE_ADMISSION_ENABLED": "false", "MAINTENANCE_ADMISSION_MAX_CASES": "0",
		"MAINTENANCE_ADMISSION_RUN_ID": "", "MAINTENANCE_ADMISSION_RUN_DURATION": "forever",
		"MAINTENANCE_ADMISSION_SUCCESS_QUOTA": "-1", "MAINTENANCE_ADMISSION_WINDOW": "24h",
	} {
		if _, err := LoadAdmissionLimits(true, func(k string) (string, bool) { return value, k == key }); err == nil {
			t.Fatalf("invalid %s accepted", key)
		}
	}
}
