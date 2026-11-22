package palace

import (
	"fmt"
	"strconv"
	"strings"
	"unicode"
)

// SPARQL subset: PREFIX, SELECT [DISTINCT] vars | * | (COUNT(*) AS ?n),
// triple patterns with . ; and , abbreviations, FILTER, OPTIONAL, UNION,
// FILTER NOT EXISTS, GRAPH <g> { }, ORDER BY [DESC], LIMIT, OFFSET, and
// << s p o >> as a subject or object binding a statement. Anything else is an
// UnsupportedQueryError before execution.

const (
	sEOF = iota
	sIRI
	sPName
	sVar
	sStr
	sInt
	sDec
	sWord
	sPunct
)

type sTok struct {
	k    int
	s    string
	pos  int
	prev int // end offset, for error positions
}

type sparqlParser struct {
	text string
	toks []sTok
	i    int
	star int
	vars []string
	seen map[string]bool
}

// ParseSPARQL parses the supported SPARQL subset into the query IR.
func ParseSPARQL(text string) (*Query, error) {
	toks, err := lexSPARQL(text)
	if err != nil {
		return nil, err
	}
	p := &sparqlParser{text: text, toks: toks, seen: map[string]bool{}}
	return p.query()
}

func isNameRune(r rune) bool { return r == '_' || unicode.IsLetter(r) || unicode.IsDigit(r) }

func lexSPARQL(text string) ([]sTok, error) {
	var toks []sTok
	rs := []rune(text)
	// byte offsets per rune index
	off := make([]int, len(rs)+1)
	n := 0
	for i, r := range rs {
		off[i] = n
		n += len(string(r))
	}
	off[len(rs)] = n
	i := 0
	emit := func(k int, s string, start, end int) {
		toks = append(toks, sTok{k: k, s: s, pos: off[start], prev: off[end]})
	}
	for i < len(rs) {
		c := rs[i]
		switch {
		case unicode.IsSpace(c):
			i++
		case c == '#':
			for i < len(rs) && rs[i] != '\n' {
				i++
			}
		case c == '<':
			if i+1 < len(rs) && rs[i+1] == '<' {
				emit(sPunct, "<<", i, i+2)
				i += 2
				continue
			}
			j := i + 1
			for j < len(rs) && !unicode.IsSpace(rs[j]) && !strings.ContainsRune("<>\"{}|^`\\", rs[j]) {
				j++
			}
			if j < len(rs) && rs[j] == '>' && j > i+1 {
				emit(sIRI, string(rs[i+1:j]), i, j+1)
				i = j + 1
			} else if i+1 < len(rs) && rs[i+1] == '=' {
				emit(sPunct, "<=", i, i+2)
				i += 2
			} else {
				emit(sPunct, "<", i, i+1)
				i++
			}
		case c == '>':
			switch {
			case i+1 < len(rs) && rs[i+1] == '>':
				emit(sPunct, ">>", i, i+2)
				i += 2
			case i+1 < len(rs) && rs[i+1] == '=':
				emit(sPunct, ">=", i, i+2)
				i += 2
			default:
				emit(sPunct, ">", i, i+1)
				i++
			}
		case c == '!':
			if i+1 < len(rs) && rs[i+1] == '=' {
				emit(sPunct, "!=", i, i+2)
				i += 2
			} else {
				emit(sPunct, "!", i, i+1)
				i++
			}
		case c == '&' && i+1 < len(rs) && rs[i+1] == '&':
			emit(sPunct, "&&", i, i+2)
			i += 2
		case c == '|' && i+1 < len(rs) && rs[i+1] == '|':
			emit(sPunct, "||", i, i+2)
			i += 2
		case c == '"' || c == '\'':
			if i+2 < len(rs) && rs[i+1] == c && rs[i+2] == c {
				return nil, &UnsupportedQueryError{Pos: off[i], Construct: "long string literal"}
			}
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
					case 'r':
						sb.WriteRune('\r')
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
				return nil, parseErr(text, off[i], "unterminated string")
			}
			emit(sStr, sb.String(), i, j+1)
			i = j + 1
		case unicode.IsDigit(c):
			j := i
			for j < len(rs) && unicode.IsDigit(rs[j]) {
				j++
			}
			k := sInt
			if j+1 < len(rs) && rs[j] == '.' && unicode.IsDigit(rs[j+1]) {
				k = sDec
				j++
				for j < len(rs) && unicode.IsDigit(rs[j]) {
					j++
				}
			}
			if j < len(rs) && (rs[j] == 'e' || rs[j] == 'E') {
				m := j + 1
				if m < len(rs) && (rs[m] == '+' || rs[m] == '-') {
					m++
				}
				if m < len(rs) && unicode.IsDigit(rs[m]) {
					for m < len(rs) && unicode.IsDigit(rs[m]) {
						m++
					}
					k, j = sDec, m
				}
			}
			emit(k, string(rs[i:j]), i, j)
			i = j
		case c == '?' || c == '$':
			j := i + 1
			for j < len(rs) && isNameRune(rs[j]) {
				j++
			}
			if j == i+1 {
				return nil, parseErr(text, off[i], "empty variable name")
			}
			emit(sVar, string(rs[i+1:j]), i, j)
			i = j
		case c == ':' || isNameRune(c):
			j := i
			for j < len(rs) && (isNameRune(rs[j]) || rs[j] == '-') && rs[j] != ':' {
				j++
			}
			if j < len(rs) && rs[j] == ':' {
				j++
				for j < len(rs) && (isNameRune(rs[j]) || strings.ContainsRune(".-:%", rs[j])) {
					j++
				}
				for rs[j-1] == '.' { // a trailing dot ends the triple
					j--
				}
				emit(sPName, string(rs[i:j]), i, j)
			} else {
				emit(sWord, string(rs[i:j]), i, j)
			}
			if j == i {
				return nil, parseErr(text, off[i], "unexpected %q", string(c))
			}
			i = j
		case strings.ContainsRune("{}().;,*=+-/[]^@|&", c):
			emit(sPunct, string(c), i, i+1)
			i++
		default:
			return nil, parseErr(text, off[i], "unexpected character %q", string(c))
		}
	}
	toks = append(toks, sTok{k: sEOF, pos: len(text), prev: len(text)})
	return toks, nil
}

