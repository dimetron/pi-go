package palace

import (
	"context"
	"fmt"
	"sort"
	"time"
)

// Evidence is what the graph records in support of one statement. Nothing is
// scored: absent evidence is absent.
type Evidence struct {
	EID           int64      `json:"eid"`
	ValidFrom     *time.Time `json:"valid_from,omitempty"`
	ValidTo       *time.Time `json:"valid_to,omitempty"`
	AddedAt       time.Time  `json:"added_at"`
	Author        string     `json:"author,omitempty"`
	Confidence    *float64   `json:"confidence,omitempty"`
	Sources       []string   `json:"sources,omitempty"`
	SourceFiles   []string   `json:"source_files,omitempty"`
	Confirmations int        `json:"confirmations,omitempty"`
}

// evidenceFor loads the evidence layers of the given statements under v.
func (v View) evidenceFor(ctx context.Context, q querier, sts []Statement) (map[int64]*Evidence, error) {
	out := make(map[int64]*Evidence, len(sts))
	if len(sts) == 0 {
		return out, nil
	}
	ids := make([]any, 0, len(sts))
	for _, st := range sts {
		out[st.EID] = &Evidence{EID: st.EID, ValidFrom: st.ValidFrom, ValidTo: st.ValidTo, AddedAt: st.AddedAt}
		ids = append(ids, makeOID(st.EID, tagStmt))
	}
	// Authors come from the writing transactions.
	authors := map[int64]string{}
	for _, st := range sts {
		if _, done := authors[st.TAdd]; done {
			out[st.EID].Author = authors[st.TAdd]
			continue
		}
		tx, err := loadTx(ctx, q, st.TAdd)
		if err != nil {
			return nil, err
		}
		if tx != nil {
			authors[st.TAdd] = tx.Author
			out[st.EID].Author = tx.Author
		}
	}
	var preds []any
	predName := map[objectID]string{}
	for _, name := range []string{predConfidence, predSource, predSourceFile} {
		o, ok, err := encodeValue(ctx, q, Ent(name), false)
		if err != nil {
			return nil, err
		}
		if ok {
			preds = append(preds, o)
			predName[o] = name
		}
	}
	if o, ok, err := encodeValue(ctx, q, sysPred(predConfirmedBy), false); err != nil {
		return nil, err
	} else if ok {
		preds = append(preds, o)
		predName[o] = predConfirmedBy
	}
	if len(preds) == 0 {
		return out, nil
	}
	cond, cargs := v.cond("")
	for start := 0; start < len(ids); start += chunkSize {
		chunk := ids[start:min(start+chunkSize, len(ids))]
		args := append(append(append([]any{}, chunk...), preds...), cargs...)
		rows, err := q.QueryContext(ctx, "SELECT s, p, o FROM kg_stmt WHERE s IN ("+placeholders(len(chunk))+
			") AND p IN ("+placeholders(len(preds))+") AND "+cond+" ORDER BY eid", args...)
		if err != nil {
			return nil, fmt.Errorf("palace: kg: evidence: %w", err)
		}
		type layer struct{ s, p, o objectID }
		var layers []layer
		var vals []objectID
		for rows.Next() {
			var l layer
			if err := rows.Scan(&l.s, &l.p, &l.o); err != nil {
				rows.Close()
				return nil, err
			}
			layers = append(layers, l)
			vals = append(vals, l.o)
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return nil, err
		}
		d := newDecoder()
		if err := d.load(ctx, q, vals); err != nil {
			return nil, err
		}
		for _, l := range layers {
			ev := out[l.s.payload()]
			val := d.value(l.o)
			switch predName[l.p] {
			case predConfidence:
				if val.Kind == KindNumber {
					c := val.Num
					ev.Confidence = &c
				}
			case predSource:
				ev.Sources = append(ev.Sources, val.String())
			case predSourceFile:
				ev.SourceFiles = append(ev.SourceFiles, val.String())
			case predConfirmedBy:
				ev.Confirmations++
			}
		}
	}
	return out, nil
}

