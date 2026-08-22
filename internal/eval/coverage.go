package eval

import (
	"sort"
	"strings"
)

// Exclusion documents a registered tool that deliberately has no eval
// scenario. Every tool in the inventory must be either targeted by a scenario
// or excluded here with a reason — TestScenarios_CoverInventory enforces it —
// so a new tool cannot silently go unmeasured.
type Exclusion struct {
	// Tool is the exact tool name, or a "prefix*" pattern for a whole family
	// (e.g. "palace-*").
	Tool   string `json:"tool"`
	Reason string `json:"reason"`
}

// matches reports whether the exclusion covers the named tool.
func (e Exclusion) matches(name string) bool {
	if strings.HasSuffix(e.Tool, "*") {
		return strings.HasPrefix(name, strings.TrimSuffix(e.Tool, "*"))
	}
	return e.Tool == name
}

// Coverage status values, from best to worst.
const (
	// CoverageOK: the tool was called and at least one call produced a
	// result that does not look like an error.
	CoverageOK = "ok"
	// CoverageErrorsOnly: every call to the tool produced an error-looking
	// result (or no result at all).
	CoverageErrorsOnly = "errors-only"
	// CoverageNotCalled: the tool has a scenario but no trajectory called it.
	CoverageNotCalled = "not-called"
	// CoverageExcluded: the tool is documented as excluded from the suite.
	CoverageExcluded = "excluded"
	// CoverageUnmapped: the tool has neither a scenario nor an exclusion.
	// This is the state the mapping test exists to prevent.
	CoverageUnmapped = "unmapped"
)

// ToolCoverage is one row of the coverage matrix.
type ToolCoverage struct {
	Name     string `json:"name"`
	Group    string `json:"group"`
	Requires string `json:"requires,omitempty"`
	// Scenarios that target this tool (may be empty for excluded tools).
	Scenarios []string `json:"scenarios,omitempty"`
	// Excluded carries the exclusion reason when the tool is excluded.
	Excluded string `json:"excluded,omitempty"`
	Calls    int    `json:"calls"`
	Results  int    `json:"results"`
	Errors   int    `json:"errors"`
	Wasted   int    `json:"wasted"`
	Status   string `json:"status"`
}

// Coverage is the tool coverage matrix for a set of trajectories against the
// registered tool inventory.
type Coverage struct {
	Tools []ToolCoverage `json:"tools"`
	// Summary counts over Tools.
	Total     int `json:"total"`
	OK        int `json:"ok"`
	NotCalled int `json:"not_called"`
	Errors    int `json:"errors_only"`
	Excluded  int `json:"excluded"`
	Unmapped  int `json:"unmapped"`
	// Unknown lists tools that were called but are not in the inventory —
	// MCP tools, or a tool the inventory does not know how to construct.
	Unknown []string `json:"unknown,omitempty"`
	// Gap is the list of non-excluded tools that were never successfully
	// exercised (status not-called, errors-only or unmapped).
	Gap []string `json:"gap,omitempty"`
}

// ComputeCoverage builds the coverage matrix: for each inventoried tool, the
// scenarios targeting it, its exclusion (if any), and what the trajectories
// show — calls, results, error-looking results and wasted calls.
func ComputeCoverage(inv []ToolInfo, scenarios []Scenario, exclusions []Exclusion, loaded []*LoadedTrajectory) Coverage {
	targets := scenarioTargets(scenarios)
	calls := callStatsByTool(loaded)

	cov := Coverage{Total: len(inv)}
	known := make(map[string]bool, len(inv))
	for _, ti := range inv {
		known[ti.Name] = true
		row := ToolCoverage{
			Name:      ti.Name,
			Group:     ti.Group,
			Requires:  ti.Requires,
			Scenarios: targets[ti.Name],
			Excluded:  exclusionReason(exclusions, ti.Name),
		}
		if st, ok := calls[ti.Name]; ok {
			row.Calls, row.Results, row.Errors, row.Wasted = st.calls, st.results, st.errors, st.wasted
		}
		row.Status = coverageStatus(row)
		cov.Tools = append(cov.Tools, row)
		cov.count(row)
	}

	for name := range calls {
		if !known[name] {
			cov.Unknown = append(cov.Unknown, name)
		}
	}
	sort.Strings(cov.Unknown)
	sort.Strings(cov.Gap)
	return cov
}