func (p *sparqlParser) peek() sTok { return p.toks[p.i] }

func (p *sparqlParser) next() sTok {
	t := p.toks[p.i]
	if t.k != sEOF {
		p.i++
	}
	return t
}

func (p *sparqlParser) isPunct(s string) bool {
	t := p.peek()
	return t.k == sPunct && t.s == s
}

func (p *sparqlParser) isWord(w string) bool {
	t := p.peek()
	return t.k == sWord && strings.EqualFold(t.s, w)
}

func (p *sparqlParser) acceptPunct(s string) bool {
	if p.isPunct(s) {
		p.i++
		return true
	}
	return false
}

func (p *sparqlParser) acceptWord(w string) bool {
	if p.isWord(w) {
		p.i++
		return true
	}
	return false
}

func (p *sparqlParser) errAt(t sTok, format string, args ...any) error {
	return parseErr(p.text, t.pos, format, args...)
}

func (p *sparqlParser) unsupported(t sTok, what string) error {
	return &UnsupportedQueryError{Pos: t.pos, Construct: what}
}

func (p *sparqlParser) expectPunct(s string) error {
	if !p.acceptPunct(s) {
		t := p.peek()
		return p.errAt(t, "expected %q, found %s", s, describeTok(t))
	}
	return nil
}

func describeTok(t sTok) string {
	if t.k == sEOF {
		return "end of query"
	}
	return strconv.Quote(t.s)
}

