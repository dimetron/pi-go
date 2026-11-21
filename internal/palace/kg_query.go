package palace

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"regexp"
	"slices"
	"sort"
	"strconv"
	"strings"
)

// Query languages, after Tiramemsu's query-ir capability. SPARQL and Cypher
// subsets both compile to the small logical IR below, which is evaluated in Go
// over a View by index nested-loop join: every triple pattern is one Match
// with the constants bound so far. Whatever a View hides (retracted
// statements, a graph scope, an as-of bound, engine predicates) is hidden from
// queries too, because the evaluator reads only through View.match.

// ErrQueryParse is wrapped by every query syntax error; the message carries
// the position as line:col.
var ErrQueryParse = errors.New("palace: kg: query syntax error")

// ErrUnsupportedQuery is matched (errors.Is) by UnsupportedQueryError.
var ErrUnsupportedQuery = errors.New("palace: kg: unsupported query construct")

// UnsupportedQueryError reports a construct outside the supported subset. It
// is returned before anything executes.
type UnsupportedQueryError struct {
	// Pos is the byte offset of the construct in the query text.
	Pos int
	// Construct names it ("property path", "CREATE", ...).
	Construct string
}

func (e *UnsupportedQueryError) Error() string {
	return fmt.Sprintf("%v: %s at offset %d", ErrUnsupportedQuery, e.Construct, e.Pos)
}

// Is makes errors.Is(err, ErrUnsupportedQuery) hold.
func (e *UnsupportedQueryError) Is(target error) bool { return target == ErrUnsupportedQuery }

// lineCol converts a byte offset into 1-based line and column.
func lineCol(text string, pos int) (line, col int) {
	line, col = 1, 1
	for i, r := range text {
		if i >= pos {
			break
		}
		if r == '\n' {
			line++
			col = 1
		} else {
			col++
		}
	}
	return line, col
}

func parseErr(text string, pos int, format string, args ...any) error {
	l, c := lineCol(text, pos)
	return fmt.Errorf("%w: %d:%d: %s", ErrQueryParse, l, c, fmt.Sprintf(format, args...))
}

// Dialect selects how the evaluator counts solutions.
type Dialect int

// The dialects. SPARQL is a set over (s, p, o) triples: statements with equal
// terms yield one solution. Cypher is a bag over statements: one row per eid,
// and one MATCH may not bind the same relationship twice.
const (
	DialectSPARQL Dialect = iota
	DialectCypher
)

// QTerm is a variable or a constant.
type QTerm struct {
	Var string
	Val Value
}

// QVar is the variable named n.
func QVar(n string) QTerm { return QTerm{Var: n} }

// QConst is the constant v.
func QConst(v Value) QTerm { return QTerm{Val: v} }

// IsVar reports whether t is a variable.
func (t QTerm) IsVar() bool { return t.Var != "" }

// ObjClass restricts what a triple's object may be.
type ObjClass uint8

// Object classes: Cypher relationships have entity objects, properties
// literal ones.
const (
	ObjAny ObjClass = iota
	ObjEntity
	ObjLiteral
)

// QElem is one element of a conjunction.
type QElem interface{ qelem() }

// QTriple matches statements (S P O). EID, when set, binds the statement
// itself (as a StmtRef) to that variable; a bound EID variable restricts the
// match to that statement. Aux marks a property accessor: it needs its subject
// bound already and never counts toward negation.
type QTriple struct {
	S, P, O QTerm
	EID     string
	Class   ObjClass
	Aux     bool
}

// QFilter keeps the solutions for which Expr is true.
type QFilter struct{ Expr *QExpr }

// QOptional is a left join. Aux marks property accessors.
type QOptional struct {
	Group []QElem
	Aux   bool
}

// QUnion concatenates the solutions of its groups.
type QUnion struct{ Groups [][]QElem }

// QNotExists keeps the solutions for which Group has no solution.
type QNotExists struct{ Group []QElem }

// QGraph evaluates Group inside the named graph.
type QGraph struct {
	Name  string
	Group []QElem
}

// QNode binds Var to every entity of the view (a bare Cypher node pattern);
// a bound Var is kept when it is an entity. It reads every statement, so a
// query with a QNode has no known predicate set.
type QNode struct{ Var string }

