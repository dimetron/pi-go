package palace

import (
	"strconv"
	"strings"
	"unicode"
)

// Cypher subset (read only): MATCH with comma-separated patterns and chains,
// both directions, [r:p] binding the statement, (a {name: 'x'}), OPTIONAL
// MATCH, WHERE with comparisons, AND/OR/NOT and r.confidence / a.prop,
// RETURN with AS, DISTINCT and count(*), ORDER BY, SKIP, LIMIT. A relationship
// is a statement whose object is an entity; a property is a statement whose
// object is a literal; r.prop is the object of a layer statement (r prop ?).

const (
	cEOF = iota
	cIdent
	cStr
	cInt
	cNum
	cPunct
)

type cTok struct {
	k      int
	s      string
	pos    int
	quoted bool
}

type cypherParser struct {
	text     string
	toks     []cTok
	i        int
	declared map[string]bool
	vars     []string
	anon     int
	scope    int
	nscope   int
	aux      map[string]string // scope|var.prop -> aux variable
	auxN     int
	target   *[]QElem
}

// ParseCypher parses the supported Cypher subset into the query IR.
func ParseCypher(text string) (*Query, error) {
	toks, err := lexCypher(text)
	if err != nil {
		return nil, err
	}
	p := &cypherParser{text: text, toks: toks, declared: map[string]bool{}, aux: map[string]string{}}
	return p.query()
}

func lexCypher(text string) ([]cTok, error) {
	var toks []cTok
	rs := []rune(text)
	off := make([]int, len(rs)+1)
	n := 0
	for i, r := range rs {
		off[i] = n
		n += len(string(r))
	}
	off[len(rs)] = n
	i := 0
	for i < len(rs) {
		c := rs[i]
		pos := off[i]
		switch {
		case unicode.IsSpace(c):
			i++
		case c == '/' && i+1 < len(rs) && rs[i+1] == '/':
			for i < len(rs) && rs[i] != '\n' {
				i++
			}
		case c == '\'' || c == '"':
			var sb strings.Builder
			j := i + 1
			closed := false
			for j < len(rs) {
				if rs[j] == '\\' && j+1 < len(rs) {
					switch rs[j+1] {
					case 'n':
						sb.WriteRune('\n')
					case 't':
						sb.WriteRune('\t')
					default:
						sb.WriteRune(rs[j+1])
					}
					j += 2
					continue
				}
				if rs[j] == c {
					closed = true
					break
				}
				sb.WriteRune(rs[j])
				j++
			}
			if !closed {
				return nil, parseErr(text, pos, "unterminated string")
			}
			toks = append(toks, cTok{k: cStr, s: sb.String(), pos: pos})
			i = j + 1
		case c == '`':
			j := i + 1
			for j < len(rs) && rs[j] != '`' {
				j++
			}
			if j >= len(rs) {
				return nil, parseErr(text, pos, "unterminated `identifier`")
			}
			toks = append(toks, cTok{k: cIdent, s: string(rs[i+1 : j]), pos: pos, quoted: true})
			i = j + 1
		case unicode.IsDigit(c):
			j := i
			for j < len(rs) && unicode.IsDigit(rs[j]) {
				j++
			}
			k := cInt
			if j+1 < len(rs) && rs[j] == '.' && unicode.IsDigit(rs[j+1]) {
				k = cNum
				j++
				for j < len(rs) && unicode.IsDigit(rs[j]) {
					j++
				}
			}
			toks = append(toks, cTok{k: k, s: string(rs[i:j]), pos: pos})
			i = j
		case c == '_' || unicode.IsLetter(c):
			j := i
			for j < len(rs) && isNameRune(rs[j]) {
				j++
			}
			toks = append(toks, cTok{k: cIdent, s: string(rs[i:j]), pos: pos})
			i = j
		default:
			two := ""
			if i+1 < len(rs) {
				two = string(rs[i : i+2])
			}
			switch two {
			case "<-", "->", "<>", "<=", ">=", "!=", "=~":
				toks = append(toks, cTok{k: cPunct, s: two, pos: pos})
				i += 2
				continue
			}
			if strings.ContainsRune("()[]{}:,.-=<>*|+/%;$^", c) {
				toks = append(toks, cTok{k: cPunct, s: string(c), pos: pos})
				i++
				continue
			}
			return nil, parseErr(text, pos, "unexpected character %q", string(c))
		}
	}
	toks = append(toks, cTok{k: cEOF, pos: len(text)})
	return toks, nil
}

