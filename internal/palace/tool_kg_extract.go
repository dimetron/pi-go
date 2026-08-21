package palace

import (
	"context"
	"fmt"
	"regexp"
	"sort"
	"strings"

	"google.golang.org/adk/v2/agent"
	"google.golang.org/adk/v2/tool"
	"google.golang.org/adk/v2/tool/functiontool"
)

// --- palace-kg-extract ---

// KGExtractToolInput defines parameters for the palace-kg-extract tool.
//
// The tool takes a chunk of text and (optionally) the source file it came
// from, and returns a deduplicated list of candidate (subject, predicate,
// object) triples. It does not write to the knowledge graph: the calling
// agent is expected to review the candidates and call palace-kg-add for the
// ones it wants to keep.
//
// The tool is heuristic: it does not call an LLM. That is deliberate — the
// extraction runs in the hot path of tool use and would otherwise add an
// extra model call per observation. The agent already has a model; it can
// reject bad candidates. Code-level signals (imports, declarations, file
// paths) are reliable enough to extract without a model; free-form text
// extraction is left to the agent.
type KGExtractToolInput struct {
	// Text is the chunk to extract from. Required.
	Text string `json:"text"`
	// SourceFile is an optional path to scope the "defined_in" and "part_of"
	// heuristics. When set, identifiers are anchored to this file.
	SourceFile string `json:"source_file,omitempty"`
	// MaxTriples caps the number of returned candidates. Default 20. The tool
	// returns at most this many triples, prioritized as documented below.
	MaxTriples int `json:"max_triples,omitempty"`
}

// ExtractedTriple is a candidate triple produced by the extractor. The agent
// reviews and may submit to palace-kg-add unchanged; field names match the
// KGAdd tool's input shape.
type ExtractedTriple struct {
	Subject   string `json:"subject"`
	Predicate string `json:"predicate"`
	Object    string `json:"object"`
	// Confidence is a rough score 0-1. Higher means more reliable (e.g. came
	// from a code construct rather than a guessed pair of identifiers).
	Confidence float64 `json:"confidence"`
	// Reason is a short human-readable note for why this triple was emitted.
	// Helps the agent decide whether to keep or reject the candidate.
	Reason string `json:"reason"`
}

// KGExtractToolOutput contains the candidate triples and a markdown summary
// the agent can scan quickly.
type KGExtractToolOutput struct {
	Content string            `json:"content"`
	Triples []ExtractedTriple `json:"triples"`
}

func newPalaceKGExtractTool(p *Palace) (tool.Tool, error) {
	return functiontool.New(functiontool.Config{
		Name: "palace-kg-extract",
		Description: "Extract candidate (subject, predicate, object) triples from a text chunk. " +
			"Returns heuristic candidates (no LLM call). The agent should review and " +
			"call palace-kg-add for the ones worth keeping. Code chunks (imports, " +
			"declarations, file paths) yield the most reliable candidates; free-form " +
			"prose yields few or none. Use this to bootstrap a knowledge graph from " +
			"drawer text, observation text, or any other source.",
	}, func(ctx agent.Context, input KGExtractToolInput) (KGExtractToolOutput, error) {
		return palaceKGExtractHandler(ctx, p, input)
	})
}

func palaceKGExtractHandler(_ context.Context, _ *Palace, input KGExtractToolInput) (KGExtractToolOutput, error) {
	if strings.TrimSpace(input.Text) == "" {
		return KGExtractToolOutput{Content: "Error: text is required"}, nil
	}

	maxTriples := input.MaxTriples
	if maxTriples <= 0 {
		maxTriples = 20
	}

	candidates := extractTriples(input.Text, input.SourceFile)
	// Stable sort: highest confidence first, then alphabetical within tier.
	sort.SliceStable(candidates, func(i, j int) bool {
		if candidates[i].Confidence != candidates[j].Confidence {
			return candidates[i].Confidence > candidates[j].Confidence
		}
		if candidates[i].Subject != candidates[j].Subject {
			return candidates[i].Subject < candidates[j].Subject
		}
		if candidates[i].Predicate != candidates[j].Predicate {
			return candidates[i].Predicate < candidates[j].Predicate
		}
		return candidates[i].Object < candidates[j].Object
	})
	if len(candidates) > maxTriples {
		candidates = candidates[:maxTriples]
	}

	content := formatExtractedTriples(candidates, input.SourceFile)
	return KGExtractToolOutput{Content: content, Triples: candidates}, nil
}

