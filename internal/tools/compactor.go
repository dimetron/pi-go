package tools

import (
	"log"

	"google.golang.org/adk/agent/llmagent"
	"google.golang.org/adk/tool"
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

// CompactResult is returned by each compaction pipeline.
type CompactResult struct {
	Output     string   // compacted output text
	Techniques []string // techniques applied (e.g., "ansi", "test-aggregate")
	OrigSize   int      // original size in bytes
	CompSize   int      // compacted size in bytes
}

// BuildCompactorCallback creates an AfterToolCallback that compacts tool output.
func BuildCompactorCallback(cfg CompactorConfig, metrics *CompactMetrics) llmagent.AfterToolCallback {
	return func(ctx tool.Context, t tool.Tool, args, result map[string]any, err error) (map[string]any, error) {
		if !cfg.Enabled || err != nil {
			return result, nil
		}

		compacted := compactToolResult(t.Name(), args, result, cfg)
		if compacted != nil {
			metrics.Record(compacted.Techniques, compacted.OrigSize, compacted.CompSize, t.Name())
			applyCompaction(result, compacted)
		}

		return result, nil
	}
}

// compactToolResult routes to the appropriate compaction pipeline.
func compactToolResult(toolName string, args, result map[string]any, cfg CompactorConfig) *CompactResult {
	switch toolName {
	case "bash":
		return compactBash(result, args, cfg)
	case "read":
		return compactRead(result, cfg)
	case "grep":
		return compactGrep(result, cfg)
	case "find":
		return compactFind(result, cfg)
	case "tree":
		return compactTree(result, cfg)
	case "git_file_diff":
		return compactGitFileDiff(result, cfg)
	case "git_overview":
		return compactGitOverview(result, cfg)
	case "git_hunk":
		return compactGitHunk(result, cfg)
	default:
		return nil
	}
}

// applyCompaction replaces output fields in the result map with compacted versions.
func applyCompaction(result map[string]any, cr *CompactResult) {
	if result == nil || cr == nil {
		return
	}

	// For bash: replace stdout
	if _, ok := result["stdout"]; ok {
		result["stdout"] = cr.Output
		return
	}
	// For read: replace content
	if _, ok := result["content"]; ok {
		result["content"] = cr.Output
		return
	}
	// For grep/find/tree: replace output
	if _, ok := result["output"]; ok {
		result["output"] = cr.Output
		return
	}
	// For git tools: replace diff or output
	if _, ok := result["diff"]; ok {
		result["diff"] = cr.Output
		return
	}

	// Fallback: try common field names
	for _, key := range []string{"result", "data"} {
		if _, ok := result[key]; ok {
			result[key] = cr.Output
			return
		}
	}

	log.Printf("compactor: no known output field in result for replacement")
}

// runStage applies a compaction technique if enabled, tracking what was applied.
func runStage(input string, techniques *[]string, name string, fn func(string) (string, bool)) (result string) {
	result = input
	defer func() {
		if r := recover(); r != nil {
			log.Printf("compactor: stage %q panicked: %v", name, r)
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
