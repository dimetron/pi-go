package diagram

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strconv"
	"strings"

	"github.com/dimetron/pi-go/internal/mermaid/renderer"
)

// ── model ────────────────────────────────────────────────────────

type gitCommit struct {
	id      string
	branch  string
	ctype   string // "NORMAL", "REVERSE", "HIGHLIGHT"
	tag     string
	parents []string
	seq     int
}

type gitBranch struct {
	name        string
	order       int
	startCommit string
}

type gitGraph struct {
	commits           []gitCommit
	branches          []gitBranch
	direction         string // "LR", "TB", "BT"
	directionExplicit bool   // true if direction was set in source
	mainBranchName    string // default "main"
	warnings          []string
}

// ── layout constants ─────────────────────────────────────────────

const (
	gitMinCommitGap = 6
	gitBranchGap    = 2
	gitMargin       = 2
	gitLabelPad     = 2
)

// ── commit markers ───────────────────────────────────────────────

func gitGetMarker(commitType string, useASCII bool) rune {
	if useASCII {
		switch commitType {
		case "REVERSE":
			return 'X'
		case "HIGHLIGHT":
			return '#'
		default:
			return 'o'
		}
	}
	switch commitType {
	case "REVERSE":
		return '\u2716' // ✖
	case "HIGHLIGHT":
		return '\u25A0' // ■
	default:
		return '\u25CF' // ●
	}
}

// ── parser ───────────────────────────────────────────────────────

// RenderGitGraph parses and renders a Mermaid git graph.
func RenderGitGraph(source string, useASCII bool) *renderer.Canvas {
	gg := parseGitGraph(source)
	return renderGitGraph(gg, useASCII)
}

type gitGraphParser struct {
	diagram       *gitGraph
	currentBranch string
	branchHeads   map[string]string
	autoID        int
	commitMap     map[string]*gitCommit
	branchSet     map[string]bool
	seq           int
}

func newGitGraphParser() *gitGraphParser {
	return &gitGraphParser{
		diagram: &gitGraph{
			direction:      "LR",
			mainBranchName: "main",
		},
		branchHeads: make(map[string]string),
		commitMap:   make(map[string]*gitCommit),
		branchSet:   make(map[string]bool),
	}
}

func parseGitGraph(source string) *gitGraph {
	p := newGitGraphParser()
	lines := gitPreprocess(source)
	if len(lines) == 0 {
		return p.diagram
	}

	// Handle %%{init}%% directives before the header
	var remaining []string
	for _, line := range lines {
		if strings.HasPrefix(line, "%%{init") {
			p.parseInit(line)
		} else {
			remaining = append(remaining, line)
		}
	}

	if len(remaining) == 0 {
		return p.diagram
	}

	// Parse header
	header := remaining[0]
	if strings.HasPrefix(header, "gitGraph") {
		rest := strings.TrimSpace(header[len("gitGraph"):])
		reDir := regexp.MustCompile(`(?i)(LR|TB|BT)\s*:?`)
		if m := reDir.FindStringSubmatch(rest); m != nil {
			p.diagram.direction = strings.ToUpper(m[1])
			p.diagram.directionExplicit = true
		}
		remaining = remaining[1:]
	}

	// Update current_branch from config
	p.currentBranch = p.diagram.mainBranchName

	// Ensure main branch exists
	p.ensureBranch(p.currentBranch, -1, "")

	for _, line := range remaining {
		p.parseLine(line)
	}

	return p.diagram
}

func gitPreprocess(text string) []string {
	var result []string
	for _, line := range strings.Split(text, "\n") {
		stripped := strings.TrimSpace(line)
		if strings.HasPrefix(stripped, "%%") && !strings.HasPrefix(stripped, "%%{") {
			continue
		}
		if stripped != "" {
			result = append(result, stripped)
		}
	}
	return result
}

