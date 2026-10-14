package maintenance

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/truvaagents/truva-g3/orchestration"
)

func TestMaintenanceSkillPackagesValidate(t *testing.T) {
	validator, err := orchestration.NewDefaultSkillPackageValidator(orchestration.DefaultSkillAuthoringLimits())
	if err != nil {
		t.Fatal(err)
	}
	paths, err := filepath.Glob("skills/packages/software-maintenance/*.json")
	if err != nil || len(paths) == 0 {
		t.Fatalf("missing skill packages: %v", err)
	}
	for _, path := range paths {
		t.Run(filepath.Base(path), func(t *testing.T) {
			data, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			var pkg orchestration.SkillPackageInput
			if err := json.Unmarshal(data, &pkg); err != nil {
				t.Fatal(err)
			}
			_, result, err := validator.Validate(context.Background(), orchestration.SkillRef{Namespace: "software-maintenance", Name: strings.TrimSuffix(filepath.Base(path), ".json")}, pkg)
			if err != nil || !result.Valid {
				t.Fatalf("invalid skill: %+v, %v", result, err)
			}
		})
	}
}
