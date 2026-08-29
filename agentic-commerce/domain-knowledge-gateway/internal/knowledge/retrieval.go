package knowledge

import (
	"context"
	"hash/fnv"
	"math"
	"regexp"
	"sort"
	"strings"
)

var wordPattern = regexp.MustCompile(`[a-z0-9]+`)

var stopWords = map[string]struct{}{
	"a": {}, "an": {}, "and": {}, "are": {}, "as": {}, "at": {}, "be": {}, "by": {}, "for": {}, "from": {}, "in": {}, "is": {}, "it": {}, "of": {}, "on": {}, "or": {}, "that": {}, "the": {}, "this": {}, "to": {}, "was": {}, "with": {},
}

// HybridSearch applies hard scope and temporal filters inside each store call,
// then fuses independent lexical and semantic rankings using RRF. No LLM is
// permitted to invent or alter citations.
func HybridSearch(ctx context.Context, store KnowledgeStore, spec CandidateSpec) (SearchResult, error) {
	release, err := store.GetReleaseInfo(ctx, spec.ReleaseID)
	if err != nil {
		return SearchResult{}, err
	}
	if spec.ReleaseID == "" {
		spec.ReleaseID = release.ReleaseID
	}
	perRetrieverLimit := spec.Limit * 3
	if perRetrieverLimit < 15 {
		perRetrieverLimit = 15
	}
	lexicalSpec := spec
	lexicalSpec.Limit = perRetrieverLimit
	semanticSpec := spec
	semanticSpec.Limit = perRetrieverLimit
	lexical, err := store.LexicalSearch(ctx, lexicalSpec)
	if err != nil {
		return SearchResult{}, err
	}
	semantic, err := store.VectorSearch(ctx, semanticSpec)
	if err != nil {
		return SearchResult{}, err
	}
	fused := reciprocalRankFusion(lexical, semantic)
	fused = preferExplicitSourceMatches(spec.Query, fused)
	limit := spec.Limit
	if limit <= 0 || limit > 20 {
		limit = 8
	}
	if len(fused) > limit {
		fused = fused[:limit]
	}
	ids := make([]string, 0, len(fused))
	for _, candidate := range fused {
		ids = append(ids, candidate.Section.SectionID)
	}
	paths, err := store.Traverse(ctx, TraversalSpec{ReleaseID: release.ReleaseID, TenantID: spec.TenantID, Jurisdiction: spec.Jurisdiction, Channel: spec.Channel, AsOf: spec.AsOf, SectionIDs: ids, MaxDepth: 1, Limit: 20})
	if err != nil {
		return SearchResult{}, err
	}
	warnings := []string{}
	if len(fused) == 0 {
		warnings = append(warnings, "no evidence matched the requested scope and effective date")
	}
	return SearchResult{Release: release, Candidates: fused, Paths: paths, Warnings: warnings}, nil
}

// preferExplicitSourceMatches resolves named-source queries without baking
// domain brands into retrieval code. If query terms identify one source within
// a source type (for example one carrier), candidates from peer sources with no
// identity-term match are removed. Other authority layers remain available.
func preferExplicitSourceMatches(query string, candidates []Candidate) []Candidate {
	queryTerms := stringSet(tokenize(query, nil))
	if len(queryTerms) == 0 {
		return candidates
	}
	affinity := make(map[string]int, len(candidates))
	maxByType := map[string]int{}
	for _, candidate := range candidates {
		key := candidate.Source.SourceID
		for _, term := range unique(tokenize(candidate.Source.Publisher+" "+candidate.Source.Title, nil)) {
			if contains(queryTerms, term) {
				affinity[key]++
			}
		}
		if affinity[key] > maxByType[candidate.Source.SourceType] {
			maxByType[candidate.Source.SourceType] = affinity[key]
		}
	}
	filtered := make([]Candidate, 0, len(candidates))
	for _, candidate := range candidates {
		maximum := maxByType[candidate.Source.SourceType]
		if maximum > 0 && affinity[candidate.Source.SourceID] == 0 {
			continue
		}
		filtered = append(filtered, candidate)
	}
	return filtered
}

