package palace

import (
	"context"
	"database/sql"
	"fmt"
	"slices"
	"strings"
	"time"
)

// viewMode selects which statements a read sees in transaction time.
type viewMode int

const (
	viewNow     viewMode = iota // believed now
	viewAsOf                    // believed after transaction t
	viewHistory                 // every statement ever written
)

// View is a read-only window on the knowledge graph: a transaction-time
// selection (now, as of a transaction, or all history), optionally narrowed
// to the statements valid at one instant. Every scan of a read applies the
// same predicate, after Tiramemsu's view.rs#scan_predicates.
type View struct {
	kg      *KnowledgeGraph
	mode    viewMode
	t       int64
	validAt *int64
	// graphs restricts reads to members of these named graphs; empty is the
	// default graph, the union of everything.
	graphs []string
}

// Now is the view of what is believed now.
func (kg *KnowledgeGraph) Now() View { return View{kg: kg, mode: viewNow} }

// AsOf is the view of what was believed right after transaction t.
func (kg *KnowledgeGraph) AsOf(t int64) View { return View{kg: kg, mode: viewAsOf, t: t} }

// History is the view of every statement ever written, retracted or not.
func (kg *KnowledgeGraph) History() View { return View{kg: kg, mode: viewHistory} }

// AsOfInstant is the view of what was believed at wall-clock time at: the
// last transaction committed at or before it. Before the first transaction it
// is empty.
func (kg *KnowledgeGraph) AsOfInstant(ctx context.Context, at time.Time) (View, error) {
	db, err := kg.database()
	if err != nil {
		return View{}, err
	}
	var t int64
	err = db.QueryRowContext(ctx,
		"SELECT COALESCE(max(t), 0) FROM kg_tx WHERE instant <= ? AND t <= (SELECT value FROM kg_meta WHERE key = 'last_t')",
		at.UnixMilli()).Scan(&t)
	if err != nil {
		return View{}, fmt.Errorf("palace: kg: resolve as-of: %w", err)
	}
	return kg.AsOf(t), nil
}

// ValidAt narrows v to statements valid at instant at.
func (v View) ValidAt(at time.Time) View {
	ms := at.UnixMilli()
	v.validAt = &ms
	return v
}

// IsHistory reports whether v is the history view.
func (v View) IsHistory() bool { return v.mode == viewHistory }

// String describes the view for people.
func (v View) String() string {
	var s string
	switch v.mode {
	case viewNow:
		s = "now"
	case viewAsOf:
		s = fmt.Sprintf("as of tx%d", v.t)
	case viewHistory:
		s = "history"
	}
	if v.validAt != nil {
		s += ", valid at " + time.UnixMilli(*v.validAt).UTC().Format(time.RFC3339)
	}
	if len(v.graphs) > 0 {
		s += ", in graph " + strings.Join(v.graphs, "|")
	}
	return s
}

// cond is the view's SQL predicate over the kg_stmt columns, prefixed with
// alias ("" or "x.").
func (v View) cond(alias string) (string, []any) {
	var (
		parts []string
		args  []any
	)
	switch v.mode {
	case viewNow:
		parts = append(parts, alias+"t_ret IS NULL")
	case viewAsOf:
		parts = append(parts, alias+"t_add <= ?", "("+alias+"t_ret IS NULL OR "+alias+"t_ret > ?)")
		args = append(args, v.t, v.t)
	case viewHistory:
		parts = append(parts, "1")
	}
	if v.validAt != nil {
		parts = append(parts, "("+alias+"v_from IS NULL OR "+alias+"v_from <= ?)",
			"("+alias+"v_to IS NULL OR "+alias+"v_to > ?)")
		args = append(args, *v.validAt, *v.validAt)
	}
	return strings.Join(parts, " AND "), args
}

// Statement is one statement as a read returns it.
type Statement struct {
	EID       int64 `json:"eid"`
	Subject   Value `json:"subject"`
	Predicate Value `json:"predicate"`
	Object    Value `json:"object"`
	// TAdd is the transaction that wrote it, AddedAt that transaction's time.
	TAdd    int64     `json:"t_add"`
	AddedAt time.Time `json:"added_at"`
	// TRet, RetractedAt and RetKind are set once it has been retracted.
	TRet        *int64       `json:"t_ret,omitempty"`
	RetractedAt *time.Time   `json:"retracted_at,omitempty"`
	RetKind     *RetractKind `json:"ret_kind,omitempty"`
	// ValidFrom and ValidTo bound when it held, [from, to); nil is unbounded.
	ValidFrom *time.Time `json:"valid_from,omitempty"`
	ValidTo   *time.Time `json:"valid_to,omitempty"`
}

