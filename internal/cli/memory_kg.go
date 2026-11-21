package cli

import (
	"context"
	"fmt"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/dimetron/pi-go/internal/palace"
)

func newMemoryKGCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "kg",
		Short: "Knowledge graph commands",
	}

	cmd.AddCommand(newMemoryKGQueryCmd())
	cmd.AddCommand(newMemoryKGAddCmd())
	cmd.AddCommand(newMemoryKGTimelineCmd())
	cmd.AddCommand(newMemoryKGExplainCmd())
	cmd.AddCommand(newMemoryKGConflictsCmd())
	cmd.AddCommand(newMemoryKGLogCmd())
	cmd.AddCommand(newMemoryKGGraphsCmd())
	cmd.AddCommand(newMemoryKGSchemaCmd())
	cmd.AddCommand(newMemoryKGEraseCmd())

	return cmd
}

func newMemoryKGQueryCmd() *cobra.Command {
	var (
		flagDB     string
		flagAsOf   string
		flagTxAsOf string
		flagDir    string
	)

	cmd := &cobra.Command{
		Use:   "query <entity>",
		Short: "Query triples for an entity",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runMemoryKGQueryAt(args[0], flagDB, flagAsOf, flagTxAsOf, flagDir)
		},
	}

	cmd.Flags().StringVar(&flagDB, "db", "", "Palace database path")
	cmd.Flags().StringVar(&flagAsOf, "as-of", "", "Only facts valid at this time (YYYY-MM-DD or RFC3339)")
	cmd.Flags().StringVar(&flagTxAsOf, "tx-as-of", "", "What was believed then: a transaction number or a time")
	cmd.Flags().StringVar(&flagDir, "direction", "", "Filter direction: subject, object, or both (default)")

	return cmd
}

func newMemoryKGAddCmd() *cobra.Command {
	var (
		flagDB        string
		flagValidFrom string
	)

	cmd := &cobra.Command{
		Use:   "add <subject> <predicate> <object>",
		Short: "Add a knowledge graph triple",
		Args:  cobra.ExactArgs(3),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runMemoryKGAdd(args[0], args[1], args[2], flagDB, flagValidFrom)
		},
	}

	cmd.Flags().StringVar(&flagDB, "db", "", "Palace database path")
	cmd.Flags().StringVar(&flagValidFrom, "valid-from", "", "Validity start date (YYYY-MM-DD or RFC3339)")

	return cmd
}

func newMemoryKGTimelineCmd() *cobra.Command {
	var flagDB string

	cmd := &cobra.Command{
		Use:   "timeline <entity>",
		Short: "Show chronological timeline for an entity",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runMemoryKGTimeline(args[0], flagDB)
		},
	}

	cmd.Flags().StringVar(&flagDB, "db", "", "Palace database path")

	return cmd
}

func openPalaceDB(dbPath string) (*palace.Palace, error) {
	if dbPath == "" {
		dbPath = filepath.Join(".pi-go", "palace.db")
	}
	return palace.New(
		palace.WithDBPath(dbPath),
		palace.WithModelPath(defaultPalaceModelPath()),
	)
}

func runMemoryKGQuery(entity, dbPath, asOf, direction string) error {
	return runMemoryKGQueryAt(entity, dbPath, asOf, "", direction)
}