func reciprocalRankFusion(rankings ...[]Candidate) []Candidate {
	const k = 60.0
	type fusedCandidate struct {
		candidate Candidate
		score     float64
		methods   map[string]struct{}
	}
	fused := map[string]*fusedCandidate{}
	for _, ranking := range rankings {
		for index, candidate := range ranking {
			entry := fused[candidate.Section.SectionID]
			if entry == nil {
				copy := candidate
				entry = &fusedCandidate{candidate: copy, methods: map[string]struct{}{}}
				fused[candidate.Section.SectionID] = entry
			}
			entry.score += 1 / (k + float64(index+1))
			entry.methods[candidate.Retrieval] = struct{}{}
			entry.candidate.MatchedTerms = unique(append(entry.candidate.MatchedTerms, candidate.MatchedTerms...))
		}
	}
	results := make([]Candidate, 0, len(fused))
	for _, entry := range fused {
		methods := make([]string, 0, len(entry.methods))
		for method := range entry.methods {
			methods = append(methods, method)
		}
		sort.Strings(methods)
		entry.candidate.Retrieval = "rrf(" + strings.Join(methods, "+") + ")"
		entry.candidate.Score = entry.score
		results = append(results, entry.candidate)
	}
	return rankCandidates(results, len(results))
}

func tokenize(text string, aliases map[string]string) []string {
	words := wordPattern.FindAllString(strings.ToLower(text), -1)
	terms := make([]string, 0, len(words))
	for _, word := range words {
		if _, skip := stopWords[word]; skip || len(word) < 2 {
			continue
		}
		if alias, ok := aliases[word]; ok {
			word = alias
		}
		word = stem(word)
		if alias, ok := aliases[word]; ok {
			word = alias
		}
		terms = append(terms, word)
	}
	return terms
}

func stem(word string) string {
	for _, suffix := range []string{"ments", "ment", "ingly", "edly", "ing", "ies", "ed", "es", "s"} {
		if strings.HasSuffix(word, suffix) && len(word) > len(suffix)+3 {
			if suffix == "ies" {
				return strings.TrimSuffix(word, suffix) + "y"
			}
			return strings.TrimSuffix(word, suffix)
		}
	}
	return word
}

func semanticVector(text string, aliases map[string]string) []float64 {
	const dimensions = 128
	vector := make([]float64, dimensions)
	terms := tokenize(text, aliases)
	for index, term := range terms {
		h := fnv.New64a()
		_, _ = h.Write([]byte(term))
		bucket := int(h.Sum64() % dimensions)
		vector[bucket] += 1
		if index > 0 {
			bigram := terms[index-1] + ":" + term
			h.Reset()
			_, _ = h.Write([]byte(bigram))
			vector[int(h.Sum64()%dimensions)] += 0.5
		}
	}
	return vector
}

func cosine(left, right []float64) float64 {
	var dot, leftNorm, rightNorm float64
	for i := range left {
		dot += left[i] * right[i]
		leftNorm += left[i] * left[i]
		rightNorm += right[i] * right[i]
	}
	if leftNorm == 0 || rightNorm == 0 {
		return 0
	}
	return dot / (math.Sqrt(leftNorm) * math.Sqrt(rightNorm))
}

func unique(values []string) []string {
	seen := map[string]struct{}{}
	result := make([]string, 0, len(values))
	for _, value := range values {
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		result = append(result, value)
	}
	return result
}

func stringSet(values []string) map[string]struct{} {
	set := make(map[string]struct{}, len(values))
	for _, value := range values {
		set[strings.ToLower(value)] = struct{}{}
	}
	return set
}

// tagSet normalizes equivalent human and machine spellings without embedding
// domain vocabulary in the retrieval engine. For example, "priority mail
// express", "priority_mail_express", and "priority-mail-express" identify the
// same controlled tag.
func tagSet(values []string) map[string]struct{} {
	set := make(map[string]struct{}, len(values))
	for _, value := range values {
		if tag := canonicalTag(value); tag != "" {
			set[tag] = struct{}{}
		}
	}
	return set
}

func canonicalTag(value string) string {
	return strings.Join(wordPattern.FindAllString(strings.ToLower(value), -1), "-")
}

func contains(set map[string]struct{}, value string) bool {
	_, ok := set[strings.ToLower(value)]
	return ok
}
