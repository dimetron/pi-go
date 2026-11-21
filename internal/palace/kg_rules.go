package palace

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"math"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode"
)

// Rules: Datalog programs over the knowledge graph, after Tiramemsu's
// datalog-language, datalog-evaluation, datalog-temporal and
// datalog-constraints capabilities.
//
// A program is a set of rules and denial constraints:
//
//	reports(X, Y) :- manager(X, Y).
//	reports(X, Z) :- manager(X, Y), reports(Y, Z).
//	peer(X, Y)    :- reports(X, M), reports(Y, M), X != Y, not reports(X, Y).
//	:- age(X, A), A < 0.                       % constraint
//
// Atoms over a predicate that no rule defines read the view ("stored" atoms,
// p(S, O); triple(S, P, O) reads any predicate). Evaluation is stratified
// semi-naive bottom-up. Valid time is pointwise: every derived tuple keeps
// fragments, one per distinct support set (the eids of the stored statements
// a derivation used), each valid over the intersection of its supports'
// intervals. Disjoint proofs keep both fragments; a fragment whose interval is
// contained in another fragment's is redundant and dropped (ties keep the
// smaller support set), so the result does not depend on input order.

// Errors returned by the rule engine.
var (
	ErrUnsafeRule        = errors.New("palace: kg: unsafe rule")
	ErrUnstratified      = errors.New("palace: kg: program is not stratified")
	ErrNotMaterializable = errors.New("palace: kg: program cannot be materialized")
	ErrRuleParse         = errors.New("palace: kg: rule parse error")
)

// Defaults for RuleOptions.
const (
	DefaultMaxFacts     = 100_000
	DefaultMaxFragments = 8
)

// RuleOptions bounds one rule evaluation.
type RuleOptions struct {
	// Budget bounds statements read and tuples derived (one meter per call).
	Budget Budget
	// MaxFacts bounds the derived tuples; 0 means DefaultMaxFacts.
	MaxFacts int
	// MaxFragments bounds the non-redundant fragments one tuple may keep; 0
	// means DefaultMaxFragments. Exceeding it fails with ErrBudget: a
	// truncated answer is never returned as complete.
	MaxFragments int
}

func (o RuleOptions) withDefaults() RuleOptions {
	if o.MaxFacts <= 0 {
		o.MaxFacts = DefaultMaxFacts
	}
	if o.MaxFragments <= 0 {
		o.MaxFragments = DefaultMaxFragments
	}
	return o
}

// --- IR ---

type term struct {
	v int // variable id, or -1 for a constant
	c Value
}

func (t term) isVar() bool { return t.v >= 0 }

type litKind uint8

const (
	litAtom litKind = iota
	litNot
	litBuiltin
)

type literal struct {
	kind litKind
	pred string // predicate, or the builtin operator
	args []term
}

type rule struct {
	head  *literal // nil for a constraint
	body  []literal
	vars  []string
	src   string
	index int
}

// Program is a compiled set of rules and constraints.
type Program struct {
	rules       []*rule
	constraints []*rule
	arity       map[string]int // intensional predicates
	stratum     map[string]int
	nStrata     int
	hasNeg      bool
}

// Rules is the number of rules (constraints excluded).
func (p *Program) Rules() int { return len(p.rules) }

// Constraints is the number of constraints.
func (p *Program) Constraints() int { return len(p.constraints) }

// HasNegation reports whether any rule body uses negation.
func (p *Program) HasNegation() bool { return p.hasNeg }

const triplePred = "triple"

var builtinOps = map[string]bool{"=": true, "!=": true, "<": true, "<=": true, ">": true, ">=": true, "contains": true}

func parseErr(line, col int, format string, args ...any) error {
	return fmt.Errorf("%w: %d:%d: %s", ErrRuleParse, line, col, fmt.Sprintf(format, args...))
}

// compile checks arities, safety and stratification and orders the strata.
func compile(rules, constraints []*rule) (*Program, error) {
	p := &Program{rules: rules, constraints: constraints, arity: map[string]int{}, stratum: map[string]int{}}
	for _, r := range rules {
		h := r.head
		if h.pred == triplePred || builtinOps[h.pred] {
			return nil, fmt.Errorf("%w: %q cannot be defined by a rule", ErrRuleParse, h.pred)
		}
		if a, ok := p.arity[h.pred]; ok && a != len(h.args) {
			return nil, fmt.Errorf("%w: %s is used with %d and %d arguments", ErrRuleParse, h.pred, a, len(h.args))
		}
		p.arity[h.pred] = len(h.args)
	}
	all := slices.Concat(rules, constraints)
	for _, r := range all {
		if err := p.checkRule(r); err != nil {
			return nil, err
		}
	}
	for _, r := range rules {
		for _, l := range r.body {
			if l.kind == litNot {
				p.hasNeg = true
			}
		}
	}
	return p, p.stratify()
}