func (p *cypherParser) peek() cTok { return p.toks[p.i] }

func (p *cypherParser) next() cTok {
	t := p.toks[p.i]
	if t.k != cEOF {
		p.i++
	}
	return t
}

func (p *cypherParser) isPunct(s string) bool {
	t := p.peek()
	return t.k == cPunct && t.s == s
}

func (p *cypherParser) acceptPunct(s string) bool {
	if p.isPunct(s) {
		p.i++
		return true
	}
	return false
}

func (p *cypherParser) isWord(w string) bool {
	t := p.peek()
	return t.k == cIdent && !t.quoted && strings.EqualFold(t.s, w)
}

func (p *cypherParser) acceptWord(w string) bool {
	if p.isWord(w) {
		p.i++
		return true
	}
	return false
}

func (p *cypherParser) errAt(t cTok, format string, args ...any) error {
	return parseErr(p.text, t.pos, format, args...)
}

func (p *cypherParser) unsupported(t cTok, what string) error {
	return &UnsupportedQueryError{Pos: t.pos, Construct: what}
}

func (p *cypherParser) expectPunct(s string) error {
	if !p.acceptPunct(s) {
		t := p.peek()
		return p.errAt(t, "expected %q, found %s", s, p.describe(t))
	}
	return nil
}

func (p *cypherParser) describe(t cTok) string {
	if t.k == cEOF {
		return "end of query"
	}
	return strconv.Quote(t.s)
}

var cypherUnsupported = map[string]bool{
	"CREATE": true, "MERGE": true, "SET": true, "DELETE": true, "DETACH": true, "REMOVE": true,
	"CALL": true, "UNWIND": true, "WITH": true, "FOREACH": true, "UNION": true, "LOAD": true,
	"USING": true, "DROP": true,
}

func (p *cypherParser) declare(name string) {
	if !p.declared[name] {
		p.declared[name] = true
		p.vars = append(p.vars, name)
	}
}

func (p *cypherParser) fresh(prefix string) string {
	p.anon++
	return "_" + prefix + strconv.Itoa(p.anon)
}

func (p *cypherParser) query() (*Query, error) {
	q := &Query{Dialect: DialectCypher}
	if t := p.peek(); t.k == cIdent && !t.quoted && cypherUnsupported[strings.ToUpper(t.s)] {
		return nil, p.unsupported(t, strings.ToUpper(t.s))
	}
	if !p.acceptWord("MATCH") {
		return nil, p.errAt(p.peek(), "expected MATCH, found %s", p.describe(p.peek()))
	}
	var where []QElem
	p.target, p.scope = &where, 0
	if err := p.matchBody(&where); err != nil {
		return nil, err
	}
	for {
		t := p.peek()
		switch {
		case p.isWord("OPTIONAL"):
			p.next()
			if !p.acceptWord("MATCH") {
				return nil, p.errAt(p.peek(), "expected MATCH after OPTIONAL")
			}
			var g []QElem
			p.nscope++
			p.scope = p.nscope
			p.target = &g
			if err := p.matchBody(&g); err != nil {
				return nil, err
			}
			where = append(where, QOptional{Group: g})
			p.scope, p.target = 0, &where
		case p.isWord("MATCH"):
			return nil, p.unsupported(t, "multiple MATCH clauses (use comma-separated patterns)")
		case t.k == cIdent && !t.quoted && cypherUnsupported[strings.ToUpper(t.s)]:
			return nil, p.unsupported(t, strings.ToUpper(t.s))
		case p.isWord("RETURN"):
			p.next()
			if err := p.returnClause(q, &where); err != nil {
				return nil, err
			}
			q.Where = where
			return q, nil
		default:
			return nil, p.errAt(t, "expected OPTIONAL MATCH or RETURN, found %s", p.describe(t))
		}
	}
}

