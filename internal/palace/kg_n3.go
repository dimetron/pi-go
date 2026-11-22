package palace

import (
	"fmt"
	"strings"
	"unicode"
)

// N3 rules, after Tiramemsu's n3-reasoning and n3-rule-sets capabilities: the
// Horn subset
//
//	@prefix : <http://example.org/> .
//	{ ?a :manager ?b . ?b :reports ?c } => { ?a :reports ?c } .
//
// compiles to the same rule IR as Datalog and shares its ruleEvaluator. A triple
// "?s :p ?o" is the atom p(S, O): a prefixed name or IRI is reduced to its
// local name (the part after the colon, or the whole <iri>), so :p and ex:p
// name the same predicate and entity. The builtins math:lessThan,
// math:greaterThan and math:notEqualTo become <, > and !=. A head with several
// triples becomes several rules. Top-level facts, `log:` builtins, variable
// predicates, formulas inside formulas and lists are rejected with
// ErrRuleParse: facts belong in the graph.

type n3Kind int

const (
	nEOF n3Kind = iota
	nPrefix
	nIRI
	nName
	nVar
	nStr
	nNum
	nLBrace
	nRBrace
	nDot
	nImplies
)

type n3Tok struct {
	kind      n3Kind
	text      string
	val       Value
	line, col int
	start     int
}