func (p *Program) checkRule(r *rule) error {
	pos := map[int]bool{}
	for _, l := range r.body {
		if l.kind != litAtom {
			continue
		}
		for _, t := range l.args {
			if t.isVar() {
				pos[t.v] = true
			}
		}
	}
	need := func(l literal, what string) error {
		for _, t := range l.args {
			if t.isVar() && !pos[t.v] {
				return fmt.Errorf("%w: variable %s in %s of %q does not occur in a positive atom", ErrUnsafeRule, r.vars[t.v], what, r.src)
			}
		}
		return nil
	}
	if r.head != nil {
		if err := need(*r.head, "the head"); err != nil {
			return err
		}
	}
	for _, l := range r.body {
		var err error
		switch l.kind {
		case litNot:
			err = need(l, "a negated atom")
		case litBuiltin:
			err = need(l, "a builtin")
		}
		if err != nil {
			return err
		}
		if l.kind == litBuiltin {
			continue
		}
		want, intensional := p.arity[l.pred]
		switch {
		case intensional:
		case l.pred == triplePred:
			want = 3
		default:
			want = 2
		}
		if len(l.args) != want {
			return fmt.Errorf("%w: %s takes %d arguments, got %d in %q", ErrRuleParse, l.pred, want, len(l.args), r.src)
		}
	}
	return nil
}

// stratify assigns each intensional predicate the least stratum at which
// every negated premise lies strictly below it.
func (p *Program) stratify() error {
	n := len(p.arity)
	for i := 0; ; i++ {
		changed := false
		for _, r := range p.rules {
			for _, l := range r.body {
				if _, ok := p.arity[l.pred]; !ok || l.kind == litBuiltin {
					continue
				}
				need := p.stratum[l.pred]
				if l.kind == litNot {
					need++
				}
				if p.stratum[r.head.pred] < need {
					p.stratum[r.head.pred] = need
					changed = true
				}
			}
		}
		if !changed {
			break
		}
		if i > n {
			return fmt.Errorf("%w: negation inside a recursive cycle", ErrUnstratified)
		}
	}
	for _, s := range p.stratum {
		p.nStrata = max(p.nStrata, s+1)
	}
	if p.nStrata == 0 && len(p.rules) > 0 {
		p.nStrata = 1
	}
	return nil
}

// --- Datalog syntax ---

type tokKind int

const (
	tkEOF tokKind = iota
	tkIdent
	tkVar
	tkStr
	tkNum
	tkPunct
)

type token struct {
	kind       tokKind
	text       string
	val        Value
	line, col  int
	start, end int
}

func numValue(s string) Value {
	if i, err := strconv.ParseInt(s, 10, 64); err == nil {
		return Int(i)
	}
	f, _ := strconv.ParseFloat(s, 64)
	return Num(f)
}

func isIdentStart(r rune) bool { return r == '_' || unicode.IsLetter(r) }
func isIdentPart(r rune) bool  { return r == '_' || unicode.IsLetter(r) || unicode.IsDigit(r) }

// lexDatalog splits src into tokens. Comments run from % to the end of line.
func lexDatalog(src string) ([]token, []rune, error) {
	rs := []rune(src)
	var toks []token
	line, col := 1, 1
	i := 0
	adv := func(n int) {
		for k := 0; k < n; k++ {
			if rs[i] == '\n' {
				line++
				col = 1
			} else {
				col++
			}
			i++
		}
	}
	at := func(k int) rune {
		if i+k < len(rs) {
			return rs[i+k]
		}
		return 0
	}
	for i < len(rs) {
		c := rs[i]
		switch {
		case unicode.IsSpace(c):
			adv(1)
		case c == '%':
			for i < len(rs) && rs[i] != '\n' {
				adv(1)
			}
		case c == '"':
			start, l, cl := i, line, col
			adv(1)
			var sb strings.Builder
			closed := false
			for i < len(rs) {
				ch := rs[i]
				if ch == '"' {
					adv(1)
					closed = true
					break
				}
				if ch == '\\' && i+1 < len(rs) {
					adv(1)
					switch rs[i] {
					case 'n':
						sb.WriteRune('\n')
					case 't':
						sb.WriteRune('\t')
					default:
						sb.WriteRune(rs[i])
					}
					adv(1)
					continue
				}
				sb.WriteRune(ch)
				adv(1)
			}
			if !closed {
				return nil, nil, parseErr(l, cl, "unterminated string")
			}
			toks = append(toks, token{kind: tkStr, text: sb.String(), val: Str(sb.String()), line: l, col: cl, start: start, end: i})
		case unicode.IsDigit(c) || (c == '-' && unicode.IsDigit(at(1))):
			start, l, cl := i, line, col
			adv(1)
			for unicode.IsDigit(at(0)) {
				adv(1)
			}
			if at(0) == '.' && unicode.IsDigit(at(1)) {
				adv(1)
				for unicode.IsDigit(at(0)) {
					adv(1)
				}
			}
			text := string(rs[start:i])
			toks = append(toks, token{kind: tkNum, text: text, val: numValue(text), line: l, col: cl, start: start, end: i})
		case isIdentStart(c):
			start, l, cl := i, line, col
			adv(1)
			for i < len(rs) {
				ch := rs[i]
				if isIdentPart(ch) || ((ch == '-' || ch == ':') && isIdentPart(at(1))) {
					adv(1)
					continue
				}
				break
			}
			text := string(rs[start:i])
			kind := tkIdent
			if c == '_' || unicode.IsUpper(c) {
				kind = tkVar
			}
			toks = append(toks, token{kind: kind, text: text, line: l, col: cl, start: start, end: i})
		default:
			start, l, cl := i, line, col
			op := ""
			for _, cand := range []string{":-", "!=", "<=", ">=", "(", ")", ",", ".", "=", "<", ">"} {
				if strings.HasPrefix(string(rs[i:min(i+2, len(rs))]), cand) {
					op = cand
					break
				}
			}
			if op == "" {
				return nil, nil, parseErr(l, cl, "unexpected character %q", c)
			}
			adv(len(op))
			toks = append(toks, token{kind: tkPunct, text: op, line: l, col: cl, start: start, end: i})
		}
	}
	toks = append(toks, token{kind: tkEOF, line: line, col: col, start: len(rs), end: len(rs)})
	return toks, rs, nil
}

