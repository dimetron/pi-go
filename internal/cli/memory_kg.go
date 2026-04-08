package cli

import (
	"context"
	"fmt"
	"path/filepath"
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

	return cmd
}

func newMemoryKGQueryCmd() *cobra.Command {
	var (
		flagDB    string
		flagAsOf  string
		flagDir   string
	)

	cmd := &cobra.Command{
		Use:   "query <entity>",
		Short: "Query triples for an entity",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runMemoryKGQuery(args[0], flagDB, flagAsOf, flagDir)
		},
	}

	cmd.Flags().StringVar(&flagDB, "db", "", "Palace database path")
	cmd.Flags().StringVar(&flagAsOf, "as-of", "", "Point-in-time filter (YYYY-MM-DD or RFC3339)")
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
	return palace.New(palace.WithDBPath(dbPath))
}

func runMemoryKGQuery(entity, dbPath, asOf, direction string) error {
	p, err := openPalaceDB(dbPath)
	if err != nil {
		return fmt.Errorf("opening palace: %w", err)
	}
	defer p.Close()

	ctx := context.Background()
	triples, err := p.KGQuery(ctx, entity, asOf, direction)
	if err != nil {
		return fmt.Errorf("kg query: %w", err)
	}

	if len(triples) == 0 {
		fmt.Printf("No triples found for %q.\n", entity)
		return nil
	}

	fmt.Printf("Triples for %q:\n\n", entity)
	fmt.Printf("%-20s %-20s %-20s %-12s %-12s\n", "Subject", "Predicate", "Object", "Valid From", "Valid To")
	fmt.Println("──────────────────── ──────────────────── ──────────────────── ──────────── ────────────")

	for _, t := range triples {
		vf := "—"
		if t.ValidFrom != nil {
			vf = t.ValidFrom.Format("2006-01-02")
		}
		vt := "active"
		if t.ValidTo != nil {
			vt = t.ValidTo.Format("2006-01-02")
		}
		fmt.Printf("%-20s %-20s %-20s %-12s %-12s\n",
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
		status := "active"
		if t.ValidTo != nil {
			status = "ended " + t.ValidTo.Format("2006-01-02")
		}
		vf := ""
		if t.ValidFrom != nil {
			vf = " (from " + t.ValidFrom.Format("2006-01-02") + ")"
		}
		fmt.Printf("  %s  %s → %s → %s%s  [%s]\n",
			t.ExtractedAt.Format("2006-01-02"),
			t.SubjectID, t.PredicateID, t.ObjectID,
			vf, status)
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