func (QNode) qelem()      {}
func (QTriple) qelem()    {}
func (QFilter) qelem()    {}
func (QOptional) qelem()  {}
func (QUnion) qelem()     {}
func (QNotExists) qelem() {}
func (QGraph) qelem()     {}

// QExpr is a filter expression. Op is one of var, const, = != < <= > >= && ||
// ! bound regex contains strstarts.
type QExpr struct {
	Op   string
	Args []*QExpr
	Var  string
	Val  Value
	Re   *regexp.Regexp
}

// QProj is one output column: variable Var shown as Name, or count(*).
type QProj struct {
	Name  string
	Var   string
	Count bool
}

// QOrder sorts by a column name (or, failing that, a variable).
type QOrder struct {
	Key  string
	Desc bool
}

// Query is the logical IR both languages compile to.
type Query struct {
	Dialect  Dialect
	Vars     []QProj
	Distinct bool
	Where    []QElem
	OrderBy  []QOrder
	Limit    int
	HasLimit bool
	Offset   int
}

// QueryOptions bounds a query evaluation.
type QueryOptions struct{ Budget Budget }

// QueryResult is the rows of a query and what they stood on.
type QueryResult struct {
	// Vars is the column order.
	Vars []string
	Rows []map[string]Value
	// Deps is the sorted eids of every statement any returned row matched.
	Deps []int64
	// Preds is the predicates the patterns read, or nil when any pattern has
	// a variable predicate.
	Preds []Value
	// Negative reports that the query uses OPTIONAL, NOT EXISTS, negation or
	// COUNT, so asserting a statement can make a row disappear.
	Negative bool
}

// RunQuery parses text in lang ("sparql" or "cypher") and runs it.
func (v View) RunQuery(ctx context.Context, lang, text string, opts QueryOptions) (*QueryResult, error) {
	var (
		q   *Query
		err error
	)
	switch strings.ToLower(strings.TrimSpace(lang)) {
	case "sparql":
		q, err = ParseSPARQL(text)
	case "cypher":
		q, err = ParseCypher(text)
	default:
		return nil, fmt.Errorf("%w: unknown query language %q (want sparql or cypher)", ErrQueryParse, lang)
	}
	if err != nil {
		return nil, err
	}
	return v.Select(ctx, q, opts)
}

// Select evaluates q in one snapshot of the view under one budget.
func (v View) Select(ctx context.Context, q *Query, opts QueryOptions) (*QueryResult, error) {
	var res *QueryResult
	err := v.kg.read(ctx, func(sq querier) error {
		var err error
		res, err = v.selectQ(ctx, sq, q, newMeter(ctx, opts.Budget))
		return err
	})
	return res, err
}

type solution struct {
	vars  map[string]Value
	eids  []int64 // every statement matched, in join order
	scope []int64 // relationships bound in the current MATCH (Cypher)
}

type evaluator struct {
	ctx context.Context
	q   querier
	v   View
	m   *meter
	d   Dialect
}

type entry struct {
	row  map[string]Value
	vars map[string]Value
	eids []int64
}

// selectQ evaluates query on q (an open snapshot) charging m.
func (v View) selectQ(ctx context.Context, q querier, query *Query, m *meter) (*QueryResult, error) {
	if query == nil {
		return nil, fmt.Errorf("%w: nil query", ErrQueryParse)
	}
	e := &evaluator{ctx: ctx, q: q, v: v, m: m, d: query.Dialect}
	sols, err := e.group(query.Where, []solution{{vars: map[string]Value{}}})
	if err != nil {
		return nil, err
	}
	sort.SliceStable(sols, func(i, j int) bool { return slices.Compare(sols[i].eids, sols[j].eids) < 0 })

	entries, err := project(query, sols)
	if err != nil {
		return nil, err
	}
	if len(query.OrderBy) > 0 {
		sort.SliceStable(entries, func(i, j int) bool {
			for _, o := range query.OrderBy {
				a, aok := entries[i].lookup(o.Key)
				b, bok := entries[j].lookup(o.Key)
				c := orderCmp(a, aok, b, bok)
				if o.Desc {
					c = -c
				}
				if c != 0 {
					return c < 0
				}
			}
			return false
		})
	}
	if query.Offset > 0 {
		entries = entries[min(query.Offset, len(entries)):]
	}
	if query.HasLimit && query.Limit < len(entries) {
		entries = entries[:max(query.Limit, 0)]
	}

	res := &QueryResult{}
	for _, p := range query.Vars {
		res.Vars = append(res.Vars, p.Name)
	}
	seen := map[int64]bool{}
	for _, en := range entries {
		n := 0
		for _, val := range en.row {
			n += valueBytes(val)
		}
		if err := m.take(1, n); err != nil {
			return nil, err
		}
		res.Rows = append(res.Rows, en.row)
		for _, id := range en.eids {
			if !seen[id] {
				seen[id] = true
				res.Deps = append(res.Deps, id)
			}
		}
	}
	slices.Sort(res.Deps)
	res.Preds, res.Negative = analyze(query)
	return res, nil
}

