package palace

import (
	"context"
	"fmt"
	"strings"
	"time"

	"google.golang.org/adk/v2/agent"
	"google.golang.org/adk/v2/tool"
	"google.golang.org/adk/v2/tool/functiontool"
)

// --- palace-kg-add ---

// KGAddToolInput defines parameters for the palace-kg-add tool.
type KGAddToolInput struct {
	// The subject entity name.
	Subject string `json:"subject"`
	// The predicate (relationship type).
	Predicate string `json:"predicate"`
	// The object entity name.
	Object string `json:"object"`
	// Optional validity start date (RFC3339 or YYYY-MM-DD).
	ValidFrom string `json:"valid_from,omitempty"`
}

// KGAddToolOutput contains the result of adding a triple.
type KGAddToolOutput struct {
	Content string `json:"content"`
}

func newPalaceKGAddTool(p *Palace) (tool.Tool, error) {
	return functiontool.New(functiontool.Config{
		Name:        "palace-kg-add",
		Description: "Add a fact (triple) to the knowledge graph. A triple consists of subject-predicate-object, e.g. 'Alice works_on auth-service'. Entities are auto-created. Duplicate active triples are detected.",
	}, func(ctx agent.Context, input KGAddToolInput) (KGAddToolOutput, error) {
		return palaceKGAddHandler(ctx, p, input)
	})
}

func palaceKGAddHandler(ctx context.Context, p *Palace, input KGAddToolInput) (KGAddToolOutput, error) {
	if input.Subject == "" {
		return KGAddToolOutput{Content: "Error: subject is required"}, nil
	}
	if input.Predicate == "" {
		return KGAddToolOutput{Content: "Error: predicate is required"}, nil
	}
	if input.Object == "" {
		return KGAddToolOutput{Content: "Error: object is required"}, nil
	}

	ti := TripleInput{
		Subject:   input.Subject,
		Predicate: input.Predicate,
		Object:    input.Object,
	}

	if input.ValidFrom != "" {
		t, err := parseFlexibleTime(input.ValidFrom)
		if err != nil {
			return KGAddToolOutput{Content: fmt.Sprintf("Error: invalid valid_from: %v", err)}, nil
		}
		ti.ValidFrom = &t
	}

	triple, err := p.KGAdd(ctx, ti)
	if err != nil {
		return KGAddToolOutput{Content: fmt.Sprintf("Error: %v", err)}, nil
	}

	return KGAddToolOutput{
		Content: fmt.Sprintf("Triple added: %s → %s → %s (id=%s)", triple.SubjectID, triple.PredicateID, triple.ObjectID, triple.ID),
	}, nil
}

// --- palace-kg-query ---

// KGQueryToolInput defines parameters for the palace-kg-query tool.
type KGQueryToolInput struct {
	// The entity name to query.
	Entity string `json:"entity"`
	// Optional point-in-time filter (RFC3339 or YYYY-MM-DD).
	AsOf string `json:"as_of,omitempty"`
	// Direction: "subject", "object", or "both" (default "both").
	Direction string `json:"direction,omitempty"`
}

// KGQueryToolOutput contains formatted query results.
type KGQueryToolOutput struct {
	Content string `json:"content"`
}

func newPalaceKGQueryTool(p *Palace) (tool.Tool, error) {
	return functiontool.New(functiontool.Config{
		Name:        "palace-kg-query",
		Description: "Query the knowledge graph for facts involving an entity. Returns triples where the entity appears as subject and/or object. Supports point-in-time queries with as_of parameter.",
	}, func(ctx agent.Context, input KGQueryToolInput) (KGQueryToolOutput, error) {
		return palaceKGQueryHandler(ctx, p, input)
	})
}

func palaceKGQueryHandler(ctx context.Context, p *Palace, input KGQueryToolInput) (KGQueryToolOutput, error) {
	if input.Entity == "" {
		return KGQueryToolOutput{Content: "Error: entity is required"}, nil
	}

	triples, err := p.KGQuery(ctx, input.Entity, input.AsOf, input.Direction)
	if err != nil {
		return KGQueryToolOutput{Content: fmt.Sprintf("Error: %v", err)}, nil
	}

	if len(triples) == 0 {
		return KGQueryToolOutput{Content: fmt.Sprintf("No facts found for entity %q.", input.Entity)}, nil
	}

	var sb strings.Builder
	fmt.Fprintf(&sb, "## Facts for %q (%d)\n\n", input.Entity, len(triples))
	fmt.Fprintf(&sb, "| Subject | Predicate | Object | Valid From | Valid To |\n")
	fmt.Fprintf(&sb, "|---------|-----------|--------|------------|----------|\n")

	for _, t := range triples {
		vf := "-"
		if t.ValidFrom != nil {
			vf = t.ValidFrom.Format("2006-01-02")
		}
		vt := "active"
		if t.ValidTo != nil {
			vt = t.ValidTo.Format("2006-01-02")
		}
		fmt.Fprintf(&sb, "| %s | %s | %s | %s | %s |\n", t.SubjectID, t.PredicateID, t.ObjectID, vf, vt)
	}

	return KGQueryToolOutput{Content: sb.String()}, nil
}