// Live reports whether the statement has not been retracted.
func (s Statement) Live() bool { return s.TRet == nil }

const stmtSelect = `SELECT st.eid, st.s, st.p, st.o, st.t_add, st.t_ret, st.v_from, st.v_to, st.ret_kind,
	ta.instant, tr.instant
	FROM kg_stmt st JOIN kg_tx ta ON ta.t = st.t_add LEFT JOIN kg_tx tr ON tr.t = st.t_ret `

// queryStatements runs a stmtSelect query and decodes its rows.
func queryStatements(ctx context.Context, q querier, where string, args ...any) ([]Statement, error) {
	rows, err := q.QueryContext(ctx, stmtSelect+where, args...)
	if err != nil {
		return nil, fmt.Errorf("palace: kg: query statements: %w", err)
	}
	type raw struct {
		row     stmtRow
		added   int64
		retired sql.NullInt64
	}
	var raws []raw
	var ids []objectID
	for rows.Next() {
		var (
			r                       raw
			tRet, vFrom, vTo, rKind sql.NullInt64
		)
		if err := rows.Scan(&r.row.eid, &r.row.s, &r.row.p, &r.row.o, &r.row.tAdd, &tRet, &vFrom, &vTo, &rKind,
			&r.added, &r.retired); err != nil {
			rows.Close()
			return nil, fmt.Errorf("palace: kg: scan statement: %w", err)
		}
		r.row.tRet, r.row.vFrom, r.row.vTo, r.row.retKind = nullPtr(tRet), nullPtr(vFrom), nullPtr(vTo), nullPtr(rKind)
		raws = append(raws, r)
		ids = append(ids, r.row.s, r.row.p, r.row.o)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, fmt.Errorf("palace: kg: query statements: %w", err)
	}
	d := newDecoder()
	if err := d.load(ctx, q, ids); err != nil {
		return nil, err
	}
	out := make([]Statement, 0, len(raws))
	for _, r := range raws {
		st := Statement{
			EID:       r.row.eid,
			Subject:   d.value(r.row.s),
			Predicate: d.value(r.row.p),
			Object:    d.value(r.row.o),
			TAdd:      r.row.tAdd,
			AddedAt:   time.UnixMilli(r.added).UTC(),
			TRet:      r.row.tRet,
			ValidFrom: msTime(r.row.vFrom),
			ValidTo:   msTime(r.row.vTo),
		}
		if r.retired.Valid {
			at := time.UnixMilli(r.retired.Int64).UTC()
			st.RetractedAt = &at
		}
		if r.row.retKind != nil {
			k := RetractKind(*r.row.retKind)
			st.RetKind = &k
		}
		out = append(out, st)
	}
	return out, nil
}

func msTime(ms *int64) *time.Time {
	if ms == nil {
		return nil
	}
	t := time.UnixMilli(*ms).UTC()
	return &t
}

// engineFilter returns "AND <alias>p NOT IN (...)" for the engine predicates
// present in the dictionary, so reads of facts and layers hide bookkeeping.
func engineFilter(ctx context.Context, q querier, alias string) (string, []any, error) {
	var args []any
	for _, name := range enginePredicates {
		o, ok, err := encodeValue(ctx, q, sysPred(name), false)
		if err != nil {
			return "", nil, err
		}
		if ok {
			args = append(args, o)
		}
	}
	if len(args) == 0 {
		return "", nil, nil
	}
	return " AND " + alias + "p NOT IN (" + placeholders(len(args)) + ")", args, nil
}

// About returns the visible statements with the entity as subject
// ("subject"), object ("object") or either (""/"both"), oldest first.
func (v View) About(ctx context.Context, entity, direction string) ([]Statement, error) {
	var out []Statement
	err := v.kg.read(ctx, func(q querier) error {
		var err error
		out, err = v.about(ctx, q, entity, direction)
		return err
	})
	return out, err
}

func (v View) about(ctx context.Context, q querier, entity, direction string) ([]Statement, error) {
	ent, ok, err := encodeValue(ctx, q, Ent(entity), false)
	if err != nil || !ok {
		return nil, err
	}
	var where string
	var args []any
	switch direction {
	case "subject":
		where, args = "st.s = ?", []any{ent}
	case "object":
		where, args = "st.o = ?", []any{ent}
	default:
		where, args = "(st.s = ? OR st.o = ?)", []any{ent, ent}
	}
	cond, cargs, err := v.condQ(ctx, q, "st.")
	if err != nil {
		return nil, err
	}
	ef, eargs, err := engineFilter(ctx, q, "st.")
	if err != nil {
		return nil, err
	}
	args = append(append(args, cargs...), eargs...)
	return v.query(ctx, q, "WHERE "+where+" AND "+cond+ef+" ORDER BY st.t_add, st.eid", args...)
}

