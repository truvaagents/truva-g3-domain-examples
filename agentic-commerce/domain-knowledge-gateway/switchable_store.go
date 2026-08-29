package main

import (
	"context"
	"fmt"
	"path/filepath"
	"sync"

	"github.com/truvaagents/truva-g3-domain-examples/agentic-commerce/domain-knowledge-gateway/internal/knowledge"
)

var demoPackDirectories = map[string]string{
	"commerce-demo":                "commerce-demo",
	"commerce-demo-policy-changed": "commerce-demo-policy-changed",
	"it-access-demo":               "it-access-demo",
}

// switchableKnowledgeStore keeps domain-pack activation behind the existing
// provider-neutral KnowledgeStore contract. Calls hold a read lock for their
// full duration, so an activated Ladybug projection is never closed in use.
type switchableKnowledgeStore struct {
	mu            sync.RWMutex
	switchMu      sync.Mutex
	active        knowledge.KnowledgeStore
	activePack    string
	packRoot      string
	projectionDir string
}

func newSwitchableKnowledgeStore(packRoot, projectionPath, initialPack string) (*switchableKnowledgeStore, error) {
	store := &switchableKnowledgeStore{
		packRoot:      filepath.Clean(packRoot),
		projectionDir: filepath.Dir(filepath.Clean(projectionPath)),
	}
	if _, err := store.Activate(context.Background(), initialPack); err != nil {
		return nil, err
	}
	return store, nil
}

func (s *switchableKnowledgeStore) Activate(ctx context.Context, packName string) (knowledge.ReleaseInfo, error) {
	if err := ctx.Err(); err != nil {
		return knowledge.ReleaseInfo{}, err
	}
	directory, allowed := demoPackDirectories[packName]
	if !allowed {
		return knowledge.ReleaseInfo{}, fmt.Errorf("unknown allow-listed domain pack %q", packName)
	}
	s.switchMu.Lock()
	defer s.switchMu.Unlock()
	s.mu.RLock()
	if packName == s.activePack && s.active != nil {
		release, err := s.active.GetReleaseInfo(ctx, "")
		s.mu.RUnlock()
		return release, err
	}
	s.mu.RUnlock()
	pack, err := knowledge.LoadPack(filepath.Join(s.packRoot, directory, "pack.json"))
	if err != nil {
		return knowledge.ReleaseInfo{}, err
	}
	projectionPath := filepath.Join(s.projectionDir, "knowledge-"+directory+".lbug")
	next, err := knowledge.NewConfiguredStore(pack, projectionPath)
	if err != nil {
		return knowledge.ReleaseInfo{}, err
	}
	release, err := next.GetReleaseInfo(ctx, "")
	if err != nil {
		_ = next.Close()
		return knowledge.ReleaseInfo{}, err
	}
	s.mu.Lock()
	previous := s.active
	s.active = next
	s.activePack = packName
	s.mu.Unlock()
	if previous != nil {
		_ = previous.Close()
	}
	return release, nil
}

func (s *switchableKnowledgeStore) ActivePack() (string, knowledge.ReleaseInfo, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.active == nil {
		return "", knowledge.ReleaseInfo{}, fmt.Errorf("no active domain pack")
	}
	release, err := s.active.GetReleaseInfo(context.Background(), "")
	return s.activePack, release, err
}

func (s *switchableKnowledgeStore) GetReleaseInfo(ctx context.Context, releaseID string) (knowledge.ReleaseInfo, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.active.GetReleaseInfo(ctx, releaseID)
}

func (s *switchableKnowledgeStore) LexicalSearch(ctx context.Context, spec knowledge.CandidateSpec) ([]knowledge.Candidate, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.active.LexicalSearch(ctx, spec)
}

func (s *switchableKnowledgeStore) VectorSearch(ctx context.Context, spec knowledge.VectorSpec) ([]knowledge.Candidate, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.active.VectorSearch(ctx, spec)
}

func (s *switchableKnowledgeStore) Traverse(ctx context.Context, spec knowledge.TraversalSpec) ([]knowledge.EvidencePath, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.active.Traverse(ctx, spec)
}

func (s *switchableKnowledgeStore) ReadExact(ctx context.Context, ref knowledge.SectionRef) (knowledge.ExactSection, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.active.ReadExact(ctx, ref)
}

func (s *switchableKnowledgeStore) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.active == nil {
		return nil
	}
	err := s.active.Close()
	s.active = nil
	return err
}
