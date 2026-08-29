package knowledge

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"testing"
	"time"
)

func loadCommercePack(t *testing.T) DomainPack {
	t.Helper()
	pack, err := LoadPack("../../../domain-packs/commerce-demo/pack.json")
	if err != nil {
		t.Fatalf("LoadPack: %v", err)
	}
	return pack
}

func TestCommercePolicyPinMatchesCanonicalPack(t *testing.T) {
	pack := loadCommercePack(t)
	data, err := os.ReadFile("../../../commerce-core/data/policy-release.json")
	if err != nil {
		t.Fatalf("read commerce policy pin: %v", err)
	}
	var pin struct {
		ReleaseID   string   `json:"release_id"`
		ContentHash string   `json:"content_hash"`
		SectionIDs  []string `json:"section_ids"`
	}
	if err := json.Unmarshal(data, &pin); err != nil {
		t.Fatalf("decode commerce policy pin: %v", err)
	}
	if pin.ReleaseID != pack.Release.ReleaseID || pin.ContentHash != pack.Release.ContentHash {
		t.Fatalf("commerce policy pin does not match pack: pin=%s/%s pack=%s/%s", pin.ReleaseID, pin.ContentHash, pack.Release.ReleaseID, pack.Release.ContentHash)
	}
	actual := make(map[string]struct{}, len(pack.Sections))
	for _, section := range pack.Sections {
		actual[section.SectionID] = struct{}{}
	}
	if len(pin.SectionIDs) != len(actual) {
		t.Fatalf("policy pin has %d sections; pack has %d", len(pin.SectionIDs), len(actual))
	}
	for _, sectionID := range pin.SectionIDs {
		if _, ok := actual[sectionID]; !ok {
			t.Fatalf("policy pin contains unknown section %q", sectionID)
		}
	}
}

func TestChangedCommercePolicyPinMatchesCanonicalPack(t *testing.T) {
	pack, err := LoadPack("../../../domain-packs/commerce-demo-policy-changed/pack.json")
	if err != nil {
		t.Fatalf("LoadPack changed policy: %v", err)
	}
	data, err := os.ReadFile("../../../commerce-core/data/policy-release-changed.json")
	if err != nil {
		t.Fatalf("read changed commerce policy pin: %v", err)
	}
	var pin struct {
		ReleaseID   string   `json:"release_id"`
		ContentHash string   `json:"content_hash"`
		SectionIDs  []string `json:"section_ids"`
	}
	if err := json.Unmarshal(data, &pin); err != nil {
		t.Fatalf("decode changed commerce policy pin: %v", err)
	}
	if pin.ReleaseID != pack.Release.ReleaseID || pin.ContentHash != pack.Release.ContentHash {
		t.Fatalf("changed commerce policy pin does not match pack: pin=%s/%s pack=%s/%s", pin.ReleaseID, pin.ContentHash, pack.Release.ReleaseID, pack.Release.ContentHash)
	}
	actual := make(map[string]struct{}, len(pack.Sections))
	for _, section := range pack.Sections {
		actual[section.SectionID] = struct{}{}
	}
	if len(pin.SectionIDs) != len(actual) {
		t.Fatalf("changed policy pin has %d sections; pack has %d", len(pin.SectionIDs), len(actual))
	}
	for _, sectionID := range pin.SectionIDs {
		if _, ok := actual[sectionID]; !ok {
			t.Fatalf("changed policy pin contains unknown section %q", sectionID)
		}
	}
}

func TestGoldenRetrievalCases(t *testing.T) {
	pack := loadCommercePack(t)
	if pack.Release.ContentHash != "sha256:49643f3bb48d35aa04ea786ab1b0205475eadcbfd247cd74cc8c55a4a5153d28" {
		t.Fatalf("commerce release hash changed without a release update: %s", pack.Release.ContentHash)
	}
	store, err := NewMemoryStore(pack)
	if err != nil {
		t.Fatalf("NewMemoryStore: %v", err)
	}
	for _, golden := range pack.GoldenCases {
		t.Run(golden.CaseID, func(t *testing.T) {
			result, searchErr := HybridSearch(context.Background(), store, CandidateSpec{
				Query: golden.Query, ReleaseID: pack.Release.ReleaseID, TenantID: pack.TenantID,
				Jurisdiction: golden.Jurisdiction, Channel: golden.Channel, AsOf: golden.AsOf, Limit: 5,
			})
			if searchErr != nil {
				t.Fatalf("HybridSearch: %v", searchErr)
			}
			found := map[string]bool{}
			for _, candidate := range result.Candidates {
				found[candidate.Section.SectionID] = true
			}
			for _, expected := range golden.ExpectedSectionIDs {
				if !found[expected] {
					t.Errorf("expected section %q; got %v", expected, candidateIDs(result.Candidates))
				}
			}
			for _, forbidden := range golden.ForbiddenSectionIDs {
				if found[forbidden] {
					t.Errorf("forbidden section %q was retrieved", forbidden)
				}
			}
		})
	}
}