// Pattern selects statements by any of subject, predicate and object.
type Pattern struct {
	Subject, Predicate, Object *Value
	// IncludeEngine keeps engine bookkeeping (supersedes, confirmations,
	// support links, schema) in the result.
	IncludeEngine bool
	Limit         int
}

// Match returns the visible statements matching p, oldest first.
func (v View) Match(ctx context.Context, p Pattern) ([]Statement, error) {
	var out []Statement
	err := v.kg.read(ctx, func(q querier) error {
		var err error
		out, err = v.match(ctx, q, p)
		return err
	})
	return out, err
}

func (v View) match(ctx context.Context, q querier, p Pattern) ([]Statement, error) {
	var (
		where []string
		args  []any
		enc   = map[string]objectID{}
	)
	// A literal constant on a sensitive predicate is stored sealed under its
	// subject's key: with a bound subject it is sealed the same way and looked
	// up by id; otherwise the rows are filtered after decoding.
	var postFilter *Value
	objectSealed := false
	if p.Predicate != nil && p.Object != nil && isLiteralKind(p.Object.Kind) {
		po, ok, err := encodeValue(ctx, q, *p.Predicate, false)
		if err != nil || !ok {
			return nil, err
		}
		sens, err := isSensitive(ctx, q, po)
		if err != nil {
			return nil, err
		}
		objectSealed = sens
	}
	for _, f := range []struct {
		col, name string
		val       *Value
	}{{"st.s", "s", p.Subject}, {"st.p", "p", p.Predicate}, {"st.o", "o", p.Object}} {
		if f.val == nil {
			continue
		}
		if f.name == "o" && objectSealed {
			s, hasS := enc["s"]
			if !hasS {
				postFilter = f.val
				continue
			}
			o, ok, err := sealedLookup(ctx, q, s, *f.val)
			if err != nil || !ok {
				return nil, err
			}
			where = append(where, f.col+" = ?")
			args = append(args, o)
			continue
		}
		if f.name == "o" && p.Predicate == nil && isLiteralKind(f.val.Kind) {
			// With the predicate free, a sensitive predicate may hold this
			// value sealed: take the sealed candidates too and compare them
			// after decoding.
			anySealed, err := hasSealedTerms(ctx, q)
			if err != nil {
				return nil, err
			}
			if anySealed {
				o, ok, err := encodeValue(ctx, q, *f.val, false)
				if err != nil {
					return nil, err
				}
				if ok {
					where = append(where, "(st.o = ? OR (st.o & 15) = 9)")
					args = append(args, o)
				} else {
					where = append(where, "(st.o & 15) = 9")
				}
				postFilter = f.val
				continue
			}
		}
		o, ok, err := encodeValue(ctx, q, *f.val, false)
		if err != nil || !ok {
			return nil, err
		}
		enc[f.name] = o
		where = append(where, f.col+" = ?")
		args = append(args, o)
	}
	cond, cargs, err := v.condQ(ctx, q, "st.")
	if err != nil {
		return nil, err
	}
	where = append(where, cond)
	args = append(args, cargs...)
	clause := "WHERE " + strings.Join(where, " AND ")
	if !p.IncludeEngine {
		ef, eargs, err := engineFilter(ctx, q, "st.")
		if err != nil {
			return nil, err
		}
		clause += ef
		args = append(args, eargs...)
	}
	clause += " ORDER BY st.t_add, st.eid"
	if p.Limit > 0 && postFilter == nil {
		clause += fmt.Sprintf(" LIMIT %d", p.Limit)
	}
	sts, err := v.query(ctx, q, clause, args...)
	if err != nil || postFilter == nil {
		return sts, err
	}
	out := sts[:0]
	for _, st := range sts {
		if st.Object == *postFilter {
			out = append(out, st)
			if p.Limit > 0 && len(out) == p.Limit {
				break
			}
		}
	}
	return out, nil
}

// Get returns statement eid whatever its state, or nil when it does not exist.
func (kg *KnowledgeGraph) Get(ctx context.Context, eid int64) (*Statement, error) {
	var out *Statement
	err := kg.read(ctx, func(q querier) error {
		st, err := getStatement(ctx, q, eid)
		out = st
		return err
	})
	return out, err
}

