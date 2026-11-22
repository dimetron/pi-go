package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"github.com/spf13/cobra"

	"github.com/dimetron/pi-go/internal/palace"
)

// Phase-2 read and reasoning commands: query languages, paths, rules,
// bundles, saved answers and volatile state.

func newMemoryKGSelectCmd() *cobra.Command {
	var flagDB, flagLang, flagTxAsOf, flagAsOf, flagGraph, flagSave string
	cmd := &cobra.Command{
		Use:   "select <query>",
		Short: "Run a SPARQL or Cypher (read) query over the knowledge graph",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return withPalace(flagDB, func(ctx context.Context, p *palace.Palace) error {
				v, err := p.KG().View(ctx, flagTxAsOf, flagAsOf)
				if err != nil {
					return err
				}
				if flagGraph != "" {
					v = v.InGraph(flagGraph)
				}
				if flagSave != "" {
					a, err := p.KG().SaveAnswer(ctx, flagSave, flagLang, args[0], v)
					if err != nil {
						return err
					}
					fmt.Printf("Saved %s (%d rows).\n%s", a.Name, len(a.Rows), palace.FormatRows(a.Vars, a.Rows))
					return nil
				}
				res, err := v.RunQuery(ctx, flagLang, args[0], palace.QueryOptions{})
				if err != nil {
					return err
				}
				fmt.Print(palace.FormatRows(res.Vars, res.Rows))
				return nil
			})
		},
	}
	cmd.Flags().StringVar(&flagDB, "db", "", "Palace database path")
	cmd.Flags().StringVar(&flagLang, "lang", "sparql", "sparql or cypher")
	cmd.Flags().StringVar(&flagTxAsOf, "tx-as-of", "", "Query what was believed then: a transaction number or a time")
	cmd.Flags().StringVar(&flagAsOf, "as-of", "", "Only facts valid at this time")
	cmd.Flags().StringVar(&flagGraph, "graph", "", "Only facts in this named graph")
	cmd.Flags().StringVar(&flagSave, "save", "", "Save the answer under this name")
	return cmd
}

func newMemoryKGPathCmd() *cobra.Command {
	var flagDB, flagMode, flagDir, flagGraph string
	var flagPreds []string
	var flagTime bool
	var flagMax int
	cmd := &cobra.Command{
		Use:   "path <from> [to]",
		Short: "Find paths between entities",
		Args:  cobra.RangeArgs(1, 2),
		RunE: func(cmd *cobra.Command, args []string) error {
			mode, err := palace.ParsePathMode(flagMode)
			if err != nil {
				return err
			}
			pq := palace.PathQuery{From: args[0], Predicates: flagPreds, Direction: flagDir, Mode: mode,
				TimeRespecting: flagTime, MaxHops: flagMax}
			if len(args) == 2 {
				pq.To = args[1]
			}
			return withPalace(flagDB, func(ctx context.Context, p *palace.Palace) error {
				v := p.KG().Now()
				if flagGraph != "" {
					v = v.InGraph(flagGraph)
				}
				paths, err := v.Paths(ctx, pq)
				if err != nil {
					return err
				}
				if len(paths) == 0 {
					fmt.Println("No path found.")
				}
				for _, path := range paths {
					var sb strings.Builder
					sb.WriteString(path.Nodes[0].String())
					for i, st := range path.Steps {
						fmt.Fprintf(&sb, " -[e%d]-> %s", st.EID, path.Nodes[i+1])
					}
					fmt.Println(sb.String())
				}
				return nil
			})
		},
	}
	cmd.Flags().StringVar(&flagDB, "db", "", "Palace database path")
	cmd.Flags().StringVar(&flagMode, "mode", "shortest", "reach, shortest, all_shortest or trails")
	cmd.Flags().StringVar(&flagDir, "direction", "out", "out, in or both")
	cmd.Flags().StringSliceVar(&flagPreds, "predicate", nil, "Only follow these predicates")
	cmd.Flags().BoolVar(&flagTime, "time-respecting", false, "Each step at or after the previous in valid time")
	cmd.Flags().IntVar(&flagMax, "max-hops", 0, "Maximum hops (default 6)")
	cmd.Flags().StringVar(&flagGraph, "graph", "", "Only facts in this named graph")
	return cmd
}

