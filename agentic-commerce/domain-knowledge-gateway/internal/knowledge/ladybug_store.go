//go:build ladybug

package knowledge

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"

	lbug "github.com/LadybugDB/go-ladybug"
)

// LadybugStore is a disposable graph projection of the canonical domain pack.
// Portable ranking remains in the reference implementation; graph existence,
// exact reads, and relation traversal are verified against LadybugDB.
type LadybugStore struct {
	*MemoryStore
	database   *lbug.Database
	connection *lbug.Connection
	mu         sync.Mutex
}

func NewConfiguredStore(pack DomainPack, path string) (KnowledgeStore, error) {
	if path == "" {
		path = ":memory:"
	}
	reference, err := NewMemoryStore(pack)
	if err != nil {
		return nil, err
	}
	config := lbug.DefaultSystemConfig()
	config.BufferPoolSize = 128 * 1024 * 1024
	config.MaxNumThreads = 2
	database, err := lbug.OpenDatabase(path, config)
	if err != nil {
		return nil, fmt.Errorf("open LadybugDB projection: %w", err)
	}
	connection, err := lbug.OpenConnection(database)
	if err != nil {
		database.Close()
		return nil, fmt.Errorf("open LadybugDB connection: %w", err)
	}
	store := &LadybugStore{MemoryStore: reference, database: database, connection: connection}
	if err := store.loadProjection(pack); err != nil {
		_ = store.Close()
		return nil, err
	}
	return store, nil
}

func StoreProviderName() string { return "ladybug-v0.17.0" }

func (s *LadybugStore) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.connection != nil {
		s.connection.Close()
		s.connection = nil
	}
	if s.database != nil {
		s.database.Close()
		s.database = nil
	}
	return nil
}

func (s *LadybugStore) ReadExact(ctx context.Context, ref SectionRef) (ExactSection, error) {
	if err := ctx.Err(); err != nil {
		return ExactSection{}, err
	}
	exact, err := s.MemoryStore.ReadExact(ctx, ref)
	if err != nil {
		return ExactSection{}, err
	}
	s.mu.Lock()
	statement, err := s.connection.Prepare("MATCH (section:Section {section_id: $section_id}) RETURN section.section_id AS section_id")
	if err != nil {
		s.mu.Unlock()
		return ExactSection{}, fmt.Errorf("prepare exact-section query: %w", err)
	}
	result, err := s.connection.Execute(statement, map[string]any{"section_id": ref.SectionID})
	statement.Close()
	if err != nil {
		s.mu.Unlock()
		return ExactSection{}, fmt.Errorf("query exact section: %w", err)
	}
	found := result.HasNext()
	result.Close()
	s.mu.Unlock()
	if !found {
		return ExactSection{}, ErrNotFound
	}
	return exact, nil
}

func (s *LadybugStore) Traverse(ctx context.Context, spec TraversalSpec) ([]EvidencePath, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	expected, err := s.MemoryStore.Traverse(ctx, spec)
	if err != nil {
		return expected, err
	}
	expectedSet := make(map[string]struct{}, len(expected))
	fromIDs := make([]string, 0, len(expected))
	seenFrom := make(map[string]struct{}, len(expected))
	for _, path := range expected {
		expectedSet[path.From.SectionID+"|"+path.RelationType+"|"+path.To.SectionID] = struct{}{}
		if _, seen := seenFrom[path.From.SectionID]; !seen {
			seenFrom[path.From.SectionID] = struct{}{}
			fromIDs = append(fromIDs, path.From.SectionID)
		}
	}
	found := make(map[string]struct{}, len(expected))
	s.mu.Lock()
	defer s.mu.Unlock()
	statement, err := s.connection.Prepare("MATCH (from:Section)-[link:LINK]->(to:Section) WHERE from.section_id = $section_id RETURN from.section_id AS from_id, link.relation_type AS relation_type, to.section_id AS to_id")
	if err != nil {
		return nil, fmt.Errorf("prepare traversal query: %w", err)
	}
	defer statement.Close()
	for _, sectionID := range fromIDs {
		result, queryErr := s.connection.Execute(statement, map[string]any{"section_id": sectionID})
		if queryErr != nil {
			return nil, fmt.Errorf("traverse section %q: %w", sectionID, queryErr)
		}
		for result.HasNext() {
			tuple, nextErr := result.Next()
			if nextErr != nil {
				result.Close()
				return nil, nextErr
			}
			row, mapErr := tuple.GetAsMap()
			tuple.Close()
			if mapErr != nil {
				result.Close()
				return nil, mapErr
			}
			key := fmt.Sprint(row["from_id"]) + "|" + fmt.Sprint(row["relation_type"]) + "|" + fmt.Sprint(row["to_id"])
			if _, allowed := expectedSet[key]; allowed {
				found[key] = struct{}{}
			}
		}
		result.Close()
	}
	paths := make([]EvidencePath, 0, len(expected))
	for _, path := range expected {
		key := path.From.SectionID + "|" + path.RelationType + "|" + path.To.SectionID
		if _, exists := found[key]; exists {
			paths = append(paths, path)
		}
	}
	return paths, nil
}

