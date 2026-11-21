package palace

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"sort"
	"strconv"
	"strings"
	"unicode"
)

// An ELPI goal engine over a view, after Tiramemsu's elpi-engine capability.
// Upstream embeds a third-party λProlog engine behind an off-by-default
// feature; this is a pure-Go subset with the same surface for agents: Horn
// clauses in ELPI's curried syntax, solved top-down (SLD resolution, depth
// first, clauses in program order) with a fuel limit, and the host builtin
// tm.triple reading the view.
//
//	reports X Y :- tm.triple X manager Y.
//	reports X Z :- tm.triple X manager Y, reports Y Z.
//
// Goal "reports alice Y" returns one row per solution. Supported goals:
// user predicates, tm.triple S P O, X = Y, X \= Y (as \=), X < Y, X > Y,
// not G (negation as failure), true, fail. Terms: Upper-case identifiers and
// _ are variables; lower-case identifiers (and iri "name") are entities;
// "text", integers and decimals are literals; (f a b) groups an application.
// λ-abstraction, higher-order unification, constraints and modules are not
// supported and fail to parse with ErrElpiProgram.

// Errors of the ELPI engine.
var (
	ErrElpiProgram   = errors.New("palace: kg: elpi program")
	ErrElpiRun       = errors.New("palace: kg: elpi run")
	ErrElpiOutOfFuel = errors.New("palace: kg: elpi out of fuel")
)

// ElpiOptions bounds a run.
type ElpiOptions struct {
	Fuel    int // resolution steps; default 100000
	MaxRows int // solutions; default 1000
	Budget  Budget
}

// ElpiResult is the answer to a goal.
type ElpiResult struct {
	// Rows bind each named goal variable; a value that is not ground is
	// returned as its printed form in a string.
	Rows     []map[string]Value `json:"rows"`
	FuelUsed int                `json:"fuel_used"`
}

type eTerm struct {
	kind int // eVar, eConst, eApp
	name string
	val  Value
	args []*eTerm
}

const (
	eVar = iota
	eConst
	eApp
)

type eClause struct {
	head *eTerm
	body []*eTerm
}

// ElpiProgram is a parsed program.
type ElpiProgram struct {
	clauses map[string][]eClause // by functor/arity
}

// ParseElpi parses a program.
func ParseElpi(text string) (*ElpiProgram, error) {
	p := &eParser{src: text}
	prog := &ElpiProgram{clauses: map[string][]eClause{}}
	for {
		p.skip()
		if p.eof() {
			return prog, nil
		}
		head, err := p.app()
		if err != nil {
			return nil, err
		}
		if head.kind == eVar {
			return nil, p.errf("a clause head must be a predicate")
		}
		c := eClause{head: head}
		p.skip()
		if p.lit(":-") {
			if c.body, err = p.goals(); err != nil {
				return nil, err
			}
		}
		p.skip()
		if !p.lit(".") {
			return nil, p.errf("expected '.' after a clause")
		}
		key := head.key()
		if isElpiBuiltin(key) {
			return nil, p.errf("%s is a builtin and cannot be redefined", head.name)
		}
		prog.clauses[key] = append(prog.clauses[key], c)
	}
}

func (t *eTerm) key() string {
	switch t.kind {
	case eApp:
		return t.name + "/" + strconv.Itoa(len(t.args))
	case eConst:
		return t.val.Text + "/0"
	}
	return ""
}

func isElpiBuiltin(key string) bool {
	switch key {
	case "tm.triple/3", "=/2", "\\=/2", "</2", ">/2", "not/1", "true/0", "fail/0":
		return true
	}
	return false
}

type eParser struct {
	src string
	pos int
}

func (p *eParser) eof() bool { return p.pos >= len(p.src) }

func (p *eParser) errf(format string, args ...any) error {
	line, col := 1, 1
	for _, r := range p.src[:min(p.pos, len(p.src))] {
		if r == '\n' {
			line, col = line+1, 1
		} else {
			col++
		}
	}
	return fmt.Errorf("%w: %d:%d: %s", ErrElpiProgram, line, col, fmt.Sprintf(format, args...))
}

func (p *eParser) skip() {
	for !p.eof() {
		c := p.src[p.pos]
		switch c {
		case '%':
			for !p.eof() && p.src[p.pos] != '\n' {
				p.pos++
			}
		case ' ', '\t', '\n', '\r':
			p.pos++
		default:
			return
		}
	}
}

func (p *eParser) lit(s string) bool {
	p.skip()
	if strings.HasPrefix(p.src[p.pos:], s) {
		p.pos += len(s)
		return true
	}
	return false
}

