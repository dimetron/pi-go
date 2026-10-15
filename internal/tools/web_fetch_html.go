package tools

import (
	"net/url"
	"strconv"
	"strings"
	"unicode"

	"golang.org/x/net/html"
)

// htmlToMarkdown converts an HTML document into a compact markdown
// approximation the model can read: headings, paragraphs, links, lists,
// tables, emphasis, and code. script, style, and similar non-content nodes
// are dropped. base resolves relative links; it may be nil.
func htmlToMarkdown(src string, base *url.URL) string {
	doc, err := html.Parse(strings.NewReader(src))
	if err != nil || doc == nil {
		return strings.TrimSpace(src)
	}
	var r mdRenderer
	r.base = base
	r.walk(doc)
	return collapseBlankLines(r.b.String())
}

// skipTags are elements whose text is not page content.
var skipTags = map[string]bool{
	"script":   true,
	"style":    true,
	"noscript": true,
	"svg":      true,
	"iframe":   true,
	"canvas":   true,
	"template": true,
	"head":     true,
	"object":   true,
	"embed":    true,
	"link":     true,
	"meta":     true,
}

// blockTags introduce a paragraph break. Headings, lists, and tables have
// their own rendering and are not listed here.
var blockTags = map[string]bool{
	"div":        true,
	"section":    true,
	"article":    true,
	"main":       true,
	"header":     true,
	"footer":     true,
	"nav":        true,
	"aside":      true,
	"figure":     true,
	"figcaption": true,
	"address":    true,
	"hgroup":     true,
	"center":     true,
}

type listFrame struct {
	ordered bool
	index   int
}

// mdRenderer walks an HTML tree and writes markdown.
//
// wantSpace collapses a run of HTML whitespace into one space, emitted before
// the next word. nl counts trailing newlines so a block break is exactly one
// blank line. atItemStart is set just after a list marker, so the item's first
// block stays on that line instead of opening a blank one.
type mdRenderer struct {
	b           strings.Builder
	base        *url.URL
	list        []listFrame
	pre         bool
	wantSpace   bool
	atItemStart bool
	nl          int
}

func (r *mdRenderer) nested() mdRenderer {
	return mdRenderer{base: r.base, pre: r.pre}
}

func (r *mdRenderer) walk(n *html.Node) {
	if n == nil {
		return
	}
	switch n.Type {
	case html.TextNode:
		r.text(n.Data)
	case html.ElementNode:
		r.element(n)
	default:
		r.children(n)
	}
}

func (r *mdRenderer) children(n *html.Node) {
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		r.walk(c)
	}
}

func (r *mdRenderer) element(n *html.Node) {
	tag := strings.ToLower(n.Data)
	if skipTags[tag] {
		return
	}
	switch tag {
	case "br":
		r.breakLine()
	case "hr":
		r.ensureBlank()
		r.write("---")
		r.ensureBlank()
	case "h1", "h2", "h3", "h4", "h5", "h6":
		r.heading(int(tag[1]-'0'), n)
	case "p":
		r.paragraph(n)
	case "pre":
		r.preformatted(n)
	case "code":
		r.code(n)
	case "a":
		r.link(n)
	case "strong", "b":
		r.wrap("**", n)
	case "em", "i":
		r.wrap("*", n)
	case "ul":
		r.openList(false, n)
	case "ol":
		r.openList(true, n)
	case "li":
		r.listItem(n)
	case "blockquote":
		r.blockquote(n)
	case "table":
		r.table(n)
	case "img":
		r.image(n)
	default:
		if blockTags[tag] {
			r.block(n)
			return
		}
		r.children(n)
	}
}

func (r *mdRenderer) block(n *html.Node) {
	if len(r.list) > 0 {
		if r.atItemStart {
			r.atItemStart = false
		} else {
			r.breakLine()
		}
		r.children(n)
		return
	}
	r.ensureBlank()
	r.children(n)
	r.ensureBlank()
}

func (r *mdRenderer) paragraph(n *html.Node) {
	if r.atItemStart {
		r.atItemStart = false
		r.children(n)
		return
	}
	if len(r.list) > 0 {
		r.breakLine()
		r.children(n)
		return
	}
	r.ensureBlank()
	r.children(n)
	r.ensureBlank()
}

func (r *mdRenderer) heading(level int, n *html.Node) {
	if level < 1 {
		level = 1
	}
	if level > 6 {
		level = 6
	}
	inner := r.nested()
	inner.children(n)
	text := collapseSpaces(inner.b.String())
	if text == "" {
		return
	}
	r.ensureBlank()
	r.write(strings.Repeat("#", level) + " " + text)
	r.ensureBlank()
}