func (en entry) lookup(key string) (Value, bool) {
	if v, ok := en.row[key]; ok {
		return v, true
	}
	v, ok := en.vars[key]
	return v, ok
}

// project turns solutions into output entries: plain projection with optional
// DISTINCT, or count(*) grouped by the other columns.
func project(query *Query, sols []solution) ([]entry, error) {
	hasCount := false
	for _, p := range query.Vars {
		hasCount = hasCount || p.Count
	}
	var (
		out   []entry
		index = map[string]int{}
		cnt   []int64
	)
	for _, s := range sols {
		row := map[string]Value{}
		var key strings.Builder
		for _, p := range query.Vars {
			if p.Count {
				continue
			}
			val, ok := s.vars[p.Var]
			if ok {
				row[p.Name] = val
			}
			fmt.Fprintf(&key, "%t|%d|%s|%d|%g;", ok, val.Kind, val.Text, val.Int, val.Num)
		}
		if i, ok := index[key.String()]; ok && (hasCount || query.Distinct) {
			out[i].eids = append(out[i].eids, s.eids...)
			if hasCount {
				cnt[i]++
			}
			continue
		}
		index[key.String()] = len(out)
		out = append(out, entry{row: row, vars: s.vars, eids: slices.Clone(s.eids)})
		cnt = append(cnt, 1)
	}
	if !hasCount {
		return out, nil
	}
	if len(out) == 0 && len(query.Vars) == 1 { // count(*) alone over nothing is one row, 0
		out = append(out, entry{row: map[string]Value{}})
		cnt = append(cnt, 0)
	}
	for i := range out {
		out[i].vars = nil
		for _, p := range query.Vars {
			if p.Count {
				out[i].row[p.Name] = Int(cnt[i])
			}
		}
	}
	return out, nil
}

// group evaluates a conjunction; filters and anti-joins run last, over the
// whole group, as in SPARQL.
func (e *evaluator) group(elems []QElem, in []solution) ([]solution, error) {
	var (
		deferred []QElem
		cur      = in
		err      error
	)
	for _, el := range elems {
		switch el.(type) {
		case QFilter, QNotExists:
			deferred = append(deferred, el)
			continue
		}
		if cur, err = e.elem(el, cur); err != nil {
			return nil, err
		}
	}
	for _, el := range deferred {
		if cur, err = e.elem(el, cur); err != nil {
			return nil, err
		}
	}
	return cur, nil
}