// matchBody parses the patterns and optional WHERE of one MATCH clause into g.
func (p *cypherParser) matchBody(g *[]QElem) error {
	for {
		if err := p.pattern(g); err != nil {
			return err
		}
		if !p.acceptPunct(",") {
			break
		}
	}
	if p.acceptWord("WHERE") {
		e, err := p.orExpr()
		if err != nil {
			return err
		}
		*g = append(*g, QFilter{Expr: e})
	}
	return nil
}

func (p *cypherParser) node(props *[]QElem) (string, error) {
	if err := p.expectPunct("("); err != nil {
		return "", err
	}
	var name string
	if t := p.peek(); t.k == cIdent {
		p.next()
		name = t.s
		p.declare(name)
	} else {
		name = p.fresh("n")
	}
	if p.isPunct(":") {
		return "", p.unsupported(p.peek(), "node label")
	}
	if p.acceptPunct("{") {
		for !p.isPunct("}") {
			k := p.next()
			if k.k != cIdent {
				return "", p.errAt(k, "expected a property name")
			}
			if err := p.expectPunct(":"); err != nil {
				return "", err
			}
			v, err := p.literal()
			if err != nil {
				return "", err
			}
			*props = append(*props, QTriple{S: QVar(name), P: QConst(Ent(k.s)), O: QConst(v), Class: ObjLiteral})
			if !p.acceptPunct(",") {
				break
			}
		}
		if err := p.expectPunct("}"); err != nil {
			return "", err
		}
	}
	if err := p.expectPunct(")"); err != nil {
		return "", err
	}
	return name, nil
}

func (p *cypherParser) literal() (Value, error) {
	t := p.next()
	neg := false
	if t.k == cPunct && t.s == "-" {
		neg = true
		t = p.next()
	}
	switch t.k {
	case cStr:
		if neg {
			return Value{}, p.errAt(t, "cannot negate a string")
		}
		return Str(t.s), nil
	case cInt:
		s := t.s
		if neg {
			s = "-" + s
		}
		i, err := strconv.ParseInt(s, 10, 64)
		if err != nil {
			return Value{}, p.errAt(t, "integer out of range")
		}
		return Int(i), nil
	case cNum:
		s := t.s
		if neg {
			s = "-" + s
		}
		f, err := strconv.ParseFloat(s, 64)
		if err != nil {
			return Value{}, p.errAt(t, "bad number")
		}
		return Num(f), nil
	case cIdent:
		switch strings.ToLower(t.s) {
		case "true":
			return Bool(true), nil
		case "false":
			return Bool(false), nil
		}
	case cPunct:
		if t.s == "[" {
			return Value{}, p.unsupported(t, "list literal")
		}
		if t.s == "{" {
			return Value{}, p.unsupported(t, "map literal")
		}
	}
	return Value{}, p.errAt(t, "expected a literal, found %s", p.describe(t))
}

