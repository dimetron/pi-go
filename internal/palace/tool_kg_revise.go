package palace

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"google.golang.org/adk/v2/agent"
	"google.golang.org/adk/v2/tool"
	"google.golang.org/adk/v2/tool/functiontool"
)

// --- palace-kg-revise ---

// KGReviseToolInput defines parameters for the palace-kg-revise tool.
type KGReviseToolInput struct {
	// supersede (correct the fact), confirm (another source agrees), or
	// retract (it was never true).
	Action string `json:"action"`
	// The statement id, the number in e12.
	EID int64 `json:"eid"`
	// supersede: the corrected object (an entity name, or text with literal).
	Object string `json:"object,omitempty"`
	// supersede: store the corrected object as a text value.
	Literal bool `json:"literal,omitempty"`
	// supersede: corrected validity start / end (RFC3339 or YYYY-MM-DD).
	ValidFrom string `json:"valid_from,omitempty"`
	ValidTo   string `json:"valid_to,omitempty"`
	// Why (recorded on the change).
	Reason string `json:"reason,omitempty"`
	// Who or what says so (file, URL, person, test).
	Source string `json:"source,omitempty"`
	// Preview the change without writing it.
	DryRun bool `json:"dry_run,omitempty"`
}

// KGReviseToolOutput contains the result.
type KGReviseToolOutput struct {
	Content string `json:"content"`
}

func newPalaceKGReviseTool(p *Palace) (tool.Tool, error) {
	return functiontool.New(functiontool.Config{
		Name: "palace-kg-revise",
		Description: "Revise a knowledge-graph fact by id. supersede corrects it (new object or valid time); its confidence, " +
			"sources and annotations move to the corrected version, and conclusions derived from it are withdrawn. confirm " +
			"records that another source agrees. retract withdraws it together with everything that rests on it. Nothing " +
			"is deleted: earlier versions stay visible with tx_as_of and in the timeline. Use dry_run to preview.",
	}, func(ctx agent.Context, input KGReviseToolInput) (KGReviseToolOutput, error) {
		return palaceKGReviseHandler(ctx, p, input)
	})
}

func palaceKGReviseHandler(ctx context.Context, p *Palace, input KGReviseToolInput) (KGReviseToolOutput, error) {
	if input.EID <= 0 {
		return KGReviseToolOutput{Content: "Error: eid is required"}, nil
	}
	author, session := txMeta(ctx)
	opts := TxOptions{Author: author, Source: input.Source, Reason: input.Reason, DryRun: input.DryRun}
	if opts.Source == "" {
		opts.Source = session
	}

	var fn func(*KGTx) error
	var result int64
	switch input.Action {
	case "supersede":
		patch := Patch{}
		if input.Object != "" {
			o := Ent(input.Object)
			if input.Literal {
				o = Str(input.Object)
			}
			patch.Object = &o
		}
		var err error
		if patch.ValidFrom, err = optionalTime(input.ValidFrom); err != nil {
			return KGReviseToolOutput{Content: fmt.Sprintf("Error: invalid valid_from: %v", err)}, nil
		}
		if patch.ValidTo, err = optionalTime(input.ValidTo); err != nil {
			return KGReviseToolOutput{Content: fmt.Sprintf("Error: invalid valid_to: %v", err)}, nil
		}
		if patch.Object == nil && patch.ValidFrom == nil && patch.ValidTo == nil {
			return KGReviseToolOutput{Content: "Error: supersede needs object, valid_from or valid_to"}, nil
		}
		fn = func(tx *KGTx) error {
			var err error
			result, err = tx.Supersede(input.EID, patch)
			return err
		}
	case "confirm":
		fn = func(tx *KGTx) error { _, err := tx.Confirm(input.EID); return err }
	case "retract":
		fn = func(tx *KGTx) error {
			ok, err := tx.Retract(input.EID)
			if err == nil && !ok {
				return fmt.Errorf("e%d is already retracted", input.EID)
			}
			return err
		}
	default:
		return KGReviseToolOutput{Content: "Error: action must be supersede, confirm or retract"}, nil
	}

	rep, err := p.KG().Transact(ctx, opts, fn)
	if err != nil {
		return KGReviseToolOutput{Content: fmt.Sprintf("Error: %v", err)}, nil
	}
	var sb strings.Builder
	if rep.DryRun {
		sb.WriteString("Dry run, nothing written. ")
	}
	switch input.Action {
	case "supersede":
		fmt.Fprintf(&sb, "Superseded e%d → e%d", input.EID, result)
		if n := len(rep.Asserted) - 2; n > 0 { // minus the new fact and its lineage link
			fmt.Fprintf(&sb, "; %d annotation(s) carried over", n)
		}
	case "confirm":
		fmt.Fprintf(&sb, "Confirmed e%d", input.EID)
	case "retract":
		fmt.Fprintf(&sb, "Retracted e%d", input.EID)
	}
	fmt.Fprintf(&sb, " (tx%d)", rep.T)
	if others := retractedBesides(rep, input.EID); len(others) > 0 {
		fmt.Fprintf(&sb, "\nAlso withdrawn: %s", strings.Join(others, ", "))
	}
	return KGReviseToolOutput{Content: sb.String()}, nil
}