func (p *gitGraphParser) parseInit(line string) {
	re := regexp.MustCompile(`%%\{init:\s*(\{.*\})\s*\}%%`)
	m := re.FindStringSubmatch(line)
	if m == nil {
		return
	}
	raw := m[1]
	var config map[string]interface{}
	if err := json.Unmarshal([]byte(raw), &config); err != nil {
		// Try replacing single quotes
		fixed := strings.ReplaceAll(raw, "'", "\"")
		if err := json.Unmarshal([]byte(fixed), &config); err != nil {
			return
		}
	}

	gitConfig, ok := config["gitGraph"]
	if !ok {
		gitConfig = config
	}
	gitMap, ok := gitConfig.(map[string]interface{})
	if !ok {
		return
	}
	if name, ok := gitMap["mainBranchName"]; ok {
		if s, ok := name.(string); ok {
			p.diagram.mainBranchName = s
		}
	}
}

func (p *gitGraphParser) parseLine(line string) {
	if strings.HasPrefix(line, "commit") {
		p.parseCommit(line)
		return
	}
	if strings.HasPrefix(line, "branch ") {
		p.parseBranch(line)
		return
	}
	if strings.HasPrefix(line, "checkout ") || strings.HasPrefix(line, "switch ") {
		parts := strings.SplitN(line, " ", 2)
		if len(parts) >= 2 {
			branchName := strings.Trim(strings.TrimSpace(parts[1]), "\"")
			if p.branchSet[branchName] {
				p.currentBranch = branchName
			} else {
				p.diagram.warnings = append(p.diagram.warnings,
					fmt.Sprintf("Checkout non-existent branch: %q", branchName))
			}
		}
		return
	}
	if strings.HasPrefix(line, "merge ") {
		p.parseMerge(line)
		return
	}
	if strings.HasPrefix(line, "cherry-pick") {
		p.parseCherryPick(line)
		return
	}
	if strings.HasPrefix(line, "reset ") {
		p.parseReset(line)
		return
	}
	p.diagram.warnings = append(p.diagram.warnings,
		fmt.Sprintf("Unrecognized line: %q", line))
}

var (
	reGitID   = regexp.MustCompile(`id:\s*"([^"]*)"`)
	reGitType = regexp.MustCompile(`type:\s*(NORMAL|REVERSE|HIGHLIGHT)`)
	reGitTag  = regexp.MustCompile(`tag:\s*"([^"]*)"`)
)

func (p *gitGraphParser) parseCommit(line string) {
	var commitID string
	commitType := "NORMAL"
	tag := ""

	if m := reGitID.FindStringSubmatch(line); m != nil {
		commitID = m[1]
	}
	if m := reGitType.FindStringSubmatch(line); m != nil {
		commitType = m[1]
	}
	if m := reGitTag.FindStringSubmatch(line); m != nil {
		tag = m[1]
	}

	if commitID == "" {
		commitID = strconv.Itoa(p.autoID)
		p.autoID++
	}

	var parents []string
	if head, ok := p.branchHeads[p.currentBranch]; ok {
		parents = append(parents, head)
	}

	commit := gitCommit{
		id:      commitID,
		branch:  p.currentBranch,
		ctype:   commitType,
		tag:     tag,
		parents: parents,
		seq:     p.seq,
	}
	p.seq++
	p.diagram.commits = append(p.diagram.commits, commit)
	p.commitMap[commitID] = &p.diagram.commits[len(p.diagram.commits)-1]
	p.branchHeads[p.currentBranch] = commitID
}

func (p *gitGraphParser) parseBranch(line string) {
	re := regexp.MustCompile(`branch\s+"?([^"]+?)"?\s*(?:order:\s*(\d+))?\s*$`)
	m := re.FindStringSubmatch(line)
	if m == nil {
		return
	}
	name := strings.TrimSpace(m[1])
	order := -1
	if m[2] != "" {
		order, _ = strconv.Atoi(m[2])
	}

	startCommit := p.branchHeads[p.currentBranch]
	p.ensureBranch(name, order, startCommit)

	if head, ok := p.branchHeads[p.currentBranch]; ok {
		p.branchHeads[name] = head
	}

	p.currentBranch = name
}