// Explanation is everything the graph knows about one statement.
type Explanation struct {
	Statement Statement `json:"statement"`
	// Visible reports whether the statement is visible in the view asked.
	Visible  bool        `json:"visible"`
	View     string      `json:"view"`
	Evidence Evidence    `json:"evidence"`
	Layers   []Statement `json:"layers,omitempty"`
	// Supports are the statements it was derived from.
	Supports []Statement `json:"supports,omitempty"`
	// Supersedes lists the versions it corrected, newest first;
	// SupersededBy the versions that corrected it, oldest first.
	Supersedes   []int64  `json:"supersedes,omitempty"`
	SupersededBy []int64  `json:"superseded_by,omitempty"`
	Confirmed    []TxInfo `json:"confirmed,omitempty"`
	AddedBy      *TxInfo  `json:"added_by,omitempty"`
	RetractedBy  *TxInfo  `json:"retracted_by,omitempty"`
	// Dependents is what retracting it would take with it, in this view.
	Dependents []int64 `json:"dependents,omitempty"`
}

// Explain gathers statement eid's layers, supports, lineage, confirmations
// and dependents under v, all from one snapshot. It returns nil when eid
// does not exist.
func (v View) Explain(ctx context.Context, eid int64) (*Explanation, error) {
	var out *Explanation
	err := v.kg.read(ctx, func(q querier) error {
		st, err := v.lookup(ctx, q, eid)
		if err != nil || st == nil {
			return err
		}
		ex := &Explanation{Statement: *st, View: v.String()}
		cond, cargs := v.cond("")
		var n int
		if err := q.QueryRowContext(ctx, "SELECT count(*) FROM kg_stmt WHERE eid = ? AND "+cond,
			append([]any{eid}, cargs...)...).Scan(&n); err != nil {
			return fmt.Errorf("palace: kg: explain: %w", err)
		}
		ex.Visible = n > 0
		if ex.Layers, err = v.match(ctx, q, Pattern{Subject: new(StmtRef(eid))}); err != nil {
			return err
		}
		ev, err := v.evidenceFor(ctx, q, []Statement{*st})
		if err != nil {
			return err
		}
		ex.Evidence = *ev[eid]
		if derived, ok, err := encodeValue(ctx, q, sysPred(predDerivedFrom), false); err != nil {
			return err
		} else if ok {
			sup, err := liveSupports(ctx, q, eid, derived, cond, cargs)
			if err != nil {
				return err
			}
			for _, s := range sup {
				if sst, err := v.lookup(ctx, q, s); err != nil {
					return err
				} else if sst != nil {
					ex.Supports = append(ex.Supports, *sst)
				}
			}
		}
		if ex.Supersedes, ex.SupersededBy, err = lineage(ctx, q, eid); err != nil {
			return err
		}
		if ex.Confirmed, err = v.confirmations(ctx, q, eid); err != nil {
			return err
		}
		if ex.AddedBy, err = loadTx(ctx, q, st.TAdd); err != nil {
			return err
		}
		if st.TRet != nil {
			if ex.RetractedBy, err = loadTx(ctx, q, *st.TRet); err != nil {
				return err
			}
		}
		deps, err := walkDependents(ctx, q, eid, cond, cargs, 0)
		if err != nil {
			return err
		}
		if len(deps) > 1 {
			ex.Dependents = deps[1:]
		}
		out = ex
		return nil
	})
	return out, err
}

// lineage follows sys:Supersedes links in both directions, over all history.
func lineage(ctx context.Context, q querier, eid int64) (older, newer []int64, err error) {
	sup, ok, err := encodeValue(ctx, q, sysPred(predSupersedes), false)
	if err != nil || !ok {
		return nil, nil, err
	}
	step := func(query string, from int64) (int64, error) {
		ids, err := collectInts(ctx, q, query, []any{makeOID(from, tagStmt), sup})
		if err != nil || len(ids) == 0 {
			return 0, err
		}
		return objectID(ids[0]).payload(), nil
	}
	seen := map[int64]bool{eid: true}
	for cur := eid; ; {
		prev, err := step("SELECT o FROM kg_stmt WHERE s = ? AND p = ? ORDER BY eid LIMIT 1", cur)
		if err != nil {
			return nil, nil, err
		}
		if prev == 0 || seen[prev] {
			break
		}
		seen[prev] = true
		older = append(older, prev)
		cur = prev
	}
	for cur := eid; ; {
		next, err := step("SELECT s FROM kg_stmt WHERE o = ? AND p = ? ORDER BY eid LIMIT 1", cur)
		if err != nil {
			return nil, nil, err
		}
		if next == 0 || seen[next] {
			break
		}
		seen[next] = true
		newer = append(newer, next)
		cur = next
	}
	return older, newer, nil
}