func newMemoryKGRulesCmd() *cobra.Command {
	var flagDB, flagSyntax, flagFile, flagGoal string
	var flagDry bool
	cmd := &cobra.Command{
		Use:   "rules <store|solve|check|materialize> <name>",
		Short: "Store, solve, check or materialize a rule program (datalog, n3, elpi for solve)",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			action, name := args[0], args[1]
			var text string
			if flagFile != "" {
				data, err := os.ReadFile(flagFile)
				if err != nil {
					return err
				}
				text = string(data)
			}
			return withPalace(flagDB, func(ctx context.Context, p *palace.Palace) error {
				return runRules(ctx, p, action, name, flagSyntax, text, flagGoal, flagDry)
			})
		},
	}
	cmd.Flags().StringVar(&flagDB, "db", "", "Palace database path")
	cmd.Flags().StringVar(&flagSyntax, "syntax", "datalog", "datalog, n3 or elpi")
	cmd.Flags().StringVar(&flagFile, "file", "", "Program file (store; or inline for solve/check)")
	cmd.Flags().StringVar(&flagGoal, "goal", "", "Goal to solve, e.g. reports(alice, Y)")
	cmd.Flags().BoolVar(&flagDry, "dry-run", false, "Materialize without writing")
	return cmd
}

func runRules(ctx context.Context, p *palace.Palace, action, name, syntax, text, goal string, dry bool) error {
	kg := p.KG()
	load := func() (*palace.Program, error) {
		if text != "" {
			return palace.ParseRules(syntax, text)
		}
		prog, _, err := kg.LoadProgram(ctx, name)
		return prog, err
	}
	switch action {
	case "store":
		var eid int64
		_, err := kg.Transact(ctx, palace.TxOptions{Author: "cli", Reason: "store rules"}, func(w *palace.KGTx) error {
			var err error
			eid, err = w.StoreProgram(name, syntax, text)
			return err
		})
		if err == nil {
			fmt.Printf("Stored %s as e%d.\n", name, eid)
		}
		return err
	case "solve":
		if syntax == "elpi" {
			prog, err := palace.ParseElpi(text)
			if err != nil {
				return err
			}
			res, err := kg.Now().Elpi(ctx, prog, goal, palace.ElpiOptions{})
			if err != nil {
				return err
			}
			fmt.Print(palace.FormatRows(nil, res.Rows))
			return nil
		}
		prog, err := load()
		if err != nil {
			return err
		}
		sols, err := kg.Now().Solve(ctx, prog, goal, palace.RuleOptions{})
		if err != nil {
			return err
		}
		rows := make([]map[string]palace.Value, len(sols))
		for i, s := range sols {
			rows[i] = s.Bindings
		}
		fmt.Print(palace.FormatRows(nil, rows))
		return nil
	case "check":
		prog, err := load()
		if err != nil {
			return err
		}
		vs, err := kg.Now().Check(ctx, prog, palace.RuleOptions{})
		if err != nil {
			return err
		}
		if len(vs) == 0 {
			fmt.Println("No constraint violations.")
		}
		for _, x := range vs {
			fmt.Printf("%s %v witnesses %v\n", x.Rule, x.Bindings, x.Witnesses)
		}
		return nil
	case "materialize":
		rep, err := kg.Materialize(ctx, palace.TxOptions{Author: "cli", Reason: "materialize " + name, DryRun: dry},
			name, kg.Now(), palace.RuleOptions{})
		if err != nil {
			return err
		}
		fmt.Printf("%s: %d new, %d existing, %d widened, %d withdrawn\n", name, len(rep.Derived), len(rep.Existing),
			len(rep.Widened), len(rep.Reconciled))
		return nil
	}
	return fmt.Errorf("unknown rules action %q", action)
}