func retractedBesides(rep *TxReport, eid int64) []string {
	var out []string
	for _, r := range rep.Retracted {
		if r.EID != eid {
			out = append(out, "e"+strconv.FormatInt(r.EID, 10)+" ("+r.Kind.String()+")")
		}
	}
	return out
}

// --- palace-kg-explain ---

// KGExplainToolInput defines parameters for the palace-kg-explain tool.
type KGExplainToolInput struct {
	// The statement id, the number in e12.
	EID int64 `json:"eid"`
	// Explain it as it was believed then: a transaction number or a time.
	TxAsOf string `json:"tx_as_of,omitempty"`
}

// KGExplainToolOutput contains the explanation.
type KGExplainToolOutput struct {
	Content string `json:"content"`
}

func newPalaceKGExplainTool(p *Palace) (tool.Tool, error) {
	return functiontool.New(functiontool.Config{
		Name: "palace-kg-explain",
		Description: "Explain a knowledge-graph fact by id: who recorded it and when, its confidence, sources and " +
			"annotations, what it was derived from, its corrections, its confirmations, and what would be withdrawn with " +
			"it. Use before revising a fact.",
	}, func(ctx agent.Context, input KGExplainToolInput) (KGExplainToolOutput, error) {
		return palaceKGExplainHandler(ctx, p, input)
	})
}

func palaceKGExplainHandler(ctx context.Context, p *Palace, input KGExplainToolInput) (KGExplainToolOutput, error) {
	if input.EID <= 0 {
		return KGExplainToolOutput{Content: "Error: eid is required"}, nil
	}
	v, err := p.KG().View(ctx, input.TxAsOf, "")
	if err != nil {
		return KGExplainToolOutput{Content: fmt.Sprintf("Error: %v", err)}, nil
	}
	ex, err := v.Explain(ctx, input.EID)
	if err != nil {
		return KGExplainToolOutput{Content: fmt.Sprintf("Error: %v", err)}, nil
	}
	if ex == nil {
		return KGExplainToolOutput{Content: fmt.Sprintf("No statement e%d.", input.EID)}, nil
	}
	return KGExplainToolOutput{Content: FormatExplanation(ex)}, nil
}

// FormatExplanation renders an explanation as markdown.
func FormatExplanation(ex *Explanation) string {
	st := ex.Statement
	var sb strings.Builder
	fmt.Fprintf(&sb, "## e%d: %s → %s → %s\n\n", st.EID, st.Subject, st.Predicate, cell(st.Object.String()))
	status := "live"
	if st.RetKind != nil {
		status = st.RetKind.String()
	}
	fmt.Fprintf(&sb, "- Status: %s", status)
	if !ex.Visible {
		fmt.Fprintf(&sb, " (not visible %s)", ex.View)
	}
	sb.WriteString("\n")
	fmt.Fprintf(&sb, "- Valid: %s to %s\n", dateOr(st.ValidFrom, "always"), dateOr(st.ValidTo, "open"))
	if ex.AddedBy != nil {
		fmt.Fprintf(&sb, "- Recorded: tx%d at %s%s\n", ex.AddedBy.T, ex.AddedBy.Instant.Format("2006-01-02 15:04:05"), txWho(*ex.AddedBy))
	}
	if ex.RetractedBy != nil {
		fmt.Fprintf(&sb, "- Withdrawn: tx%d at %s%s\n", ex.RetractedBy.T, ex.RetractedBy.Instant.Format("2006-01-02 15:04:05"), txWho(*ex.RetractedBy))
	}
	if c := ex.Evidence.Confidence; c != nil {
		fmt.Fprintf(&sb, "- Confidence: %s\n", strconv.FormatFloat(*c, 'g', -1, 64))
	}
	if srcs := append(append([]string{}, ex.Evidence.Sources...), ex.Evidence.SourceFiles...); len(srcs) > 0 {
		fmt.Fprintf(&sb, "- Sources: %s\n", strings.Join(srcs, ", "))
	}
	for _, c := range ex.Confirmed {
		fmt.Fprintf(&sb, "- Confirmed: tx%d at %s%s\n", c.T, c.Instant.Format("2006-01-02 15:04:05"), txWho(c))
	}
	if len(ex.Supports) > 0 {
		sb.WriteString("- Derived from:\n")
		for _, s := range ex.Supports {
			fmt.Fprintf(&sb, "  - e%d: %s → %s → %s\n", s.EID, s.Subject, s.Predicate, cell(s.Object.String()))
		}
	}
	if len(ex.Supersedes) > 0 {
		fmt.Fprintf(&sb, "- Corrects: %s\n", eids(ex.Supersedes))
	}
	if len(ex.SupersededBy) > 0 {
		fmt.Fprintf(&sb, "- Corrected by: %s\n", eids(ex.SupersededBy))
	}
	if len(ex.Layers) > 0 {
		sb.WriteString("- Annotations:\n")
		for _, l := range ex.Layers {
			fmt.Fprintf(&sb, "  - e%d: %s = %s\n", l.EID, l.Predicate, cell(l.Object.String()))
		}
	}
	if len(ex.Dependents) > 0 {
		fmt.Fprintf(&sb, "- Retracting it would also withdraw: %s\n", eids(ex.Dependents))
	}
	return sb.String()
}