func (v View) confirmations(ctx context.Context, q querier, eid int64) ([]TxInfo, error) {
	p, ok, err := encodeValue(ctx, q, sysPred(predConfirmedBy), false)
	if err != nil || !ok {
		return nil, err
	}
	cond, cargs := v.cond("")
	ts, err := collectInts(ctx, q, "SELECT o FROM kg_stmt WHERE s = ? AND p = ? AND "+cond+" ORDER BY eid",
		append([]any{makeOID(eid, tagStmt), p}, cargs...))
	if err != nil {
		return nil, err
	}
	var out []TxInfo
	for _, o := range ts {
		if tx, err := loadTx(ctx, q, objectID(o).payload()); err != nil {
			return nil, err
		} else if tx != nil {
			out = append(out, *tx)
		}
	}
	return out, nil
}

// ConflictFilter narrows Conflicts.
type ConflictFilter struct {
	Subject, Predicate string
	// Limit caps the subject/predicate pairs returned; 0 means 20.
	Limit int
}

// Conflict is one subject/predicate pair holding two or more distinct
// objects at the same valid time, each with its evidence.
type Conflict struct {
	Subject   Value           `json:"subject"`
	Predicate Value           `json:"predicate"`
	Values    []ConflictValue `json:"values"`
}

// ConflictValue is one of the disagreeing objects.
type ConflictValue struct {
	Object     Value      `json:"object"`
	Statements []Evidence `json:"statements"`
}

// Conflicts lists where memory disagrees with itself: visible statements with
// the same subject and predicate, different objects, and overlapping valid
// time. Multi-valued predicates (a person knows several people) appear too;
// the evidence is there to decide, the graph does not. It is not defined on
// the history view, where a corrected value and its correction were never
// believed together.
func (v View) Conflicts(ctx context.Context, f ConflictFilter) ([]Conflict, error) {
	if v.mode == viewHistory {
		return nil, ErrHistoryConflicts
	}
	limit := f.Limit
	if limit <= 0 {
		limit = 20
	}
	var out []Conflict
	err := v.kg.read(ctx, func(q querier) error {
		ca, aargs := v.cond("a.")
		cb, bargs := v.cond("b.")
		where := ca + " AND " + cb
		args := append(append([]any{}, aargs...), bargs...)
		for _, sel := range []struct {
			col, name string
		}{{"a.s", f.Subject}, {"a.p", f.Predicate}} {
			if sel.name == "" {
				continue
			}
			o, ok, err := encodeValue(ctx, q, Ent(sel.name), false)
			if err != nil || !ok {
				return err
			}
			where += " AND " + sel.col + " = ?"
			args = append(args, o)
		}
		ef, eargs, err := engineFilter(ctx, q, "a.")
		if err != nil {
			return err
		}
		where += ef
		args = append(args, eargs...)
		rows, err := q.QueryContext(ctx, `SELECT a.s, a.p, a.eid, b.eid FROM kg_stmt a
			JOIN kg_stmt b ON a.s = b.s AND a.p = b.p AND a.o <> b.o
			WHERE `+where+` AND (a.v_from IS NULL OR b.v_to IS NULL OR a.v_from < b.v_to)
			AND (b.v_from IS NULL OR a.v_to IS NULL OR b.v_from < a.v_to)
			ORDER BY a.s, a.p, a.eid, b.eid LIMIT 5000`, args...)
		if err != nil {
			return fmt.Errorf("palace: kg: conflicts: %w", err)
		}
		type key struct{ s, p objectID }
		var order []key
		groups := map[key]map[int64]bool{}
		for rows.Next() {
			var (
				k    key
				a, b int64
			)
			if err := rows.Scan(&k.s, &k.p, &a, &b); err != nil {
				rows.Close()
				return err
			}
			if groups[k] == nil {
				if len(order) == limit {
					continue
				}
				groups[k] = map[int64]bool{}
				order = append(order, k)
			}
			groups[k][a], groups[k][b] = true, true
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return err
		}
		for _, k := range order {
			eids := make([]any, 0, len(groups[k]))
			for e := range groups[k] {
				eids = append(eids, e)
			}
			sts, err := v.query(ctx, q, "WHERE st.eid IN ("+placeholders(len(eids))+") ORDER BY st.eid", eids...)
			if err != nil {
				return err
			}
			ev, err := v.evidenceFor(ctx, q, sts)
			if err != nil {
				return err
			}
			c := Conflict{Subject: sts[0].Subject, Predicate: sts[0].Predicate}
			byObj := map[string]int{}
			for _, st := range sts {
				key := fmt.Sprintf("%d|%s", st.Object.Kind, st.Object.String())
				i, ok := byObj[key]
				if !ok {
					i = len(c.Values)
					byObj[key] = i
					c.Values = append(c.Values, ConflictValue{Object: st.Object})
				}
				c.Values[i].Statements = append(c.Values[i].Statements, *ev[st.EID])
			}
			out = append(out, c)
		}
		return nil
	})
	return out, err
}