// ruleBuilder numbers the variables of one clause.
type ruleBuilder struct {
	vars []string
	ids  map[string]int
}

func newRuleBuilder() *ruleBuilder { return &ruleBuilder{ids: map[string]int{}} }

func (b *ruleBuilder) variable(name string) term {
	if name == "_" {
		b.vars = append(b.vars, "_")
		return term{v: len(b.vars) - 1}
	}
	if id, ok := b.ids[name]; ok {
		return term{v: id}
	}
	b.vars = append(b.vars, name)
	b.ids[name] = len(b.vars) - 1
	return term{v: len(b.vars) - 1}
}

func constTerm(v Value) term { return term{v: -1, c: v} }

type dlParser struct {
	toks []token
	rs   []rune
	pos  int
}

func (p *dlParser) peek() token        { return p.toks[p.pos] }
func (p *dlParser) peekAt(k int) token { return p.toks[min(p.pos+k, len(p.toks)-1)] }
func (p *dlParser) next() token {
	t := p.toks[p.pos]
	if t.kind != tkEOF {
		p.pos++
	}
	return t
}

func (p *dlParser) isPunct(s string) bool {
	t := p.peek()
	return t.kind == tkPunct && t.text == s
}

func (p *dlParser) expect(s string) error {
	t := p.next()
	if t.kind != tkPunct || t.text != s {
		return parseErr(t.line, t.col, "expected %q, found %s", s, describe(t))
	}
	return nil
}

func describe(t token) string {
	if t.kind == tkEOF {
		return "end of input"
	}
	return strconv.Quote(t.text)
}

// term parses a variable, constant, string or number.
func (p *dlParser) term(b *ruleBuilder) (term, error) {
	t := p.next()
	switch t.kind {
	case tkVar:
		return b.variable(t.text), nil
	case tkIdent:
		return constTerm(Ent(t.text)), nil
	case tkStr, tkNum:
		return constTerm(t.val), nil
	}
	return term{}, parseErr(t.line, t.col, "expected a term, found %s", describe(t))
}

func (p *dlParser) args(b *ruleBuilder) ([]term, error) {
	if err := p.expect("("); err != nil {
		return nil, err
	}
	var out []term
	for {
		t, err := p.term(b)
		if err != nil {
			return nil, err
		}
		out = append(out, t)
		if p.isPunct(",") {
			p.next()
			continue
		}
		break
	}
	return out, p.expect(")")
}

func (p *dlParser) atom(b *ruleBuilder) (literal, error) {
	t := p.next()
	if t.kind != tkIdent {
		return literal{}, parseErr(t.line, t.col, "expected a predicate name, found %s", describe(t))
	}
	args, err := p.args(b)
	if err != nil {
		return literal{}, err
	}
	return literal{kind: litAtom, pred: entityID(t.text), args: args}, nil
}

var cmpOps = map[string]bool{"=": true, "!=": true, "<": true, "<=": true, ">": true, ">=": true}

