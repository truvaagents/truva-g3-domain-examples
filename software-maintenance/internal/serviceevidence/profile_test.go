package serviceevidence

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestLoadRetailOrderProfile(t *testing.T) {
	t.Parallel()
	store, err := loadProfileStore(filepath.Join("..", "..", "profiles", "retail-order-service.json"))
	if err != nil {
		t.Fatalf("loadProfileStore() error = %v", err)
	}
	profile, ok := store.get("retail-order-service")
	if !ok {
		t.Fatal("retail-order-service profile not loaded")
	}
	if !profile.AssessmentReady || len(profile.ContractReferences) == 0 {
		t.Fatal("profile must be assessment-ready with a pinned contract")
	}
	if !strings.HasPrefix(profile.version, "sha256:") || len(profile.version) != len("sha256:")+64 {
		t.Fatalf("profile version = %q", profile.version)
	}
}

func TestValidateProfileRejectsUnsafeVariants(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name   string
		mutate func(*serviceProfile)
	}{
		{name: "caller supplied URL shape", mutate: func(p *serviceProfile) { p.Upstream.BaseURL = "file:///etc/passwd" }},
		{name: "path traversal", mutate: func(p *serviceProfile) { p.Upstream.Endpoints.Catalog = "/../secret" }},
		{name: "assessment without contract", mutate: func(p *serviceProfile) { p.AssessmentReady = true }},
		{name: "assessment with unpinned contract", mutate: func(p *serviceProfile) {
			p.AssessmentReady = true
			p.ContractReferences = []contractReference{{Kind: "service-contract", Revision: "main", Path: "README.md"}}
		}},
		{name: "excessive window", mutate: func(p *serviceProfile) { p.Limits.MaximumWindowSeconds = 601 }},
		{name: "duplicate caller", mutate: func(p *serviceProfile) { p.AllowedCallers = append(p.AllowedCallers, p.AllowedCallers[0]) }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			profile := validTestProfile()
			test.mutate(&profile)
			if err := validateProfile(&profile); err == nil {
				t.Fatal("validateProfile() accepted unsafe profile")
			}
		})
	}
}

func validTestProfile() serviceProfile {
	return serviceProfile{
		SchemaVersion: 1,
		ProfileID:     "retail-order-service",
		ServiceName:   "retail-order-service",
		Repository:    repositoryReference{Name: "truvaagents/retail-order-service", DefaultBranch: "main"},
		Upstream: upstreamProfile{
			BaseURL: "http://retail-order-service.example",
			Endpoints: upstreamEndpoints{
				Catalog: "/catalog", Observations: "/observations", Evidence: "/evidence/{id}",
				BusinessOutcome: "/orders/{id}", BuildInfo: "/build-info",
			},
		},
		Limits: profileLimits{
			TimeoutMilliseconds: 5000, MaximumResponseBytes: 65536, MaximumRequestBytes: 16384,
			MaximumWindowSeconds: 600, RetentionSeconds: 86400, MaximumItems: 50,
		},
		AllowedCallers:    []string{"maintenance-agent"},
		SupportedEvidence: []string{"observation-feed"},
		Limitations:       []string{"advisory only"},
	}
}
