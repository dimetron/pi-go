package tools

import (
	"google.golang.org/adk/v2/agent"
	"google.golang.org/adk/v2/agent/llmagent"
	"google.golang.org/adk/v2/tool"
)

// CompactorConfig holds all compaction settings, loaded from config.json.
type CompactorConfig struct {
	Enabled               bool   `json:"enabled"`
	StripAnsi             bool   `json:"strip_ansi"`
	AggregateTestOutput   bool   `json:"aggregate_test_output"`
	FilterBuildOutput     bool   `json:"filter_build_output"`
	CompactGitOutput      bool   `json:"compact_git_output"`
	AggregateLinterOutput bool   `json:"aggregate_linter_output"`
	GroupSearchOutput     bool   `json:"group_search_output"`
	SmartTruncate         bool   `json:"smart_truncate"`
	SourceCodeFiltering   string `json:"source_code_filtering"` // "none", "minimal", "aggressive"

	MaxChars         int `json:"max_chars"`
	MaxLines         int `json:"max_lines"`
	MaxTestFailures  int `json:"max_test_failures"`
	MaxTestFailLines int `json:"max_test_fail_lines"`
	MaxBuildErrors   int `json:"max_build_errors"`
	MaxBuildErrLines int `json:"max_build_err_lines"`
	MaxDiffLines     int `json:"max_diff_lines"`
	MaxDiffHunkLines int `json:"max_diff_hunk_lines"`
	MaxStatusFiles   int `json:"max_status_files"`
	MaxLogEntries    int `json:"max_log_entries"`
	MaxLinterRules   int `json:"max_linter_rules"`
	MaxLinterFiles   int `json:"max_linter_files"`
	MaxSearchPerFile int `json:"max_search_per_file"`
	MaxSearchTotal   int `json:"max_search_total"`
}

// DefaultCompactorConfig returns a CompactorConfig with all stages enabled
// and reasonable limits (doubled from rtk-optimizer defaults).
func DefaultCompactorConfig() CompactorConfig {
	return CompactorConfig{
		Enabled:               true,
		StripAnsi:             true,
		AggregateTestOutput:   true,
		FilterBuildOutput:     true,
		CompactGitOutput:      true,
		AggregateLinterOutput: true,
		GroupSearchOutput:     true,
		SmartTruncate:         true,
		SourceCodeFiltering:   "none",

		MaxChars:         24000,
		MaxLines:         440,
		MaxTestFailures:  10,
		MaxTestFailLines: 8,
		MaxBuildErrors:   10,
		MaxBuildErrLines: 20,
		MaxDiffLines:     100,
		MaxDiffHunkLines: 20,
		MaxStatusFiles:   10,
		MaxLogEntries:    40,
		MaxLinterRules:   20,
		MaxLinterFiles:   20,
		MaxSearchPerFile: 20,
		MaxSearchTotal:   100,
	}
}

// CompactWrite names one result key and the compacted value that replaces it.
//
// The key is chosen by the pipeline that read it, so a pipeline can only ever
// write back the field it read. That is deliberate: the previous design had
// applyCompaction guess a target from a fixed probe order (stdout → content →
// output → diff), which silently wrote to the wrong field or to none, and is
// why seven of nine pipelines were no-ops in production.
//
// Value must have the same dynamic type as the field it replaces. ADK
// round-trips every tool result through json.Marshal/Unmarshal
// (adk v2.4.0 internal/typeutil/convert.go), so a struct field arrives as
// []any of map[string]any for a slice and string for a string. Replacing an
// array with a string would break the TUI result summaries
// (internal/tui/tool_display.go) and change the shape ADK hands the model.
type CompactWrite struct {
	Key   string
	Value any
}

// CompactResult is returned by each compaction pipeline.
type CompactResult struct {
	Writes     []CompactWrite // result keys to replace, with same-typed values
	Techniques []string       // techniques applied (e.g., "ansi", "test-aggregate")
	OrigSize   int            // original size in bytes
	CompSize   int            // compacted size in bytes
}