// count folds one row into the summary counters.
func (c *Coverage) count(row ToolCoverage) {
	switch row.Status {
	case CoverageOK:
		c.OK++
	case CoverageExcluded:
		c.Excluded++
	case CoverageNotCalled:
		c.NotCalled++
		c.Gap = append(c.Gap, row.Name)
	case CoverageErrorsOnly:
		c.Errors++
		c.Gap = append(c.Gap, row.Name)
	case CoverageUnmapped:
		c.Unmapped++
		c.Gap = append(c.Gap, row.Name)
	}
}

// coverageStatus derives a row's status. A successful call wins over an
// exclusion: an excluded tool that was exercised anyway is covered, and the
// exclusion reason stays on the row for context.
func coverageStatus(row ToolCoverage) string {
	switch {
	case row.Results-row.Errors > 0:
		return CoverageOK
	case row.Calls > 0:
		return CoverageErrorsOnly
	case row.Excluded != "":
		return CoverageExcluded
	case len(row.Scenarios) > 0:
		return CoverageNotCalled
	default:
		return CoverageUnmapped
	}
}

// exclusionReason returns the reason of the first exclusion matching name.
func exclusionReason(exclusions []Exclusion, name string) string {
	for _, e := range exclusions {
		if e.matches(name) {
			return e.Reason
		}
	}
	return ""
}

// scenarioTargets maps every tool name to the scenarios that target it,
// expanding alternatives ("grep|ripgrep") so either spelling is covered.
func scenarioTargets(scenarios []Scenario) map[string][]string {
	out := make(map[string][]string)
	for _, s := range scenarios {
		for _, target := range s.Tools {
			for _, name := range strings.Split(target, "|") {
				name = strings.TrimSpace(name)
				if name == "" {
					continue
				}
				out[name] = append(out[name], s.Name)
			}
		}
	}
	return out
}

// UnmappedTools returns the inventory tools that have neither a scenario
// targeting them nor an exclusion. A non-empty result means the suite has a
// blind spot; the mapping test fails on it.
func UnmappedTools(inv []ToolInfo, scenarios []Scenario, exclusions []Exclusion) []string {
	targets := scenarioTargets(scenarios)
	var out []string
	for _, ti := range inv {
		if len(targets[ti.Name]) == 0 && exclusionReason(exclusions, ti.Name) == "" {
			out = append(out, ti.Name)
		}
	}
	return out
}

// UnknownTargets returns scenario tool targets that match nothing in the
// inventory — a typo, a renamed tool, or a tool that was removed. For an
// alternatives target ("grep|ripgrep") at least one alternative must exist.
func UnknownTargets(inv []ToolInfo, scenarios []Scenario) []string {
	known := make(map[string]bool, len(inv))
	for _, ti := range inv {
		known[ti.Name] = true
	}
	var out []string
	for _, s := range scenarios {
		for _, target := range s.Tools {
			if !anyKnown(known, target) {
				out = append(out, s.Name+": "+target)
			}
		}
	}
	return out
}

func anyKnown(known map[string]bool, target string) bool {
	for _, name := range strings.Split(target, "|") {
		if known[strings.TrimSpace(name)] {
			return true
		}
	}
	return false
}

// toolCallStats is the per-tool call bookkeeping shared by the coverage matrix
// and the scenario evaluation.
type toolCallStats struct {
	calls, results, errors, wasted int
}

// callStatsByTool pairs every call in every trajectory with its observation
// and folds the outcome into per-tool counts.
func callStatsByTool(loaded []*LoadedTrajectory) map[string]toolCallStats {
	out := make(map[string]toolCallStats)
	for _, lt := range loaded {
		if lt == nil || lt.Traj == nil {
			continue
		}
		for _, rec := range pairCalls(lt.Traj) {
			st := out[rec.fn]
			st.calls++
			switch {
			case !rec.observed:
				st.wasted++
			case rec.isError:
				st.results++
				st.errors++
			default:
				st.results++
			}
			out[rec.fn] = st
		}
	}
	return out
}
