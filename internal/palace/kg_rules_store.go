package palace

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"sort"
	"strings"
)

// Stored programs and materialization. A program is a statement,
// (program:<name> sys:RuleText "<text>"), so it has transaction time and a
// Materialize run can be traced to a program version. The program statement
// joins the support of every conclusion: retracting or superseding it
// withdraws what it concluded, exactly as retracting a premise does.

// ErrProgramNotFound is returned when no live program has the given name.
var ErrProgramNotFound = errors.New("palace: kg: no such program")

// programHeader records the syntax on the first line of the stored text. It is
// a comment in Datalog, and LoadProgram strips it before parsing N3.
const programHeader = "%% syntax: "

func programSubject(name string) Value { return Ent("program:" + name) }

func programText(syntax, text string) string { return programHeader + syntax + "\n" + text }

func splitProgramText(stored string) (syntax, text string) {
	if rest, ok := strings.CutPrefix(stored, programHeader); ok {
		line, body, _ := strings.Cut(rest, "\n")
		return strings.TrimSpace(line), body
	}
	return "datalog", stored
}

// StoreProgram validates text in the given syntax ("datalog" or "n3") and
// records it as (program:<name> sys:RuleText text). Storing the same text
// again returns the live statement; storing different text supersedes it, so
// the conclusions of the old version are withdrawn and must be materialized
// again. It returns the program's eid.
func (w *KGTx) StoreProgram(name, syntax, text string) (int64, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return 0, errors.New("palace: kg: program name is required")
	}
	syntax = strings.ToLower(strings.TrimSpace(syntax))
	if syntax == "" {
		syntax = "datalog"
	}
	if _, err := ParseRules(syntax, text); err != nil {
		return 0, err
	}
	stored := programText(syntax, text)
	subj := programSubject(name)
	live, err := findProgram(w.ctx, w.q, subj)
	if err != nil {
		return 0, err
	}
	if live != nil {
		if live.Object == Str(stored) {
			w.report.Existing = append(w.report.Existing, live.EID)
			return live.EID, nil
		}
		o := Str(stored)
		return w.Supersede(live.EID, Patch{Object: &o})
	}
	so, _, err := encodeValue(w.ctx, w.q, subj, true)
	if err != nil {
		return 0, err
	}
	eid, _, err := w.engineWrite(so, predRuleText, Str(stored))
	return eid, err
}

// findProgram returns the live rule-text statement of program subject, or nil.
func findProgram(ctx context.Context, q querier, subj Value) (*Statement, error) {
	so, ok, err := encodeValue(ctx, q, subj, false)
	if err != nil || !ok {
		return nil, err
	}
	po, ok, err := encodeValue(ctx, q, sysPred(predRuleText), false)
	if err != nil || !ok {
		return nil, err
	}
	sts, err := queryStatements(ctx, q, "WHERE st.s = ? AND st.p = ? AND st.t_ret IS NULL ORDER BY st.eid", so, po)
	if err != nil || len(sts) == 0 {
		return nil, err
	}
	return &sts[len(sts)-1], nil
}

func loadProgramQ(ctx context.Context, q querier, name string) (*Program, int64, error) {
	st, err := findProgram(ctx, q, programSubject(strings.TrimSpace(name)))
	if err != nil {
		return nil, 0, err
	}
	if st == nil {
		return nil, 0, fmt.Errorf("%w: %s", ErrProgramNotFound, name)
	}
	syntax, text := splitProgramText(st.Object.Text)
	p, err := ParseRules(syntax, text)
	return p, st.EID, err
}

// LoadProgram returns the live program stored under name and its eid.
func (kg *KnowledgeGraph) LoadProgram(ctx context.Context, name string) (*Program, int64, error) {
	var (
		p   *Program
		eid int64
	)
	err := kg.read(ctx, func(q querier) error {
		var err error
		p, eid, err = loadProgramQ(ctx, q, name)
		return err
	})
	return p, eid, err
}