// emitImport produces the "imports" triple for one path, with the
// appropriate confidence based on whether the chunk has a source file.
// Stdlib paths are filtered out: they are noise in a project knowledge
// graph.
func emitImport(path, sourceFile string, ts *tripleSet) {
	if isStdlibImport(path) {
		return
	}
	if sourceFile != "" {
		ts.add(fileBase(sourceFile), "imports", path, "import statement, anchored to source file", 0.95)
		return
	}
	ts.add("codebase", "imports", path, "import statement (no source file anchor)", 0.50)
}

// --- heuristics ---

// Patterns used by the heuristic extractor. They are intentionally narrow:
// the goal is "few false positives," not "complete extraction." The agent
// does the rest.
var (
	// Matches a string literal in any of the common import styles:
	//   import "path"
	//   import alias "path"
	//   from "path"
	//   require("path")
	// The string is captured in group 1; the surrounding syntax is not
	// pinned to the same line, so it also matches parenthesized Go import
	// blocks where the keyword and the literal are on different lines.
	importPathRe = regexp.MustCompile(`(?:import\s+(?:\w+\s+)?|from\s+|require\(\s*)["']([^"']+)["']`)

	// Matches the opening of a Go parenthesized import block: `import (`
	// possibly preceded by whitespace. Used by the imports heuristic to
	// decide whether a string literal found later in the chunk counts as an
	// import path.
	importBlockOpenRe = regexp.MustCompile(`(?m)^\s*import\s*\(`)

	// Matches any quoted string literal. Used to find path candidates that
	// sit inside an import block whose opening keyword is on an earlier line.
	quotedStringRe = regexp.MustCompile(`["']([^"']+)["']`)

	// Matches Go-style function declarations: `func Name(`, `func (recv) Name(`.
	// Captures the name in group 1.
	goFuncDeclRe = regexp.MustCompile(`(?m)^func\s+(?:\([^)]*\)\s+)?([A-Z][A-Za-z0-9_]*)`)

	// Matches Go-style type declarations: `type Name struct`, `type Name interface`.
	goTypeDeclRe = regexp.MustCompile(`(?m)^type\s+([A-Z][A-Za-z0-9_]*)\s+(struct|interface)`)

	// Matches Python/JS-style function declarations: `def Name(`, `function Name(`,
	// `class Name`, `const Name =`, `let Name =`, `var Name =`. The trailing
	// boundary stops the identifier at the first non-identifier char.
	genericDeclRe = regexp.MustCompile(`(?m)^\s*(?:def|function|class|const|let|var)\s+([A-Za-z_][A-Za-z0-9_]*)`)

	// Matches file paths in code or prose: things like `internal/auth/handler.go`
	// or `cmd/server/main.go`. We keep it strict to avoid swallowing URLs or
	// commit SHAs.
	filePathRe = regexp.MustCompile(`(?:^|[\s"'(\[])([a-zA-Z0-9_./-]+/[a-zA-Z0-9_.-]+\.[a-z]{1,8})(?:\b|["')])`)
)

// declPattern is one declaration-matching heuristic: a regex whose first
// capture group is the declared name, the reason recorded on the emitted
// triple, and whether English noise words should be filtered out.
//
// Only the generic pattern sets skipNoise. Its keywords (`const`, `let`,
// `var`) routinely introduce identifiers like `err` or `result`, where a Go
// `func` or `type` declaration named that is rare enough not to be worth
// losing the real ones over.
type declPattern struct {
	re        *regexp.Regexp
	reason    string
	skipNoise bool
}

// declPatterns runs in order, and order is visible in the output: the first
// pattern to produce a given (subject, predicate, object) owns its reason.
var declPatterns = []declPattern{
	{re: goFuncDeclRe, reason: "Go func declaration in source file"},
	{re: goTypeDeclRe, reason: "Go type declaration in source file"},
	{re: genericDeclRe, reason: "declaration in source file", skipNoise: true},
}

