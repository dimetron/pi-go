package cli

import (
	"context"
	"fmt"

	"github.com/spf13/cobra"

	"github.com/dimetron/pi-go/internal/palace"
)

// Phase-2 knowledge-graph commands: named graphs, predicate schema and
// erasure. Erasure is a CLI-only operation on purpose: it destroys data and
// should be a human decision, not an agent tool call.

func newMemoryKGGraphsCmd() *cobra.Command {
	var flagDB string
	cmd := &cobra.Command{
		Use:   "graphs",
		Short: "List named graphs and their member counts",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return withPalace(flagDB, func(ctx context.Context, p *palace.Palace) error {
				gs, err := p.KG().Now().ListGraphs(ctx)
				if err != nil {
					return err
				}
				if len(gs) == 0 {
					fmt.Println("No named graphs.")
					return nil
				}
				for _, g := range gs {
					fmt.Printf("%-30s %d\n", g.Name, g.Members)
				}
				return nil
			})
		},
	}
	cmd.Flags().StringVar(&flagDB, "db", "", "Palace database path")
	return cmd
}

func newMemoryKGSchemaCmd() *cobra.Command {
	var (
		flagDB                        string
		flagSet                       bool
		flagOne, flagUnique, flagEdge bool
		flagSensitive                 bool
		flagValueType, flagSubject    string
	)
	cmd := &cobra.Command{
		Use:   "schema <predicate>",
		Short: "Show or set a predicate's schema (cardinality, unique, types, sensitive)",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return withPalace(flagDB, func(ctx context.Context, p *palace.Palace) error {
				if flagSet {
					sch := palace.PredicateSchema{One: flagOne, Unique: flagUnique, IsEdge: flagEdge,
						Sensitive: flagSensitive, ValueType: flagValueType, SubjectType: flagSubject}
					if _, err := p.KG().Transact(ctx, palace.TxOptions{Author: "cli", Reason: "set schema"},
						func(w *palace.KGTx) error { return w.SetPredicate(palace.Ent(args[0]), sch) }); err != nil {
						return err
					}
				}
				sch, err := p.KG().Schema(ctx, args[0])
				if err != nil {
					return err
				}
				fmt.Printf("%s: one=%t unique=%t value_type=%q subject_type=%q is_edge=%t sensitive=%t\n",
					args[0], sch.One, sch.Unique, sch.ValueType, sch.SubjectType, sch.IsEdge, sch.Sensitive)
				return nil
			})
		},
	}
	cmd.Flags().StringVar(&flagDB, "db", "", "Palace database path")
	cmd.Flags().BoolVar(&flagSet, "set", false, "Replace the schema with the flags given")
	cmd.Flags().BoolVar(&flagOne, "one", false, "Cardinality one")
	cmd.Flags().BoolVar(&flagUnique, "unique", false, "One subject per value")
	cmd.Flags().BoolVar(&flagEdge, "edge", false, "Mark as a relationship")
	cmd.Flags().BoolVar(&flagSensitive, "sensitive", false, "Seal literal values so they can be erased")
	cmd.Flags().StringVar(&flagValueType, "value-type", "", "entity|string|int|number|bool|time|statement")
	cmd.Flags().StringVar(&flagSubject, "subject-type", "", "entity|statement")
	return cmd
}

func newMemoryKGEraseCmd() *cobra.Command {
	var flagDB, flagReason string
	var flagYes bool
	cmd := &cobra.Command{
		Use:   "erase <subject>",
		Short: "Crypto-shred a data subject: destroy the key of its sensitive values",
		Long: "Destroys the seal key of <subject>. Every value of a sensitive predicate about it then reads as " +
			"sys:Erased. Statements, ids and history stay. Backups taken earlier still hold the key.",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if !flagYes {
				return fmt.Errorf("erasing %q cannot be undone; pass --yes to confirm", args[0])
			}
			return withPalace(flagDB, func(ctx context.Context, p *palace.Palace) error {
				rep, err := p.KG().Erase(ctx, palace.TxOptions{Author: "cli", Reason: flagReason}, args[0])
				if err != nil {
					return err
				}
				if !rep.Erased {
					fmt.Printf("%s has no sensitive values to erase.\n", args[0])
					return nil
				}
				fmt.Printf("Erased %s at tx%d.\n", args[0], rep.Tx.T)
				if !rep.Checkpointed {
					fmt.Println("The WAL could not be checkpointed now; the old key leaves the file at the next checkpoint.")
				}
				return nil
			})
		},
	}
	cmd.Flags().StringVar(&flagDB, "db", "", "Palace database path")
	cmd.Flags().StringVar(&flagReason, "reason", "", "Why (recorded on the transaction)")
	cmd.Flags().BoolVar(&flagYes, "yes", false, "Confirm the erasure")
	return cmd
}

// withPalace opens the palace, runs fn and closes it.
func withPalace(dbPath string, fn func(context.Context, *palace.Palace) error) error {
	p, err := openPalaceDB(dbPath)
	if err != nil {
		return fmt.Errorf("opening palace: %w", err)
	}
	defer p.Close()
	return fn(context.Background(), p)
}