func (p *dlParser) bodyLiteral(b *ruleBuilder) (literal, error) {
	t := p.peek()
	if t.kind == tkIdent && t.text == "not" && p.peekAt(1).kind == tkIdent && p.peekAt(2).text == "(" {
		p.next()
		l, err := p.atom(b)
		if err != nil {
			return l, err
		}
		if l.pred == "contains" {
			return l, parseErr(t.line, t.col, "contains cannot be negated")
		}
		l.kind = litNot
		return l, nil
	}
	if t.kind == tkIdent && p.peekAt(1).kind == tkPunct && p.peekAt(1).text == "(" {
		l, err := p.atom(b)
		if err != nil {
			return l, err
		}
		if l.pred == "contains" {
			if len(l.args) != 2 {
				return l, parseErr(t.line, t.col, "contains takes 2 arguments")
			}
			l.kind = litBuiltin
		}
		return l, nil
	}
	left, err := p.term(b)
	if err != nil {
		return literal{}, err
	}
	op := p.next()
	if op.kind != tkPunct || !cmpOps[op.text] {
		return literal{}, parseErr(op.line, op.col, "expected a comparison operator, found %s", describe(op))
	}
	right, err := p.term(b)
	if err != nil {
		return literal{}, err
	}
	return literal{kind: litBuiltin, pred: op.text, args: []term{left, right}}, nil
}

// ParseDatalog parses a Datalog program with denial constraints. Facts
// (rules without a body) are rejected: facts belong in the graph, where they
// have transaction and valid time.
func ParseDatalog(text string) (*Program, error) {
	toks, rs, err := lexDatalog(text)
	if err != nil {
		return nil, err
	}
	p := &dlParser{toks: toks, rs: rs}
	var rules, cons []*rule
	for p.peek().kind != tkEOF {
		start := p.peek()
		b := newRuleBuilder()
		r := &rule{}
		if p.isPunct(":-") {
			p.next()
		} else {
			h, err := p.atom(b)
			if err != nil {
				return nil, err
			}
			if h.pred == "contains" {
				return nil, parseErr(start.line, start.col, "contains is a builtin and cannot be defined")
			}
			r.head = &h
			if p.isPunct(".") {
				return nil, parseErr(start.line, start.col, "facts are not supported in a program: assert them in the graph")
			}
			if err := p.expect(":-"); err != nil {
				return nil, err
			}
		}
		for {
			l, err := p.bodyLiteral(b)
			if err != nil {
				return nil, err
			}
			r.body = append(r.body, l)
			if p.isPunct(",") {
				p.next()
				continue
			}
			break
		}
		if err := p.expect("."); err != nil {
			return nil, err
		}
		r.vars = b.vars
		r.src = strings.TrimSpace(string(rs[start.start:p.toks[p.pos-1].end]))
		if r.head == nil {
			r.index = len(cons) + 1
			cons = append(cons, r)
		} else {
			r.index = len(rules) + 1
			rules = append(rules, r)
		}
	}
	return compile(rules, cons)
}

// ParseRules parses text in the named syntax: "datalog" or "n3".
func ParseRules(syntax, text string) (*Program, error) {
	switch strings.ToLower(strings.TrimSpace(syntax)) {
	case "datalog", "":
		return ParseDatalog(text)
	case "n3":
		return ParseN3(text)
	}
	return nil, fmt.Errorf("%w: unknown syntax %q (want datalog or n3)", ErrRuleParse, syntax)
}

// --- fragments and relations ---

// frag is one derivation class of a tuple: the stored statements it rests on
// and the interval over which all of them hold.
type frag struct {
	sup      []int64
	from, to *int64
}

func mergeSorted(a, b []int64) []int64 {
	out := make([]int64, 0, len(a)+len(b))
	i, j := 0, 0
	for i < len(a) || j < len(b) {
		switch {
		case j >= len(b) || (i < len(a) && a[i] < b[j]):
			out = append(out, a[i])
			i++
		case i >= len(a) || b[j] < a[i]:
			out = append(out, b[j])
			j++
		default:
			out = append(out, a[i])
			i++
			j++
		}
	}
	return out
}

func combine(a, b frag) (frag, bool) {
	f := frag{sup: mergeSorted(a.sup, b.sup), from: a.from, to: a.to}
	if b.from != nil && (f.from == nil || *b.from > *f.from) {
		f.from = b.from
	}
	if b.to != nil && (f.to == nil || *b.to < *f.to) {
		f.to = b.to
	}
	if f.from != nil && f.to != nil && *f.from >= *f.to {
		return frag{}, false
	}
	return f, true
}

func ptrEq(a, b *int64) bool { return (a == nil && b == nil) || (a != nil && b != nil && *a == *b) }

// covers reports whether interval [af, at) contains [bf, bt).
func covers(af, at, bf, bt *int64) bool {
	return (af == nil || (bf != nil && *af <= *bf)) && (at == nil || (bt != nil && *at >= *bt))
}

// dominates reports whether a makes b redundant: a holds over all of b's
// interval and, on equal intervals, has the smaller support set.
func dominates(a, b frag) bool {
	if !covers(a.from, a.to, b.from, b.to) {
		return false
	}
	if ptrEq(a.from, b.from) && ptrEq(a.to, b.to) {
		return slices.Compare(a.sup, b.sup) <= 0
	}
	return true
}

func cmpPtr(a, b *int64, nilFirst bool) int {
	switch {
	case a == nil && b == nil:
		return 0
	case a == nil:
		if nilFirst {
			return -1
		}
		return 1
	case b == nil:
		if nilFirst {
			return 1
		}
		return -1
	}
	return cmp.Compare(*a, *b)
}