func (p *eParser) goals() ([]*eTerm, error) {
	var out []*eTerm
	for {
		g, err := p.goal()
		if err != nil {
			return nil, err
		}
		out = append(out, g)
		if !p.lit(",") {
			return out, nil
		}
	}
}

// goal is an application, optionally followed by an infix comparison.
func (p *eParser) goal() (*eTerm, error) {
	left, err := p.app()
	if err != nil {
		return nil, err
	}
	for _, op := range []string{"\\=", "=", "<", ">"} {
		if p.lit(op) {
			right, err := p.app()
			if err != nil {
				return nil, err
			}
			return &eTerm{kind: eApp, name: op, args: []*eTerm{left, right}}, nil
		}
	}
	return left, nil
}

// app is a head followed by arguments.
func (p *eParser) app() (*eTerm, error) {
	head, err := p.atom(true)
	if err != nil {
		return nil, err
	}
	var args []*eTerm
	for {
		p.skip()
		if p.eof() || !p.startsAtom() {
			break
		}
		a, err := p.atom(false)
		if err != nil {
			return nil, err
		}
		args = append(args, a)
	}
	if head.kind == eConst && head.val.Kind == KindEntity && head.val.Text == "iri" && len(args) == 1 &&
		args[0].kind == eConst && args[0].val.Kind == KindString {
		return &eTerm{kind: eConst, val: Ent(args[0].val.Text)}, nil
	}
	if len(args) == 0 {
		return head, nil
	}
	if head.kind != eConst || head.val.Kind != KindEntity {
		return nil, p.errf("only a name can be applied to arguments")
	}
	return &eTerm{kind: eApp, name: head.val.Text, args: args}, nil
}

func (p *eParser) startsAtom() bool {
	c := rune(p.src[p.pos])
	return c == '(' || c == '"' || c == '_' || c == '-' || unicode.IsLetter(c) || unicode.IsDigit(c)
}

func (p *eParser) atom(head bool) (*eTerm, error) {
	p.skip()
	if p.eof() {
		return nil, p.errf("unexpected end of program")
	}
	c := rune(p.src[p.pos])
	switch {
	case c == '(':
		p.pos++
		t, err := p.app()
		if err != nil {
			return nil, err
		}
		if !p.lit(")") {
			return nil, p.errf("expected ')'")
		}
		return t, nil
	case c == '"':
		end := strings.IndexByte(p.src[p.pos+1:], '"')
		if end < 0 {
			return nil, p.errf("unterminated string")
		}
		s := p.src[p.pos+1 : p.pos+1+end]
		p.pos += end + 2
		return &eTerm{kind: eConst, val: Str(s)}, nil
	case c == '-' || unicode.IsDigit(c):
		start := p.pos
		p.pos++
		for !p.eof() && (unicode.IsDigit(rune(p.src[p.pos])) || p.src[p.pos] == '.') {
			if p.src[p.pos] == '.' && (p.pos+1 >= len(p.src) || !unicode.IsDigit(rune(p.src[p.pos+1]))) {
				break
			}
			p.pos++
		}
		num := p.src[start:p.pos]
		if i, err := strconv.ParseInt(num, 10, 64); err == nil {
			return &eTerm{kind: eConst, val: Int(i)}, nil
		}
		f, err := strconv.ParseFloat(num, 64)
		if err != nil {
			return nil, p.errf("bad number %q", num)
		}
		return &eTerm{kind: eConst, val: Num(f)}, nil
	case c == '_' || unicode.IsLetter(c):
		start := p.pos
		for !p.eof() {
			r := rune(p.src[p.pos])
			if r == '_' || r == '.' && p.pos+1 < len(p.src) && unicode.IsLetter(rune(p.src[p.pos+1])) ||
				unicode.IsLetter(r) || unicode.IsDigit(r) || r == '-' && p.pos > start {
				p.pos++
				continue
			}
			break
		}
		name := p.src[start:p.pos]
		if name == "_" || unicode.IsUpper(rune(name[0])) || name[0] == '_' {
			return &eTerm{kind: eVar, name: name}, nil
		}
		return &eTerm{kind: eConst, val: Value{Kind: KindEntity, Text: name}}, nil
	case c == '\\':
		return nil, p.errf("λ-abstraction is not supported")
	}
	return nil, p.errf("unexpected %q", c)
}

// --- solving ---

type eSolver struct {
	ctx   context.Context
	v     View
	q     querier
	prog  *ElpiProgram
	m     *meter
	fuel  int
	used  int
	fresh int
	bind  map[string]*eTerm
	trail []string
	err   error
}

