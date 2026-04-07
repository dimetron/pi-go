package palace

import (
	"context"
	"crypto/md5"
	"fmt"
	"strings"
	"time"
)

// KnowledgeGraph provides a high-level API for the temporal knowledge graph.
type KnowledgeGraph struct {
	store PalaceStore
}

// NewKnowledgeGraph creates a KnowledgeGraph wrapping the given store.
func NewKnowledgeGraph(store PalaceStore) *KnowledgeGraph {
	return &KnowledgeGraph{store: store}
}

// entityID normalises a name into an entity ID: lowercase, spaces to underscores, strip apostrophes.
func entityID(name string) string {
	s := strings.ToLower(strings.TrimSpace(name))
	s = strings.ReplaceAll(s, "'", "")
	s = strings.ReplaceAll(s, " ", "_")
	return s
}

// tripleID generates a deterministic triple ID from subject, predicate, and object.
func tripleID(subj, pred, obj string) string {
	h := md5.Sum([]byte(subj + ":" + pred + ":" + obj))
	return fmt.Sprintf("t_%s_%s_%s_%x", subj, pred, obj, h[:4])
}

// Add creates a triple, auto-creating entities if they don't exist.
// If an identical active triple already exists (same subj/pred/obj with valid_to IS NULL),
// it returns the existing triple rather than inserting a duplicate.
func (kg *KnowledgeGraph) Add(ctx context.Context, input TripleInput) (*Triple, error) {
	if input.Subject == "" || input.Predicate == "" || input.Object == "" {
		return nil, fmt.Errorf("palace: kg: subject, predicate, and object are required")
	}

	subjID := entityID(input.Subject)
	predID := entityID(input.Predicate)
	objID := entityID(input.Object)
	now := time.Now().UTC()

	// Auto-create entities (INSERT OR IGNORE).
	if err := kg.store.InsertEntity(ctx, &Entity{ID: subjID, Name: input.Subject, CreatedAt: now}); err != nil {
		return nil, fmt.Errorf("palace: kg: create subject entity: %w", err)
	}
	if err := kg.store.InsertEntity(ctx, &Entity{ID: objID, Name: input.Object, CreatedAt: now}); err != nil {
		return nil, fmt.Errorf("palace: kg: create object entity: %w", err)
	}

	// Check for existing active triple (same subj/pred/obj, valid_to IS NULL).
	existing, err := kg.store.QueryTriples(ctx, subjID, "", "subject")
	if err != nil {
		return nil, fmt.Errorf("palace: kg: check existing: %w", err)
	}
	for _, t := range existing {
		if t.PredicateID == predID && t.ObjectID == objID && t.ValidTo == nil {
			return t, nil // idempotent: return existing active triple
		}
	}

	tid := tripleID(subjID, predID, objID)
	triple := &Triple{
		ID:          tid,
		SubjectID:   subjID,
		PredicateID: predID,
		ObjectID:    objID,
		ValidFrom:   input.ValidFrom,
		ExtractedAt: now,
	}

	if err := kg.store.InsertTriple(ctx, triple); err != nil {
		return nil, fmt.Errorf("palace: kg: insert triple: %w", err)
	}
	return triple, nil
}

// Query returns triples involving the named entity. If asOf is non-empty, it
// filters to triples valid at that point in time. Direction can be "subject",
// "object", or "" (both).
func (kg *KnowledgeGraph) Query(ctx context.Context, entity string, asOf string, direction string) ([]*Triple, error) {
	if entity == "" {
		return nil, fmt.Errorf("palace: kg: entity is required")
	}
	eid := entityID(entity)
	return kg.store.QueryTriples(ctx, eid, asOf, direction)
}

// Invalidate sets valid_to = now() on the active triple matching subject/predicate/object.
func (kg *KnowledgeGraph) Invalidate(ctx context.Context, subject, predicate, object string) error {
	if subject == "" || predicate == "" || object == "" {
		return fmt.Errorf("palace: kg: subject, predicate, and object are required")
	}
	return kg.store.InvalidateTriple(ctx, entityID(subject), entityID(predicate), entityID(object))
}

// Timeline returns all triples for the named entity ordered chronologically.
func (kg *KnowledgeGraph) Timeline(ctx context.Context, entity string) ([]*Triple, error) {
	if entity == "" {
		return nil, fmt.Errorf("palace: kg: entity is required")
	}
	return kg.store.TimelineTriples(ctx, entityID(entity))
}

// Stats returns aggregate KG statistics.
func (kg *KnowledgeGraph) Stats(ctx context.Context) (*KGStats, error) {
	return kg.store.KGStats(ctx)
}