func cmpFrag(a, b frag) int {
	if c := cmpPtr(a.from, b.from, true); c != 0 {
		return c
	}
	if c := cmpPtr(a.to, b.to, false); c != 0 {
		return c
	}
	return slices.Compare(a.sup, b.sup)
}

type tuple struct {
	vals  []Value
	frags []frag
}

type relation struct {
	arity  int
	tuples []*tuple
	byKey  map[string]*tuple
	idx    []map[Value][]*tuple
}

func newRelation(arity int) *relation {
	r := &relation{arity: arity, byKey: map[string]*tuple{}, idx: make([]map[Value][]*tuple, arity)}
	for i := range r.idx {
		r.idx[i] = map[Value][]*tuple{}
	}
	return r
}

func tupleKey(vals []Value) string {
	var sb strings.Builder
	for _, v := range vals {
		sb.WriteByte(byte('a' + v.Kind))
		switch v.Kind {
		case KindEntity, KindString:
			sb.WriteString(strconv.Itoa(len(v.Text)))
			sb.WriteByte(':')
			sb.WriteString(v.Text)
		case KindNumber:
			sb.WriteString(strconv.FormatUint(math.Float64bits(v.Num), 16))
		default:
			sb.WriteString(strconv.FormatInt(v.Int, 10))
		}
		sb.WriteByte(';')
	}
	return sb.String()
}

// addFrag keeps f unless an existing fragment makes it redundant; it drops
// the fragments f makes redundant. It reports whether the tuple changed.
func (t *tuple) addFrag(f frag, maxFrags int) (bool, error) {
	for _, g := range t.frags {
		if dominates(g, f) {
			return false, nil
		}
	}
	kept := make([]frag, 0, len(t.frags)+1)
	for _, g := range t.frags {
		if !dominates(f, g) {
			kept = append(kept, g)
		}
	}
	kept = append(kept, f)
	if len(kept) > maxFrags {
		return false, fmt.Errorf("%w: more than %d disjoint derivations of one tuple", ErrBudget, maxFrags)
	}
	slices.SortFunc(kept, cmpFrag)
	t.frags = kept
	return true, nil
}

// --- builtins ---

func numOf(v Value) (float64, bool) {
	switch v.Kind {
	case KindInt:
		return float64(v.Int), true
	case KindNumber:
		return v.Num, true
	}
	return 0, false
}

func textOf(v Value) (string, bool) {
	if v.Kind == KindEntity || v.Kind == KindString {
		return v.Text, true
	}
	return "", false
}

func valuesEqual(a, b Value) bool {
	if x, ok := numOf(a); ok {
		if y, ok := numOf(b); ok {
			return x == y
		}
	}
	return a == b
}

func compareValues(a, b Value) (int, bool) {
	if x, ok := numOf(a); ok {
		if y, ok := numOf(b); ok {
			return cmp.Compare(x, y), true
		}
		return 0, false
	}
	if a.Kind == KindTime && b.Kind == KindTime {
		return cmp.Compare(a.Int, b.Int), true
	}
	if x, ok := textOf(a); ok {
		if y, ok := textOf(b); ok {
			return strings.Compare(x, y), true
		}
	}
	return 0, false
}

func evalBuiltin(l *literal, env []Value) bool {
	val := func(t term) Value {
		if t.isVar() {
			return env[t.v]
		}
		return t.c
	}
	a, b := val(l.args[0]), val(l.args[1])
	switch l.pred {
	case "=":
		return valuesEqual(a, b)
	case "!=":
		return !valuesEqual(a, b)
	case "contains":
		x, ok1 := textOf(a)
		y, ok2 := textOf(b)
		return ok1 && ok2 && strings.Contains(x, y)
	}
	c, ok := compareValues(a, b)
	if !ok {
		return false
	}
	switch l.pred {
	case "<":
		return c < 0
	case "<=":
		return c <= 0
	case ">":
		return c > 0
	case ">=":
		return c >= 0
	}
	return false
}

// --- evaluator ---

type evaluator struct {
	ctx     context.Context
	q       querier
	v       View
	m       *meter
	opts    RuleOptions
	prog    *Program
	rels    map[string]*relation // intensional
	stored  map[string]*relation
	exclude map[int64]bool
	facts   int
	steps   int
	cur     *deltaSet
}

type deltaSet struct {
	by   map[string][]*tuple
	seen map[*tuple]bool
}

func newDeltaSet() *deltaSet { return &deltaSet{by: map[string][]*tuple{}, seen: map[*tuple]bool{}} }

func (d *deltaSet) add(pred string, t *tuple) {
	if !d.seen[t] {
		d.seen[t] = true
		d.by[pred] = append(d.by[pred], t)
	}
}