func getStatement(ctx context.Context, q querier, eid int64) (*Statement, error) {
	sts, err := queryStatements(ctx, q, "WHERE st.eid = ?", eid)
	if err != nil || len(sts) == 0 {
		return nil, err
	}
	return &sts[0], nil
}

// Layers returns the visible statements about statement eid, excluding
// engine bookkeeping.
func (v View) Layers(ctx context.Context, eid int64) ([]Statement, error) {
	return v.Match(ctx, Pattern{Subject: new(StmtRef(eid))})
}

// Dependents returns what retracting eid would take with it in this view:
// eid first, then every visible statement standing on it, breadth-first. It
// is the cascade walk as a read, and is never truncated.
func (v View) Dependents(ctx context.Context, eid int64) ([]int64, error) {
	var out []int64
	err := v.kg.read(ctx, func(q querier) error {
		cond, args := v.cond("")
		var err error
		out, err = walkDependents(ctx, q, eid, cond, args, 0)
		return err
	})
	return out, err
}

// walkDependents is the cascade walk: from root, breadth-first over the
// statements (visible under view) whose subject or object is a reached
// statement, and over statements derived from a reached statement. Each
// frontier is expanded in eid order. limit > 0 fails the walk with
// ErrCascadeLimit once it exceeds limit statements.
func walkDependents(ctx context.Context, q querier, root int64, view string, viewArgs []any, limit int) ([]int64, error) {
	var n int
	if err := q.QueryRowContext(ctx, "SELECT count(*) FROM kg_stmt WHERE eid = ? AND "+view,
		append([]any{root}, viewArgs...)...).Scan(&n); err != nil {
		return nil, fmt.Errorf("palace: kg: dependents: %w", err)
	}
	if n == 0 {
		return nil, nil
	}
	derived, hasDerived, err := encodeValue(ctx, q, sysPred(predDerivedFrom), false)
	if err != nil {
		return nil, err
	}
	order := []int64{root}
	visited := map[int64]bool{root: true}
	frontier := []int64{root}
	for len(frontier) > 0 {
		var next []int64
		for start := 0; start < len(frontier); start += chunkSize {
			chunk := frontier[start:min(start+chunkSize, len(frontier))]
			ids := make([]any, len(chunk))
			for i, e := range chunk {
				ids[i] = makeOID(e, tagStmt)
			}
			ph := placeholders(len(ids))
			found, err := collectInts(ctx, q,
				"SELECT eid FROM kg_stmt WHERE (s IN ("+ph+") OR o IN ("+ph+")) AND "+view,
				append(append(append([]any{}, ids...), ids...), viewArgs...))
			if err != nil {
				return nil, err
			}
			if hasDerived {
				ds, err := collectInts(ctx, q,
					"SELECT s >> 4 FROM kg_stmt WHERE p = ? AND o IN ("+ph+") AND "+view,
					append(append([]any{derived}, ids...), viewArgs...))
				if err != nil {
					return nil, err
				}
				found = append(found, ds...)
			}
			slices.Sort(found)
			for _, e := range slices.Compact(found) {
				if visited[e] {
					continue
				}
				visited[e] = true
				order = append(order, e)
				next = append(next, e)
				if limit > 0 && len(order) > limit {
					return nil, fmt.Errorf("%w (%d)", ErrCascadeLimit, limit)
				}
			}
		}
		frontier = next
	}
	return order, nil
}

func collectInts(ctx context.Context, q querier, query string, args []any) ([]int64, error) {
	rows, err := q.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("palace: kg: %w", err)
	}
	defer rows.Close()
	var out []int64
	for rows.Next() {
		var v int64
		if err := rows.Scan(&v); err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, rows.Err()
}

// TxInfo describes one transaction.
type TxInfo struct {
	T       int64     `json:"t"`
	Instant time.Time `json:"instant"`
	Author  string    `json:"author,omitempty"`
	Source  string    `json:"source,omitempty"`
	Reason  string    `json:"reason,omitempty"`
}

func loadTx(ctx context.Context, q querier, t int64) (*TxInfo, error) {
	var (
		info TxInfo
		ms   int64
	)
	err := q.QueryRowContext(ctx, "SELECT t, instant, author, source, reason FROM kg_tx WHERE t = ?", t).
		Scan(&info.T, &ms, &info.Author, &info.Source, &info.Reason)
	if errNoRows(err) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("palace: kg: load transaction: %w", err)
	}
	info.Instant = time.UnixMilli(ms).UTC()
	return &info, nil
}

// Transaction returns committed transaction t, or nil.
func (kg *KnowledgeGraph) Transaction(ctx context.Context, t int64) (*TxInfo, error) {
	var out *TxInfo
	err := kg.read(ctx, func(q querier) error {
		var err error
		out, err = loadTx(ctx, q, t)
		return err
	})
	return out, err
}