func TestFiltersApplyBeforeRanking(t *testing.T) {
	pack := loadCommercePack(t)
	store, _ := NewMemoryStore(pack)
	result, err := HybridSearch(context.Background(), store, CandidateSpec{
		Query: "Priority Mail Express guaranteed delivery refund", TenantID: "another-tenant", Jurisdiction: "US", Channel: "shipping", AsOf: time.Date(2026, 8, 20, 18, 0, 0, 0, time.UTC), Limit: 8,
	})
	if err != nil {
		t.Fatalf("HybridSearch: %v", err)
	}
	if len(result.Candidates) != 0 {
		t.Fatalf("tenant filter returned %d candidates", len(result.Candidates))
	}

	_, err = HybridSearch(context.Background(), store, CandidateSpec{
		Query: "electronics return", TenantID: pack.TenantID, Jurisdiction: "US", Channel: "online", AsOf: time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC), Limit: 8,
	})
	if !errors.Is(err, ErrReleaseInactive) {
		t.Fatalf("expected inactive release rejection, got %v", err)
	}

	decisionTime := time.Date(2026, 8, 20, 18, 0, 0, 0, time.UTC)
	pack.Sections[0].EffectiveFrom = decisionTime.Add(time.Hour)
	store, _ = NewMemoryStore(pack)
	result, err = HybridSearch(context.Background(), store, CandidateSpec{
		Query: "shipment representation default period", TenantID: pack.TenantID, Jurisdiction: "US", Channel: "online", AsOf: decisionTime, Limit: 8,
	})
	if err != nil {
		t.Fatalf("HybridSearch effective date: %v", err)
	}
	for _, candidate := range result.Candidates {
		if candidate.Section.EffectiveFrom.After(decisionTime) {
			t.Fatalf("effective-date filter leaked future section %q", candidate.Section.SectionID)
		}
	}
}

func TestNaturalTagVariantsFindCarrierEvidenceInApplicableChannel(t *testing.T) {
	pack := loadCommercePack(t)
	store, _ := NewMemoryStore(pack)
	result, err := HybridSearch(context.Background(), store, CandidateSpec{
		Query:        "USPS Priority Mail Express missed guaranteed delivery time carrier service-failure claim eligibility mailing receipt tracking evidence claim timing record preservation",
		TenantID:     pack.TenantID,
		Jurisdiction: "US",
		Channel:      "shipping",
		AsOf:         time.Date(2026, 8, 20, 18, 0, 0, 0, time.UTC),
		Tags:         []string{"carrier_claim", "USPS", "Priority Mail Express"},
		Limit:        8,
	})
	if err != nil {
		t.Fatalf("HybridSearch: %v", err)
	}
	found := map[string]bool{}
	for _, candidate := range result.Candidates {
		found[candidate.Section.SectionID] = true
	}
	for _, sectionID := range []string{"usps-priority-express-eligibility", "usps-priority-express-claim-window"} {
		if !found[sectionID] {
			t.Errorf("expected carrier section %q after tag normalization; got %v", sectionID, candidateIDs(result.Candidates))
		}
	}
	if found["fedex-guarantee-service-scope"] {
		t.Errorf("named-source query returned peer carrier evidence: %v", candidateIDs(result.Candidates))
	}
}

func TestExactReadAndTraversal(t *testing.T) {
	pack := loadCommercePack(t)
	store, _ := NewMemoryStore(pack)
	asOf := time.Date(2026, 8, 20, 18, 0, 0, 0, time.UTC)
	exact, err := store.ReadExact(context.Background(), SectionRef{ReleaseID: pack.Release.ReleaseID, SectionID: "ftc-delay-consent-or-refund", TenantID: pack.TenantID, Jurisdiction: "US", Channel: "online", AsOf: asOf})
	if err != nil {
		t.Fatalf("ReadExact: %v", err)
	}
	if exact.Source.URL == "" || exact.Release.ContentHash == "" {
		t.Fatalf("missing provenance: %+v", exact)
	}
	paths, err := store.Traverse(context.Background(), TraversalSpec{TenantID: pack.TenantID, Jurisdiction: "US", Channel: "online", AsOf: asOf, SectionIDs: []string{"ftc-delay-consent-or-refund"}, MaxDepth: 1})
	if err != nil {
		t.Fatalf("Traverse: %v", err)
	}
	if len(paths) < 2 {
		t.Fatalf("expected related authority and refund timing, got %+v", paths)
	}
}