// BuildCompactorCallback creates an AfterToolCallback that compacts tool output.
func BuildCompactorCallback(cfg CompactorConfig, metrics *CompactMetrics) llmagent.AfterToolCallback {
	return func(ctx agent.Context, t tool.Tool, args, result map[string]any, err error) (map[string]any, error) {
		if !cfg.Enabled || err != nil {
			return result, nil
		}

		compacted := compactToolResult(t.Name(), args, result, cfg)
		// Record only a change that actually landed. Metrics for a pipeline
		// whose write found no key would overstate the compactor's savings.
		if compacted != nil && applyCompaction(result, compacted) {
			metrics.Record(compacted.Techniques, compacted.OrigSize, compacted.CompSize, t.Name())
		}

		return result, nil
	}
}

// compactorPipelines maps a registered tool name to its compaction pipeline.
//
// The keys are the names the tools actually register — hyphens for the git
// tools (git_diff.go:36, git_overview.go:52, git_hunk.go:42) and "ripgrep" for
// the search tool (grep.go:128-133, which self-names "ripgrep" whenever rg is
// on PATH). Routing on the underscore spellings is what made three git
// pipelines unreachable, and a `case "grep"` is unreachable on any host with rg.
//
// A map rather than a switch so a test can assert that every registered
// bulk-output tool has an entry — see TestCompactorRouting_EveryRegisteredTool.
var compactorPipelines = map[string]func(map[string]any, map[string]any, CompactorConfig) *CompactResult{
	"bash":          compactBash,
	"read":          compactRead,
	"ripgrep":       compactGrep,
	"grep":          compactGrep, // hosts without rg register "grep" (grep.go:129-131)
	"find":          compactFind,
	"tree":          compactTree,
	"ls":            compactLs,
	"git-file-diff": compactGitFileDiff,
	"git-overview":  compactGitOverview,
	"git-hunk":      compactGitHunk,
}

// compactToolResult routes to the appropriate compaction pipeline.
func compactToolResult(toolName string, args, result map[string]any, cfg CompactorConfig) *CompactResult {
	fn, ok := compactorPipelines[toolName]
	if !ok {
		return nil
	}
	return fn(result, args, cfg)
}

// applyCompaction writes the pipeline's own keys back into the result map.
//
// It writes only the keys the pipeline named, so it cannot misfire the way the
// old fixed probe order did. It returns false when nothing was written (nil
// result, no writes, or no key applied), which lets the caller decide not to
// record metrics for a change that never landed.
//
// A named key is written whether or not it is already present. Some keys the
// compactor must set are absent from a complete result because their struct
// field carries `omitempty` — `truncated` on GrepOutput/FindOutput/LsOutput is
// the case that matters. Requiring the key to pre-exist would silently drop the
// marker that tells the caller a capped list is partial.
func applyCompaction(result map[string]any, cr *CompactResult) bool {
	if result == nil || cr == nil || len(cr.Writes) == 0 {
		return false
	}

	wrote := false
	for _, w := range cr.Writes {
		if w.Key == "" {
			continue
		}
		result[w.Key] = w.Value
		wrote = true
	}
	return wrote
}

// runStage applies a compaction technique if enabled, tracking what was applied.
//
// A panicking stage is recorded as a technique so it stays observable through
// the metrics channel rather than being swallowed. It must not be logged to
// stderr: this runs inside an AfterToolCallback while the TUI owns the
// terminal's alternate screen, where any write to stdout/stderr corrupts the
// display (see the TUI output safety section of AGENTS.md).
func runStage(input string, techniques *[]string, name string, fn func(string) (string, bool)) (result string) {
	result = input
	defer func() {
		if r := recover(); r != nil {
			*techniques = append(*techniques, name+"-panic")
			result = input
		}
	}()

	output, applied := fn(input)
	if applied {
		*techniques = append(*techniques, name)
		return output
	}
	return input
}