func (p *gitGraphParser) parseMerge(line string) {
	re := regexp.MustCompile(`merge\s+"?([^"]+?)"?(?:\s|$)`)
	m := re.FindStringSubmatch(line)
	if m == nil {
		return
	}
	mergedBranch := strings.TrimSpace(m[1])

	var commitID string
	commitType := "NORMAL"
	tag := ""

	if m := reGitID.FindStringSubmatch(line); m != nil {
		commitID = m[1]
	}
	if m := reGitType.FindStringSubmatch(line); m != nil {
		commitType = m[1]
	}
	if m := reGitTag.FindStringSubmatch(line); m != nil {
		tag = m[1]
	}

	if commitID == "" {
		commitID = strconv.Itoa(p.autoID)
		p.autoID++
	}

	var parents []string
	if head, ok := p.branchHeads[p.currentBranch]; ok {
		parents = append(parents, head)
	}
	if head, ok := p.branchHeads[mergedBranch]; ok {
		parents = append(parents, head)
	}

	commit := gitCommit{
		id:      commitID,
		branch:  p.currentBranch,
		ctype:   commitType,
		tag:     tag,
		parents: parents,
		seq:     p.seq,
	}
	p.seq++
	p.diagram.commits = append(p.diagram.commits, commit)
	p.commitMap[commitID] = &p.diagram.commits[len(p.diagram.commits)-1]
	p.branchHeads[p.currentBranch] = commitID
}

func (p *gitGraphParser) parseCherryPick(line string) {
	m := reGitID.FindStringSubmatch(line)
	if m == nil {
		return
	}
	sourceID := m[1]

	if _, ok := p.commitMap[sourceID]; !ok {
		p.diagram.warnings = append(p.diagram.warnings,
			fmt.Sprintf("Cherry-pick non-existent commit: %q", sourceID))
		return
	}

	commitID := sourceID + "-cherry"
	for p.commitMap[commitID] != nil {
		commitID += "-cp"
	}

	tag := ""
	if m := reGitTag.FindStringSubmatch(line); m != nil {
		tag = m[1]
	}

	var parents []string
	if head, ok := p.branchHeads[p.currentBranch]; ok {
		parents = append(parents, head)
	}
	parents = append(parents, sourceID)

	commit := gitCommit{
		id:      commitID,
		branch:  p.currentBranch,
		ctype:   "NORMAL",
		tag:     tag,
		parents: parents,
		seq:     p.seq,
	}
	p.seq++
	p.diagram.commits = append(p.diagram.commits, commit)
	p.commitMap[commitID] = &p.diagram.commits[len(p.diagram.commits)-1]
	p.branchHeads[p.currentBranch] = commitID
}

func (p *gitGraphParser) parseReset(line string) {
	re := regexp.MustCompile(`reset\s+"?([^"~]+?)"?\s*(?:~(\d+))?\s*$`)
	m := re.FindStringSubmatch(line)
	if m == nil {
		return
	}
	ref := strings.TrimSpace(m[1])
	ancestor := 0
	if m[2] != "" {
		ancestor, _ = strconv.Atoi(m[2])
	}

	var commitID string
	if head, ok := p.branchHeads[ref]; ok {
		commitID = head
	} else if _, ok := p.commitMap[ref]; ok {
		commitID = ref
	} else {
		p.diagram.warnings = append(p.diagram.warnings,
			fmt.Sprintf("Reset to unknown ref: %q", ref))
		return
	}

	for i := 0; i < ancestor; i++ {
		c := p.commitMap[commitID]
		if c != nil && len(c.parents) > 0 {
			commitID = c.parents[0]
		} else {
			p.diagram.warnings = append(p.diagram.warnings,
				fmt.Sprintf("Cannot walk back %d ancestors from %q", ancestor, ref))
			return
		}
	}

	p.branchHeads[p.currentBranch] = commitID
}