func (s *eSolver) deref(t *eTerm) *eTerm {
	for t.kind == eVar {
		b, ok := s.bind[t.name]
		if !ok {
			return t
		}
		t = b
	}
	return t
}

func (s *eSolver) bindVar(name string, t *eTerm) {
	s.bind[name] = t
	s.trail = append(s.trail, name)
}

func (s *eSolver) undo(mark int) {
	for _, n := range s.trail[mark:] {
		delete(s.bind, n)
	}
	s.trail = s.trail[:mark]
}

func (s *eSolver) unify(a, b *eTerm) bool {
	a, b = s.deref(a), s.deref(b)
	switch {
	case a.kind == eVar && b.kind == eVar && a.name == b.name:
		return true
	case a.kind == eVar:
		s.bindVar(a.name, b)
		return true
	case b.kind == eVar:
		s.bindVar(b.name, a)
		return true
	case a.kind == eConst && b.kind == eConst:
		return a.val == b.val
	case a.kind == eApp && b.kind == eApp:
		if a.name != b.name || len(a.args) != len(b.args) {
			return false
		}
		for i := range a.args {
			if !s.unify(a.args[i], b.args[i]) {
				return false
			}
		}
		return true
	}
	return false
}

func (s *eSolver) rename(t *eTerm, suffix string, anon *int) *eTerm {
	switch t.kind {
	case eVar:
		if t.name == "_" {
			*anon++
			return &eTerm{kind: eVar, name: fmt.Sprintf("_%d%s", *anon, suffix)}
		}
		return &eTerm{kind: eVar, name: t.name + suffix}
	case eApp:
		out := &eTerm{kind: eApp, name: t.name, args: make([]*eTerm, len(t.args))}
		for i, a := range t.args {
			out.args[i] = s.rename(a, suffix, anon)
		}
		return out
	}
	return t
}

// solve proves goals left to right and calls k for each proof; k returns
// false to stop. It returns false when the search must stop.
func (s *eSolver) solve(goals []*eTerm, k func() bool) bool {
	if s.err != nil {
		return false
	}
	if len(goals) == 0 {
		return k()
	}
	s.used++
	if s.used > s.fuel {
		s.err = fmt.Errorf("%w after %d steps", ErrElpiOutOfFuel, s.fuel)
		return false
	}
	if s.used%256 == 0 {
		if err := s.m.check(); err != nil {
			s.err = err
			return false
		}
	}
	g, rest := s.deref(goals[0]), goals[1:]
	key := g.key()
	switch key {
	case "true/0":
		return s.solve(rest, k)
	case "fail/0":
		return true
	case "=/2":
		mark := len(s.trail)
		if s.unify(g.args[0], g.args[1]) {
			if !s.solve(rest, k) {
				return false
			}
		}
		s.undo(mark)
		return true
	case "\\=/2":
		mark := len(s.trail)
		ok := s.unify(g.args[0], g.args[1])
		s.undo(mark)
		if ok {
			return true
		}
		return s.solve(rest, k)
	case "</2", ">/2":
		a, b := s.deref(g.args[0]), s.deref(g.args[1])
		if a.kind != eConst || b.kind != eConst {
			s.err = fmt.Errorf("%w: %s needs bound numbers", ErrElpiRun, g.name)
			return false
		}
		x, okA := numeric(a.val)
		y, okB := numeric(b.val)
		if !okA || !okB {
			return true // not comparable: fails
		}
		if (g.name == "<" && x < y) || (g.name == ">" && x > y) {
			return s.solve(rest, k)
		}
		return true
	case "not/1":
		mark := len(s.trail)
		found := false
		s.solve([]*eTerm{g.args[0]}, func() bool { found = true; return false })
		s.undo(mark)
		if s.err != nil {
			return false
		}
		if found {
			return true
		}
		return s.solve(rest, k)
	case "tm.triple/3":
		return s.triple(g, rest, k)
	}
	clauses, ok := s.prog.clauses[key]
	if !ok {
		s.err = fmt.Errorf("%w: unknown predicate %s", ErrElpiRun, key)
		return false
	}
	for _, c := range clauses {
		s.fresh++
		suffix := "#" + strconv.Itoa(s.fresh)
		anon := 0
		head := s.rename(c.head, suffix, &anon)
		mark := len(s.trail)
		if s.unify(g, head) {
			body := make([]*eTerm, 0, len(c.body)+len(rest))
			for _, b := range c.body {
				body = append(body, s.rename(b, suffix, &anon))
			}
			if !s.solve(append(body, rest...), k) {
				s.undo(mark)
				return false
			}
		}
		s.undo(mark)
	}
	return true
}