func (p *sparqlParser) noteVar(n string) {
	if !p.seen[n] {
		p.seen[n] = true
		p.vars = append(p.vars, n)
	}
}

func (p *sparqlParser) query() (*Query, error) {
	q := &Query{Dialect: DialectSPARQL}
	for {
		t := p.peek()
		if p.acceptWord("PREFIX") {
			n := p.next()
			if n.k != sPName || !strings.HasSuffix(n.s, ":") {
				return nil, p.errAt(n, "expected a prefix name like ex:")
			}
			if i := p.next(); i.k != sIRI {
				return nil, p.errAt(i, "expected <iri> after the prefix name")
			}
			continue
		}
		if p.isWord("BASE") {
			return nil, p.unsupported(t, "BASE")
		}
		break
	}
	t := p.next()
	if t.k != sWord || !strings.EqualFold(t.s, "SELECT") {
		if t.k == sWord {
			switch strings.ToUpper(t.s) {
			case "CONSTRUCT", "ASK", "DESCRIBE", "INSERT", "DELETE", "LOAD", "CLEAR", "DROP", "CREATE", "WITH":
				return nil, p.unsupported(t, strings.ToUpper(t.s))
			}
		}
		return nil, p.errAt(t, "expected SELECT, found %s", describeTok(t))
	}
	if p.acceptWord("DISTINCT") {
		q.Distinct = true
	} else if p.isWord("REDUCED") {
		return nil, p.unsupported(p.peek(), "REDUCED")
	}
	star, err := p.selectList(q)
	if err != nil {
		return nil, err
	}
	if p.isWord("FROM") {
		return nil, p.unsupported(p.peek(), "FROM")
	}
	p.acceptWord("WHERE")
	if !p.isPunct("{") {
		return nil, p.errAt(p.peek(), "expected { to open the pattern, found %s", describeTok(p.peek()))
	}
	where, err := p.groupBlock()
	if err != nil {
		return nil, err
	}
	q.Where = where
	if star {
		for _, v := range p.vars {
			if !strings.HasPrefix(v, "_") {
				q.Vars = append(q.Vars, QProj{Name: v, Var: v})
			}
		}
	}
	if err := p.modifiers(q); err != nil {
		return nil, err
	}
	return q, nil
}

func (p *sparqlParser) selectList(q *Query) (star bool, err error) {
	if p.acceptPunct("*") {
		return true, nil
	}
	hasCount := false
	for {
		t := p.peek()
		switch {
		case t.k == sVar:
			p.next()
			q.Vars = append(q.Vars, QProj{Name: t.s, Var: t.s})
			continue
		case t.k == sPunct && t.s == "(":
			p.next()
			f := p.next()
			if f.k != sWord {
				return false, p.unsupported(f, "projection expression")
			}
			if !strings.EqualFold(f.s, "COUNT") {
				return false, p.unsupported(f, "aggregate "+strings.ToUpper(f.s))
			}
			if err := p.expectPunct("("); err != nil {
				return false, err
			}
			if p.isWord("DISTINCT") {
				return false, p.unsupported(p.peek(), "COUNT(DISTINCT)")
			}
			if !p.isPunct("*") {
				return false, p.unsupported(p.peek(), "COUNT of an expression (only COUNT(*))")
			}
			p.next()
			if err := p.expectPunct(")"); err != nil {
				return false, err
			}
			if !p.acceptWord("AS") {
				return false, p.errAt(p.peek(), "expected AS")
			}
			v := p.next()
			if v.k != sVar {
				return false, p.errAt(v, "expected a variable after AS")
			}
			if err := p.expectPunct(")"); err != nil {
				return false, err
			}
			q.Vars = append(q.Vars, QProj{Name: v.s, Count: true})
			hasCount = true
			continue
		}
		break
	}
	if len(q.Vars) == 0 {
		return false, p.errAt(p.peek(), "expected variables, * or (COUNT(*) AS ?n) after SELECT")
	}
	if hasCount && len(q.Vars) > 1 {
		return false, p.unsupported(p.peek(), "COUNT mixed with variables (GROUP BY)")
	}
	return false, nil
}