// tripleSet accumulates candidate triples and enforces the rules every
// heuristic shares: a triple needs all three parts present, needs a subject
// that differs from its object, and must not already be in the set.
//
// Dedup is case-insensitive over the whole (subject, predicate, object) key,
// and first write wins — so when two heuristics find the same fact, the one
// that ran first owns the confidence and the reason.
type tripleSet struct {
	out  []ExtractedTriple
	seen map[string]bool
}

func newTripleSet() *tripleSet {
	return &tripleSet{seen: make(map[string]bool)}
}

func (ts *tripleSet) add(subj, pred, obj, reason string, conf float64) {
	s := strings.TrimSpace(subj)
	p := strings.TrimSpace(pred)
	o := strings.TrimSpace(obj)
	if s == "" || p == "" || o == "" || strings.EqualFold(s, o) {
		return
	}
	key := strings.ToLower(s) + "|" + strings.ToLower(p) + "|" + strings.ToLower(o)
	if ts.seen[key] {
		return
	}
	ts.seen[key] = true
	ts.out = append(ts.out, ExtractedTriple{
		Subject:    s,
		Predicate:  normalizePredicate(p),
		Object:     o,
		Confidence: conf,
		Reason:     reason,
	})
}

// extractTriples runs the heuristic pass over text and returns candidate
// triples. The result is unsorted; the caller sorts and truncates.
//
// Confidence scale (rough):
//
//	0.95 — directly parsed from a code construct (imports, declarations)
//	0.70 — anchored to a known file (defined_in, part_of with source_file)
//	0.50 — heuristic pair from free-form identifiers
//	0.30 — bare identifier pair with no anchor
func extractTriples(text, sourceFile string) []ExtractedTriple {
	ts := newTripleSet()
	extractImportTriples(text, sourceFile, ts)
	extractDeclTriples(text, sourceFile, ts)
	extractSiblingPathTriples(text, sourceFile, ts)
	return ts.out
}

// extractImportTriples emits one "imports" triple per non-stdlib import path
// found in text.
//
// Two passes. The first catches imports written on a single line —
// `import "path"`, `from "path"`, `require("path")`, with the keyword and the
// literal on the same line. The second catches Go's parenthesized form:
// `import ( ... "path" ... )` where the keyword is on one line and the literal
// is on a later line. The single-line regex cannot match that form because the
// quote is not on the same line as the `import` keyword, and tightening the
// alternation to allow a `( )` block just shifts the problem: the `import (`
// would then have to be followed by a quote on the same line, which it never
// is.
//
// A path seen in the first pass is skipped in the second, so a package
// imported both ways keeps the reason from the single-line pass.
func extractImportTriples(text, sourceFile string, ts *tripleSet) {
	seenPath := make(map[string]bool)
	for _, m := range importPathRe.FindAllStringSubmatch(text, -1) {
		seenPath[m[1]] = true
		emitImport(m[1], sourceFile, ts)
	}
	for _, block := range goImportBlocks(text) {
		for _, m := range quotedStringRe.FindAllStringSubmatch(block, -1) {
			if seenPath[m[1]] {
				continue
			}
			seenPath[m[1]] = true
			emitImport(m[1], sourceFile, ts)
		}
	}
}

// goImportBlocks returns the body of every Go parenthesized import block in
// text: the span between the `import (` opener and the matching `)`, with
// neither delimiter included.
//
// The closer is found by line rather than by counting parens — Go does not
// allow nested parens in an import block, so the first `)` at the start of a
// line reliably ends it. A block with no closer, which is what a truncated
// chunk looks like, runs to the end of the text rather than being discarded.
func goImportBlocks(text string) []string {
	var blocks []string
	for _, open := range importBlockOpenRe.FindAllStringIndex(text, -1) {
		body := text[open[1]:] // just after the `(`
		if end := strings.Index(body, "\n)"); end >= 0 {
			body = body[:end]
		}
		blocks = append(blocks, body)
	}
	return blocks
}

// extractDeclTriples emits a "defined_in" triple per declaration matched by
// declPatterns. Every such triple points at sourceFile, so an unanchored chunk
// yields none at all and the whole pass is skipped.
func extractDeclTriples(text, sourceFile string, ts *tripleSet) {
	if sourceFile == "" {
		return
	}
	for _, pat := range declPatterns {
		for _, m := range pat.re.FindAllStringSubmatch(text, -1) {
			if pat.skipNoise && isCommonWord(m[1]) {
				continue
			}
			ts.add(m[1], "defined_in", sourceFile, pat.reason, 0.95)
		}
	}
}

