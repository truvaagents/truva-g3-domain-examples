package main

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/truvaagents/truva-g3-domain-examples/software-maintenance/internal/lifecycle"
	"github.com/truvaagents/truva-g3/core"
)

// Exercise the shipped policy rather than a hand-built approximation: changing
// the saved business outcome must invalidate an otherwise identical assessment.
func TestRetailAssessmentComparesSavedTotal(t *testing.T) {
	profile, err := loadCoordinatorProfile("../../profiles/retail-order-maintenance.json")
	if err != nil {
		t.Fatal(err)
	}
	policy := profile.AssessmentPolicy
	rule := policy.Rules[0]
	now := time.Now().UTC()
	scope := lifecycle.Scope{CoordinatorProfileID: profile.CoordinatorProfileID, ServiceProfileID: profile.ServiceProfileID, RepositoryID: profile.RepositoryID, EnvironmentID: policy.EnvironmentID}
	document := map[string]any{
		"observation": map[string]any{"operation": "create_order", "sku": "labelled-product", "quantity": 2, "discount_cents": 0, "http_status": 201, "unit_price_cents": 50, "currency": "USD", "requested_promotion_code": ""},
		"outcome":     map[string]any{"ledger": map[string]any{"total_cents": 100}},
	}
	build := func() (lifecycle.Facts, error) {
		return lifecycle.BuildFacts(scope, policy, rule, document, map[string]json.RawMessage{"/release/revision": json.RawMessage(`"labelled-release"`)}, "labelled-observation", now, []lifecycle.EvidenceReference{{Capability: "labelled-outcome", PayloadSHA256: "labelled-hash"}}, 32768)
	}
	original, err := build()
	if err != nil {
		t.Fatal(err)
	}
	assessment := lifecycle.Assessment{SchemaVersion: 1, Facts: original, AssessedAt: now, RevalidateAfter: now.Add(time.Hour)}
	if match := lifecycle.Compare(assessment, original, policy, now, time.Hour); !match.Eligible {
		t.Fatal("unchanged outcome must remain eligible")
	}
	observation := document["observation"].(map[string]any)
	observation["requested_promotion_code"] = "WELCOME100"
	promotionChanged, err := build()
	if err != nil {
		t.Fatal(err)
	}
	if match := lifecycle.Compare(assessment, promotionChanged, policy, now, time.Hour); match.Eligible || match.Reason != "facts_changed" {
		t.Fatal("changed attempted promotion was reusable")
	}
	delete(observation, "requested_promotion_code")
	if _, err := build(); err == nil {
		t.Fatal("missing attempted promotion must remain unknown")
	}
	observation["requested_promotion_code"] = nil
	if _, err := build(); err == nil {
		t.Fatal("null attempted promotion must remain unknown")
	}
	observation["requested_promotion_code"] = ""
	ledger := document["outcome"].(map[string]any)["ledger"].(map[string]any)
	ledger["total_cents"] = 101
	changed, err := build()
	if err != nil {
		t.Fatal(err)
	}
	if match := lifecycle.Compare(assessment, changed, policy, now, time.Hour); match.Eligible || match.Reason != "facts_changed" {
		t.Fatal("changed saved total was reusable")
	}
	delete(ledger, "total_cents")
	if _, err := build(); err == nil {
		t.Fatal("missing saved outcome must fail closed")
	}
	oldPolicy := *policy
	oldPolicy.Revision = "retail-order-contract-v1"
	assessment.Facts.PolicySHA256 = lifecycle.PolicyDigest(&oldPolicy)
	if match := lifecycle.Compare(assessment, original, policy, now, time.Hour); match.Eligible || match.Reason != "policy_changed" {
		t.Fatal("older policy assessment was silently renewed")
	}
}

func TestInternalAssessmentWriterDiscoveryIsNarrow(t *testing.T) {
	service := &core.ServiceInfo{Name: "maintenance-assessment-tool", Type: core.ComponentTypeTool, Capabilities: []core.Capability{{Name: "record_assessment", Internal: true}, {Name: "other_internal", Internal: true}}}
	if len(filterOwnedServices([]*core.ServiceInfo{service}, service.Name, "record_assessment")) != 1 {
		t.Fatal("post-completion writer became undiscoverable")
	}
	if len(filterOwnedServices([]*core.ServiceInfo{service}, service.Name, "other_internal")) != 0 {
		t.Fatal("unrelated internal capability became callable")
	}
	service.Name = "different-tool"
	if len(filterOwnedServices([]*core.ServiceInfo{service}, service.Name, "record_assessment")) != 0 {
		t.Fatal("internal writer exception expanded to another issuer")
	}
}
