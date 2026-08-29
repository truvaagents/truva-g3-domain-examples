//go:build ladybug

package knowledge

import (
	"context"
	"path/filepath"
	"testing"
	"time"
)

func TestLadybugProjectionConformsForExactReadAndTraversal(t *testing.T) {
	pack := loadCommercePack(t)
	store, err := NewConfiguredStore(pack, filepath.Join(t.TempDir(), "knowledge.lbug"))
	if err != nil {
		t.Fatalf("NewConfiguredStore: %v", err)
	}
	defer store.Close()
	asOf := time.Date(2026, 8, 20, 18, 0, 0, 0, time.UTC)
	exact, err := store.ReadExact(context.Background(), SectionRef{ReleaseID: pack.Release.ReleaseID, SectionID: "demo-auto-recovery-bounds", TenantID: pack.TenantID, Jurisdiction: "US", Channel: "online", AsOf: asOf})
	if err != nil {
		t.Fatalf("ReadExact: %v", err)
	}
	if exact.Section.Title == "" || exact.Source.URL == "" {
		t.Fatalf("incomplete exact result: %+v", exact)
	}
	paths, err := store.Traverse(context.Background(), TraversalSpec{ReleaseID: pack.Release.ReleaseID, TenantID: pack.TenantID, Jurisdiction: "US", Channel: "online", AsOf: asOf, SectionIDs: []string{"demo-auto-recovery-bounds"}, MaxDepth: 1, Limit: 10})
	if err != nil {
		t.Fatalf("Traverse: %v", err)
	}
	if len(paths) != 3 {
		t.Fatalf("expected 3 projected relations, got %+v", paths)
	}
	paths, err = store.Traverse(context.Background(), TraversalSpec{ReleaseID: pack.Release.ReleaseID, TenantID: pack.TenantID, Jurisdiction: "US", Channel: "online", AsOf: asOf, SectionIDs: []string{"demo-auto-recovery-bounds"}, MaxDepth: 2, Limit: 10})
	if err != nil {
		t.Fatalf("Traverse depth two: %v", err)
	}
	foundDepthTwo := false
	for _, path := range paths {
		if path.Depth == 2 && path.From.SectionID == "demo-approval-thresholds" && path.To.SectionID == "demo-verification-notification" {
			foundDepthTwo = true
		}
	}
	if !foundDepthTwo {
		t.Fatalf("expected graph-verified depth-two path, got %+v", paths)
	}
}