func newEvaluator(ctx context.Context, q querier, v View, p *Program, opts RuleOptions, exclude map[int64]bool) *evaluator {
	opts = opts.withDefaults()
	e := &evaluator{
		ctx: ctx, q: q, v: v, m: newMeter(ctx, opts.Budget), opts: opts, prog: p,
		rels: map[string]*relation{}, stored: map[string]*relation{}, exclude: exclude,
	}
	for pred, a := range p.arity {
		e.rels[pred] = newRelation(a)
	}
	return e
}

func (e *evaluator) tick() error {
	e.steps++
	if e.steps&0xff == 0 {
		return e.m.check()
	}
	return nil
}

func msPtr(t *time.Time) *int64 {
	if t == nil {
		return nil
	}
	ms := t.UnixMilli()
	return &ms
}

// relation returns the relation for pred, loading a stored one from the view.
func (e *evaluator) relation(pred string, arity int) (*relation, error) {
	if r, ok := e.rels[pred]; ok {
		return r, nil
	}
	if r, ok := e.stored[pred]; ok {
		return r, nil
	}
	pat := Pattern{}
	if pred != triplePred {
		pv := Ent(pred)
		pat.Predicate = &pv
	}
	sts, err := e.v.match(e.ctx, e.q, pat)
	if err != nil {
		return nil, err
	}
	if err := e.m.takeStatements(sts); err != nil {
		return nil, err
	}
	rel := newRelation(arity)
	for _, st := range sts {
		if e.exclude[st.EID] {
			continue
		}
		vals := []Value{st.Subject, st.Object}
		if pred == triplePred {
			vals = []Value{st.Subject, st.Predicate, st.Object}
		}
		if len(vals) != arity {
			return nil, fmt.Errorf("%w: %s takes %d arguments", ErrRuleParse, pred, len(vals))
		}
		if err := e.addTuple(rel, vals, frag{sup: []int64{st.EID}, from: msPtr(st.ValidFrom), to: msPtr(st.ValidTo)}, nil, "", false); err != nil {
			return nil, err
		}
	}
	e.stored[pred] = rel
	return rel, nil
}

// addTuple adds a fragment to the relation's tuple, creating the tuple. count
// charges derived tuples against MaxFacts and the budget.
func (e *evaluator) addTuple(rel *relation, vals []Value, f frag, d *deltaSet, pred string, count bool) error {
	key := tupleKey(vals)
	t, ok := rel.byKey[key]
	if !ok {
		if count {
			if e.facts >= e.opts.MaxFacts {
				return fmt.Errorf("%w: more than %d derived facts", ErrBudget, e.opts.MaxFacts)
			}
			bytes := 0
			for _, v := range vals {
				bytes += valueBytes(v)
			}
			if err := e.m.take(1, bytes); err != nil {
				return err
			}
			e.facts++
		}
		t = &tuple{vals: vals}
		rel.byKey[key] = t
		rel.tuples = append(rel.tuples, t)
		for i, v := range vals {
			rel.idx[i][v] = append(rel.idx[i][v], t)
		}
	}
	changed, err := t.addFrag(f, e.opts.MaxFragments)
	if err != nil {
		return err
	}
	if changed && d != nil {
		d.add(pred, t)
	}
	return nil
}

func atomVars(l *literal, bound []bool) (all bool, any bool) {
	all = true
	for _, t := range l.args {
		if !t.isVar() {
			continue
		}
		if bound[t.v] {
			any = true
		} else {
			all = false
		}
	}
	return all, any
}

// planOrder orders a rule body: the delta atom first, filters as soon as
// their variables are bound, then atoms that share a bound variable.
func planOrder(r *rule, first int) []int {
	n := len(r.body)
	bound := make([]bool, len(r.vars))
	placed := make([]bool, n)
	var order []int
	place := func(i int) {
		placed[i] = true
		order = append(order, i)
		if r.body[i].kind == litAtom {
			for _, t := range r.body[i].args {
				if t.isVar() {
					bound[t.v] = true
				}
			}
		}
	}
	flush := func() {
		for progress := true; progress; {
			progress = false
			for i := range r.body {
				if placed[i] || r.body[i].kind == litAtom {
					continue
				}
				if all, _ := atomVars(&r.body[i], bound); all {
					place(i)
					progress = true
				}
			}
		}
	}
	if first >= 0 {
		place(first)
	}
	flush()
	for {
		pick := -1
		for i := range r.body {
			if placed[i] || r.body[i].kind != litAtom {
				continue
			}
			if _, anyBound := atomVars(&r.body[i], bound); anyBound {
				pick = i
				break
			}
			if pick < 0 {
				pick = i
			}
		}
		if pick < 0 {
			break
		}
		place(pick)
		flush()
	}
	for i := range r.body { // unsafe leftovers cannot happen after compile
		if !placed[i] {
			order = append(order, i)
		}
	}
	return order
}

type joinState struct {
	r     *rule
	order []int
	delta int
	dts   []*tuple
	env   []Value
	bound []bool
	emit  func(frag) error
}