// Event is one entry of the log: a statement asserted or retracted at t.
type Event struct {
	T    int64        `json:"t"`
	EID  int64        `json:"eid"`
	Op   string       `json:"op"` // "assert" or "retract"
	Kind *RetractKind `json:"kind,omitempty"`
}

// Events returns the log of committed transactions after since and up to
// until (0 = the last committed one), ordered by t, asserts before retracts,
// then eid. The log ignores views: it is the whole history, and replaying it
// up to t reproduces AsOf(t).
func (kg *KnowledgeGraph) Events(ctx context.Context, since, until int64) ([]Event, error) {
	var out []Event
	err := kg.read(ctx, func(q querier) error {
		last, err := lastT(ctx, q)
		if err != nil {
			return err
		}
		if until <= 0 || until > last {
			until = last
		}
		rows, err := q.QueryContext(ctx, `SELECT t, eid, op, kind FROM kg_event WHERE t > ? AND t <= ?
			ORDER BY t, CASE op WHEN 'assert' THEN 0 ELSE 1 END, eid`, since, until)
		if err != nil {
			return fmt.Errorf("palace: kg: events: %w", err)
		}
		defer rows.Close()
		for rows.Next() {
			var (
				e    Event
				kind sql.NullInt64
			)
			if err := rows.Scan(&e.T, &e.EID, &e.Op, &kind); err != nil {
				return err
			}
			if kind.Valid {
				k := RetractKind(kind.Int64)
				e.Kind = &k
			}
			out = append(out, e)
		}
		return rows.Err()
	})
	return out, err
}

// mask hides what happened after an AsOf bound: a statement retracted later
// than the view's transaction is shown as live, so a historical row never
// changes when the future is written.
func (v View) mask(sts []Statement) []Statement {
	if v.mode != viewAsOf {
		return sts
	}
	for i := range sts {
		if sts[i].TRet != nil && *sts[i].TRet > v.t {
			sts[i].TRet, sts[i].RetKind, sts[i].RetractedAt = nil, nil, nil
		}
	}
	return sts
}

// query runs queryStatements and masks the rows for the view.
func (v View) query(ctx context.Context, q querier, where string, args ...any) ([]Statement, error) {
	sts, err := queryStatements(ctx, q, where, args...)
	return v.mask(sts), err
}

// condQ is cond plus the view's graph restriction: a statement is visible
// when it, or the statement it is about (one layer up), has a membership in
// one of the graphs that the same view selects.
func (v View) condQ(ctx context.Context, q querier, alias string) (string, []any, error) {
	c, args := v.cond(alias)
	if len(v.graphs) == 0 {
		return c, args, nil
	}
	in, ok, err := encodeValue(ctx, q, sysPred(predInGraph), false)
	if err != nil {
		return "", nil, err
	}
	var gs []any
	if ok {
		for _, g := range v.graphs {
			o, found, err := encodeValue(ctx, q, Ent(g), false)
			if err != nil {
				return "", nil, err
			}
			if found {
				gs = append(gs, o)
			}
		}
	}
	if len(gs) == 0 {
		return c + " AND 0", args, nil
	}
	mc, margs := v.cond("m.")
	sub := " AND EXISTS (SELECT 1 FROM kg_stmt m WHERE m.p = ? AND m.o IN (" + placeholders(len(gs)) + ")" +
		" AND (m.s = ((" + alias + "eid << 4) | 2) OR ((" + alias + "s & 15) = 2 AND m.s = " + alias + "s))" +
		" AND " + mc + ")"
	args = append(append(append(args, in), gs...), margs...)
	return c + sub, args, nil
}

// InGraph restricts v to the members of the named graphs (any of them).
func (v View) InGraph(graphs ...string) View {
	v.graphs = nil
	for _, g := range graphs {
		if g = strings.TrimSpace(g); g != "" {
			v.graphs = append(v.graphs, entityID(g))
		}
	}
	return v
}

// Graphs reports the graph restriction of v.
func (v View) Graphs() []string { return slices.Clone(v.graphs) }

// hasSealedTerms reports whether any sealed value exists.
func hasSealedTerms(ctx context.Context, q querier) (bool, error) {
	var n int
	err := q.QueryRowContext(ctx, "SELECT EXISTS (SELECT 1 FROM kg_term WHERE tag = ?)", tagSealed).Scan(&n)
	if err != nil {
		return false, fmt.Errorf("palace: kg: sealed terms: %w", err)
	}
	return n == 1, nil
}