func (e *evaluator) elem(el QElem, in []solution) ([]solution, error) {
	switch x := el.(type) {
	case QTriple:
		return e.triple(x, in)
	case QNode:
		return e.node(x, in)
	case QFilter:
		var out []solution
		for _, s := range in {
			if err := e.m.check(); err != nil {
				return nil, err
			}
			if toTri(x.Expr.eval(s.vars)) == triTrue {
				out = append(out, s)
			}
		}
		return out, nil
	case QOptional:
		var out []solution
		for _, s := range in {
			if err := e.m.check(); err != nil {
				return nil, err
			}
			inner := solution{vars: s.vars, eids: s.eids}
			got, err := e.group(x.Group, []solution{inner})
			if err != nil {
				return nil, err
			}
			if len(got) == 0 {
				out = append(out, s)
				continue
			}
			for _, g := range got {
				g.scope = s.scope
				out = append(out, g)
			}
		}
		return out, nil
	case QUnion:
		var out []solution
		for _, g := range x.Groups {
			got, err := e.group(g, in)
			if err != nil {
				return nil, err
			}
			out = append(out, got...)
		}
		return out, nil
	case QNotExists:
		var out []solution
		for _, s := range in {
			if err := e.m.check(); err != nil {
				return nil, err
			}
			got, err := e.group(x.Group, []solution{{vars: s.vars, eids: s.eids}})
			if err != nil {
				return nil, err
			}
			if len(got) == 0 {
				out = append(out, s)
			}
		}
		return out, nil
	case QGraph:
		e2 := *e
		e2.v = e.v.InGraph(x.Name)
		return e2.group(x.Group, in)
	}
	return nil, fmt.Errorf("%w: unknown element %T", ErrQueryParse, el)
}

type spo struct{ s, p, o Value }

func (e *evaluator) triple(t QTriple, in []solution) ([]solution, error) {
	var out []solution
	for _, s := range in {
		if err := e.m.check(); err != nil {
			return nil, err
		}
		bound := func(term QTerm) *Value {
			if !term.IsVar() {
				v := term.Val
				return &v
			}
			if v, ok := s.vars[term.Var]; ok {
				return &v
			}
			return nil
		}
		pat := Pattern{Subject: bound(t.S), Predicate: bound(t.P), Object: bound(t.O)}
		if t.Aux && pat.Subject == nil {
			continue
		}
		if pat.Subject != nil && isLiteralKind(pat.Subject.Kind) || pat.Predicate != nil && pat.Predicate.Kind != KindEntity {
			continue
		}
		var wantEID int64
		eidBound := false
		if t.EID != "" {
			if v, ok := s.vars[t.EID]; ok {
				if v.Kind != KindStatement {
					continue
				}
				wantEID, eidBound = v.Int, true
			}
		}
		sts, err := e.v.match(e.ctx, e.q, pat)
		if err != nil {
			return nil, err
		}
		if err := e.m.takeStatements(sts); err != nil {
			return nil, err
		}
		type hit struct {
			st   Statement
			eids []int64
		}
		var hits []hit
		at := map[spo]int{}
		for _, st := range sts {
			if eidBound && st.EID != wantEID {
				continue
			}
			switch t.Class {
			case ObjEntity:
				if st.Object.Kind != KindEntity {
					continue
				}
			case ObjLiteral:
				if !isLiteralKind(st.Object.Kind) {
					continue
				}
			}
			if e.d == DialectCypher {
				if t.Class == ObjEntity && !t.Aux && !eidBound && slices.Contains(s.scope, st.EID) {
					continue // relationship isomorphism within one MATCH
				}
				hits = append(hits, hit{st, []int64{st.EID}})
				continue
			}
			k := spo{st.Subject, st.Predicate, st.Object}
			if i, ok := at[k]; ok {
				hits[i].eids = append(hits[i].eids, st.EID)
				continue
			}
			at[k] = len(hits)
			hits = append(hits, hit{st, []int64{st.EID}})
		}
		for _, h := range hits {
			vars := make(map[string]Value, len(s.vars)+4)
			for k, v := range s.vars {
				vars[k] = v
			}
			ok := bindTerm(vars, t.S, h.st.Subject) && bindTerm(vars, t.P, h.st.Predicate) && bindTerm(vars, t.O, h.st.Object)
			if ok && t.EID != "" {
				ok = bindTerm(vars, QVar(t.EID), StmtRef(h.st.EID))
			}
			if !ok {
				continue
			}
			ns := solution{vars: vars, eids: append(slices.Clone(s.eids), h.eids...), scope: s.scope}
			if t.Class == ObjEntity && !t.Aux {
				ns.scope = append(slices.Clone(s.scope), h.eids...)
			}
			out = append(out, ns)
		}
	}
	return out, nil
}