func lexN3(src string) ([]n3Tok, []rune, error) {
	rs := []rune(src)
	var toks []n3Tok
	line, col, i := 1, 1, 0
	adv := func(n int) {
		for k := 0; k < n && i < len(rs); k++ {
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
	nameRune := func(r rune) bool {
		return r == '_' || r == ':' || r == '-' || unicode.IsLetter(r) || unicode.IsDigit(r)
	}
	for i < len(rs) {
		c := rs[i]
		l, cl, start := line, col, i
		switch {
		case unicode.IsSpace(c):
			adv(1)
		case c == '#':
			for i < len(rs) && rs[i] != '\n' {
				adv(1)
			}
		case c == '{':
			adv(1)
			toks = append(toks, n3Tok{kind: nLBrace, line: l, col: cl, start: start})
		case c == '}':
			adv(1)
			toks = append(toks, n3Tok{kind: nRBrace, line: l, col: cl, start: start})
		case c == '=' && at(1) == '>':
			adv(2)
			toks = append(toks, n3Tok{kind: nImplies, line: l, col: cl, start: start})
		case c == '.':
			adv(1)
			toks = append(toks, n3Tok{kind: nDot, line: l, col: cl, start: start})
		case c == '@':
			adv(1)
			for unicode.IsLetter(at(0)) {
				adv(1)
			}
			toks = append(toks, n3Tok{kind: nPrefix, text: string(rs[start:i]), line: l, col: cl, start: start})
		case c == '<':
			adv(1)
			for i < len(rs) && rs[i] != '>' {
				adv(1)
			}
			if i >= len(rs) {
				return nil, nil, ruleParseErr(l, cl, "unterminated IRI")
			}
			text := string(rs[start+1 : i])
			adv(1)
			toks = append(toks, n3Tok{kind: nIRI, text: text, line: l, col: cl, start: start})
		case c == '?':
			adv(1)
			for isIdentPart(at(0)) {
				adv(1)
			}
			if i == start+1 {
				return nil, nil, ruleParseErr(l, cl, "empty variable name")
			}
			toks = append(toks, n3Tok{kind: nVar, text: string(rs[start+1 : i]), line: l, col: cl, start: start})
		case c == '"':
			adv(1)
			var sb strings.Builder
			closed := false
			for i < len(rs) {
				if rs[i] == '"' {
					adv(1)
					closed = true
					break
				}
				if rs[i] == '\\' && i+1 < len(rs) {
					adv(1)
				}
				sb.WriteRune(rs[i])
				adv(1)
			}
			if !closed {
				return nil, nil, ruleParseErr(l, cl, "unterminated string")
			}
			toks = append(toks, n3Tok{kind: nStr, text: sb.String(), val: Str(sb.String()), line: l, col: cl, start: start})
		case unicode.IsDigit(c) || (c == '-' && unicode.IsDigit(at(1))):
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
			toks = append(toks, n3Tok{kind: nNum, text: text, val: numValue(text), line: l, col: cl, start: start})
		case nameRune(c):
			for nameRune(at(0)) {
				adv(1)
			}
			toks = append(toks, n3Tok{kind: nName, text: string(rs[start:i]), line: l, col: cl, start: start})
		default:
			return nil, nil, ruleParseErr(l, cl, "unexpected character %q", c)
		}
	}
	toks = append(toks, n3Tok{kind: nEOF, line: line, col: col, start: len(rs)})
	return toks, rs, nil
}

// n3Term is a term before variable numbering.
type n3Term struct {
	isVar bool
	name  string
	val   Value
}

type n3Triple struct {
	s, o n3Term
	pred string // local predicate name or builtin operator
	op   bool   // pred is a builtin operator
	tok  n3Tok
}

type n3Parser struct {
	toks     []n3Tok
	rs       []rune
	pos      int
	prefixes map[string]string
}

func (p *n3Parser) peek() n3Tok { return p.toks[p.pos] }
func (p *n3Parser) next() n3Tok {
	t := p.toks[p.pos]
	if t.kind != nEOF {
		p.pos++
	}
	return t
}

func n3Describe(t n3Tok) string {
	if t.kind == nEOF {
		return "end of input"
	}
	return fmt.Sprintf("%q", t.text)
}

// local resolves a prefixed name or IRI to its local name.
func (p *n3Parser) local(t n3Tok) (string, bool, error) {
	if t.kind == nIRI {
		return t.text, false, nil
	}
	i := strings.Index(t.text, ":")
	if i < 0 {
		return "", false, ruleParseErr(t.line, t.col, "bare word %q is not supported; use a prefixed name", t.text)
	}
	prefix, local := t.text[:i], t.text[i+1:]
	iri, declared := p.prefixes[prefix]
	if swapNamespaces[prefix] || strings.Contains(iri, "w3.org/2000/10/swap") {
		return prefix + ":" + local, true, nil
	}
	if !declared {
		return "", false, ruleParseErr(t.line, t.col, "undeclared prefix %q", prefix+":")
	}
	if local == "" {
		return "", false, ruleParseErr(t.line, t.col, "empty local name in %q", t.text)
	}
	return local, false, nil
}

func (p *n3Parser) term() (n3Term, error) {
	t := p.next()
	switch t.kind {
	case nVar:
		return n3Term{isVar: true, name: t.text}, nil
	case nStr, nNum:
		return n3Term{val: t.val}, nil
	case nName, nIRI:
		l, builtin, err := p.local(t)
		if err != nil {
			return n3Term{}, err
		}
		if builtin {
			return n3Term{}, ruleParseErr(t.line, t.col, "%s is only valid as a predicate", t.text)
		}
		return n3Term{val: Ent(l)}, nil
	case nLBrace:
		return n3Term{}, ruleParseErr(t.line, t.col, "nested formulas are not supported")
	}
	return n3Term{}, ruleParseErr(t.line, t.col, "expected a term, found %s", n3Describe(t))
}

// swapNamespaces are the N3 builtin vocabularies; only three math: predicates
// are supported, the rest are rejected rather than read as plain predicates.
var swapNamespaces = map[string]bool{"math": true, "log": true, "list": true, "string": true, "os": true, "time": true, "crypto": true}

var n3Builtins = map[string]string{
	"math:lessThan":    "<",
	"math:greaterThan": ">",
	"math:notEqualTo":  "!=",
}

// formula parses "{ s p o . s p o }".
func (p *n3Parser) formula() ([]n3Triple, error) {
	open := p.next()
	if open.kind != nLBrace {
		return nil, ruleParseErr(open.line, open.col, "expected '{', found %s", n3Describe(open))
	}
	var out []n3Triple
	for {
		if p.peek().kind == nRBrace {
			p.next()
			return out, nil
		}
		s, err := p.term()
		if err != nil {
			return nil, err
		}
		pt := p.next()
		if pt.kind == nVar {
			return nil, ruleParseErr(pt.line, pt.col, "variable predicates are not supported")
		}
		if pt.kind != nName && pt.kind != nIRI {
			return nil, ruleParseErr(pt.line, pt.col, "expected a predicate, found %s", n3Describe(pt))
		}
		name, builtin, err := p.local(pt)
		if err != nil {
			return nil, err
		}
		tr := n3Triple{s: s, pred: entityID(name), tok: pt}
		if builtin {
			op, ok := n3Builtins[name]
			if !ok {
				return nil, ruleParseErr(pt.line, pt.col, "unsupported builtin %s (want math:lessThan, math:greaterThan or math:notEqualTo)", pt.text)
			}
			tr.pred, tr.op = op, true
		}
		if tr.o, err = p.term(); err != nil {
			return nil, err
		}
		out = append(out, tr)
		switch t := p.peek(); t.kind {
		case nDot:
			p.next()
		case nRBrace:
		default:
			return nil, ruleParseErr(t.line, t.col, "expected '.' or '}', found %s", n3Describe(t))
		}
	}
}

func (b *ruleBuilder) n3Term(t n3Term) term {
	if t.isVar {
		return b.variable(t.name)
	}
	return constTerm(t.val)
}

// ParseN3 parses the Horn subset of N3 described above.
func ParseN3(text string) (*Program, error) {
	toks, rs, err := lexN3(text)
	if err != nil {
		return nil, err
	}
	p := &n3Parser{toks: toks, rs: rs, prefixes: map[string]string{}}
	var rules []*rule
	for p.peek().kind != nEOF {
		t := p.peek()
		if t.kind == nPrefix {
			if err := p.prefixDecl(); err != nil {
				return nil, err
			}
			continue
		}
		if t.kind != nLBrace {
			return nil, ruleParseErr(t.line, t.col, "facts are not supported in a program: assert them in the graph (found %s)", n3Describe(t))
		}
		body, err := p.formula()
		if err != nil {
			return nil, err
		}
		imp := p.next()
		if imp.kind != nImplies {
			return nil, ruleParseErr(imp.line, imp.col, "expected '=>', found %s", n3Describe(imp))
		}
		head, err := p.formula()
		if err != nil {
			return nil, err
		}
		end := p.next()
		if end.kind != nDot {
			return nil, ruleParseErr(end.line, end.col, "expected '.', found %s", n3Describe(end))
		}
		src := strings.TrimSpace(string(rs[t.start : end.start+1]))
		for _, h := range head {
			if h.op {
				return nil, ruleParseErr(h.tok.line, h.tok.col, "a builtin cannot be a rule head")
			}
			b := newRuleBuilder()
			r := &rule{src: src, index: len(rules) + 1}
			hl := literal{kind: litAtom, pred: h.pred, args: []term{b.n3Term(h.s), b.n3Term(h.o)}}
			r.head = &hl
			for _, bt := range body {
				l := literal{kind: litAtom, pred: bt.pred, args: []term{b.n3Term(bt.s), b.n3Term(bt.o)}}
				if bt.op {
					l.kind = litBuiltin
				}
				r.body = append(r.body, l)
			}
			r.vars = b.vars
			rules = append(rules, r)
		}
	}
	return compile(rules, nil)
}

func (p *n3Parser) prefixDecl() error {
	kw := p.next()
	if kw.text != "@prefix" {
		return ruleParseErr(kw.line, kw.col, "unsupported directive %s", kw.text)
	}
	name := p.next()
	if name.kind != nName || !strings.HasSuffix(name.text, ":") {
		return ruleParseErr(name.line, name.col, "expected a prefix name like 'ex:', found %s", n3Describe(name))
	}
	iri := p.next()
	if iri.kind != nIRI {
		return ruleParseErr(iri.line, iri.col, "expected <iri>, found %s", n3Describe(iri))
	}
	if dot := p.next(); dot.kind != nDot {
		return ruleParseErr(dot.line, dot.col, "expected '.', found %s", n3Describe(dot))
	}
	p.prefixes[strings.TrimSuffix(name.text, ":")] = iri.text
	return nil
}