func (p *gitGraphParser) ensureBranch(name string, order int, startCommit string) {
	if !p.branchSet[name] {
		p.branchSet[name] = true
		p.diagram.branches = append(p.diagram.branches, gitBranch{
			name:        name,
			order:       order,
			startCommit: startCommit,
		})
	}
}

// ── renderer ─────────────────────────────────────────────────────

func renderGitGraph(gg *gitGraph, useASCII bool) *renderer.Canvas {
	cs := renderer.UNICODE
	if useASCII {
		cs = renderer.ASCII
	}

	if len(gg.commits) == 0 {
		return renderer.NewCanvas(1, 1)
	}

	if gg.direction == "TB" || gg.direction == "BT" {
		c := renderer.NewCanvas(1, 1)
		gitDrawTB(gg, c, useASCII, cs, gg.direction == "BT")
		return c
	}

	// LR (default) — auto-flip to TB if it would overflow
	commitCol, branchRow, sortedBranches, width, height, leftOffset := gitComputeLayoutLR(gg, useASCII)
	if width > usableWidth() && !gg.directionExplicit {
		c := renderer.NewCanvas(1, 1)
		gitDrawTB(gg, c, useASCII, cs, false)
		return c
	}
	c := renderer.NewCanvas(width, height)
	gitDrawLR(gg, c, commitCol, branchRow, sortedBranches, leftOffset, cs, useASCII)
	return c
}

func gitSortBranches(gg *gitGraph) []string {
	type sortEntry struct {
		key  int
		idx  int
		name string
	}
	var ordered []sortEntry
	for i, b := range gg.branches {
		key := 1000 + i
		if b.name == gg.mainBranchName {
			key = -2
		} else if b.order >= 0 {
			key = b.order
		}
		ordered = append(ordered, sortEntry{key, i, b.name})
	}
	// Stable sort by key then idx
	for i := 1; i < len(ordered); i++ {
		for j := i; j > 0; j-- {
			if ordered[j].key < ordered[j-1].key || (ordered[j].key == ordered[j-1].key && ordered[j].idx < ordered[j-1].idx) {
				ordered[j], ordered[j-1] = ordered[j-1], ordered[j]
			} else {
				break
			}
		}
	}
	result := make([]string, len(ordered))
	for i, e := range ordered {
		result[i] = e.name
	}
	return result
}

func gitCommitFootprint(c *gitCommit) int {
	w := len(c.id)
	if c.tag != "" {
		tagW := len(c.tag) + 2 // "[tag]"
		if tagW > w {
			w = tagW
		}
	}
	return (w + 1) / 2
}

func gitComputeLayoutLR(gg *gitGraph, useASCII bool) (map[string]int, map[string]int, []string, int, int, int) {
	sortedBranches := gitSortBranches(gg)
	branchRow := make(map[string]int)
	rowHeight := gitBranchGap + 1
	for i, name := range sortedBranches {
		branchRow[name] = gitMargin + i*rowHeight
	}

	branchLabelWidth := 0
	for _, name := range sortedBranches {
		if len(name) > branchLabelWidth {
			branchLabelWidth = len(name)
		}
	}
	leftOffset := gitMargin + branchLabelWidth + 2

	commitCol := make(map[string]int)
	commits := gg.commits

	if len(commits) > 0 {
		fp0 := gitCommitFootprint(&commits[0])
		commitCol[commits[0].id] = leftOffset + fp0

		for i := 1; i < len(commits); i++ {
			prev := &commits[i-1]
			curr := &commits[i]
			prevFP := gitCommitFootprint(prev)
			currFP := gitCommitFootprint(curr)
			labelGap := prevFP + gitLabelPad + currFP
			gap := max(gitMinCommitGap, labelGap)
			commitCol[curr.id] = commitCol[prev.id] + gap
		}
	}

	lastCol := leftOffset
	for _, col := range commitCol {
		if col > lastCol {
			lastCol = col
		}
	}
	lastFP := 0
	if len(commits) > 0 {
		lastFP = gitCommitFootprint(&commits[len(commits)-1])
	}
	canvasWidth := lastCol + lastFP + gitMargin + 1
	canvasHeight := gitMargin + len(sortedBranches)*rowHeight + gitMargin

	return commitCol, branchRow, sortedBranches, canvasWidth, canvasHeight, leftOffset
}