// node enumerates the entities of the view: every entity that is the subject
// or object of a visible statement, in order of first appearance.
func (e *evaluator) node(n QNode, in []solution) ([]solution, error) {
	var ents []Value
	var out []solution
	for _, s := range in {
		if err := e.m.check(); err != nil {
			return nil, err
		}
		if v, ok := s.vars[n.Var]; ok {
			if v.Kind == KindEntity {
				out = append(out, s)
			}
			continue
		}
		if ents == nil {
			sts, err := e.v.match(e.ctx, e.q, Pattern{})
			if err != nil {
				return nil, err
			}
			if err := e.m.takeStatements(sts); err != nil {
				return nil, err
			}
			seen := map[Value]bool{}
			for _, st := range sts {
				for _, v := range []Value{st.Subject, st.Object} {
					if v.Kind == KindEntity && !seen[v] {
						seen[v] = true
						ents = append(ents, v)
					}
				}
			}
			if ents == nil {
				ents = []Value{}
			}
		}
		// Each binding is an intermediate row: charge it before allocating.
		if err := e.m.take(len(ents), 0); err != nil {
			return nil, err
		}
		for _, v := range ents {
			vars := make(map[string]Value, len(s.vars)+1)
			for k, x := range s.vars {
				vars[k] = x
			}
			vars[n.Var] = v
			out = append(out, solution{vars: vars, eids: s.eids, scope: s.scope})
		}
	}
	return out, nil
}

func bindTerm(vars map[string]Value, t QTerm, v Value) bool {
	if !t.IsVar() {
		return true
	}
	if cur, ok := vars[t.Var]; ok {
		return cur == v
	}
	vars[t.Var] = v
	return true
}

// analyze reports the predicates a query reads and whether it is non-monotonic.
func analyze(q *Query) (preds []Value, negative bool) {
	seen := map[string]bool{}
	varPred := false
	for _, p := range q.Vars {
		negative = negative || p.Count
	}
	// OFFSET skips rows whose retraction shifts later rows into the answer.
	negative = negative || q.Offset > 0
	var walk func(elems []QElem)
	walk = func(elems []QElem) {
		for _, el := range elems {
			switch x := el.(type) {
			case QTriple:
				if x.P.IsVar() {
					varPred = true
				} else if !seen[x.P.Val.Text] {
					seen[x.P.Val.Text] = true
					preds = append(preds, x.P.Val)
				}
			case QNode:
				varPred = true
			case QFilter:
				negative = negative || x.Expr.nonMonotonic()
			case QOptional:
				negative = negative || !x.Aux
				walk(x.Group)
			case QUnion:
				for _, g := range x.Groups {
					walk(g)
				}
			case QNotExists:
				negative = true
				walk(x.Group)
			case QGraph:
				// Membership changes add and remove rows without touching
				// the facts the query matched.
				negative = true
				if !seen[predInGraph] {
					seen[predInGraph] = true
					preds = append(preds, sysPred(predInGraph))
				}
				walk(x.Group)
			}
		}
	}
	walk(q.Where)
	if varPred {
		return nil, negative
	}
	slices.SortFunc(preds, func(a, b Value) int { return cmp.Compare(a.Text, b.Text) })
	return preds, negative
}

func (x *QExpr) nonMonotonic() bool {
	if x == nil {
		return false
	}
	if x.Op == "!" || x.Op == "bound" {
		return true
	}
	return slices.ContainsFunc(x.Args, (*QExpr).nonMonotonic)
}

// Kleene three-valued logic: a filter keeps a row only when its expression is
// true; unknown (an unbound variable, an ill-typed comparison) rejects it, but
// only at that boundary, so !(unknown) stays unknown.
type tri int8

const (
	triFalse tri = iota
	triTrue
	triUnknown
)

type xval struct {
	v    Value
	null bool
}

func toTri(x xval) tri {
	if x.null || x.v.Kind != KindBool {
		return triUnknown
	}
	if x.v.Int != 0 {
		return triTrue
	}
	return triFalse
}

func fromTri(t tri) xval {
	switch t {
	case triTrue:
		return xval{v: Bool(true)}
	case triFalse:
		return xval{v: Bool(false)}
	}
	return xval{null: true}
}