// extractSiblingPathTriples emits a "part_of" triple for every path mentioned
// in text that lives in the same directory as sourceFile. The source file's
// own path is not part of itself, and without a source file there is nothing
// to be part of.
func extractSiblingPathTriples(text, sourceFile string, ts *tripleSet) {
	if sourceFile == "" {
		return
	}
	srcDir := pathDir(sourceFile)
	for _, m := range filePathRe.FindAllStringSubmatch(text, -1) {
		if m[1] == sourceFile || pathDir(m[1]) != srcDir {
			continue
		}
		ts.add(m[1], "part_of", fileBase(sourceFile), "shares directory with source file", 0.70)
	}
}

// formatExtractedTriples renders the candidates as a markdown block the agent
// can read and the user can paste. The structured Triples field is for code
// that wants to act on the candidates programmatically.
func formatExtractedTriples(candidates []ExtractedTriple, sourceFile string) string {
	if len(candidates) == 0 {
		header := "No candidate triples extracted."
		if sourceFile != "" {
			header = fmt.Sprintf("No candidate triples extracted from %s.", sourceFile)
		}
		return header + " " +
			"Free-form prose rarely yields reliable triples; consider whether " +
			"palace-kg-add would be more direct, or look for code constructs in the source."
	}

	var sb strings.Builder
	if sourceFile != "" {
		fmt.Fprintf(&sb, "## Candidate triples from %s (%d)\n\n", sourceFile, len(candidates))
	} else {
		fmt.Fprintf(&sb, "## Candidate triples (%d)\n\n", len(candidates))
	}
	sb.WriteString("| Subject | Predicate | Object | Confidence | Reason |\n")
	sb.WriteString("|---------|-----------|--------|------------|--------|\n")
	for _, t := range candidates {
		fmt.Fprintf(&sb, "| %s | %s | %s | %.2f | %s |\n",
			t.Subject, t.Predicate, t.Object, t.Confidence, t.Reason)
	}
	sb.WriteString("\nReview and call palace-kg-add for the ones you want to keep. " +
		"Duplicates are no-ops; re-running extract is safe.\n")
	return sb.String()
}

// normalizePredicate lower-cases and converts spaces to underscores, so the
// extracted predicate matches KGAdd's normalization (entityID in kg.go).
func normalizePredicate(p string) string {
	p = strings.TrimSpace(p)
	p = strings.ToLower(p)
	p = strings.ReplaceAll(p, " ", "_")
	p = strings.ReplaceAll(p, "-", "_")
	return p
}

// isStdlibImport returns true for Go standard library imports and relative
// paths. We only emit "imports" triples for third-party paths because stdlib
// is noise.
func isStdlibImport(path string) bool {
	if path == "" {
		return true
	}
	// Relative imports: "./foo", "../foo".
	if strings.HasPrefix(path, ".") {
		return true
	}
	// Go stdlib has no "." in the first path segment.
	first := path
	if i := strings.Index(path, "/"); i >= 0 {
		first = path[:i]
	}
	return !strings.Contains(first, ".")
}

// fileBase returns the basename of a file path, or the path itself if there
// is no slash. Used to anchor "imports"/"part_of" triples to a stable entity.
func fileBase(path string) string {
	if i := strings.LastIndex(path, "/"); i >= 0 {
		return path[i+1:]
	}
	return path
}

// pathDir returns the directory of a path, or "" if none.
func pathDir(path string) string {
	if i := strings.LastIndex(path, "/"); i >= 0 {
		return path[:i]
	}
	return ""
}

// isCommonWord filters English noise words that look like identifiers but
// are almost never worth a triple. Conservative: only the most common.
func isCommonWord(s string) bool {
	switch strings.ToLower(s) {
	case "this", "that", "true", "false", "nil", "null", "none", "default",
		"err", "ok", "ctx", "args", "result", "input", "output", "value",
		"self", "other", "new", "old", "tmp", "temp":
		return true
	}
	return false
}