func (e *evaluator) literalArity(l *literal) int {
	if a, ok := e.prog.arity[l.pred]; ok {
		return a
	}
	if l.pred == triplePred {
		return 3
	}
	return 2
}

func (e *evaluator) join(st *joinState, k int, cur frag) error {
	if err := e.tick(); err != nil {
		return err
	}
	if k == len(st.order) {
		return st.emit(cur)
	}
	li := st.order[k]
	l := &st.r.body[li]
	switch l.kind {
	case litBuiltin:
		if !evalBuiltin(l, st.env) {
			return nil
		}
		return e.join(st, k+1, cur)
	case litNot:
		rel, err := e.relation(l.pred, e.literalArity(l))
		if err != nil {
			return err
		}
		vals := make([]Value, len(l.args))
		for i, t := range l.args {
			vals[i] = st.value(t)
		}
		if _, found := rel.byKey[tupleKey(vals)]; found {
			return nil
		}
		return e.join(st, k+1, cur)
	}
	rel, err := e.relation(l.pred, e.literalArity(l))
	if err != nil {
		return err
	}
	var cands []*tuple
	if li == st.delta {
		cands = st.dts
	} else {
		cands = rel.tuples
		for i, t := range l.args {
			if !t.isVar() || st.bound[t.v] {
				cands = rel.idx[i][st.value(t)]
				break
			}
		}
	}
	for _, t := range slices.Clone(cands) {
		var newly []int
		ok := true
		for i, a := range l.args {
			switch {
			case !a.isVar():
				ok = valuesIdentical(a.c, t.vals[i])
			case st.bound[a.v]:
				ok = valuesIdentical(st.env[a.v], t.vals[i])
			default:
				st.env[a.v], st.bound[a.v] = t.vals[i], true
				newly = append(newly, a.v)
			}
			if !ok {
				break
			}
		}
		var err error
		if ok {
			for _, f := range t.frags {
				nf, fine := combine(cur, f)
				if !fine {
					continue
				}
				if err = e.join(st, k+1, nf); err != nil {
					break
				}
			}
		}
		for _, v := range newly {
			st.bound[v] = false
		}
		if err != nil {
			return err
		}
	}
	return nil
}

func valuesIdentical(a, b Value) bool { return a == b }

func (st *joinState) value(t term) Value {
	if t.isVar() {
		return st.env[t.v]
	}
	return t.c
}

func (e *evaluator) evalBody(r *rule, delta int, dts []*tuple, emit func(env []Value, f frag) error) error {
	st := &joinState{r: r, order: planOrder(r, delta), delta: delta, dts: dts,
		env: make([]Value, len(r.vars)), bound: make([]bool, len(r.vars))}
	st.emit = func(f frag) error { return emit(st.env, f) }
	return e.join(st, 0, frag{})
}

func (e *evaluator) evalRule(r *rule, delta int, dts []*tuple) error {
	head := r.head
	rel := e.rels[head.pred]
	return e.evalBody(r, delta, dts, func(env []Value, f frag) error {
		vals := make([]Value, len(head.args))
		for i, t := range head.args {
			if t.isVar() {
				vals[i] = env[t.v]
			} else {
				vals[i] = t.c
			}
		}
		return e.addTuple(rel, vals, f, e.cur, head.pred, true)
	})
}

func (e *evaluator) runStratum(s int) error {
	var rules []*rule
	for _, r := range e.prog.rules {
		if e.prog.stratum[r.head.pred] == s {
			rules = append(rules, r)
		}
	}
	var delta *deltaSet
	for {
		e.cur = newDeltaSet()
		for _, r := range rules {
			if err := e.m.check(); err != nil {
				return err
			}
			if delta == nil {
				if err := e.evalRule(r, -1, nil); err != nil {
					return err
				}
				continue
			}
			for i := range r.body {
				l := &r.body[i]
				if l.kind != litAtom {
					continue
				}
				if _, ok := e.prog.arity[l.pred]; !ok || e.prog.stratum[l.pred] != s {
					continue
				}
				if dts := delta.by[l.pred]; len(dts) > 0 {
					if err := e.evalRule(r, i, dts); err != nil {
						return err
					}
				}
			}
		}
		if len(e.cur.seen) == 0 {
			return nil
		}
		delta = e.cur
	}
}

// run derives every rule to its fixpoint, stratum by stratum.
func (e *evaluator) run() error {
	for s := 0; s < e.prog.nStrata; s++ {
		if err := e.runStratum(s); err != nil {
			return err
		}
	}
	return nil
}

// --- Solve and Check ---

// Solution is one answer to a goal: the variable bindings, the eids of the
// stored statements the answer rests on, and the interval over which it holds.
type Solution struct {
	Bindings           map[string]Value
	Support            []int64
	ValidFrom, ValidTo *time.Time
}