// --- palace-kg-invalidate ---

// KGInvalidateToolInput defines parameters for the palace-kg-invalidate tool.
type KGInvalidateToolInput struct {
	// The subject entity name.
	Subject string `json:"subject"`
	// The predicate (relationship type).
	Predicate string `json:"predicate"`
	// The object entity name.
	Object string `json:"object"`
}

// KGInvalidateToolOutput contains the result.
type KGInvalidateToolOutput struct {
	Content string `json:"content"`
}

func newPalaceKGInvalidateTool(p *Palace) (tool.Tool, error) {
	return functiontool.New(functiontool.Config{
		Name:        "palace-kg-invalidate",
		Description: "Invalidate (end) an active fact in the knowledge graph. Sets the valid_to timestamp to now, marking the triple as no longer current. The fact remains in the timeline for historical queries.",
	}, func(ctx agent.Context, input KGInvalidateToolInput) (KGInvalidateToolOutput, error) {
		return palaceKGInvalidateHandler(ctx, p, input)
	})
}

func palaceKGInvalidateHandler(ctx context.Context, p *Palace, input KGInvalidateToolInput) (KGInvalidateToolOutput, error) {
	if input.Subject == "" || input.Predicate == "" || input.Object == "" {
		return KGInvalidateToolOutput{Content: "Error: subject, predicate, and object are all required"}, nil
	}

	err := p.KGInvalidate(ctx, input.Subject, input.Predicate, input.Object)
	if err != nil {
		return KGInvalidateToolOutput{Content: fmt.Sprintf("Error: %v", err)}, nil
	}

	return KGInvalidateToolOutput{
		Content: fmt.Sprintf("Invalidated: %s → %s → %s", input.Subject, input.Predicate, input.Object),
	}, nil
}

// --- palace-kg-timeline ---

// KGTimelineToolInput defines parameters for the palace-kg-timeline tool.
type KGTimelineToolInput struct {
	// The entity name to get timeline for.
	Entity string `json:"entity"`
}

// KGTimelineToolOutput contains formatted timeline.
type KGTimelineToolOutput struct {
	Content string `json:"content"`
}

func newPalaceKGTimelineTool(p *Palace) (tool.Tool, error) {
	return functiontool.New(functiontool.Config{
		Name:        "palace-kg-timeline",
		Description: "View the chronological timeline of all facts (including invalidated ones) for an entity. Useful for understanding how relationships evolved over time.",
	}, func(ctx agent.Context, input KGTimelineToolInput) (KGTimelineToolOutput, error) {
		return palaceKGTimelineHandler(ctx, p, input)
	})
}

func palaceKGTimelineHandler(ctx context.Context, p *Palace, input KGTimelineToolInput) (KGTimelineToolOutput, error) {
	if input.Entity == "" {
		return KGTimelineToolOutput{Content: "Error: entity is required"}, nil
	}

	triples, err := p.KGTimeline(ctx, input.Entity)
	if err != nil {
		return KGTimelineToolOutput{Content: fmt.Sprintf("Error: %v", err)}, nil
	}

	if len(triples) == 0 {
		return KGTimelineToolOutput{Content: fmt.Sprintf("No timeline for entity %q.", input.Entity)}, nil
	}

	var sb strings.Builder
	fmt.Fprintf(&sb, "## Timeline for %q (%d entries)\n\n", input.Entity, len(triples))
	fmt.Fprintf(&sb, "| Date | Subject | Predicate | Object | Status |\n")
	fmt.Fprintf(&sb, "|------|---------|-----------|--------|--------|\n")

	for _, t := range triples {
		date := t.ExtractedAt.Format("2006-01-02")
		status := "active"
		if t.ValidTo != nil {
			status = "ended " + t.ValidTo.Format("2006-01-02")
		}
		fmt.Fprintf(&sb, "| %s | %s | %s | %s | %s |\n", date, t.SubjectID, t.PredicateID, t.ObjectID, status)
	}

	return KGTimelineToolOutput{Content: sb.String()}, nil
}

// parseFlexibleTime parses a time string in RFC3339 or YYYY-MM-DD format.
func parseFlexibleTime(s string) (time.Time, error) {
	if t, err := time.Parse(time.RFC3339, s); err == nil {
		return t, nil
	}
	return time.Parse("2006-01-02", s)
}