// pattern parses one path: (a)-[r:p]->(b)<-[:q]-(c) ...
func (p *cypherParser) pattern(g *[]QElem) error {
	var props, rels []QElem
	known := len(p.declared)
	left, err := p.node(&props)
	if err != nil {
		return err
	}
	if len(props) == 0 && len(p.declared) > known && !p.isPunct("-") && !p.isPunct("<-") {
		*g = append(*g, QNode{Var: left}) // a bare node pattern scans the entities
	}
	for p.isPunct("-") || p.isPunct("<-") {
		first := p.next()
		back := first.s == "<-"
		relVar, relType, hasType := "", "", false
		if p.acceptPunct("[") {
			if t := p.peek(); t.k == cIdent {
				p.next()
				relVar = t.s
				p.declare(relVar)
			}
			if p.acceptPunct(":") {
				t := p.next()
				if t.k != cIdent {
					return p.errAt(t, "expected a relationship type")
				}
				relType, hasType = t.s, true
				if p.isPunct("|") {
					return p.unsupported(p.peek(), "alternative relationship types")
				}
			}
			if p.isPunct("*") {
				return p.unsupported(p.peek(), "variable-length relationship")
			}
			if p.isPunct("{") {
				return p.unsupported(p.peek(), "relationship properties")
			}
			if err := p.expectPunct("]"); err != nil {
				return err
			}
		}
		second := p.next()
		var dir int // 1 forward, -1 backward, 0 either
		switch {
		case second.k == cPunct && second.s == "->" && !back:
			dir = 1
		case second.k == cPunct && second.s == "-" && back:
			dir = -1
		case second.k == cPunct && second.s == "-" && !back:
			dir = 0
		case second.k == cPunct && second.s == "->" && back:
			return p.unsupported(second, "bidirectional relationship")
		default:
			return p.errAt(second, "expected - or -> to close the relationship, found %s", p.describe(second))
		}
		right, err := p.node(&props)
		if err != nil {
			return err
		}
		pred := QVar(p.fresh("p"))
		if hasType {
			pred = QConst(Ent(relType))
		}
		mk := func(s, o string) QTriple {
			return QTriple{S: QVar(s), P: pred, O: QVar(o), EID: relVar, Class: ObjEntity}
		}
		switch dir {
		case 1:
			rels = append(rels, mk(left, right))
		case -1:
			rels = append(rels, mk(right, left))
		default:
			rels = append(rels, QUnion{Groups: [][]QElem{{mk(left, right)}, {mk(right, left)}}})
		}
		left = right
	}
	*g = append(append(*g, props...), rels...)
	return nil
}

// propVar returns the variable holding x.prop, adding the accessor to the
// current clause's group on first use.
func (p *cypherParser) propVar(x, prop string, at cTok) (string, error) {
	if !p.declared[x] {
		return "", p.errAt(at, "unknown variable %q", x)
	}
	key := strconv.Itoa(p.scope) + "|" + x + "." + prop
	if v, ok := p.aux[key]; ok {
		return v, nil
	}
	p.auxN++
	v := "_prop" + strconv.Itoa(p.auxN)
	p.aux[key] = v
	*p.target = append(*p.target, QOptional{Aux: true, Group: []QElem{
		QTriple{S: QVar(x), P: QConst(Ent(prop)), O: QVar(v), Class: ObjLiteral, Aux: true}}})
	return v, nil
}

// Expressions.

func (p *cypherParser) orExpr() (*QExpr, error) {
	l, err := p.andExpr()
	if err != nil {
		return nil, err
	}
	for p.acceptWord("OR") {
		r, err := p.andExpr()
		if err != nil {
			return nil, err
		}
		l = &QExpr{Op: "||", Args: []*QExpr{l, r}}
	}
	return l, nil
}

func (p *cypherParser) andExpr() (*QExpr, error) {
	l, err := p.notExpr()
	if err != nil {
		return nil, err
	}
	for p.acceptWord("AND") {
		r, err := p.notExpr()
		if err != nil {
			return nil, err
		}
		l = &QExpr{Op: "&&", Args: []*QExpr{l, r}}
	}
	return l, nil
}

func (p *cypherParser) notExpr() (*QExpr, error) {
	if p.acceptWord("NOT") {
		x, err := p.notExpr()
		if err != nil {
			return nil, err
		}
		return &QExpr{Op: "!", Args: []*QExpr{x}}, nil
	}
	return p.cmpExpr()
}

