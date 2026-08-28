package diagram

import (
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/dimetron/pi-go/internal/mermaid/renderer"
)

type ganttTask struct {
	label       string
	id          string
	start       time.Time
	end         time.Time
	section     string
	isMilestone bool
}

type ganttData struct {
	title       string
	dateFormat  string
	tasks       []ganttTask
	todayMarker bool // true by default, false if "todayMarker off"
}

var (
	reGanttHeader       = regexp.MustCompile(`(?i)^\s*gantt\s*$`)
	reGanttTitle        = regexp.MustCompile(`(?i)^\s*title\s+(.+)$`)
	reGanttDateFormat   = regexp.MustCompile(`(?i)^\s*dateFormat\s+(.+)$`)
	reGanttSection      = regexp.MustCompile(`(?i)^\s*section\s+(.+)$`)
	reGanttExcludes     = regexp.MustCompile(`(?i)^\s*excludes\s+`)
	reGanttTodayMarker  = regexp.MustCompile(`(?i)^\s*todayMarker\s+`)
	reGanttAxisFormat   = regexp.MustCompile(`(?i)^\s*axisFormat\s+`)
	reGanttTickInterval = regexp.MustCompile(`(?i)^\s*tickInterval\s+`)
)

func parseGantt(source string) *ganttData {
	gd := &ganttData{dateFormat: "YYYY-MM-DD", todayMarker: true}
	lines := strings.Split(source, "\n")
	currentSection := ""
	taskMap := map[string]*ganttTask{} // for "after" references

	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || strings.HasPrefix(trimmed, "%%") {
			continue
		}
		if reGanttHeader.MatchString(trimmed) {
			continue
		}
		if m := reGanttTitle.FindStringSubmatch(trimmed); m != nil {
			gd.title = strings.TrimSpace(m[1])
			continue
		}
		if m := reGanttDateFormat.FindStringSubmatch(trimmed); m != nil {
			gd.dateFormat = strings.TrimSpace(m[1])
			continue
		}
		if m := reGanttSection.FindStringSubmatch(trimmed); m != nil {
			currentSection = strings.TrimSpace(m[1])
			continue
		}
		if reGanttTodayMarker.MatchString(trimmed) {
			if strings.Contains(strings.ToLower(trimmed), "off") {
				gd.todayMarker = false
			}
			continue
		}
		if reGanttExcludes.MatchString(trimmed) ||
			reGanttAxisFormat.MatchString(trimmed) || reGanttTickInterval.MatchString(trimmed) {
			continue
		}

		// Task line: "label :id, start, duration" or "label :start, duration"
		task := parseGanttTask(trimmed, gd.dateFormat, currentSection, taskMap)
		if task != nil {
			gd.tasks = append(gd.tasks, *task)
			if task.id != "" {
				taskMap[task.id] = task
			}
		}
	}

	return gd
}

func parseGanttTask(line, dateFormat, section string, taskMap map[string]*ganttTask) *ganttTask {
	// Split on first ":"
	colonIdx := strings.Index(line, ":")
	if colonIdx < 0 {
		return nil
	}

	label := strings.TrimSpace(line[:colonIdx])
	rest := strings.TrimSpace(line[colonIdx+1:])

	parts := strings.Split(rest, ",")
	for i := range parts {
		parts[i] = strings.TrimSpace(parts[i])
	}

	// Filter out modifiers like "active", "done", "crit", "milestone"
	var cleanParts []string
	isMilestone := false
	id := ""
	for _, p := range parts {
		switch p {
		case "active", "done", "crit":
			continue
		case "milestone":
			isMilestone = true
			continue
		default:
			// Check if it's an ID (starts with letter, no spaces)
			if len(cleanParts) == 0 && !strings.Contains(p, " ") && !strings.Contains(p, "-") && len(p) < 20 {
				// Could be an ID or a date — try to parse as date first
				if _, err := parseGanttDate(p, dateFormat); err != nil {
					if !strings.HasPrefix(p, "after ") {
						id = p
						continue
					}
				}
			}
			cleanParts = append(cleanParts, p)
		}
	}

	task := &ganttTask{
		label:       label,
		id:          id,
		section:     section,
		isMilestone: isMilestone,
	}

	goFmt := mermaidToGoDateFormat(dateFormat)

	if len(cleanParts) >= 2 {
		startStr := cleanParts[0]
		durStr := cleanParts[1]

		if strings.HasPrefix(startStr, "after ") {
			refID := strings.TrimPrefix(startStr, "after ")
			if ref, ok := taskMap[refID]; ok {
				task.start = ref.end
			} else {
				task.start = time.Now()
			}
		} else {
			t, err := time.Parse(goFmt, startStr)
			if err != nil {
				task.start = time.Now()
			} else {
				task.start = t
			}
		}

		task.end = task.start.Add(parseDuration(durStr))
	} else if len(cleanParts) == 1 {
		// Just a duration — start after last task
		if len(taskMap) > 0 {
			// Find the latest end
			latest := time.Time{}
			for _, t := range taskMap {
				if t.end.After(latest) {
					latest = t.end
				}
			}
			task.start = latest
		} else {
			task.start = time.Now()
		}
		task.end = task.start.Add(parseDuration(cleanParts[0]))
	}

	if task.end.IsZero() || task.end.Before(task.start) {
		task.end = task.start.Add(24 * time.Hour)
	}

	return task
}