func (x *QExpr) eval(vars map[string]Value) xval {
	switch x.Op {
	case "var":
		v, ok := vars[x.Var]
		return xval{v: v, null: !ok}
	case "const":
		return xval{v: x.Val}
	case "bound":
		_, ok := vars[x.Args[0].Var]
		return xval{v: Bool(ok)}
	case "&&", "||":
		a, b := toTri(x.Args[0].eval(vars)), toTri(x.Args[1].eval(vars))
		short := triFalse
		if x.Op == "||" {
			short = triTrue
		}
		switch {
		case a == short || b == short:
			return fromTri(short)
		case a == triUnknown || b == triUnknown:
			return fromTri(triUnknown)
		}
		return fromTri(1 - short)
	case "!":
		switch toTri(x.Args[0].eval(vars)) {
		case triTrue:
			return fromTri(triFalse)
		case triFalse:
			return fromTri(triTrue)
		}
		return fromTri(triUnknown)
	case "=", "!=", "<", "<=", ">", ">=":
		a, b := x.Args[0].eval(vars), x.Args[1].eval(vars)
		if a.null || b.null {
			return fromTri(triUnknown)
		}
		c, ok := compareValues(a.v, b.v)
		switch x.Op {
		case "=":
			return xval{v: Bool(ok && c == 0)}
		case "!=":
			return xval{v: Bool(!ok || c != 0)}
		}
		if !ok {
			return fromTri(triUnknown)
		}
		r := false
		switch x.Op {
		case "<":
			r = c < 0
		case "<=":
			r = c <= 0
		case ">":
			r = c > 0
		case ">=":
			r = c >= 0
		}
		return xval{v: Bool(r)}
	case "regex", "contains", "strstarts":
		a := x.Args[0].eval(vars)
		if a.null || (a.v.Kind != KindString && a.v.Kind != KindEntity) {
			return fromTri(triUnknown)
		}
		if x.Op == "regex" {
			return xval{v: Bool(x.Re.MatchString(a.v.Text))}
		}
		b := x.Args[1].eval(vars)
		if b.null || (b.v.Kind != KindString && b.v.Kind != KindEntity) {
			return fromTri(triUnknown)
		}
		if x.Op == "contains" {
			return xval{v: Bool(strings.Contains(a.v.Text, b.v.Text))}
		}
		return xval{v: Bool(strings.HasPrefix(a.v.Text, b.v.Text))}
	}
	return fromTri(triUnknown)
}

func numericOf(v Value) (float64, bool) {
	switch v.Kind {
	case KindInt:
		return float64(v.Int), true
	case KindNumber:
		return v.Num, true
	}
	return 0, false
}

// compareValues orders two values of comparable kinds; ok is false across
// kinds (other than int and number).
func compareValues(a, b Value) (int, bool) {
	if a.Kind == KindInt && b.Kind == KindInt {
		return cmp.Compare(a.Int, b.Int), true
	}
	if x, ok := numericOf(a); ok {
		if y, ok := numericOf(b); ok {
			return cmp.Compare(x, y), true
		}
		return 0, false
	}
	if a.Kind != b.Kind {
		return 0, false
	}
	switch a.Kind {
	case KindString, KindEntity:
		return strings.Compare(a.Text, b.Text), true
	case KindBool, KindTime, KindStatement, KindTx:
		return cmp.Compare(a.Int, b.Int), true
	}
	return 0, false
}

func kindRank(v Value) int {
	switch v.Kind {
	case KindInt, KindNumber:
		return 0
	case KindString:
		return 1
	}
	return int(v.Kind) + 1
}

// orderCmp is the total order of ORDER BY: unbound first, then by kind, then
// by value.
func orderCmp(a Value, aok bool, b Value, bok bool) int {
	switch {
	case !aok && !bok:
		return 0
	case !aok:
		return -1
	case !bok:
		return 1
	}
	if ra, rb := kindRank(a), kindRank(b); ra != rb {
		return cmp.Compare(ra, rb)
	}
	c, _ := compareValues(a, b)
	return c
}

// compileRegex builds the regex of a regex() call; flags may hold "i".
func compileRegex(pattern, flags string) (*regexp.Regexp, error) {
	if strings.Contains(flags, "i") {
		pattern = "(?i)" + pattern
	}
	return regexp.Compile(pattern)
}

// intTok parses a non-negative integer token.
func intTok(s string) (int, error) { return strconv.Atoi(s) }