func (p *sparqlParser) modifiers(q *Query) error {
	for {
		t := p.peek()
		switch {
		case t.k == sEOF:
			return nil
		case p.acceptWord("ORDER"):
			if !p.acceptWord("BY") {
				return p.errAt(p.peek(), "expected BY after ORDER")
			}
			n := 0
		orderLoop:
			for {
				desc := false
				switch {
				case p.acceptWord("DESC"):
					desc = true
					fallthrough
				case p.acceptWord("ASC"):
					if err := p.expectPunct("("); err != nil {
						return err
					}
					v := p.next()
					if v.k != sVar {
						return p.unsupported(v, "ORDER BY expression")
					}
					if err := p.expectPunct(")"); err != nil {
						return err
					}
					q.OrderBy = append(q.OrderBy, QOrder{Key: v.s, Desc: desc})
				case p.peek().k == sVar:
					q.OrderBy = append(q.OrderBy, QOrder{Key: p.next().s})
				default:
					if n == 0 {
						return p.errAt(p.peek(), "expected an ORDER BY condition")
					}
					break orderLoop
				}
				n++
			}
		case p.acceptWord("LIMIT"):
			v, err := p.intArg()
			if err != nil {
				return err
			}
			q.Limit, q.HasLimit = v, true
		case p.acceptWord("OFFSET"):
			v, err := p.intArg()
			if err != nil {
				return err
			}
			q.Offset = v
		case p.isWord("GROUP") || p.isWord("HAVING") || p.isWord("VALUES"):
			return p.unsupported(t, strings.ToUpper(t.s))
		default:
			return p.errAt(t, "unexpected %s after the pattern", describeTok(t))
		}
	}
}

func (p *sparqlParser) intArg() (int, error) {
	t := p.next()
	if t.k != sInt {
		return 0, p.errAt(t, "expected an integer, found %s", describeTok(t))
	}
	n, err := intTok(t.s)
	if err != nil {
		return 0, p.errAt(t, "integer %s is out of range", t.s)
	}
	return n, nil
}

// groupBlock parses { ... } into a conjunction.
func (p *sparqlParser) groupBlock() ([]QElem, error) {
	if err := p.expectPunct("{"); err != nil {
		return nil, err
	}
	var elems []QElem
	for {
		t := p.peek()
		switch {
		case t.k == sEOF:
			return nil, p.errAt(t, "missing } before end of query")
		case p.acceptPunct("}"):
			return elems, nil
		case p.acceptPunct("."):
		case p.isPunct("{"):
			g, err := p.groupBlock()
			if err != nil {
				return nil, err
			}
			groups := [][]QElem{g}
			for p.acceptWord("UNION") {
				g2, err := p.groupBlock()
				if err != nil {
					return nil, err
				}
				groups = append(groups, g2)
			}
			if len(groups) == 1 {
				elems = append(elems, g...)
			} else {
				elems = append(elems, QUnion{Groups: groups})
			}
		case p.acceptWord("OPTIONAL"):
			g, err := p.groupBlock()
			if err != nil {
				return nil, err
			}
			elems = append(elems, QOptional{Group: g})
		case p.acceptWord("FILTER"):
			el, err := p.filter()
			if err != nil {
				return nil, err
			}
			elems = append(elems, el)
		case p.acceptWord("GRAPH"):
			g := p.next()
			if g.k != sIRI && g.k != sPName {
				if g.k == sVar {
					return nil, p.unsupported(g, "GRAPH with a variable")
				}
				return nil, p.errAt(g, "expected a graph name after GRAPH")
			}
			body, err := p.groupBlock()
			if err != nil {
				return nil, err
			}
			elems = append(elems, QGraph{Name: localName(g.s, g.k == sIRI), Group: body})
		case t.k == sWord && (strings.EqualFold(t.s, "BIND") || strings.EqualFold(t.s, "VALUES") ||
			strings.EqualFold(t.s, "MINUS") || strings.EqualFold(t.s, "SERVICE") || strings.EqualFold(t.s, "SELECT")):
			what := strings.ToUpper(t.s)
			if what == "SELECT" {
				what = "subquery"
			}
			return nil, p.unsupported(t, what)
		default:
			ts, err := p.triplesBlock()
			if err != nil {
				return nil, err
			}
			elems = append(elems, ts...)
		}
	}
}