func parseDuration(s string) time.Duration {
	s = strings.TrimSpace(s)
	if strings.HasSuffix(s, "d") {
		days, _ := strconv.Atoi(strings.TrimSuffix(s, "d"))
		return time.Duration(days) * 24 * time.Hour
	}
	if strings.HasSuffix(s, "w") {
		weeks, _ := strconv.Atoi(strings.TrimSuffix(s, "w"))
		return time.Duration(weeks) * 7 * 24 * time.Hour
	}
	if strings.HasSuffix(s, "h") {
		hours, _ := strconv.Atoi(strings.TrimSuffix(s, "h"))
		return time.Duration(hours) * time.Hour
	}
	// Try as number of days
	days, err := strconv.Atoi(s)
	if err == nil {
		return time.Duration(days) * 24 * time.Hour
	}
	return 7 * 24 * time.Hour // default 1 week
}

func parseGanttDate(s, format string) (time.Time, error) {
	goFmt := mermaidToGoDateFormat(format)
	return time.Parse(goFmt, s)
}

func mermaidToGoDateFormat(mermaid string) string {
	r := strings.NewReplacer(
		"YYYY", "2006",
		"YY", "06",
		"MM", "01",
		"DD", "02",
		"HH", "15",
		"mm", "04",
		"ss", "05",
	)
	return r.Replace(mermaid)
}