func runMemoryKGQueryAt(entity, dbPath, asOf, txAsOf, direction string) error {
	p, err := openPalaceDB(dbPath)
	if err != nil {
		return fmt.Errorf("opening palace: %w", err)
	}
	defer p.Close()

	ctx := context.Background()
	triples, err := p.KG().QueryWith(ctx, palace.KGQuery{Entity: entity, AsOf: asOf, TxAsOf: txAsOf, Direction: direction})
	if err != nil {
		return fmt.Errorf("kg query: %w", err)
	}

	if len(triples) == 0 {
		fmt.Printf("No triples found for %q.\n", entity)
		return nil
	}

	fmt.Printf("Triples for %q:\n\n", entity)
	fmt.Printf("%-7s %-20s %-20s %-20s %-12s %-12s\n", "Id", "Subject", "Predicate", "Object", "Valid From", "Valid To")
	fmt.Println("─────── ──────────────────── ──────────────────── ──────────────────── ──────────── ────────────")

	for _, t := range triples {
		vf := "—"
		if t.ValidFrom != nil {
			vf = t.ValidFrom.Format("2006-01-02")
		}
		vt := "active"
		if t.ValidTo != nil {
			vt = t.ValidTo.Format("2006-01-02")
		}
		fmt.Printf("%-7s %-20s %-20s %-20s %-12s %-12s\n",
			t.ID,
			truncateCol(t.SubjectID, 20),
			truncateCol(t.PredicateID, 20),
			truncateCol(t.ObjectID, 20),
			vf, vt)
	}

	return nil
}

func runMemoryKGAdd(subject, predicate, object, dbPath, validFromStr string) error {
	p, err := openPalaceDB(dbPath)
	if err != nil {
		return fmt.Errorf("opening palace: %w", err)
	}
	defer p.Close()

	input := palace.TripleInput{
		Subject:   subject,
		Predicate: predicate,
		Object:    object,
	}

	if validFromStr != "" {
		t, err := parseDate(validFromStr)
		if err != nil {
			return fmt.Errorf("parsing --valid-from: %w", err)
		}
		input.ValidFrom = &t
	}

	ctx := context.Background()
	triple, err := p.KGAdd(ctx, input)
	if err != nil {
		return fmt.Errorf("kg add: %w", err)
	}

	fmt.Printf("Added triple: %s\n", triple.ID)

	return nil
}

func runMemoryKGTimeline(entity, dbPath string) error {
	p, err := openPalaceDB(dbPath)
	if err != nil {
		return fmt.Errorf("opening palace: %w", err)
	}
	defer p.Close()

	ctx := context.Background()
	triples, err := p.KGTimeline(ctx, entity)
	if err != nil {
		return fmt.Errorf("kg timeline: %w", err)
	}

	if len(triples) == 0 {
		fmt.Printf("No timeline entries for %q.\n", entity)
		return nil
	}

	fmt.Printf("Timeline for %q:\n\n", entity)
	for _, t := range triples {
		status := t.Status
		switch {
		case t.Status == "ended" && t.ValidTo != nil:
			status = "ended " + t.ValidTo.Format("2006-01-02")
		case t.SupersededBy != 0:
			status = fmt.Sprintf("superseded by e%d", t.SupersededBy)
		}
		vf := ""
		if t.ValidFrom != nil {
			vf = " (from " + t.ValidFrom.Format("2006-01-02") + ")"
		}
		fmt.Printf("  %s  %-6s %s → %s → %s%s  [%s]\n",
			t.ExtractedAt.Format("2006-01-02"), t.ID,
			t.SubjectID, t.PredicateID, t.ObjectID,
			vf, status)
	}

	return nil
}

func newMemoryKGExplainCmd() *cobra.Command {
	var flagDB, flagTxAsOf string
	cmd := &cobra.Command{
		Use:   "explain <eid>",
		Short: "Explain a fact: its evidence, derivation, corrections and dependents",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			eid, err := parseEID(args[0])
			if err != nil {
				return err
			}
			return runMemoryKGExplain(eid, flagDB, flagTxAsOf)
		},
	}
	cmd.Flags().StringVar(&flagDB, "db", "", "Palace database path")
	cmd.Flags().StringVar(&flagTxAsOf, "tx-as-of", "", "Explain it as believed then: a transaction number or a time")
	return cmd
}

// parseEID accepts "12" or "e12".
func parseEID(s string) (int64, error) {
	eid, err := strconv.ParseInt(strings.TrimPrefix(s, "e"), 10, 64)
	if err != nil || eid <= 0 {
		return 0, fmt.Errorf("invalid statement id %q (want e.g. e12)", s)
	}
	return eid, nil
}