func (r *mdRenderer) preformatted(n *html.Node) {
	inner := r.nested()
	inner.pre = true
	inner.children(n)
	code := strings.Trim(inner.b.String(), "\n")
	if strings.TrimSpace(code) == "" {
		return
	}
	r.ensureBlank()
	if lang := preLang(n); lang != "" {
		r.write("```" + lang + "\n")
	} else {
		r.write("```\n")
	}
	r.write(code)
	r.write("\n```")
	r.ensureBlank()
}

func (r *mdRenderer) code(n *html.Node) {
	if r.pre {
		r.children(n)
		return
	}
	inner := r.nested()
	inner.pre = true
	inner.children(n)
	text := strings.TrimSpace(inner.b.String())
	text = strings.ReplaceAll(text, "\n", " ")
	text = strings.ReplaceAll(text, "`", "'")
	if text == "" {
		return
	}
	r.write("`" + text + "`")
}

func (r *mdRenderer) link(n *html.Node) {
	inner := r.nested()
	inner.children(n)
	label := collapseSpaces(inner.b.String())
	if label == "" {
		return
	}
	href := resolveHref(r.base, strings.TrimSpace(nodeAttr(n, "href")))
	if href == "" {
		r.write(label)
		return
	}
	r.write("[" + label + "](" + href + ")")
}

func (r *mdRenderer) wrap(mark string, n *html.Node) {
	inner := r.nested()
	inner.children(n)
	text := collapseSpaces(inner.b.String())
	if text == "" {
		return
	}
	r.write(mark + text + mark)
}

func (r *mdRenderer) openList(ordered bool, n *html.Node) {
	nested := len(r.list) > 0
	if nested {
		r.breakLine()
	} else {
		r.ensureBlank()
	}
	frame := listFrame{ordered: ordered, index: 1}
	if ordered {
		if start := strings.TrimSpace(nodeAttr(n, "start")); start != "" {
			if i, err := strconv.Atoi(start); err == nil && i > 0 {
				frame.index = i
			}
		}
	}
	r.list = append(r.list, frame)
	r.children(n)
	r.list = r.list[:len(r.list)-1]
	if nested {
		r.breakLine()
	} else {
		r.ensureBlank()
	}
}

func (r *mdRenderer) listItem(n *html.Node) {
	r.breakLine()
	depth := 0
	marker := "- "
	if len(r.list) > 0 {
		depth = len(r.list) - 1
		top := &r.list[len(r.list)-1]
		if top.ordered {
			marker = strconv.Itoa(top.index) + ". "
			top.index++
		}
	}
	r.write(strings.Repeat("  ", depth) + marker)
	r.wantSpace = false
	r.atItemStart = true
	r.children(n)
	r.atItemStart = false
}

func (r *mdRenderer) blockquote(n *html.Node) {
	inner := r.nested()
	inner.children(n)
	text := strings.TrimSpace(inner.b.String())
	if text == "" {
		return
	}
	r.ensureBlank()
	for _, line := range strings.Split(text, "\n") {
		if line == "" {
			r.write(">\n")
			continue
		}
		r.write("> " + line + "\n")
	}
}

func (r *mdRenderer) table(n *html.Node) {
	rows := tableRows(n, r.base)
	if len(rows) == 0 {
		return
	}
	width := 0
	for _, row := range rows {
		if len(row) > width {
			width = len(row)
		}
	}
	if width == 0 {
		return
	}
	r.ensureBlank()
	for i, row := range rows {
		for len(row) < width {
			row = append(row, "")
		}
		r.write("| " + strings.Join(row, " | ") + " |\n")
		if i == 0 {
			sep := make([]string, width)
			for j := range sep {
				sep[j] = "---"
			}
			r.write("| " + strings.Join(sep, " | ") + " |\n")
		}
	}
}

func (r *mdRenderer) image(n *html.Node) {
	alt := collapseSpaces(nodeAttr(n, "alt"))
	if alt == "" {
		return
	}
	r.write(alt)
}

func (r *mdRenderer) text(s string) {
	if r.pre {
		s = strings.ReplaceAll(s, "\r\n", "\n")
		s = strings.ReplaceAll(s, "\r", "\n")
		r.write(s)
		return
	}
	if r.atItemStart && strings.TrimSpace(s) == "" {
		return
	}
	for _, rn := range s {
		if unicode.IsSpace(rn) {
			r.wantSpace = true
			continue
		}
		r.atItemStart = false
		if r.wantSpace && r.b.Len() > 0 && r.nl == 0 {
			r.b.WriteByte(' ')
		}
		r.wantSpace = false
		r.b.WriteRune(rn)
		r.nl = 0
	}
}