// MaterializeReport is what one Materialize run did.
type MaterializeReport struct {
	// Program is the eid of the program statement the conclusions rest on.
	Program int64
	// Derived are the new conclusions, Existing the ones already stored,
	// Widened the replacements of conclusions whose interval grew, Reconciled
	// the conclusions retracted because they are no longer derivable.
	Derived, Existing, Widened, Reconciled []int64
	Tx                                     *TxReport
}

type conclusion struct {
	eid      int64
	vals     []Value
	sup      []int64
	from, to *int64
	done     bool
}

// Materialize evaluates stored program name over view and writes every
// derived tuple as a derived statement in one transaction. Each fragment is
// written through Derive with the support of its stored statements plus the
// program statement, so a conclusion falls with any premise and with the
// program. The program's own earlier conclusions are not input. A live
// conclusion with the same support and a narrower interval is widened with
// Supersede (support links are replayed). Conclusions of this program that are
// no longer derivable are retracted only when view is the plain Now view
// (no valid-time or graph restriction); any other view only adds. A second run
// with nothing changed writes no statements.
//
// Programs with negation are rejected (ErrNotMaterializable): a conclusion
// that rests on the absence of a statement cannot fall with a premise.
func (kg *KnowledgeGraph) Materialize(ctx context.Context, opts TxOptions, name string, view View, ro RuleOptions) (*MaterializeReport, error) {
	view.kg = kg
	if view.mode != viewNow {
		// Conclusions rest on live premises; a past or history view would
		// derive from statements that may no longer be believed.
		return nil, fmt.Errorf("%w: materialize reads the now view, not %s", ErrNotMaterializable, view)
	}
	rep := &MaterializeReport{}
	tr, err := kg.Transact(ctx, opts, func(w *KGTx) error {
		prog, peid, err := loadProgramQ(ctx, w.q, name)
		if err != nil {
			return err
		}
		rep.Program = peid
		if prog.hasNeg {
			return fmt.Errorf("%w: %s uses negation", ErrNotMaterializable, name)
		}
		for pred, a := range prog.arity {
			if a != 2 {
				return fmt.Errorf("%w: %s has %d arguments, a conclusion is (subject, predicate, object)", ErrNotMaterializable, pred, a)
			}
		}
		return materialize(ctx, w, prog, peid, view, ro, rep)
	})
	if err != nil {
		return nil, err
	}
	rep.Tx = tr
	return rep, nil
}

func materialize(ctx context.Context, w *KGTx, prog *Program, peid int64, view View, ro RuleOptions, rep *MaterializeReport) error {
	// Its own conclusions, and what stands on them, are not input.
	cond, cargs := view.cond("")
	deps, err := walkDependents(ctx, w.q, peid, cond, cargs, 0)
	if err != nil {
		return err
	}
	exclude := map[int64]bool{}
	for _, d := range deps {
		exclude[d] = true
	}
	e := newEvaluator(ctx, w.q, view, prog, ro, exclude)
	if err := e.run(); err != nil {
		return err
	}
	have, err := liveConclusions(ctx, w, peid)
	if err != nil {
		return err
	}
	kept := map[int64]bool{}

	preds := make([]string, 0, len(prog.arity))
	for pred := range prog.arity {
		preds = append(preds, pred)
	}
	sort.Strings(preds)
	for _, pred := range preds {
		rel := e.rels[pred]
		tuples := slices.Clone(rel.tuples)
		slices.SortFunc(tuples, func(a, b *tuple) int { return strings.Compare(tupleKey(a.vals), tupleKey(b.vals)) })
		for _, t := range tuples {
			for _, f := range t.frags {
				if err := e.m.check(); err != nil {
					return err
				}
				if err := writeFragment(w, pred, t.vals, f, peid, have, kept, rep); err != nil {
					return err
				}
			}
		}
	}

	if view.mode != viewNow || view.validAt != nil || len(view.graphs) != 0 {
		return nil
	}
	var stale []*conclusion
	for _, cs := range have {
		for _, c := range cs {
			if !c.done && !kept[c.eid] {
				stale = append(stale, c)
			}
		}
	}
	slices.SortFunc(stale, func(a, b *conclusion) int { return int(a.eid - b.eid) })
	for _, c := range stale {
		ok, err := w.Retract(c.eid)
		if err != nil {
			return err
		}
		if ok {
			rep.Reconciled = append(rep.Reconciled, c.eid)
		}
	}
	return nil
}