func txWho(t TxInfo) string {
	var parts []string
	if t.Author != "" {
		parts = append(parts, "by "+t.Author)
	}
	if t.Source != "" {
		parts = append(parts, "from "+t.Source)
	}
	if t.Reason != "" {
		parts = append(parts, "("+t.Reason+")")
	}
	if len(parts) == 0 {
		return ""
	}
	return " " + strings.Join(parts, " ")
}

func eids(ids []int64) string {
	s := make([]string, len(ids))
	for i, id := range ids {
		s[i] = "e" + strconv.FormatInt(id, 10)
	}
	return strings.Join(s, ", ")
}

// --- palace-kg-conflicts ---

// KGConflictsToolInput defines parameters for the palace-kg-conflicts tool.
type KGConflictsToolInput struct {
	// Only conflicts about this subject entity.
	Subject string `json:"subject,omitempty"`
	// Only conflicts for this predicate.
	Predicate string `json:"predicate,omitempty"`
	// Maximum subject/predicate pairs (default 20).
	Limit int `json:"limit,omitempty"`
	// Look at what was believed then: a transaction number or a time.
	TxAsOf string `json:"tx_as_of,omitempty"`
}

// KGConflictsToolOutput contains the conflicts.
type KGConflictsToolOutput struct {
	Content string `json:"content"`
}

func newPalaceKGConflictsTool(p *Palace) (tool.Tool, error) {
	return functiontool.New(functiontool.Config{
		Name: "palace-kg-conflicts",
		Description: "List where the knowledge graph disagrees with itself: one subject and predicate holding different " +
			"objects at the same time (e.g. two databases), each value with its ids, confidence, sources and confirmations. " +
			"Multi-valued relationships also appear; decide from the evidence, then use palace-kg-revise.",
	}, func(ctx agent.Context, input KGConflictsToolInput) (KGConflictsToolOutput, error) {
		return palaceKGConflictsHandler(ctx, p, input)
	})
}

func palaceKGConflictsHandler(ctx context.Context, p *Palace, input KGConflictsToolInput) (KGConflictsToolOutput, error) {
	v, err := p.KG().View(ctx, input.TxAsOf, "")
	if err != nil {
		return KGConflictsToolOutput{Content: fmt.Sprintf("Error: %v", err)}, nil
	}
	cs, err := v.Conflicts(ctx, ConflictFilter{Subject: input.Subject, Predicate: input.Predicate, Limit: input.Limit})
	if err != nil {
		return KGConflictsToolOutput{Content: fmt.Sprintf("Error: %v", err)}, nil
	}
	if len(cs) == 0 {
		return KGConflictsToolOutput{Content: "No conflicting facts."}, nil
	}
	return KGConflictsToolOutput{Content: FormatConflicts(cs)}, nil
}

// FormatConflicts renders conflicts as markdown.
func FormatConflicts(cs []Conflict) string {
	var sb strings.Builder
	fmt.Fprintf(&sb, "## Conflicts (%d)\n", len(cs))
	for _, c := range cs {
		fmt.Fprintf(&sb, "\n### %s → %s\n", c.Subject, c.Predicate)
		for _, v := range c.Values {
			for _, e := range v.Statements {
				fmt.Fprintf(&sb, "- %s: e%d, valid %s to %s, recorded %s", cell(v.Object.String()), e.EID,
					dateOr(e.ValidFrom, "always"), dateOr(e.ValidTo, "open"), e.AddedAt.Format("2006-01-02"))
				if e.Author != "" {
					fmt.Fprintf(&sb, " by %s", e.Author)
				}
				if e.Confidence != nil {
					fmt.Fprintf(&sb, ", confidence %s", strconv.FormatFloat(*e.Confidence, 'g', -1, 64))
				}
				if srcs := append(append([]string{}, e.Sources...), e.SourceFiles...); len(srcs) > 0 {
					fmt.Fprintf(&sb, ", sources %s", strings.Join(srcs, ", "))
				}
				if e.Confirmations > 0 {
					fmt.Fprintf(&sb, ", confirmed %d×", e.Confirmations)
				}
				sb.WriteString("\n")
			}
		}
	}
	return sb.String()
}
