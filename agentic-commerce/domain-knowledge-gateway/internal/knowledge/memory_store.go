package knowledge

import (
	"context"
	"fmt"
	"math"
	"sort"
	"strings"
	"sync"
	"time"
)

// MemoryStore is the conformance/reference implementation used by unit tests
// and non-CGO builds. The Ladybug adapter loads the same canonical pack and
// must return equivalent public results.
type MemoryStore struct {
	mu        sync.RWMutex
	pack      DomainPack
	sources   map[string]Source
	sections  map[string]Section
	relations []Relation
}

func NewMemoryStore(pack DomainPack) (*MemoryStore, error) {
	if err := ValidatePack(&pack); err != nil {
		return nil, err
	}
	sources := make(map[string]Source, len(pack.Sources))
	for _, source := range pack.Sources {
		sources[source.SourceID] = source
	}
	sections := make(map[string]Section, len(pack.Sections))
	for _, section := range pack.Sections {
		sections[section.SectionID] = section
	}
	return &MemoryStore{pack: pack, sources: sources, sections: sections, relations: append([]Relation(nil), pack.Relations...)}, nil
}

func (s *MemoryStore) Close() error { return nil }

func (s *MemoryStore) GetReleaseInfo(ctx context.Context, releaseID string) (ReleaseInfo, error) {
	if err := ctx.Err(); err != nil {
		return ReleaseInfo{}, err
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if releaseID != "" && releaseID != s.pack.Release.ReleaseID {
		return ReleaseInfo{}, fmt.Errorf("%w: requested %q, active %q", ErrReleaseMismatch, releaseID, s.pack.Release.ReleaseID)
	}
	return s.pack.Release, nil
}

func (s *MemoryStore) LexicalSearch(ctx context.Context, spec CandidateSpec) ([]Candidate, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	sections, err := s.filteredSections(spec)
	if err != nil {
		return nil, err
	}
	queryTerms := tokenize(spec.Query, nil)
	if len(queryTerms) == 0 {
		return []Candidate{}, nil
	}
	docTerms := make([][]string, len(sections))
	docFrequency := make(map[string]int)
	totalLength := 0
	for i, section := range sections {
		docTerms[i] = tokenize(section.Title+" "+section.Content+" "+strings.Join(section.Tags, " "), nil)
		totalLength += len(docTerms[i])
		seen := map[string]struct{}{}
		for _, term := range docTerms[i] {
			seen[term] = struct{}{}
		}
		for term := range seen {
			docFrequency[term]++
		}
	}
	avgLength := float64(totalLength) / math.Max(1, float64(len(sections)))
	results := make([]Candidate, 0, len(sections))
	for i, section := range sections {
		frequencies := make(map[string]int)
		for _, term := range docTerms[i] {
			frequencies[term]++
		}
		var score float64
		matched := make([]string, 0, len(queryTerms))
		for _, term := range unique(queryTerms) {
			tf := float64(frequencies[term])
			if tf == 0 {
				continue
			}
			matched = append(matched, term)
			df := float64(docFrequency[term])
			idf := math.Log(1 + (float64(len(sections))-df+0.5)/(df+0.5))
			const k1, b = 1.2, 0.75
			normalized := tf + k1*(1-b+b*float64(len(docTerms[i]))/avgLength)
			score += idf * (tf * (k1 + 1)) / normalized
		}
		if score == 0 {
			continue
		}
		score *= 1 + float64(max(section.Priority, 0))/100
		results = append(results, s.candidate(section, score, "bm25", matched))
	}
	return rankCandidates(results, spec.Limit), nil
}

func (s *MemoryStore) VectorSearch(ctx context.Context, spec VectorSpec) ([]Candidate, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	sections, err := s.filteredSections(spec)
	if err != nil {
		return nil, err
	}
	queryVector := semanticVector(spec.Query, s.pack.SemanticAliases)
	results := make([]Candidate, 0, len(sections))
	for _, section := range sections {
		vector := semanticVector(section.Title+" "+section.Content+" "+strings.Join(section.Tags, " "), s.pack.SemanticAliases)
		score := cosine(queryVector, vector)
		if score <= 0 {
			continue
		}
		results = append(results, s.candidate(section, score, "semantic", nil))
	}
	return rankCandidates(results, spec.Limit), nil
}

func (s *MemoryStore) Traverse(ctx context.Context, spec TraversalSpec) ([]EvidencePath, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if spec.ReleaseID != "" && spec.ReleaseID != s.pack.Release.ReleaseID {
		return nil, ErrReleaseMismatch
	}
	if err := validateRequiredScope(spec.TenantID, spec.Jurisdiction, spec.Channel, spec.AsOf); err != nil {
		return nil, err
	}
	if !releaseActiveAt(s.pack.Release, spec.AsOf) {
		return nil, ErrReleaseInactive
	}
	if spec.TenantID != s.pack.TenantID {
		return []EvidencePath{}, nil
	}
	allowedSections := s.scopedSectionIDs(spec.TenantID, spec.Jurisdiction, spec.Channel, spec.AsOf)
	if spec.MaxDepth <= 0 {
		spec.MaxDepth = 1
	}
	if spec.MaxDepth > 3 {
		spec.MaxDepth = 3
	}
	if spec.Limit <= 0 || spec.Limit > 100 {
		spec.Limit = 30
	}
	allowedTypes := stringSet(spec.RelationTypes)
	frontier := append([]string(nil), spec.SectionIDs...)
	visited := stringSet(spec.SectionIDs)
	paths := make([]EvidencePath, 0)
	for depth := 1; depth <= spec.MaxDepth && len(frontier) > 0 && len(paths) < spec.Limit; depth++ {
		next := make([]string, 0)
		for _, current := range frontier {
			if _, allowed := allowedSections[current]; !allowed {
				continue
			}
			for _, relation := range s.relations {
				if relation.FromSectionID != current || (len(allowedTypes) > 0 && !contains(allowedTypes, relation.Type)) {
					continue
				}
				if _, allowed := allowedSections[relation.ToSectionID]; !allowed {
					continue
				}
				paths = append(paths, EvidencePath{
					From:         SectionRef{ReleaseID: s.pack.Release.ReleaseID, SectionID: relation.FromSectionID},
					RelationType: relation.Type,
					To:           SectionRef{ReleaseID: s.pack.Release.ReleaseID, SectionID: relation.ToSectionID},
					Depth:        depth,
				})
				if len(paths) >= spec.Limit {
					break
				}
				if !contains(visited, relation.ToSectionID) {
					visited[relation.ToSectionID] = struct{}{}
					next = append(next, relation.ToSectionID)
				}
			}
		}
		frontier = next
	}
	return paths, nil
}

func (s *MemoryStore) ReadExact(ctx context.Context, ref SectionRef) (ExactSection, error) {
	if err := ctx.Err(); err != nil {
		return ExactSection{}, err
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if ref.ReleaseID != "" && ref.ReleaseID != s.pack.Release.ReleaseID {
		return ExactSection{}, ErrReleaseMismatch
	}
	if err := validateRequiredScope(ref.TenantID, ref.Jurisdiction, ref.Channel, ref.AsOf); err != nil {
		return ExactSection{}, err
	}
	if !releaseActiveAt(s.pack.Release, ref.AsOf) {
		return ExactSection{}, ErrReleaseInactive
	}
	if ref.TenantID != s.pack.TenantID {
		return ExactSection{}, ErrNotFound
	}
	section, ok := s.sections[ref.SectionID]
	if !ok || !activeAt(section, ref.AsOf) || !scopeMatches(section.Jurisdiction, ref.Jurisdiction) || !scopeMatches(section.Channel, ref.Channel) {
		return ExactSection{}, ErrNotFound
	}
	return ExactSection{Release: s.pack.Release, Section: section, Source: s.sources[section.DocumentID]}, nil
}

func (s *MemoryStore) filteredSections(spec CandidateSpec) ([]Section, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if spec.ReleaseID != "" && spec.ReleaseID != s.pack.Release.ReleaseID {
		return nil, ErrReleaseMismatch
	}
	if err := validateRequiredScope(spec.TenantID, spec.Jurisdiction, spec.Channel, spec.AsOf); err != nil {
		return nil, err
	}
	if !releaseActiveAt(s.pack.Release, spec.AsOf) {
		return nil, ErrReleaseInactive
	}
	if spec.TenantID != "" && spec.TenantID != s.pack.TenantID {
		return []Section{}, nil
	}
	requiredTags := tagSet(spec.Tags)
	sections := make([]Section, 0, len(s.pack.Sections))
	for _, section := range s.pack.Sections {
		if !activeAt(section, spec.AsOf) || !scopeMatches(section.Jurisdiction, spec.Jurisdiction) || !scopeMatches(section.Channel, spec.Channel) {
			continue
		}
		if len(requiredTags) > 0 && !hasAny(section.Tags, requiredTags) {
			continue
		}
		sections = append(sections, section)
	}
	return sections, nil
}

func (s *MemoryStore) scopedSectionIDs(tenantID, jurisdiction, channel string, asOf time.Time) map[string]struct{} {
	allowed := make(map[string]struct{})
	if tenantID != s.pack.TenantID {
		return allowed
	}
	for _, section := range s.pack.Sections {
		if activeAt(section, asOf) && scopeMatches(section.Jurisdiction, jurisdiction) && scopeMatches(section.Channel, channel) {
			allowed[section.SectionID] = struct{}{}
		}
	}
	return allowed
}

func validateRequiredScope(tenantID, jurisdiction, channel string, asOf time.Time) error {
	if strings.TrimSpace(tenantID) == "" || strings.TrimSpace(jurisdiction) == "" || strings.TrimSpace(channel) == "" || asOf.IsZero() {
		return ErrInvalidScope
	}
	return nil
}

func (s *MemoryStore) candidate(section Section, score float64, retrieval string, matched []string) Candidate {
	return Candidate{Section: section, Source: s.sources[section.DocumentID], Score: score, Retrieval: retrieval, MatchedTerms: matched}
}

func rankCandidates(candidates []Candidate, limit int) []Candidate {
	sort.SliceStable(candidates, func(i, j int) bool {
		if candidates[i].Score == candidates[j].Score {
			if candidates[i].Section.Priority == candidates[j].Section.Priority {
				return candidates[i].Section.SectionID < candidates[j].Section.SectionID
			}
			return candidates[i].Section.Priority > candidates[j].Section.Priority
		}
		return candidates[i].Score > candidates[j].Score
	})
	if limit <= 0 || limit > 50 {
		limit = 10
	}
	if len(candidates) > limit {
		return candidates[:limit]
	}
	return candidates
}

func scopeMatches(value, requested string) bool {
	return requested == "" || strings.EqualFold(value, requested) || strings.EqualFold(value, "global") || strings.EqualFold(value, "all")
}

func hasAny(values []string, wanted map[string]struct{}) bool {
	for _, value := range values {
		if _, ok := wanted[canonicalTag(value)]; ok {
			return true
		}
	}
	return false
}