func goalSortKey(vars []string, b map[string]Value) string {
	vals := make([]Value, len(vars))
	for i, n := range vars {
		vals[i] = b[n]
	}
	return tupleKey(vals)
}

// parseGoal parses an atom like reports(alice, Y).
func parseGoal(goal string) (*literal, []string, error) {
	toks, rs, err := lexDatalog(goal)
	if err != nil {
		return nil, nil, err
	}
	p := &dlParser{toks: toks, rs: rs}
	b := newRuleBuilder()
	l, err := p.atom(b)
	if err != nil {
		return nil, nil, err
	}
	if p.isPunct(".") {
		p.next()
	}
	if t := p.peek(); t.kind != tkEOF {
		return nil, nil, parseErr(t.line, t.col, "unexpected %s after the goal", describe(t))
	}
	return &l, b.vars, nil
}

// Solve answers a goal such as "reports(alice, Y)": it evaluates the program
// over the view in one snapshot and returns one Solution per binding and
// fragment, in a deterministic order. Negation is evaluated atemporally over
// the whole view: "not p(X)" holds when no visible statement says p(X), at any
// valid time, and does not restrict a Solution's interval.
func (v View) Solve(ctx context.Context, p *Program, goal string, opts RuleOptions) ([]Solution, error) {
	g, names, err := parseGoal(goal)
	if err != nil {
		return nil, err
	}
	var out []Solution
	err = v.kg.read(ctx, func(q querier) error {
		e := newEvaluator(ctx, q, v, p, opts, nil)
		if err := e.run(); err != nil {
			return err
		}
		arity := e.literalArity(g)
		if len(g.args) != arity {
			return fmt.Errorf("%w: %s takes %d arguments", ErrRuleParse, g.pred, arity)
		}
		rel, err := e.relation(g.pred, arity)
		if err != nil {
			return err
		}
		for _, t := range rel.tuples {
			b := map[string]Value{}
			ok := true
			for i, a := range g.args {
				switch {
				case !a.isVar():
					ok = valuesIdentical(a.c, t.vals[i])
				default:
					if old, seen := b[names[a.v]]; seen && names[a.v] != "_" {
						ok = valuesIdentical(old, t.vals[i])
					}
					b[names[a.v]] = t.vals[i]
				}
				if !ok {
					break
				}
			}
			if !ok {
				continue
			}
			delete(b, "_")
			for _, f := range t.frags {
				bb := make(map[string]Value, len(b))
				for k, val := range b {
					bb[k] = val
				}
				out = append(out, Solution{Bindings: bb, Support: f.sup, ValidFrom: msTime(f.from), ValidTo: msTime(f.to)})
			}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	var vars []string
	for n := range solutionVars(out) {
		vars = append(vars, n)
	}
	sort.Strings(vars)
	slices.SortStableFunc(out, func(a, b Solution) int {
		if c := strings.Compare(goalSortKey(vars, a.Bindings), goalSortKey(vars, b.Bindings)); c != 0 {
			return c
		}
		return slices.Compare(a.Support, b.Support)
	})
	return out, nil
}

func solutionVars(sols []Solution) map[string]bool {
	m := map[string]bool{}
	for _, s := range sols {
		for n := range s.Bindings {
			m[n] = true
		}
	}
	return m
}

// Violation is one way a constraint is broken.
type Violation struct {
	Rule      string
	Bindings  map[string]Value
	Witnesses []int64
}

// Check evaluates the program over the view in one snapshot and returns the
// bindings that satisfy a constraint body, with the eids of the stored
// statements that witness each. A violation counts only where its witnesses
// are valid together (pointwise valid time).
func (v View) Check(ctx context.Context, p *Program, opts RuleOptions) ([]Violation, error) {
	var out []Violation
	err := v.kg.read(ctx, func(q querier) error {
		e := newEvaluator(ctx, q, v, p, opts, nil)
		if err := e.run(); err != nil {
			return err
		}
		for _, c := range p.constraints {
			seen := map[string]bool{}
			err := e.evalBody(c, -1, nil, func(env []Value, f frag) error {
				b := map[string]Value{}
				var names []string
				for i, n := range c.vars {
					if !strings.HasPrefix(n, "_") {
						b[n] = env[i]
						names = append(names, n)
					}
				}
				sort.Strings(names)
				key := goalSortKey(names, b) + fmt.Sprint(f.sup)
				if seen[key] {
					return nil
				}
				seen[key] = true
				out = append(out, Violation{Rule: c.src, Bindings: b, Witnesses: slices.Clone(f.sup)})
				return nil
			})
			if err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	slices.SortStableFunc(out, func(a, b Violation) int {
		if c := strings.Compare(a.Rule, b.Rule); c != 0 {
			return c
		}
		if c := slices.Compare(a.Witnesses, b.Witnesses); c != 0 {
			return c
		}
		return strings.Compare(fmt.Sprint(a.Bindings), fmt.Sprint(b.Bindings))
	})
	return out, nil
}