func runMemoryKGExplain(eid int64, dbPath, txAsOf string) error {
	p, err := openPalaceDB(dbPath)
	if err != nil {
		return fmt.Errorf("opening palace: %w", err)
	}
	defer p.Close()

	ctx := context.Background()
	v, err := p.KG().View(ctx, txAsOf, "")
	if err != nil {
		return err
	}
	ex, err := v.Explain(ctx, eid)
	if err != nil {
		return fmt.Errorf("kg explain: %w", err)
	}
	if ex == nil {
		return fmt.Errorf("no statement e%d", eid)
	}
	fmt.Print(palace.FormatExplanation(ex))
	return nil
}

func newMemoryKGConflictsCmd() *cobra.Command {
	var flagDB, flagSubject, flagPredicate string
	cmd := &cobra.Command{
		Use:   "conflicts",
		Short: "List facts that disagree: one subject and predicate with different objects at the same time",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runMemoryKGConflicts(flagDB, flagSubject, flagPredicate)
		},
	}
	cmd.Flags().StringVar(&flagDB, "db", "", "Palace database path")
	cmd.Flags().StringVar(&flagSubject, "subject", "", "Only this subject")
	cmd.Flags().StringVar(&flagPredicate, "predicate", "", "Only this predicate")
	return cmd
}

func runMemoryKGConflicts(dbPath, subject, predicate string) error {
	p, err := openPalaceDB(dbPath)
	if err != nil {
		return fmt.Errorf("opening palace: %w", err)
	}
	defer p.Close()

	cs, err := p.KG().Now().Conflicts(context.Background(), palace.ConflictFilter{Subject: subject, Predicate: predicate})
	if err != nil {
		return fmt.Errorf("kg conflicts: %w", err)
	}
	if len(cs) == 0 {
		fmt.Println("No conflicting facts.")
		return nil
	}
	fmt.Print(palace.FormatConflicts(cs))
	return nil
}

func newMemoryKGLogCmd() *cobra.Command {
	var (
		flagDB    string
		flagSince int64
	)
	cmd := &cobra.Command{
		Use:   "log",
		Short: "Show the knowledge-graph change log: every assertion and retraction by transaction",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runMemoryKGLog(flagDB, flagSince)
		},
	}
	cmd.Flags().StringVar(&flagDB, "db", "", "Palace database path")
	cmd.Flags().Int64Var(&flagSince, "since", 0, "Only transactions after this one")
	return cmd
}

func runMemoryKGLog(dbPath string, since int64) error {
	p, err := openPalaceDB(dbPath)
	if err != nil {
		return fmt.Errorf("opening palace: %w", err)
	}
	defer p.Close()

	ctx := context.Background()
	kg := p.KG()
	evs, err := kg.Events(ctx, since, 0)
	if err != nil {
		return fmt.Errorf("kg log: %w", err)
	}
	if len(evs) == 0 {
		fmt.Println("No changes.")
		return nil
	}
	var lastT int64
	for _, ev := range evs {
		if ev.T != lastT {
			lastT = ev.T
			info, err := kg.Transaction(ctx, ev.T)
			if err != nil {
				return err
			}
			fmt.Printf("tx%d  %s  %s %s %s\n", info.T, info.Instant.Format("2006-01-02 15:04:05"), info.Author, info.Source, info.Reason)
		}
		st, err := kg.Get(ctx, ev.EID)
		if err != nil {
			return err
		}
		op := "+"
		if ev.Op == "retract" {
			op = "-"
			if ev.Kind != nil {
				op += " (" + ev.Kind.String() + ")"
			}
		}
		fmt.Printf("  %s e%d  %s → %s → %s\n", op, ev.EID, st.Subject, st.Predicate, truncateCol(st.Object.String(), 60))
	}
	return nil
}

// parseDate tries RFC3339 first, then YYYY-MM-DD.
func parseDate(s string) (time.Time, error) {
	if t, err := time.Parse(time.RFC3339, s); err == nil {
		return t, nil
	}
	return time.Parse("2006-01-02", s)
}

// truncateCol shortens a string to maxLen, adding "…" if truncated.
func truncateCol(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen-1] + "…"
}
