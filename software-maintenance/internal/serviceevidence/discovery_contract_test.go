package serviceevidence

import (
	"strings"
	"testing"

	"github.com/truvaagents/truva-g3-domain-examples/software-maintenance/internal/discoverytest"
)

func TestEvidenceWindowRestrictionsReachPlanner(t *testing.T) {
	tool := newTestTool(t, &fakeEvidenceClient{})
	catalog := discoverytest.Catalog(t, tool.GetCapabilities())
	for _, expected := range []string{"observation_constraints.retention_seconds", "maximum_window_seconds", "no later than now", "observation_constraints.maximum_items", "omitted or zero"} {
		if !strings.Contains(catalog, expected) {
			t.Errorf("planner catalog lacks %q", expected)
		}
	}
}