func (p *sparqlParser) filter() (QElem, error) {
	if p.isWord("NOT") {
		p.next()
		if !p.acceptWord("EXISTS") {
			return nil, p.errAt(p.peek(), "expected EXISTS after NOT")
		}
		g, err := p.groupBlock()
		if err != nil {
			return nil, err
		}
		return QNotExists{Group: g}, nil
	}
	if p.isWord("EXISTS") {
		return nil, p.unsupported(p.peek(), "FILTER EXISTS (use FILTER NOT EXISTS)")
	}
	var (
		e   *QExpr
		err error
	)
	if p.acceptPunct("(") {
		if e, err = p.expr(); err != nil {
			return nil, err
		}
		if err := p.expectPunct(")"); err != nil {
			return nil, err
		}
	} else if e, err = p.primary(); err != nil {
		return nil, err
	}
	return QFilter{Expr: e}, nil
}

// localName turns an IRI or prefixed name into the entity name the palace
// uses: the part after the last # / or :.
func localName(s string, iri bool) string {
	if iri {
		s = strings.TrimRight(s, "/#")
		if i := strings.LastIndexAny(s, "#/"); i >= 0 {
			s = s[i+1:]
		}
		return s
	}
	return s[strings.Index(s, ":")+1:]
}

func (p *sparqlParser) triplesBlock() ([]QElem, error) {
	var out []QElem
	subj, err := p.term(&out)
	if err != nil {
		return nil, err
	}
	for {
		pred, err := p.verb()
		if err != nil {
			return nil, err
		}
		for {
			obj, err := p.term(&out)
			if err != nil {
				return nil, err
			}
			out = append(out, QTriple{S: subj, P: pred, O: obj})
			if !p.acceptPunct(",") {
				break
			}
		}
		if !p.acceptPunct(";") {
			break
		}
		for p.acceptPunct(";") {
		}
		if p.isPunct(".") || p.isPunct("}") {
			break
		}
	}
	p.acceptPunct(".")
	return out, nil
}

func (p *sparqlParser) verb() (QTerm, error) {
	t := p.peek()
	switch {
	case t.k == sVar:
		p.next()
		p.noteVar(t.s)
		return QVar(t.s), nil
	case t.k == sIRI || t.k == sPName:
		p.next()
		if t.k == sPName && strings.HasPrefix(t.s, "_:") {
			return QTerm{}, p.unsupported(t, "blank node")
		}
		return p.checkPathEnd(QConst(Ent(localName(t.s, t.k == sIRI))))
	case t.k == sWord && t.s == "a":
		return QTerm{}, p.unsupported(t, "a (rdf:type shorthand)")
	case t.k == sPunct && strings.Contains("^/|+*!(", t.s):
		return QTerm{}, p.unsupported(t, "property path")
	}
	return QTerm{}, p.errAt(t, "expected a predicate, found %s", describeTok(t))
}

// checkPathEnd rejects a property path operator right after a predicate.
func (p *sparqlParser) checkPathEnd(t QTerm) (QTerm, error) {
	if n := p.peek(); n.k == sPunct && strings.Contains("/|+*^", n.s) {
		return QTerm{}, p.unsupported(n, "property path")
	}
	return t, nil
}