func writeFragment(w *KGTx, pred string, vals []Value, f frag, peid int64, have map[string][]*conclusion, kept map[int64]bool, rep *MaterializeReport) error {
	sup := normalizeSupport(append(slices.Clone(f.sup), peid))
	valid := Valid{From: msTime(f.from), To: msTime(f.to)}
	key := pred + "\x00" + tupleKey(vals)
	for _, c := range have[key] {
		if c.done || !slices.Equal(c.sup, sup) || !intervalsOverlap(c.from, c.to, f.from, f.to) {
			continue
		}
		if !ptrEq(c.from, f.from) || !ptrEq(c.to, f.to) {
			if covers(f.from, f.to, c.from, c.to) {
				return widen(w, c, f, rep, kept)
			}
		}
		break
	}
	return deriveFragment(w, pred, vals, valid, sup, kept, rep)
}

func deriveFragment(w *KGTx, pred string, vals []Value, valid Valid, sup []int64, kept map[int64]bool, rep *MaterializeReport) error {
	eid, isNew, err := w.Derive(vals[0], Ent(pred), vals[1], valid, sup)
	if err != nil {
		return fmt.Errorf("palace: kg: materialize %s(%s, %s): %w", pred, vals[0], vals[1], err)
	}
	kept[eid] = true
	if isNew {
		rep.Derived = append(rep.Derived, eid)
	} else {
		rep.Existing = append(rep.Existing, eid)
	}
	return nil
}

func widen(w *KGTx, c *conclusion, f frag, rep *MaterializeReport, kept map[int64]bool) error {
	var p Patch
	if f.from == nil {
		p.ClearValidFrom = true
	} else {
		p.ValidFrom = msTime(f.from)
	}
	if f.to == nil {
		p.ClearValidTo = true
	} else {
		p.ValidTo = msTime(f.to)
	}
	neweid, err := w.Supersede(c.eid, p)
	if err != nil {
		return fmt.Errorf("palace: kg: widen e%d: %w", c.eid, err)
	}
	c.done = true
	kept[neweid] = true
	rep.Widened = append(rep.Widened, neweid)
	return nil
}

func intervalsOverlap(af, at, bf, bt *int64) bool {
	return (bt == nil || af == nil || *af < *bt) && (at == nil || bf == nil || *bf < *at)
}

// liveConclusions loads the live statements derived with peid in their support,
// indexed by predicate and tuple.
func liveConclusions(ctx context.Context, w *KGTx, peid int64) (map[string][]*conclusion, error) {
	out := map[string][]*conclusion{}
	derived, ok, err := w.engineOID(sysPred(predDerivedFrom), false)
	if err != nil || !ok {
		return out, err
	}
	eids, err := collectInts(ctx, w.q,
		"SELECT s >> 4 FROM kg_stmt WHERE p = ? AND o = ? AND (s & 15) = 2 AND t_ret IS NULL ORDER BY s",
		[]any{derived, makeOID(peid, tagStmt)})
	if err != nil {
		return nil, err
	}
	for _, eid := range eids {
		st, err := getStatement(ctx, w.q, eid)
		if err != nil || st == nil {
			return nil, err
		}
		sup, err := liveSupports(ctx, w.q, eid, derived, "t_ret IS NULL", nil)
		if err != nil {
			return nil, err
		}
		c := &conclusion{eid: eid, vals: []Value{st.Subject, st.Object}, sup: sup,
			from: msPtr(st.ValidFrom), to: msPtr(st.ValidTo)}
		key := st.Predicate.Text + "\x00" + tupleKey(c.vals)
		out[key] = append(out[key], c)
	}
	return out, nil
}
