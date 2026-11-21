package palace

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	"google.golang.org/adk/v2/agent"
	"google.golang.org/adk/v2/tool"
	"google.golang.org/adk/v2/tool/functiontool"
)

// txMeta names who is writing: the agent and its session, when the tool runs
// inside an ADK invocation.
func txMeta(ctx context.Context) (author, session string) {
	rc, ok := ctx.(agent.ReadonlyContext)
	if !ok {
		return "", ""
	}
	if id := rc.SessionID(); id != "" {
		session = "session:" + id
	}
	return rc.AgentName(), session
}

// --- palace-kg-add ---

// KGAddToolInput defines parameters for the palace-kg-add tool.
type KGAddToolInput struct {
	// The subject entity name. Omit it when annotating a statement with about.
	Subject string `json:"subject,omitempty"`
	// The predicate (relationship type), e.g. works_on, confidence, note.
	Predicate string `json:"predicate"`
	// The object: an entity name, or a text value when literal is true.
	Object string `json:"object"`
	// Optional validity start date (RFC3339 or YYYY-MM-DD).
	ValidFrom string `json:"valid_from,omitempty"`
	// Optional validity end date, exclusive (RFC3339 or YYYY-MM-DD).
	ValidTo string `json:"valid_to,omitempty"`
	// Store the object as a text value rather than an entity.
	Literal bool `json:"literal,omitempty"`
	// Statement id (the number in e12) to annotate instead of an entity subject.
	About int64 `json:"about,omitempty"`
	// Statement ids this fact is concluded from; it is retracted automatically
	// if any of them is retracted or corrected.
	Support []int64 `json:"support,omitempty"`
	// How sure you are, 0 to 1.
	Confidence *float64 `json:"confidence,omitempty"`
	// Where the fact came from (file, URL, person, test).
	Source string `json:"source,omitempty"`
	// When the fact is already known, record this as a confirmation.
	ConfirmExisting bool `json:"confirm_existing,omitempty"`
}

// KGAddToolOutput contains the result of adding a triple.
type KGAddToolOutput struct {
	Content string `json:"content"`
}

func newPalaceKGAddTool(p *Palace) (tool.Tool, error) {
	return functiontool.New(functiontool.Config{
		Name: "palace-kg-add",
		Description: "Add a fact (subject-predicate-object) to the knowledge graph, e.g. 'Alice works_on auth-service'. " +
			"Every fact gets an id (e12). Record how sure you are (confidence) and where it came from (source); " +
			"annotate an existing fact with about=<id>; derive a conclusion from facts with support=[ids], so it is " +
			"withdrawn when they are. Entities are auto-created; a fact already known is returned, not duplicated.",
	}, func(ctx agent.Context, input KGAddToolInput) (KGAddToolOutput, error) {
		return palaceKGAddHandler(ctx, p, input)
	})
}

func palaceKGAddHandler(ctx context.Context, p *Palace, input KGAddToolInput) (KGAddToolOutput, error) {
	if input.Subject == "" && input.About == 0 {
		return KGAddToolOutput{Content: "Error: subject is required (or about, to annotate a statement)"}, nil
	}
	if input.Predicate == "" {
		return KGAddToolOutput{Content: "Error: predicate is required"}, nil
	}
	if input.Object == "" {
		return KGAddToolOutput{Content: "Error: object is required"}, nil
	}

	author, session := txMeta(ctx)
	ti := TripleInput{
		Subject:         input.Subject,
		Predicate:       input.Predicate,
		Object:          input.Object,
		Literal:         input.Literal,
		About:           input.About,
		Support:         input.Support,
		Confidence:      input.Confidence,
		Source:          input.Source,
		ConfirmExisting: input.ConfirmExisting,
		Author:          author,
		Session:         session,
	}
	var err error
	if ti.ValidFrom, err = optionalTime(input.ValidFrom); err != nil {
		return KGAddToolOutput{Content: fmt.Sprintf("Error: invalid valid_from: %v", err)}, nil
	}
	if ti.ValidTo, err = optionalTime(input.ValidTo); err != nil {
		return KGAddToolOutput{Content: fmt.Sprintf("Error: invalid valid_to: %v", err)}, nil
	}

	triple, err := p.KGAdd(ctx, ti)
	if err != nil {
		return KGAddToolOutput{Content: fmt.Sprintf("Error: %v", err)}, nil
	}
	verb := "Triple added"
	if !triple.New {
		verb = "Already known"
		if input.ConfirmExisting {
			verb = "Already known, confirmed"
		}
	}
	return KGAddToolOutput{
		Content: fmt.Sprintf("%s: %s → %s → %s (id=%s)%s", verb, triple.SubjectID, triple.PredicateID, triple.ObjectID,
			triple.ID, evidenceSuffix(triple)),
	}, nil
}

func optionalTime(s string) (*time.Time, error) {
	if s == "" {
		return nil, nil
	}
	t, err := parseFlexibleTime(s)
	if err != nil {
		return nil, err
	}
	return &t, nil
}

func evidenceSuffix(t *Triple) string {
	var parts []string
	if t.Confidence != nil {
		parts = append(parts, "confidence "+strconv.FormatFloat(*t.Confidence, 'g', -1, 64))
	}
	if len(t.Sources) > 0 {
		parts = append(parts, "source "+strings.Join(t.Sources, ", "))
	}
	if t.Confirmations > 0 {
		parts = append(parts, fmt.Sprintf("confirmed %d×", t.Confirmations))
	}
	if len(parts) == 0 {
		return ""
	}
	return " [" + strings.Join(parts, "; ") + "]"
}

// --- palace-kg-query ---