// term parses a subject or object. A << s p o >> term appends its triple to
// pre, binds the statement to a fresh variable and returns that variable.
func (p *sparqlParser) term(pre *[]QElem) (QTerm, error) {
	t := p.next()
	switch t.k {
	case sVar:
		p.noteVar(t.s)
		return QVar(t.s), nil
	case sIRI:
		return QConst(Ent(localName(t.s, true))), nil
	case sPName:
		if strings.HasPrefix(t.s, "_:") {
			return QTerm{}, p.unsupported(t, "blank node")
		}
		return QConst(Ent(localName(t.s, false))), nil
	case sStr:
		if n := p.peek(); n.k == sPunct && (n.s == "@" || n.s == "^") {
			return QTerm{}, p.unsupported(n, "language tag or datatype")
		}
		return QConst(Str(t.s)), nil
	case sInt, sDec:
		return p.number(t, false)
	case sWord:
		switch strings.ToLower(t.s) {
		case "true":
			return QConst(Bool(true)), nil
		case "false":
			return QConst(Bool(false)), nil
		}
		return QTerm{}, p.errAt(t, "unexpected %s; terms are <iri>, prefix:name, literals or ?vars", describeTok(t))
	case sPunct:
		switch t.s {
		case "<<":
			s, err := p.term(pre)
			if err != nil {
				return QTerm{}, err
			}
			pr, err := p.verb()
			if err != nil {
				return QTerm{}, err
			}
			o, err := p.term(pre)
			if err != nil {
				return QTerm{}, err
			}
			if err := p.expectPunct(">>"); err != nil {
				return QTerm{}, err
			}
			p.star++
			name := "_star" + strconv.Itoa(p.star)
			*pre = append(*pre, QTriple{S: s, P: pr, O: o, EID: name})
			return QVar(name), nil
		case "-", "+":
			n := p.next()
			if n.k != sInt && n.k != sDec {
				return QTerm{}, p.errAt(n, "expected a number after %s", t.s)
			}
			return p.number(n, t.s == "-")
		case "[":
			return QTerm{}, p.unsupported(t, "blank node")
		case "(":
			return QTerm{}, p.unsupported(t, "collection")
		}
	}
	return QTerm{}, p.errAt(t, "expected a term, found %s", describeTok(t))
}

func (p *sparqlParser) number(t sTok, neg bool) (QTerm, error) {
	s := t.s
	if neg {
		s = "-" + s
	}
	if t.k == sInt {
		i, err := strconv.ParseInt(s, 10, 64)
		if err != nil {
			return QTerm{}, p.errAt(t, "integer out of range")
		}
		return QConst(Int(i)), nil
	}
	f, err := strconv.ParseFloat(s, 64)
	if err != nil {
		return QTerm{}, p.errAt(t, "bad number %s", t.s)
	}
	return QConst(Num(f)), nil
}

// Expressions.

func (p *sparqlParser) expr() (*QExpr, error) {
	l, err := p.andExpr()
	if err != nil {
		return nil, err
	}
	for p.acceptPunct("||") {
		r, err := p.andExpr()
		if err != nil {
			return nil, err
		}
		l = &QExpr{Op: "||", Args: []*QExpr{l, r}}
	}
	return l, nil
}

func (p *sparqlParser) andExpr() (*QExpr, error) {
	l, err := p.relExpr()
	if err != nil {
		return nil, err
	}
	for p.acceptPunct("&&") {
		r, err := p.relExpr()
		if err != nil {
			return nil, err
		}
		l = &QExpr{Op: "&&", Args: []*QExpr{l, r}}
	}
	return l, nil
}

