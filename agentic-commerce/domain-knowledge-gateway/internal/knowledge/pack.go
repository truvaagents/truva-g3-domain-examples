package knowledge

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

var (
	ErrNotFound        = errors.New("knowledge record not found")
	ErrInvalidPack     = errors.New("invalid domain pack")
	ErrReleaseMismatch = errors.New("release mismatch")
	ErrInvalidScope    = errors.New("tenant, jurisdiction, channel, and as_of are required")
	ErrReleaseInactive = errors.New("release is not valid at the requested time")
)

func LoadPack(path string) (DomainPack, error) {
	cleanPath := filepath.Clean(path)
	if filepath.Base(cleanPath) != "pack.json" {
		return DomainPack{}, fmt.Errorf("%w: domain pack path must name pack.json", ErrInvalidPack)
	}
	packRoot, err := os.OpenRoot(filepath.Dir(cleanPath))
	if err != nil {
		return DomainPack{}, fmt.Errorf("open domain pack root: %w", err)
	}
	defer func() { _ = packRoot.Close() }()
	data, err := packRoot.ReadFile("pack.json")
	if err != nil {
		return DomainPack{}, fmt.Errorf("read domain pack: %w", err)
	}
	var pack DomainPack
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&pack); err != nil {
		return DomainPack{}, fmt.Errorf("decode domain pack: %w", err)
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return DomainPack{}, fmt.Errorf("decode domain pack: expected one JSON object")
	}
	declaredHash := strings.TrimSpace(pack.Release.ContentHash)
	if err := ValidatePack(&pack); err != nil {
		return DomainPack{}, err
	}
	canonical, err := canonicalPackBytes(pack)
	if err != nil {
		return DomainPack{}, fmt.Errorf("canonicalize domain pack: %w", err)
	}
	sum := sha256.Sum256(canonical)
	computedHash := "sha256:" + hex.EncodeToString(sum[:])
	if declaredHash != "" && declaredHash != computedHash {
		return DomainPack{}, fmt.Errorf("%w: declared content hash %q does not match %q", ErrInvalidPack, declaredHash, computedHash)
	}
	pack.Release.ContentHash = computedHash
	pack.Release.Domain = pack.Domain
	pack.Release.TenantID = pack.TenantID
	pack.Release.SectionCount = len(pack.Sections)
	pack.Release.SourceCount = len(pack.Sources)
	return pack, nil
}