func (p *cypherParser) cmpExpr() (*QExpr, error) {
	l, err := p.atom()
	if err != nil {
		return nil, err
	}
	t := p.peek()
	if t.k == cPunct {
		op := t.s
		switch op {
		case "=", "<", "<=", ">", ">=", "!=", "<>":
			p.next()
			if op == "<>" {
				op = "!="
			}
			r, err := p.atom()
			if err != nil {
				return nil, err
			}
			return &QExpr{Op: op, Args: []*QExpr{l, r}}, nil
		case "=~":
			return nil, p.unsupported(t, "regular expression match =~")
		case "+", "-", "*", "/", "%", "^":
			return nil, p.unsupported(t, "arithmetic")
		}
	}
	switch {
	case p.isWord("IS"):
		p.next()
		not := p.acceptWord("NOT")
		if !p.acceptWord("NULL") {
			return nil, p.errAt(p.peek(), "expected NULL after IS")
		}
		if l.Op != "var" {
			return nil, p.unsupported(t, "IS NULL on an expression")
		}
		b := &QExpr{Op: "bound", Args: []*QExpr{{Op: "var", Var: l.Var}}}
		if not {
			return b, nil
		}
		return &QExpr{Op: "!", Args: []*QExpr{b}}, nil
	case p.isWord("CONTAINS"):
		p.next()
		r, err := p.atom()
		if err != nil {
			return nil, err
		}
		return &QExpr{Op: "contains", Args: []*QExpr{l, r}}, nil
	case p.isWord("STARTS"):
		p.next()
		if !p.acceptWord("WITH") {
			return nil, p.errAt(p.peek(), "expected WITH after STARTS")
		}
		r, err := p.atom()
		if err != nil {
			return nil, err
		}
		return &QExpr{Op: "strstarts", Args: []*QExpr{l, r}}, nil
	case p.isWord("IN"):
		return nil, p.unsupported(t, "IN")
	}
	return l, nil
}

func (p *cypherParser) atom() (*QExpr, error) {
	t := p.peek()
	switch {
	case t.k == cPunct && t.s == "(":
		p.next()
		e, err := p.orExpr()
		if err != nil {
			return nil, err
		}
		if err := p.expectPunct(")"); err != nil {
			return nil, err
		}
		return e, nil
	case t.k == cIdent && !t.quoted && (strings.EqualFold(t.s, "true") || strings.EqualFold(t.s, "false")):
		v, _ := p.literal()
		return &QExpr{Op: "const", Val: v}, nil
	case t.k == cIdent:
		p.next()
		if p.isPunct("(") {
			return nil, p.unsupported(t, "function "+t.s)
		}
		if p.acceptPunct(".") {
			prop := p.next()
			if prop.k != cIdent {
				return nil, p.errAt(prop, "expected a property name after .")
			}
			v, err := p.propVar(t.s, prop.s, t)
			if err != nil {
				return nil, err
			}
			return &QExpr{Op: "var", Var: v}, nil
		}
		if !p.declared[t.s] {
			return nil, p.errAt(t, "unknown variable %q", t.s)
		}
		return &QExpr{Op: "var", Var: t.s}, nil
	case t.k == cStr || t.k == cInt || t.k == cNum || (t.k == cPunct && t.s == "-"):
		v, err := p.literal()
		if err != nil {
			return nil, err
		}
		return &QExpr{Op: "const", Val: v}, nil
	}
	return nil, p.errAt(t, "expected an expression, found %s", p.describe(t))
}