func gitComputeBranchExtentsLR(gg *gitGraph, branchCommits map[string][]*gitCommit, commitCol map[string]int, commitMap map[string]*gitCommit, mainBranch string, lineStartCol int) map[string][2]int {
	extents := make(map[string][2]int)

	for name, commits := range branchCommits {
		if len(commits) == 0 {
			continue
		}
		firstCol := commitCol[commits[0].id]
		lastCol := commitCol[commits[len(commits)-1].id]

		startCol := firstCol
		if name == mainBranch {
			startCol = lineStartCol
		}
		endCol := lastCol + 1
		extents[name] = [2]int{startCol, endCol}
	}

	// Extend branches to cover merge/fork points
	for _, c := range gg.commits {
		for _, parentID := range c.parents {
			parent, ok := commitMap[parentID]
			if !ok {
				continue
			}
			if parent.branch != c.branch {
				if ext, ok := extents[parent.branch]; ok {
					mergeCol := commitCol[c.id]
					if mergeCol > ext[1] {
						ext[1] = mergeCol
					}
					extents[parent.branch] = ext
				}
			}
		}
	}

	return extents
}

func gitDrawLR(gg *gitGraph, c *renderer.Canvas, commitCol, branchRow map[string]int, sortedBranches []string, leftOffset int, cs renderer.CharSet, useASCII bool) {
	hChar := cs.LineHorizontal
	vChar := cs.LineVertical

	branchCommits := make(map[string][]*gitCommit)
	for _, name := range sortedBranches {
		branchCommits[name] = nil
	}
	for i := range gg.commits {
		cmt := &gg.commits[i]
		if _, ok := branchCommits[cmt.branch]; ok {
			branchCommits[cmt.branch] = append(branchCommits[cmt.branch], cmt)
		}
	}

	commitMap := make(map[string]*gitCommit)
	for i := range gg.commits {
		commitMap[gg.commits[i].id] = &gg.commits[i]
	}

	branchLabelWidth := 0
	for _, b := range sortedBranches {
		if len(b) > branchLabelWidth {
			branchLabelWidth = len(b)
		}
	}
	lineStartCol := gitMargin + branchLabelWidth + 1

	extents := gitComputeBranchExtentsLR(gg, branchCommits, commitCol, commitMap, gg.mainBranchName, lineStartCol)

	// 1. Draw branch name labels
	for _, name := range sortedBranches {
		row := branchRow[name]
		c.PutText(row, gitMargin, name, "subgraph")
	}

	// 2. Draw branch lines (horizontal)
	for _, name := range sortedBranches {
		ext, ok := extents[name]
		if !ok {
			continue
		}
		row := branchRow[name]
		c.DrawHorizontal(row, ext[0], ext[1], hChar, "edge")
	}

	// 3. Draw fork and merge lines (vertical)
	for i := range gg.commits {
		cmt := &gg.commits[i]
		col := commitCol[cmt.id]
		targetRow := branchRow[cmt.branch]

		for _, parentID := range cmt.parents {
			parent, ok := commitMap[parentID]
			if !ok {
				continue
			}
			if parent.branch == cmt.branch {
				continue
			}
			sourceRow := branchRow[parent.branch]
			if sourceRow == targetRow {
				continue
			}
			rMin, rMax := min(sourceRow, targetRow), max(sourceRow, targetRow)
			// Draw vertical line between branch rows (excluding endpoints)
			for r := rMin + 1; r < rMax; r++ {
				c.Put(r, col, vChar, true, "edge")
			}
			// Place T-junction characters where vertical meets horizontal branch lines
			if !useASCII {
				c.Put(rMin, col, '┬', false, "edge")
				c.Put(rMax, col, '┴', false, "edge")
			} else {
				c.Put(rMin, col, '+', false, "edge")
				c.Put(rMax, col, '+', false, "edge")
			}
		}
	}

	// 4. Draw commit markers and labels (LAST)
	for i := range gg.commits {
		cmt := &gg.commits[i]
		col := commitCol[cmt.id]
		row := branchRow[cmt.branch]
		marker := gitGetMarker(cmt.ctype, useASCII)

		c.Put(row, col, marker, false, "node")

		label := cmt.id
		labelCol := col - len(label)/2
		c.PutText(row+1, labelCol, label, "label")

		if cmt.tag != "" {
			tagText := "[" + cmt.tag + "]"
			tagCol := col - len(tagText)/2
			c.PutText(row-1, tagCol, tagText, "edge_label")
		}
	}
}