func (p *sparqlParser) relExpr() (*QExpr, error) {
	l, err := p.unary()
	if err != nil {
		return nil, err
	}
	t := p.peek()
	if t.k == sPunct {
		switch t.s {
		case "=", "!=", "<", "<=", ">", ">=":
			p.next()
			r, err := p.unary()
			if err != nil {
				return nil, err
			}
			l = &QExpr{Op: t.s, Args: []*QExpr{l, r}}
			t = p.peek()
		}
	}
	if t.k == sPunct && strings.Contains("+-*/", t.s) {
		return nil, p.unsupported(t, "arithmetic")
	}
	if t.k == sWord && (strings.EqualFold(t.s, "IN") || strings.EqualFold(t.s, "NOT")) {
		return nil, p.unsupported(t, strings.ToUpper(t.s)+" in an expression")
	}
	return l, nil
}

func (p *sparqlParser) unary() (*QExpr, error) {
	if p.acceptPunct("!") {
		x, err := p.unary()
		if err != nil {
			return nil, err
		}
		return &QExpr{Op: "!", Args: []*QExpr{x}}, nil
	}
	return p.primary()
}

func (p *sparqlParser) primary() (*QExpr, error) {
	t := p.peek()
	switch t.k {
	case sVar:
		p.next()
		p.noteVar(t.s)
		return &QExpr{Op: "var", Var: t.s}, nil
	case sIRI, sPName, sStr, sInt, sDec:
		var pre []QElem
		term, err := p.term(&pre)
		if err != nil {
			return nil, err
		}
		return &QExpr{Op: "const", Val: term.Val}, nil
	case sPunct:
		switch t.s {
		case "(":
			p.next()
			e, err := p.expr()
			if err != nil {
				return nil, err
			}
			if err := p.expectPunct(")"); err != nil {
				return nil, err
			}
			return e, nil
		case "-", "+":
			var pre []QElem
			term, err := p.term(&pre)
			if err != nil {
				return nil, err
			}
			return &QExpr{Op: "const", Val: term.Val}, nil
		}
	case sWord:
		return p.wordExpr()
	}
	return nil, p.errAt(t, "expected an expression, found %s", describeTok(t))
}

func (p *sparqlParser) wordExpr() (*QExpr, error) {
	t := p.next()
	name := strings.ToLower(t.s)
	if name == "true" || name == "false" {
		return &QExpr{Op: "const", Val: Bool(name == "true")}, nil
	}
	if !p.isPunct("(") {
		return nil, p.errAt(t, "unexpected %s in expression", describeTok(t))
	}
	p.next()
	switch name {
	case "bound":
		v := p.next()
		if v.k != sVar {
			return nil, p.errAt(v, "bound() takes a variable")
		}
		p.noteVar(v.s)
		if err := p.expectPunct(")"); err != nil {
			return nil, err
		}
		return &QExpr{Op: "bound", Args: []*QExpr{{Op: "var", Var: v.s}}}, nil
	case "regex":
		subj, err := p.expr()
		if err != nil {
			return nil, err
		}
		if err := p.expectPunct(","); err != nil {
			return nil, err
		}
		pat := p.next()
		if pat.k != sStr {
			return nil, p.unsupported(pat, "regex with a non-constant pattern")
		}
		flags := ""
		if p.acceptPunct(",") {
			f := p.next()
			if f.k != sStr {
				return nil, p.errAt(f, "regex flags must be a string")
			}
			flags = f.s
		}
		if err := p.expectPunct(")"); err != nil {
			return nil, err
		}
		re, err := compileRegex(pat.s, flags)
		if err != nil {
			return nil, p.errAt(pat, "bad regular expression: %v", err)
		}
		return &QExpr{Op: "regex", Args: []*QExpr{subj}, Re: re}, nil
	case "contains", "strstarts":
		a, err := p.expr()
		if err != nil {
			return nil, err
		}
		if err := p.expectPunct(","); err != nil {
			return nil, err
		}
		b, err := p.expr()
		if err != nil {
			return nil, err
		}
		if err := p.expectPunct(")"); err != nil {
			return nil, err
		}
		return &QExpr{Op: name, Args: []*QExpr{a, b}}, nil
	}
	return nil, p.unsupported(t, fmt.Sprintf("function %s", strings.ToUpper(t.s)))
}