// KGQueryToolInput defines parameters for the palace-kg-query tool.
type KGQueryToolInput struct {
	// The entity name to query. Omit it to search with text instead.
	Entity string `json:"entity,omitempty"`
	// Words to search for in entity names and text values.
	Text string `json:"text,omitempty"`
	// Optional point-in-time filter on when facts held (RFC3339 or YYYY-MM-DD).
	AsOf string `json:"as_of,omitempty"`
	// Show what was believed at that moment: a transaction number or a time.
	TxAsOf string `json:"tx_as_of,omitempty"`
	// Direction: "subject", "object", or "both" (default "both").
	Direction string `json:"direction,omitempty"`
	// Maximum facts to return (default all for entity, 20 for text).
	Limit int `json:"limit,omitempty"`
}

// KGQueryToolOutput contains formatted query results.
type KGQueryToolOutput struct {
	Content string `json:"content"`
}

func newPalaceKGQueryTool(p *Palace) (tool.Tool, error) {
	return functiontool.New(functiontool.Config{
		Name: "palace-kg-query",
		Description: "Query the knowledge graph for facts about an entity, or search facts by text. Each fact shows its id, " +
			"valid time, confidence and sources. as_of filters by when facts held; tx_as_of shows what was believed at an " +
			"earlier moment (before a correction).",
	}, func(ctx agent.Context, input KGQueryToolInput) (KGQueryToolOutput, error) {
		return palaceKGQueryHandler(ctx, p, input)
	})
}

func palaceKGQueryHandler(ctx context.Context, p *Palace, input KGQueryToolInput) (KGQueryToolOutput, error) {
	if input.Entity == "" && input.Text == "" {
		return KGQueryToolOutput{Content: "Error: entity is required (or text, to search)"}, nil
	}
	triples, err := p.KG().QueryWith(ctx, KGQuery(input))
	if err != nil {
		return KGQueryToolOutput{Content: fmt.Sprintf("Error: %v", err)}, nil
	}
	what := fmt.Sprintf("%q", input.Entity)
	if input.Entity == "" {
		what = fmt.Sprintf("text %q", input.Text)
	}
	if len(triples) == 0 {
		return KGQueryToolOutput{Content: fmt.Sprintf("No facts found for entity %s.", what)}, nil
	}

	var sb strings.Builder
	fmt.Fprintf(&sb, "## Facts for %s (%d)\n\n", what, len(triples))
	sb.WriteString("| Id | Subject | Predicate | Object | Valid From | Valid To | Evidence |\n")
	sb.WriteString("|----|---------|-----------|--------|------------|----------|----------|\n")
	for _, t := range triples {
		fmt.Fprintf(&sb, "| %s | %s | %s | %s | %s | %s | %s |\n", t.ID, t.SubjectID, t.PredicateID, cell(t.ObjectID),
			dateOr(t.ValidFrom, "-"), dateOr(t.ValidTo, "active"), strings.TrimSpace(strings.Trim(evidenceSuffix(t), " []")))
	}
	return KGQueryToolOutput{Content: sb.String()}, nil
}

func dateOr(t *time.Time, dflt string) string {
	if t == nil {
		return dflt
	}
	return t.Format("2006-01-02")
}

// cell keeps a value from breaking a markdown table row.
func cell(s string) string {
	s = strings.ReplaceAll(s, "|", `\|`)
	s = strings.ReplaceAll(s, "\n", " ")
	if len(s) > 120 {
		s = s[:117] + "..."
	}
	return s
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
		Name: "palace-kg-invalidate",
		Description: "End an active fact in the knowledge graph: it stops holding now. Its confidence and sources carry " +
			"over, and the earlier open-ended version stays in the timeline. Use palace-kg-revise to correct a fact that " +
			"was wrong rather than ended.",
	}, func(ctx agent.Context, input KGInvalidateToolInput) (KGInvalidateToolOutput, error) {
		return palaceKGInvalidateHandler(ctx, p, input)
	})
}

func palaceKGInvalidateHandler(ctx context.Context, p *Palace, input KGInvalidateToolInput) (KGInvalidateToolOutput, error) {
	if input.Subject == "" || input.Predicate == "" || input.Object == "" {
		return KGInvalidateToolOutput{Content: "Error: subject, predicate, and object are all required"}, nil
	}
	author, session := txMeta(ctx)
	err := p.KG().InvalidateWith(ctx, input.Subject, input.Predicate, input.Object,
		TxOptions{Author: author, Source: session})
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
		Name: "palace-kg-timeline",
		Description: "View the chronological timeline of every fact ever recorded for an entity, including ended, corrected " +
			"and retracted ones, with what replaced them. Useful for understanding how knowledge evolved.",
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
	sb.WriteString("| Learned | Id | Subject | Predicate | Object | Status |\n")
	sb.WriteString("|---------|----|---------|-----------|--------|--------|\n")
	for _, t := range triples {
		fmt.Fprintf(&sb, "| %s | %s | %s | %s | %s | %s |\n", t.ExtractedAt.Format("2006-01-02"), t.ID,
			t.SubjectID, t.PredicateID, cell(t.ObjectID), tripleStatus(t))
	}
	return KGTimelineToolOutput{Content: sb.String()}, nil
}

func tripleStatus(t *Triple) string {
	switch {
	case t.Status == "ended":
		return "ended " + dateOr(t.ValidTo, "")
	case t.Status == "superseded" && t.SupersededBy != 0:
		return fmt.Sprintf("superseded by e%d", t.SupersededBy)
	}
	return t.Status
}

// parseFlexibleTime parses a time string in RFC3339 or YYYY-MM-DD format.
func parseFlexibleTime(s string) (time.Time, error) {
	if t, err := time.Parse(time.RFC3339, s); err == nil {
		return t, nil
	}
	return time.Parse("2006-01-02", s)
}