func numeric(v Value) (float64, bool) {
	switch v.Kind {
	case KindInt, KindTime:
		return float64(v.Int), true
	case KindNumber:
		return v.Num, true
	}
	return 0, false
}

// triple resolves tm.triple S P O against the view.
func (s *eSolver) triple(g *eTerm, rest []*eTerm, k func() bool) bool {
	var pat Pattern
	for i, dst := range []**Value{&pat.Subject, &pat.Predicate, &pat.Object} {
		a := s.deref(g.args[i])
		switch a.kind {
		case eConst:
			v := a.val
			if v.Kind == KindEntity {
				v = Ent(v.Text)
			}
			*dst = &v
		case eApp:
			s.err = fmt.Errorf("%w: tm.triple argument %d is not a value", ErrElpiRun, i+1)
			return false
		}
	}
	sts, err := s.v.match(s.ctx, s.q, pat)
	if err != nil {
		s.err = err
		return false
	}
	if err := s.m.takeStatements(sts); err != nil {
		s.err = err
		return false
	}
	for _, st := range sts {
		mark := len(s.trail)
		if s.unify(g.args[0], &eTerm{kind: eConst, val: st.Subject}) &&
			s.unify(g.args[1], &eTerm{kind: eConst, val: st.Predicate}) &&
			s.unify(g.args[2], &eTerm{kind: eConst, val: st.Object}) {
			if !s.solve(rest, k) {
				s.undo(mark)
				return false
			}
		}
		s.undo(mark)
	}
	return true
}

func (s *eSolver) print(t *eTerm) string {
	t = s.deref(t)
	switch t.kind {
	case eVar:
		return "_"
	case eConst:
		return t.val.String()
	}
	parts := []string{t.name}
	for _, a := range t.args {
		parts = append(parts, s.print(a))
	}
	return "(" + strings.Join(parts, " ") + ")"
}

func collectVars(t *eTerm, seen map[string]bool, out *[]string) {
	switch t.kind {
	case eVar:
		if t.name != "_" && !seen[t.name] {
			seen[t.name] = true
			*out = append(*out, t.name)
		}
	case eApp:
		for _, a := range t.args {
			collectVars(a, seen, out)
		}
	}
}

// Elpi solves goal against prog in v and returns one row per solution. It
// writes nothing.
func (v View) Elpi(ctx context.Context, prog *ElpiProgram, goal string, opts ElpiOptions) (*ElpiResult, error) {
	gp := &eParser{src: goal}
	goals, err := gp.goals()
	if err != nil {
		return nil, err
	}
	gp.skip()
	gp.lit(".")
	if gp.skip(); !gp.eof() {
		return nil, gp.errf("unexpected text after the goal")
	}
	if opts.Fuel <= 0 {
		opts.Fuel = 100_000
	}
	if opts.MaxRows <= 0 {
		opts.MaxRows = 1000
	}
	var names []string
	seen := map[string]bool{}
	for _, g := range goals {
		collectVars(g, seen, &names)
	}
	res := &ElpiResult{}
	err = v.kg.read(ctx, func(q querier) error {
		s := &eSolver{ctx: ctx, v: v, q: q, prog: prog, m: newMeter(ctx, opts.Budget), fuel: opts.Fuel,
			bind: map[string]*eTerm{}}
		s.solve(goals, func() bool {
			row := make(map[string]Value, len(names))
			for _, n := range names {
				t := s.deref(&eTerm{kind: eVar, name: n})
				if t.kind == eConst {
					row[n] = t.val
				} else {
					row[n] = Str(s.print(t))
				}
			}
			res.Rows = append(res.Rows, row)
			if len(res.Rows) > opts.MaxRows {
				s.err = fmt.Errorf("%w: more than %d solutions", ErrElpiOutOfFuel, opts.MaxRows)
				return false
			}
			if err := s.m.take(1, 0); err != nil {
				s.err = err
				return false
			}
			return true
		})
		res.FuelUsed = s.used
		return s.err
	})
	if err != nil {
		return nil, err
	}
	return res, nil
}

// ElpiVars returns the sorted variable names a goal binds, for display.
func ElpiVars(rows []map[string]Value) []string {
	set := map[string]bool{}
	for _, r := range rows {
		for k := range r {
			set[k] = true
		}
	}
	out := make([]string, 0, len(set))
	for k := range set {
		out = append(out, k)
	}
	sort.Strings(out)
	return slices.Clip(out)
}