// TextSearch finds visible statements whose subject or object is an entity
// or string matching the words of query (all of them), newest first.
func (v View) TextSearch(ctx context.Context, query string, limit int) ([]Statement, error) {
	var out []Statement
	err := v.kg.read(ctx, func(q querier) error {
		var err error
		out, err = v.textSearch(ctx, q, query, limit)
		return err
	})
	return out, err
}

func (v View) textSearch(ctx context.Context, q querier, query string, limit int) ([]Statement, error) {
	match := sanitizeFTS5Query(query)
	if match == "" {
		return nil, nil
	}
	if limit <= 0 {
		limit = 20
	}
	rows, err := q.QueryContext(ctx, `SELECT t.id, t.tag FROM kg_term_fts f JOIN kg_term t ON t.id = f.rowid
		WHERE kg_term_fts MATCH ? ORDER BY f.rank LIMIT 200`, match)
	if err != nil {
		return nil, fmt.Errorf("palace: kg: text search: %w", err)
	}
	var terms []any
	for rows.Next() {
		var id, tag int64
		if err := rows.Scan(&id, &tag); err != nil {
			rows.Close()
			return nil, err
		}
		terms = append(terms, makeOID(id, int(tag)))
	}
	err = rows.Err()
	rows.Close()
	if err != nil || len(terms) == 0 {
		return nil, err
	}
	ph := placeholders(len(terms))
	cond, cargs := v.cond("st.")
	ef, eargs, err := engineFilter(ctx, q, "st.")
	if err != nil {
		return nil, err
	}
	args := append(append(append(append([]any{}, terms...), terms...), cargs...), eargs...)
	return v.query(ctx, q, "WHERE (st.s IN ("+ph+") OR st.o IN ("+ph+")) AND "+cond+ef+
		fmt.Sprintf(" ORDER BY st.t_add DESC, st.eid DESC LIMIT %d", limit), args...)
}

// Stats returns aggregate knowledge-graph statistics. TripleCount and
// ActiveTriples keep their old meaning: live facts, and live facts whose
// valid time is still open.
func (kg *KnowledgeGraph) Stats(ctx context.Context) (*KGStats, error) {
	stats := &KGStats{}
	err := kg.read(ctx, func(q querier) error {
		if err := q.QueryRowContext(ctx, "SELECT COUNT(*) FROM entities").Scan(&stats.EntityCount); err != nil {
			return fmt.Errorf("palace: kg stats entities: %w", err)
		}
		ef, eargs, err := engineFilter(ctx, q, "")
		if err != nil {
			return err
		}
		facts := "t_ret IS NULL AND (s & 15) = 1" + ef
		layers := "t_ret IS NULL AND (s & 15) = 2" + ef
		for _, c := range []struct {
			dst   *int
			query string
			args  []any
		}{
			{&stats.TripleCount, "SELECT COUNT(*) FROM kg_stmt WHERE " + facts, eargs},
			{&stats.ActiveTriples, "SELECT COUNT(*) FROM kg_stmt WHERE v_to IS NULL AND " + facts, eargs},
			{&stats.Statements, "SELECT COUNT(*) FROM kg_stmt", nil},
			{&stats.Layers, "SELECT COUNT(*) FROM kg_stmt WHERE " + layers, eargs},
			{&stats.Retracted, "SELECT COUNT(*) FROM kg_stmt WHERE t_ret IS NOT NULL", nil},
		} {
			if err := q.QueryRowContext(ctx, c.query, c.args...).Scan(c.dst); err != nil {
				return fmt.Errorf("palace: kg stats: %w", err)
			}
		}
		last, err := lastT(ctx, q)
		if err != nil {
			return err
		}
		stats.Transactions = int(last)
		ps, err := collectInts(ctx, q, "SELECT DISTINCT p FROM kg_stmt WHERE "+facts, eargs)
		if err != nil {
			return err
		}
		ids := make([]objectID, len(ps))
		for i, p := range ps {
			ids[i] = objectID(p)
		}
		d := newDecoder()
		if err := d.load(ctx, q, ids); err != nil {
			return err
		}
		for _, id := range ids {
			stats.Predicates = append(stats.Predicates, d.value(id).String())
		}
		sort.Strings(stats.Predicates)
		return nil
	})
	if err != nil {
		return nil, err
	}
	return stats, nil
}
