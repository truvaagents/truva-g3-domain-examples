package maintenance_test

import (
	"os"
	"os/exec"
	"strings"
	"testing"
)

func TestDiagnosticSetupProfile(t *testing.T) {
	cache, err := exec.Command("go", "env", "GOCACHE").Output()
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name, script string
		fail         bool
	}{
		{"enabled", `MAINTENANCE_DIAGNOSTIC_PROFILE=expanded-budget; load_diagnostic_profile; [[ "$TRUVAG3_PLAN_MAX_TOKENS" == 128000 && "$TRUVAG3_EXECUTION_STEP_TIMEOUT" == 35m ]]; apply_diagnostic_configmap repair`, false},
		{"normal-planning-budget", `MAINTENANCE_DIAGNOSTIC_PROFILE=expanded-budget; TRUVAG3_PLAN_MAX_TOKENS=128000; load_diagnostic_profile; [[ "$TRUVAG3_PLAN_MAX_TOKENS" == 128000 ]]`, false},
		{"disabled", `unset MAINTENANCE_DIAGNOSTIC_PROFILE TRUVAG3_PLAN_MAX_TOKENS; load_diagnostic_profile; [[ -z "${TRUVAG3_PLAN_MAX_TOKENS-}" ]]; apply_diagnostic_configmap repair`, false},
		{"higher-budget", `MAINTENANCE_DIAGNOSTIC_PROFILE=expanded-budget; TRUVAG3_PLAN_MAX_TOKENS=150000; load_diagnostic_profile`, true},
		{"unknown", `MAINTENANCE_DIAGNOSTIC_PROFILE=other; load_diagnostic_profile`, true},
		{"no-profile-invalid-timeout", `export TRUVAG3_SKILL_RESOURCE_SELECTION_ATTEMPT_TIMEOUT=invalid; load_diagnostic_profile`, true},
		{"no-profile-empty-memory", `export TRUVAG3_SHARED_MEMORY_ENRICHMENT_MAX_TOKENS=""; load_diagnostic_profile`, true},
		{"profile-invalid-memory", `export MAINTENANCE_DIAGNOSTIC_PROFILE=expanded-budget TRUVAG3_SHARED_MEMORY_STREAM_MAXLEN=-1; load_diagnostic_profile`, true},
		{"no-profile-compound-duration", `export TRUVAG3_SHARED_MEMORY_INVESTIGATION_TTL=1h2m3.5s TRUVAG3_SKILL_RESOURCE_SELECTION_ATTEMPT_TIMEOUT=0 TRUVAG3_SHARED_MEMORY_COMPACTION_RECENT_DETAIL=0; load_diagnostic_profile`, false},
		{"duration-overflow", `export TRUVAG3_SHARED_MEMORY_INVESTIGATION_TTL=9223372036854775808ns; load_diagnostic_profile`, true},
		{"integer-overflow", `export TRUVAG3_SHARED_MEMORY_ENRICHMENT_MAX_TOKENS=9223372036854775807; load_diagnostic_profile`, true},
		{"ingress-enabled", `MAINTENANCE_DIAGNOSTIC_PROFILE=expanded-budget; apply_component_manifest review "$SCRIPT_DIR/deploy/maintenance-review-agent.yaml"`, false},
		{"ingress-rollback", `unset MAINTENANCE_DIAGNOSTIC_PROFILE; apply_component_manifest review "$SCRIPT_DIR/deploy/maintenance-review-agent.yaml"`, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cmd := exec.Command("bash", "-c", `set -euo pipefail
SCRIPT_DIR="$PWD"
NAMESPACE=test
fail() { printf '%s\n' "$1" >&2; exit 1; }
component_app() { printf 'maintenance-%s-agent\n' "$1"; }
kubectl() { printf '%s\n' "$@"; }
source "$SCRIPT_DIR/diagnostic-profile.sh"
`+tc.script)
			// Do not let developer credentials or tuning change an isolated profile test.
			cmd.Env = []string{"PATH=" + os.Getenv("PATH"), "GOCACHE=" + strings.TrimSpace(string(cache))}
			out, err := cmd.CombinedOutput()
			if (err != nil) != tc.fail {
				t.Fatalf("%v: %s", err, out)
			}
			if tc.fail && tc.name != "unknown" && !strings.Contains(string(out), "TRUVAG3_") {
				t.Fatalf("did not fail on the intended configuration setting: %s", out)
			}
			if tc.name == "enabled" && !strings.Contains(string(out), `"TRUVAG3_PLAN_MAX_TOKENS": "128000"`) {
				t.Fatal(string(out))
			}
			if tc.name == "disabled" && !strings.Contains(string(out), `"TRUVAG3_PLAN_MAX_TOKENS": null`) {
				t.Fatal("rollback must remove stale diagnostic-only keys")
			}
			if tc.name == "ingress-enabled" && !strings.Contains(string(out), "proxy-read-timeout=2520") {
				t.Fatal(string(out))
			}
			if tc.name == "ingress-rollback" && !strings.Contains(string(out), "proxy-read-timeout=660") {
				t.Fatal(string(out))
			}
		})
	}
}