func (r *mdRenderer) write(s string) {
	if s == "" {
		return
	}
	if r.wantSpace && r.b.Len() > 0 && r.nl == 0 && s[0] != '\n' {
		r.b.WriteByte(' ')
	}
	r.wantSpace = false
	if s[0] != '\n' {
		r.atItemStart = false
	}
	r.b.WriteString(s)
	r.note(s)
}

func (r *mdRenderer) note(s string) {
	n := 0
	for i := len(s) - 1; i >= 0 && s[i] == '\n'; i-- {
		n++
	}
	if n == 0 {
		r.nl = 0
		return
	}
	if n == len(s) {
		r.nl += n
		return
	}
	r.nl = n
}

func (r *mdRenderer) breakLine() {
	r.wantSpace = false
	if r.b.Len() == 0 || r.nl > 0 {
		return
	}
	r.b.WriteByte('\n')
	r.nl++
}

func (r *mdRenderer) ensureBlank() {
	r.wantSpace = false
	r.atItemStart = false
	if r.b.Len() == 0 {
		return
	}
	for r.nl < 2 {
		r.b.WriteByte('\n')
		r.nl++
	}
}

func tableRows(n *html.Node, base *url.URL) [][]string {
	var rows [][]string
	var walk func(*html.Node)
	walk = func(node *html.Node) {
		for c := node.FirstChild; c != nil; c = c.NextSibling {
			if c.Type != html.ElementNode {
				continue
			}
			if strings.EqualFold(c.Data, "tr") {
				if cells := rowCells(c, base); len(cells) > 0 {
					rows = append(rows, cells)
				}
				continue
			}
			walk(c)
		}
	}
	walk(n)
	return rows
}

func rowCells(tr *html.Node, base *url.URL) []string {
	var cells []string
	for c := tr.FirstChild; c != nil; c = c.NextSibling {
		if c.Type != html.ElementNode {
			continue
		}
		tag := strings.ToLower(c.Data)
		if tag != "td" && tag != "th" {
			continue
		}
		cell := mdRenderer{base: base}
		cell.children(c)
		text := collapseSpaces(cell.b.String())
		text = strings.ReplaceAll(text, "|", `\|`)
		cells = append(cells, text)
	}
	return cells
}

func preLang(n *html.Node) string {
	if lang := codeLang(n); lang != "" {
		return lang
	}
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		if c.Type == html.ElementNode && strings.EqualFold(c.Data, "code") {
			return codeLang(c)
		}
	}
	return ""
}

func codeLang(n *html.Node) string {
	for _, field := range strings.Fields(nodeAttr(n, "class")) {
		for _, prefix := range []string{"language-", "lang-"} {
			if strings.HasPrefix(field, prefix) {
				return safeLang(field[len(prefix):])
			}
		}
	}
	return ""
}

func safeLang(s string) string {
	if s == "" || len(s) > 20 {
		return ""
	}
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
		case r == '_' || r == '+' || r == '#' || r == '-' || r == '.':
		default:
			return ""
		}
	}
	return s
}

func resolveHref(base *url.URL, href string) string {
	if href == "" || strings.HasPrefix(href, "#") {
		return ""
	}
	u, err := url.Parse(href)
	if err != nil {
		return ""
	}
	scheme := strings.ToLower(u.Scheme)
	if scheme == "javascript" || scheme == "data" {
		return ""
	}
	if base != nil {
		u = base.ResolveReference(u)
		scheme = strings.ToLower(u.Scheme)
	}
	switch scheme {
	case "", "http", "https", "mailto":
		return u.String()
	default:
		return ""
	}
}

func nodeAttr(n *html.Node, key string) string {
	for _, a := range n.Attr {
		if strings.EqualFold(a.Key, key) {
			return a.Val
		}
	}
	return ""
}

func collapseSpaces(s string) string {
	return strings.Join(strings.Fields(s), " ")
}

func collapseBlankLines(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	nl := 0
	for _, r := range s {
		if r == '\n' {
			nl++
			if nl <= 2 {
				b.WriteRune(r)
			}
			continue
		}
		nl = 0
		b.WriteRune(r)
	}
	return strings.TrimSpace(b.String())
}