func ValidatePack(pack *DomainPack) error {
	if pack == nil {
		return fmt.Errorf("%w: pack is nil", ErrInvalidPack)
	}
	if pack.SchemaVersion != "1.0" {
		return fmt.Errorf("%w: unsupported schema_version %q", ErrInvalidPack, pack.SchemaVersion)
	}
	if strings.TrimSpace(pack.Domain) == "" || strings.TrimSpace(pack.TenantID) == "" || strings.TrimSpace(pack.Release.ReleaseID) == "" {
		return fmt.Errorf("%w: domain, tenant_id, and release.release_id are required", ErrInvalidPack)
	}
	if pack.Release.PublishedAt.IsZero() || pack.Release.ValidFrom.IsZero() {
		return fmt.Errorf("%w: release timestamps are required", ErrInvalidPack)
	}
	if !pack.Release.ValidTo.IsZero() && !pack.Release.ValidFrom.Before(pack.Release.ValidTo) {
		return fmt.Errorf("%w: release valid_to must be after valid_from", ErrInvalidPack)
	}
	if len(pack.SemanticAliases) == 0 {
		return fmt.Errorf("%w: semantic_aliases must define the domain vocabulary", ErrInvalidPack)
	}
	for term, alias := range pack.SemanticAliases {
		if strings.TrimSpace(term) == "" || strings.TrimSpace(alias) == "" || term != strings.ToLower(term) || alias != strings.ToLower(alias) || strings.ContainsAny(term+alias, " \t\r\n") {
			return fmt.Errorf("%w: semantic aliases must be non-empty lowercase single terms", ErrInvalidPack)
		}
	}
	sourceIDs := make(map[string]struct{}, len(pack.Sources))
	for _, source := range pack.Sources {
		if source.SourceID == "" || source.Title == "" || source.URL == "" || source.RetrievedAt.IsZero() {
			return fmt.Errorf("%w: source identity, URL, and retrieved_at are required", ErrInvalidPack)
		}
		if source.RetrievedAt.After(pack.Release.PublishedAt) {
			return fmt.Errorf("%w: source %q was retrieved after release publication", ErrInvalidPack, source.SourceID)
		}
		if _, duplicate := sourceIDs[source.SourceID]; duplicate {
			return fmt.Errorf("%w: duplicate source %q", ErrInvalidPack, source.SourceID)
		}
		sourceIDs[source.SourceID] = struct{}{}
	}
	sectionIDs := make(map[string]struct{}, len(pack.Sections))
	for _, section := range pack.Sections {
		if section.SectionID == "" || section.DocumentID == "" || strings.TrimSpace(section.Content) == "" || section.EffectiveFrom.IsZero() {
			return fmt.Errorf("%w: incomplete section %q", ErrInvalidPack, section.SectionID)
		}
		if _, ok := sourceIDs[section.DocumentID]; !ok {
			return fmt.Errorf("%w: section %q references unknown source %q", ErrInvalidPack, section.SectionID, section.DocumentID)
		}
		if _, duplicate := sectionIDs[section.SectionID]; duplicate {
			return fmt.Errorf("%w: duplicate section %q", ErrInvalidPack, section.SectionID)
		}
		if !section.EffectiveTo.IsZero() && !section.EffectiveFrom.Before(section.EffectiveTo) {
			return fmt.Errorf("%w: section %q effective_to must be after effective_from", ErrInvalidPack, section.SectionID)
		}
		sectionIDs[section.SectionID] = struct{}{}
	}
	for _, relation := range pack.Relations {
		if _, ok := sectionIDs[relation.FromSectionID]; !ok {
			return fmt.Errorf("%w: relation references unknown from section %q", ErrInvalidPack, relation.FromSectionID)
		}
		if _, ok := sectionIDs[relation.ToSectionID]; !ok {
			return fmt.Errorf("%w: relation references unknown to section %q", ErrInvalidPack, relation.ToSectionID)
		}
		if strings.TrimSpace(relation.Type) == "" {
			return fmt.Errorf("%w: relation type is required", ErrInvalidPack)
		}
	}
	caseIDs := make(map[string]struct{}, len(pack.GoldenCases))
	for _, golden := range pack.GoldenCases {
		if golden.CaseID == "" || strings.TrimSpace(golden.Query) == "" || golden.Jurisdiction == "" || golden.Channel == "" || golden.AsOf.IsZero() || len(golden.ExpectedSectionIDs) == 0 {
			return fmt.Errorf("%w: golden case identity, query, scope, as_of, and expected sections are required", ErrInvalidPack)
		}
		if _, duplicate := caseIDs[golden.CaseID]; duplicate {
			return fmt.Errorf("%w: duplicate golden case %q", ErrInvalidPack, golden.CaseID)
		}
		caseIDs[golden.CaseID] = struct{}{}
		if !releaseActiveAt(pack.Release, golden.AsOf) {
			return fmt.Errorf("%w: golden case %q is outside release availability", ErrInvalidPack, golden.CaseID)
		}
		for _, sectionID := range append(append([]string(nil), golden.ExpectedSectionIDs...), golden.ForbiddenSectionIDs...) {
			if _, exists := sectionIDs[sectionID]; !exists {
				return fmt.Errorf("%w: golden case %q references unknown section %q", ErrInvalidPack, golden.CaseID, sectionID)
			}
		}
	}
	return nil
}

func canonicalPackBytes(pack DomainPack) ([]byte, error) {
	pack.Release.ContentHash = ""
	pack.Release.Domain = pack.Domain
	pack.Release.TenantID = pack.TenantID
	pack.Release.SectionCount = len(pack.Sections)
	pack.Release.SourceCount = len(pack.Sources)
	sort.Slice(pack.Sources, func(i, j int) bool { return pack.Sources[i].SourceID < pack.Sources[j].SourceID })
	sort.Slice(pack.Sections, func(i, j int) bool { return pack.Sections[i].SectionID < pack.Sections[j].SectionID })
	sort.Slice(pack.Relations, func(i, j int) bool {
		left := pack.Relations[i].FromSectionID + pack.Relations[i].Type + pack.Relations[i].ToSectionID
		right := pack.Relations[j].FromSectionID + pack.Relations[j].Type + pack.Relations[j].ToSectionID
		return left < right
	})
	return json.Marshal(pack)
}

func activeAt(section Section, asOf time.Time) bool {
	if asOf.IsZero() {
		asOf = time.Now().UTC()
	}
	if asOf.Before(section.EffectiveFrom) {
		return false
	}
	return section.EffectiveTo.IsZero() || asOf.Before(section.EffectiveTo)
}

func releaseActiveAt(release ReleaseInfo, asOf time.Time) bool {
	if asOf.IsZero() || asOf.Before(release.PublishedAt) || asOf.Before(release.ValidFrom) {
		return false
	}
	return release.ValidTo.IsZero() || asOf.Before(release.ValidTo)
}
