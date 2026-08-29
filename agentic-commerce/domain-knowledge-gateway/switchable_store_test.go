package main

import (
	"context"
	"path/filepath"
	"testing"
)

func TestSwitchableStoreActivatesAllowListedDomainPacks(t *testing.T) {
	store, err := newSwitchableKnowledgeStore("../domain-packs", filepath.Join(t.TempDir(), "knowledge.lbug"), "commerce-demo")
	if err != nil {
		t.Fatalf("newSwitchableKnowledgeStore: %v", err)
	}
	defer store.Close()

	pack, release, err := store.ActivePack()
	if err != nil || pack != "commerce-demo" || release.Domain != "commerce-order-recovery" || release.ReleaseID != "commerce-us-2026-08-20" {
		t.Fatalf("initial pack=%q release=%+v err=%v", pack, release, err)
	}

	release, err = store.Activate(context.Background(), "it-access-demo")
	if err != nil || release.Domain != "it-access-governance" || release.ReleaseID != "it-access-2026-08-20" {
		t.Fatalf("IT activation release=%+v err=%v", release, err)
	}
	pack, release, err = store.ActivePack()
	if err != nil || pack != "it-access-demo" || release.TenantID != "it-demo" {
		t.Fatalf("active IT pack=%q release=%+v err=%v", pack, release, err)
	}

	if _, err := store.Activate(context.Background(), "not-allow-listed"); err == nil {
		t.Fatal("activation accepted an unknown domain pack")
	}
	pack, _, err = store.ActivePack()
	if err != nil || pack != "it-access-demo" {
		t.Fatalf("failed activation changed active pack: pack=%q err=%v", pack, err)
	}
}

func TestSwitchableStoreActivatesChangedCommercePolicy(t *testing.T) {
	store, err := newSwitchableKnowledgeStore("../domain-packs", filepath.Join(t.TempDir(), "knowledge.lbug"), "commerce-demo")
	if err != nil {
		t.Fatalf("newSwitchableKnowledgeStore: %v", err)
	}
	defer store.Close()

	release, err := store.Activate(context.Background(), "commerce-demo-policy-changed")
	if err != nil || release.ReleaseID != "commerce-us-2026-08-20-policy-change" || release.Domain != "commerce-order-recovery" {
		t.Fatalf("changed commerce activation release=%+v err=%v", release, err)
	}
}