func TestAlternateDomainPackUsesTheSameGatewayContract(t *testing.T) {
	pack, err := LoadPack("../../../domain-packs/it-access-demo/pack.json")
	if err != nil {
		t.Fatalf("LoadPack alternate domain: %v", err)
	}
	store, err := NewMemoryStore(pack)
	if err != nil {
		t.Fatalf("NewMemoryStore alternate domain: %v", err)
	}
	result, err := HybridSearch(context.Background(), store, CandidateSpec{
		Query: "departed employee access revocation deadline", TenantID: "it-demo",
		Jurisdiction: "global", Channel: "internal", AsOf: time.Date(2026, 8, 20, 18, 0, 0, 0, time.UTC), Limit: 3,
	})
	if err != nil {
		t.Fatalf("HybridSearch alternate domain: %v", err)
	}
	if len(result.Candidates) == 0 || result.Candidates[0].Section.SectionID != "departed-user-revocation" {
		t.Fatalf("expected domain-neutral retrieval to rank departed-user-revocation first, got %v", candidateIDs(result.Candidates))
	}
	paths, err := store.Traverse(context.Background(), TraversalSpec{TenantID: pack.TenantID, Jurisdiction: "global", Channel: "internal", AsOf: time.Date(2026, 8, 20, 18, 0, 0, 0, time.UTC), SectionIDs: []string{"departed-user-revocation"}, MaxDepth: 1})
	if err != nil {
		t.Fatalf("Traverse alternate domain: %v", err)
	}
	if len(paths) != 1 || paths[0].To.SectionID != "offboarding-verification" {
		t.Fatalf("expected domain-neutral relation traversal, got %+v", paths)
	}
}

func TestSemanticAliasesBelongToEachDomainPack(t *testing.T) {
	commerce := loadCommercePack(t)
	it, err := LoadPack("../../../domain-packs/it-access-demo/pack.json")
	if err != nil {
		t.Fatalf("LoadPack alternate domain: %v", err)
	}
	commerceTerms := tokenize("employee", commerce.SemanticAliases)
	itTerms := tokenize("employee", it.SemanticAliases)
	if len(commerceTerms) != 1 || commerceTerms[0] != "employee" {
		t.Fatalf("commerce pack unexpectedly rewrote IT vocabulary: %v", commerceTerms)
	}
	if len(itTerms) != 1 || itTerms[0] != "user" {
		t.Fatalf("IT pack alias was not applied: %v", itTerms)
	}
}

func TestExactReadRequiresScopeAndActiveRelease(t *testing.T) {
	pack := loadCommercePack(t)
	store, _ := NewMemoryStore(pack)
	if _, err := store.ReadExact(context.Background(), SectionRef{ReleaseID: pack.Release.ReleaseID, SectionID: "ftc-delay-consent-or-refund"}); !errors.Is(err, ErrInvalidScope) {
		t.Fatalf("expected required scope error, got %v", err)
	}
	if _, err := store.ReadExact(context.Background(), SectionRef{ReleaseID: pack.Release.ReleaseID, SectionID: "ftc-delay-consent-or-refund", TenantID: pack.TenantID, Jurisdiction: "US", Channel: "online", AsOf: time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC)}); !errors.Is(err, ErrReleaseInactive) {
		t.Fatalf("expected inactive release error, got %v", err)
	}
	if _, err := store.ReadExact(context.Background(), SectionRef{ReleaseID: pack.Release.ReleaseID, SectionID: "usps-priority-express-eligibility", TenantID: pack.TenantID, Jurisdiction: "US", Channel: "online", AsOf: time.Date(2026, 8, 20, 18, 0, 0, 0, time.UTC)}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("expected channel-specific evidence rejection, got %v", err)
	}
}

func candidateIDs(candidates []Candidate) []string {
	ids := make([]string, 0, len(candidates))
	for _, candidate := range candidates {
		ids = append(ids, candidate.Section.SectionID)
	}
	return ids
}