func gitDrawTB(gg *gitGraph, canvas *renderer.Canvas, useASCII bool, cs renderer.CharSet, bottomToTop bool) {
	hChar := cs.LineHorizontal
	vChar := cs.LineVertical

	sortedBranches := gitSortBranches(gg)

	branchCommits := make(map[string][]*gitCommit)
	for _, name := range sortedBranches {
		branchCommits[name] = nil
	}
	for i := range gg.commits {
		cmt := &gg.commits[i]
		if _, ok := branchCommits[cmt.branch]; ok {
			branchCommits[cmt.branch] = append(branchCommits[cmt.branch], cmt)
		}
	}

	commitMap := make(map[string]*gitCommit)
	for i := range gg.commits {
		commitMap[gg.commits[i].id] = &gg.commits[i]
	}

	// Compute column gap based on max label width
	maxLabel := 0
	for _, b := range sortedBranches {
		if len(b) > maxLabel {
			maxLabel = len(b)
		}
	}
	for i := range gg.commits {
		cmt := &gg.commits[i]
		if len(cmt.id) > maxLabel {
			maxLabel = len(cmt.id)
		}
		if cmt.tag != "" && len(cmt.tag)+2 > maxLabel {
			maxLabel = len(cmt.tag) + 2
		}
	}

	colGap := max(maxLabel+4, 10)

	branchCol := make(map[string]int)
	for i, name := range sortedBranches {
		branchCol[name] = gitMargin + i*colGap
	}

	rowGap := 4
	nCommits := len(gg.commits)
	labelMargin := 2

	commitRow := make(map[string]int)
	var canvasH int
	var bottomLabelRowOffset int

	if bottomToTop {
		bottomLabelRowOffset = gitMargin + nCommits*rowGap + labelMargin
		canvasH = bottomLabelRowOffset + 2 + gitMargin

		for i, cmt := range gg.commits {
			commitRow[cmt.id] = gitMargin + (nCommits-1-i)*rowGap + 2
		}
	} else {
		topOffset := gitMargin + 2
		canvasH = topOffset + nCommits*rowGap + gitMargin

		for i, cmt := range gg.commits {
			commitRow[cmt.id] = topOffset + i*rowGap
		}
	}

	canvasW := gitMargin + len(sortedBranches)*colGap + gitMargin
	// Create new canvas with computed dimensions (matching Python's canvas.__init__ call)
	newC := renderer.NewCanvas(canvasW, canvasH)
	*canvas = *newC

	// Compute vertical branch extents (with merge extensions)
	branchStart := make(map[string]int)
	branchEnd := make(map[string]int)
	for name, commits := range branchCommits {
		if len(commits) == 0 {
			continue
		}
		minRow, maxRow := commitRow[commits[0].id], commitRow[commits[0].id]
		for _, cmt := range commits {
			r := commitRow[cmt.id]
			if r < minRow {
				minRow = r
			}
			if r > maxRow {
				maxRow = r
			}
		}
		if name == gg.mainBranchName {
			if bottomToTop {
				branchStart[name] = minRow - 1
				branchEnd[name] = bottomLabelRowOffset - 1
			} else {
				branchStart[name] = gitMargin + 1
				branchEnd[name] = maxRow + 1
			}
		} else {
			branchStart[name] = minRow
			branchEnd[name] = maxRow + 1
		}
	}

	// Extend branches to cover merge points
	for i := range gg.commits {
		cmt := &gg.commits[i]
		for _, parentID := range cmt.parents {
			parent, ok := commitMap[parentID]
			if !ok {
				continue
			}
			if parent.branch != cmt.branch {
				if _, ok := branchEnd[parent.branch]; ok {
					mergeRow := commitRow[cmt.id]
					if mergeRow < branchStart[parent.branch] {
						branchStart[parent.branch] = mergeRow
					}
					if mergeRow > branchEnd[parent.branch] {
						branchEnd[parent.branch] = mergeRow
					}
				}
			}
		}
	}

	// 1. Draw branch name labels
	var labelRow int
	if bottomToTop {
		labelRow = bottomLabelRowOffset
	} else {
		labelRow = gitMargin
	}
	for _, name := range sortedBranches {
		col := branchCol[name]
		labelCol := col - len(name)/2
		canvas.PutText(labelRow, labelCol, name, "subgraph")
	}

	// 2. Draw branch lines (vertical)
	for _, name := range sortedBranches {
		if _, ok := branchStart[name]; !ok {
			continue
		}
		col := branchCol[name]
		canvas.DrawVertical(col, branchStart[name], branchEnd[name], vChar, "edge")
	}

	// 3. Draw fork/merge lines (horizontal) BEFORE markers
	for i := range gg.commits {
		cmt := &gg.commits[i]
		row := commitRow[cmt.id]
		targetCol := branchCol[cmt.branch]

		for _, parentID := range cmt.parents {
			parent, ok := commitMap[parentID]
			if !ok {
				continue
			}
			if parent.branch == cmt.branch {
				continue
			}
			sourceCol := branchCol[parent.branch]
			if sourceCol == targetCol {
				continue
			}
			cMin, cMax := min(sourceCol, targetCol), max(sourceCol, targetCol)
			// Draw horizontal line between branch columns (excluding endpoints)
			for cc := cMin + 1; cc < cMax; cc++ {
				canvas.Put(row, cc, hChar, true, "edge")
			}
			// Place T-junction characters where horizontal meets vertical branch lines
			if !useASCII {
				canvas.Put(row, cMin, '├', false, "edge")
				canvas.Put(row, cMax, '┤', false, "edge")
			} else {
				canvas.Put(row, cMin, '+', false, "edge")
				canvas.Put(row, cMax, '+', false, "edge")
			}
		}
	}

	// 4. Draw commit markers and labels LAST
	for i := range gg.commits {
		cmt := &gg.commits[i]
		row := commitRow[cmt.id]
		col := branchCol[cmt.branch]
		marker := gitGetMarker(cmt.ctype, useASCII)

		canvas.Put(row, col, marker, false, "node")

		labelCol := col - len(cmt.id)/2
		if bottomToTop {
			canvas.PutText(row-1, labelCol, cmt.id, "label")
			if cmt.tag != "" {
				tagText := "[" + cmt.tag + "]"
				tagCol := col - len(tagText)/2
				canvas.PutText(row+1, tagCol, tagText, "edge_label")
			}
		} else {
			canvas.PutText(row+1, labelCol, cmt.id, "label")
			if cmt.tag != "" {
				tagText := "[" + cmt.tag + "]"
				tagCol := col - len(tagText)/2
				canvas.PutText(row-1, tagCol, tagText, "edge_label")
			}
		}
	}
}