func (s *LadybugStore) loadProjection(pack DomainPack) error {
	schema := []string{
		"CREATE NODE TABLE IF NOT EXISTS Section(section_id STRING PRIMARY KEY, document_id STRING, title STRING, content STRING, jurisdiction STRING, channel STRING, effective_from STRING, effective_to STRING, priority INT64, tags_json STRING, facts_json STRING)",
		"CREATE REL TABLE IF NOT EXISTS LINK(FROM Section TO Section, relation_type STRING)",
	}
	for _, query := range schema {
		if err := s.exec(query, nil); err != nil {
			return fmt.Errorf("create LadybugDB schema: %w", err)
		}
	}
	sectionStatement, err := s.connection.Prepare("MERGE (section:Section {section_id: $section_id}) SET section.document_id=$document_id, section.title=$title, section.content=$content, section.jurisdiction=$jurisdiction, section.channel=$channel, section.effective_from=$effective_from, section.effective_to=$effective_to, section.priority=$priority, section.tags_json=$tags_json, section.facts_json=$facts_json")
	if err != nil {
		return fmt.Errorf("prepare section projection: %w", err)
	}
	defer sectionStatement.Close()
	for _, section := range pack.Sections {
		tags, _ := json.Marshal(section.Tags)
		facts, _ := json.Marshal(section.Facts)
		result, executeErr := s.connection.Execute(sectionStatement, map[string]any{
			"section_id": section.SectionID, "document_id": section.DocumentID, "title": section.Title, "content": section.Content,
			"jurisdiction": section.Jurisdiction, "channel": section.Channel, "effective_from": section.EffectiveFrom.Format("2006-01-02T15:04:05Z07:00"),
			"effective_to": formatOptionalTime(section.EffectiveTo), "priority": int64(section.Priority), "tags_json": string(tags), "facts_json": string(facts),
		})
		if executeErr != nil {
			return fmt.Errorf("project section %q: %w", section.SectionID, executeErr)
		}
		result.Close()
	}
	// The projection lives in an emptyDir for the demo. Re-opening within the
	// same pod is rare; avoid duplicate links by checking the exact triple.
	relationStatement, err := s.connection.Prepare("MATCH (from:Section {section_id: $from_id}), (to:Section {section_id: $to_id}) MERGE (from)-[link:LINK {relation_type: $relation_type}]->(to)")
	if err != nil {
		return fmt.Errorf("prepare relation projection: %w", err)
	}
	defer relationStatement.Close()
	for _, relation := range pack.Relations {
		result, executeErr := s.connection.Execute(relationStatement, map[string]any{"from_id": relation.FromSectionID, "to_id": relation.ToSectionID, "relation_type": strings.ToUpper(relation.Type)})
		if executeErr != nil {
			return fmt.Errorf("project relation: %w", executeErr)
		}
		result.Close()
	}
	return nil
}

func (s *LadybugStore) exec(query string, args map[string]any) error {
	if len(args) == 0 {
		result, err := s.connection.Query(query)
		if err != nil {
			return err
		}
		result.Close()
		return nil
	}
	statement, err := s.connection.Prepare(query)
	if err != nil {
		return err
	}
	defer statement.Close()
	result, err := s.connection.Execute(statement, args)
	if err != nil {
		return err
	}
	result.Close()
	return nil
}

func formatOptionalTime(value interface {
	IsZero() bool
	Format(string) string
}) string {
	if value.IsZero() {
		return ""
	}
	return value.Format("2006-01-02T15:04:05Z07:00")
}