func (p *cypherParser) returnClause(q *Query, where *[]QElem) error {
	p.target, p.scope = where, 0
	if p.acceptWord("DISTINCT") {
		q.Distinct = true
	}
	if p.acceptPunct("*") {
		for _, v := range p.vars {
			q.Vars = append(q.Vars, QProj{Name: v, Var: v})
		}
	} else {
		for {
			pr, err := p.returnItem()
			if err != nil {
				return err
			}
			q.Vars = append(q.Vars, pr)
			if !p.acceptPunct(",") {
				break
			}
		}
	}
	counts := 0
	for _, pr := range q.Vars {
		if pr.Count {
			counts++
		}
	}
	if counts > 1 {
		return p.unsupported(p.peek(), "more than one count(*)")
	}
	if p.acceptWord("ORDER") {
		if !p.acceptWord("BY") {
			return p.errAt(p.peek(), "expected BY after ORDER")
		}
		for {
			t := p.peek()
			key := ""
			if t.k == cIdent && !p.toks[p.i+1].isDot() {
				for _, pr := range q.Vars {
					if pr.Name == t.s {
						key = t.s
					}
				}
			}
			if key == "" {
				e, err := p.atom()
				if err != nil {
					return err
				}
				if e.Op != "var" {
					return p.unsupported(t, "ORDER BY expression")
				}
				key = e.Var
			} else {
				p.next()
			}
			o := QOrder{Key: key}
			if p.acceptWord("DESC") || p.acceptWord("DESCENDING") {
				o.Desc = true
			} else if !p.acceptWord("ASC") {
				p.acceptWord("ASCENDING")
			}
			q.OrderBy = append(q.OrderBy, o)
			if !p.acceptPunct(",") {
				break
			}
		}
	}
	for {
		switch {
		case p.acceptWord("SKIP"):
			t := p.next()
			if t.k != cInt {
				return p.errAt(t, "SKIP takes an integer")
			}
			q.Offset, _ = intTok(t.s)
			continue
		case p.acceptWord("LIMIT"):
			t := p.next()
			if t.k != cInt {
				return p.errAt(t, "LIMIT takes an integer")
			}
			q.Limit, _ = intTok(t.s)
			q.HasLimit = true
			continue
		}
		break
	}
	p.acceptPunct(";")
	if t := p.peek(); t.k != cEOF {
		if t.k == cIdent && cypherUnsupported[strings.ToUpper(t.s)] {
			return p.unsupported(t, strings.ToUpper(t.s))
		}
		return p.errAt(t, "unexpected %s after RETURN", p.describe(t))
	}
	return nil
}

func (t cTok) isDot() bool { return t.k == cPunct && t.s == "." }

func (p *cypherParser) returnItem() (QProj, error) {
	t := p.peek()
	if t.k == cIdent && !t.quoted && strings.EqualFold(t.s, "count") && p.toks[p.i+1].k == cPunct && p.toks[p.i+1].s == "(" {
		p.next()
		p.next()
		if p.isWord("DISTINCT") {
			return QProj{}, p.unsupported(p.peek(), "count(DISTINCT)")
		}
		if !p.acceptPunct("*") {
			return QProj{}, p.unsupported(p.peek(), "count of an expression (only count(*))")
		}
		if err := p.expectPunct(")"); err != nil {
			return QProj{}, err
		}
		name := "count(*)"
		if p.acceptWord("AS") {
			a := p.next()
			if a.k != cIdent {
				return QProj{}, p.errAt(a, "expected an alias after AS")
			}
			name = a.s
		}
		return QProj{Name: name, Count: true}, nil
	}
	if t.k != cIdent {
		return QProj{}, p.errAt(t, "expected a variable or property in RETURN, found %s", p.describe(t))
	}
	name := t.s
	e, err := p.atom()
	if err != nil {
		return QProj{}, err
	}
	if e.Op != "var" {
		return QProj{}, p.unsupported(t, "RETURN expression")
	}
	if p.toks[p.i-2].isDot() { // a.prop
		name = name + "." + p.toks[p.i-1].s
	}
	if p.acceptWord("AS") {
		a := p.next()
		if a.k != cIdent {
			return QProj{}, p.errAt(a, "expected an alias after AS")
		}
		name = a.s
	}
	return QProj{Name: name, Var: e.Var}, nil
}