func newMemoryKGBundleCmd() *cobra.Command {
	var flagDB, flagTxAsOf string
	var flagSensitive bool
	cmd := &cobra.Command{
		Use:   "bundle <export <eid> | import <file>>",
		Short: "Export a fact with its layers as JSON, or import such a bundle",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			return withPalace(flagDB, func(ctx context.Context, p *palace.Palace) error {
				kg := p.KG()
				switch args[0] {
				case "export":
					eid, err := parseEID(args[1])
					if err != nil {
						return err
					}
					v, err := kg.View(ctx, flagTxAsOf, "")
					if err != nil {
						return err
					}
					b, err := v.Bundle(ctx, eid, palace.BundleOptions{IncludeSensitive: flagSensitive})
					if err != nil {
						return err
					}
					data, err := json.MarshalIndent(b, "", "  ")
					if err != nil {
						return err
					}
					fmt.Println(string(data))
					return nil
				case "import":
					data, err := os.ReadFile(args[1])
					if err != nil {
						return err
					}
					var b palace.Bundle
					if err := json.Unmarshal(data, &b); err != nil {
						return err
					}
					rep, err := kg.Transact(ctx, palace.TxOptions{Author: "cli", Source: args[1], Reason: "import bundle"},
						func(w *palace.KGTx) error { _, err := w.ImportBundle(&b); return err })
					if err != nil {
						return err
					}
					fmt.Printf("Imported: %d new, %d already known.\n", len(rep.Asserted), len(rep.Existing))
					return nil
				}
				return fmt.Errorf("unknown bundle action %q", args[0])
			})
		},
	}
	cmd.Flags().StringVar(&flagDB, "db", "", "Palace database path")
	cmd.Flags().StringVar(&flagTxAsOf, "tx-as-of", "", "Export the fact as believed then")
	cmd.Flags().BoolVar(&flagSensitive, "include-sensitive", false, "Include sealed values (decrypted)")
	return cmd
}

func newMemoryKGAnswersCmd() *cobra.Command {
	var flagDB string
	cmd := &cobra.Command{
		Use:   "answers <list|check|refresh|delete> [name]",
		Short: "Manage saved answers",
		Args:  cobra.RangeArgs(1, 2),
		RunE: func(cmd *cobra.Command, args []string) error {
			return withPalace(flagDB, func(ctx context.Context, p *palace.Palace) error {
				kg := p.KG()
				if args[0] == "list" {
					list, err := kg.ListAnswers(ctx)
					if err != nil {
						return err
					}
					for _, a := range list {
						status := "fresh"
						if a.Stale != "" {
							status = "stale: " + a.Stale
						}
						fmt.Printf("%-20s %-7s %3d rows  %s\n", a.Name, a.Lang, len(a.Rows), status)
					}
					return nil
				}
				if len(args) != 2 {
					return fmt.Errorf("%s needs a name", args[0])
				}
				var (
					a   *palace.SavedAnswer
					err error
				)
				switch args[0] {
				case "check":
					a, err = kg.CheckAnswer(ctx, args[1])
				case "refresh":
					a, err = kg.RefreshAnswer(ctx, args[1])
				case "delete":
					return kg.DeleteAnswer(ctx, args[1])
				default:
					return fmt.Errorf("unknown answers action %q", args[0])
				}
				if err != nil {
					return err
				}
				if a.Stale != "" {
					fmt.Printf("%s is stale: %s\n", a.Name, a.Stale)
					return nil
				}
				fmt.Printf("%s is fresh (checked to tx%d).\n%s", a.Name, a.Checkpoint, palace.FormatRows(a.Vars, a.Rows))
				return nil
			})
		},
	}
	cmd.Flags().StringVar(&flagDB, "db", "", "Palace database path")
	return cmd
}

func newMemoryKGVolatileCmd() *cobra.Command {
	var flagDB string
	cmd := &cobra.Command{
		Use:   "volatile <get|set|clear> <subject> <key> [value]",
		Short: "Read or write volatile state (no history)",
		Args:  cobra.RangeArgs(3, 4),
		RunE: func(cmd *cobra.Command, args []string) error {
			s, k := palace.Ent(args[1]), palace.Ent(args[2])
			return withPalace(flagDB, func(ctx context.Context, p *palace.Palace) error {
				kg := p.KG()
				opts := palace.TxOptions{Author: "cli"}
				switch args[0] {
				case "set":
					if len(args) != 4 {
						return fmt.Errorf("set needs a value")
					}
					_, err := kg.Transact(ctx, opts, func(w *palace.KGTx) error {
						return w.SetVolatile(s, k, palace.Str(args[3]))
					})
					return err
				case "clear":
					_, err := kg.Transact(ctx, opts, func(w *palace.KGTx) error { return w.ClearVolatile(s, k) })
					return err
				case "get":
					vals, err := kg.Now().Resolve(ctx, s, k)
					if err != nil {
						return err
					}
					for _, r := range vals {
						fmt.Println(r.Value.String())
					}
					return nil
				}
				return fmt.Errorf("unknown volatile action %q", args[0])
			})
		},
	}
	cmd.Flags().StringVar(&flagDB, "db", "", "Palace database path")
	return cmd
}