// RenderGantt parses and renders a Mermaid gantt chart.
func RenderGantt(source string, useASCII bool, theme *renderer.Theme) *renderer.Canvas {
	gd := parseGantt(source)
	if len(gd.tasks) == 0 {
		c := renderer.NewCanvas(30, 1)
		c.PutText(0, 0, "[gantt] no tasks", "default")
		return c
	}

	// Find time range
	minTime := gd.tasks[0].start
	maxTime := gd.tasks[0].end
	for _, t := range gd.tasks {
		if t.start.Before(minTime) {
			minTime = t.start
		}
		if t.end.After(maxTime) {
			maxTime = t.end
		}
	}

	totalDays := maxTime.Sub(minTime).Hours() / 24
	if totalDays < 1 {
		totalDays = 1
	}

	// Layout
	labelW := 0
	for _, t := range gd.tasks {
		if len(t.label) > labelW {
			labelW = len(t.label)
		}
	}
	labelW += 2

	barW := scaleWidth(labelW+5, 20, maxScaledWidth)
	titleRows := 0
	if gd.title != "" {
		titleRows = 2
	}

	// Section headers take a row each
	sections := []string{}
	sectionMap := map[string]bool{}
	for _, t := range gd.tasks {
		if t.section != "" && !sectionMap[t.section] {
			sectionMap[t.section] = true
			sections = append(sections, t.section)
		}
	}

	totalRows := len(gd.tasks) + len(sections)
	canvasWidth := labelW + 3 + barW + 2 // label + " │ " + bar
	canvasHeight := titleRows + totalRows + 3

	c := renderer.NewCanvas(canvasWidth, canvasHeight)

	// Wallpaper: base background behind entire diagram
	if theme != nil && theme.HasDepthColors() {
		for r := 0; r < canvasHeight; r++ {
			for col := 0; col < canvasWidth; col++ {
				c.SetFill(r, col, "subgraph_fill")
			}
		}
	}

	// Title
	if gd.title != "" {
		titleCol := (canvasWidth - len(gd.title)) / 2
		c.PutText(0, titleCol, gd.title, "bold_label")
	}

	vLine := '│'
	hLine := '─'
	barCh := '▓'
	barHalf := '▒'
	milestone := '◆'
	if useASCII {
		vLine = '|'
		hLine = '-'
		barCh = '='
		barHalf = '='
		milestone = '*'
	}

	barStartCol := labelW + 3
	row := titleRows

	// Date header
	startLabel := minTime.Format("Jan 02")
	endLabel := maxTime.Format("Jan 02")
	c.PutText(row, barStartCol, startLabel, "default")
	c.PutText(row, barStartCol+barW-len(endLabel), endLabel, "default")
	row++

	// Header line
	for col := barStartCol; col < barStartCol+barW; col++ {
		c.Put(row, col, hLine, false, "edge")
	}
	row++

	useRegion := theme != nil && theme.HasDepthColors()
	currentSection := ""
	sectionIdx := -1
	for _, t := range gd.tasks {
		// Section header
		if t.section != "" && t.section != currentSection {
			currentSection = t.section
			sectionIdx++
			// Section label: colored text on wallpaper (left side)
			sectionStyle := "subgraph_label"
			if useRegion {
				sectionStyle = "_ansi:" + theme.RegionTextStyle(sectionIdx, 0)
			}
			c.PutText(row, 1, t.section, sectionStyle)
			c.Put(row, labelW+1, vLine, false, "edge")

			// Color strip only on bar area (right of divider)
			if useRegion {
				fillStyle := "_ansi:" + theme.RegionStyle(sectionIdx, 0)
				for col := barStartCol; col < barStartCol+barW; col++ {
					c.SetFill(row, col, fillStyle)
				}
			}
			row++
		}

		// Label — use section-colored text (no bg) for subtlety
		labelStyle := "label"
		if useRegion && sectionIdx >= 0 {
			labelStyle = "_ansi:" + theme.RegionTextStyle(sectionIdx, 1)
		}
		padding := labelW - len(t.label) - 1
		c.PutText(row, padding, t.label, labelStyle)
		c.Put(row, labelW+1, vLine, false, "edge")

		// Row background
		if useRegion && sectionIdx >= 0 {
			fillStyle := "_ansi:" + theme.RegionStyle(sectionIdx, 1)
			for col := barStartCol; col < barStartCol+barW; col++ {
				c.SetFill(row, col, fillStyle)
			}
		}

		// Bar
		startOffset := t.start.Sub(minTime).Hours() / 24
		endOffset := t.end.Sub(minTime).Hours() / 24
		barStart := barStartCol + int(startOffset/totalDays*float64(barW))
		barEnd := barStartCol + int(endOffset/totalDays*float64(barW))

		if t.isMilestone {
			mid := (barStart + barEnd) / 2
			c.Put(row, mid, milestone, false, "arrow")
		} else {
			if barEnd <= barStart {
				barEnd = barStart + 1
			}
			if useRegion && sectionIdx >= 0 {
				// Colored bar: │ delimiters + ░ fill interior
				barStyle := "_ansi:" + theme.RegionBarStyle(sectionIdx, 0)
				borderStyle := "_ansi:" + theme.RegionBorderStyle(sectionIdx, 0)
				c.Put(row, barStart, vLine, false, borderStyle)
				if barEnd-1 > barStart {
					c.Put(row, barEnd-1, vLine, false, borderStyle)
				}
				for col := barStart + 1; col < barEnd-1; col++ {
					c.Put(row, col, '░', false, barStyle)
				}
			} else {
				for col := barStart; col < barEnd; col++ {
					ch := barCh
					if col == barEnd-1 && barEnd-barStart > 1 {
						ch = barHalf
					}
					c.Put(row, col, ch, false, "node")
				}
			}
		}

		row++
	}

	// Today marker: vertical dashed line at current date
	if gd.todayMarker {
		now := time.Now()
		if !now.Before(minTime) && !now.After(maxTime) {
			todayOffset := now.Sub(minTime).Hours() / 24
			todayCol := barStartCol + int(todayOffset/totalDays*float64(barW))
			if todayCol >= barStartCol && todayCol < barStartCol+barW {
				todayStyle := "arrow"
				if useRegion {
					todayStyle = "_ansi:\033[1m\033[38;2;255;100;100m" // bright red
				}
				// Draw from header line to last task row
				for r := titleRows + 2; r < row; r++ {
					existing := c.Get(r, todayCol)
					if existing == ' ' || existing == '░' {
						todayMark := '┆'
						if useASCII {
							todayMark = ':'
						}
						c.Put(r, todayCol, todayMark, false, todayStyle)
					}
				}
				// Label above
				label := "today"
				labelCol := todayCol - len(label)/2
				if labelCol < barStartCol {
					labelCol = barStartCol
				}
				c.PutText(titleRows+1, labelCol, label, todayStyle)
			}
		}
	}

	return c
}
