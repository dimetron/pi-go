package tools

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// CompactMetrics tracks per-session compaction statistics.
type CompactMetrics struct {
	mu      sync.Mutex
	Records []CompactRecord `json:"records"`
}

// CompactRecord is a single compaction event.
type CompactRecord struct {
	Tool       string   `json:"tool"`
	Techniques []string `json:"techniques"`
	OrigSize   int      `json:"orig_size"`
	CompSize   int      `json:"comp_size"`
	Timestamp  time.Time `json:"timestamp"`
}

// CompactSummary provides aggregated compaction statistics.
type CompactSummary struct {
	TotalOrig  int                       `json:"total_orig"`
	TotalComp  int                       `json:"total_comp"`
	SavingsPct float64                   `json:"savings_pct"`
	ByTool     map[string]ToolCompactSum `json:"by_tool"`
}

// ToolCompactSum holds per-tool aggregated stats.
type ToolCompactSum struct {
	Count int `json:"count"`
	Orig  int `json:"orig"`
	Comp  int `json:"comp"`
}

// NewCompactMetrics creates a new CompactMetrics instance.
func NewCompactMetrics() *CompactMetrics {
	return &CompactMetrics{}
}

// Record adds a compaction record.
func (m *CompactMetrics) Record(techniques []string, origSize, compSize int, toolName string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.Records = append(m.Records, CompactRecord{
		Tool:       toolName,
		Techniques: techniques,
		OrigSize:   origSize,
		CompSize:   compSize,
		Timestamp:  time.Now(),
	})
}

// Summary computes aggregated compaction statistics.
func (m *CompactMetrics) Summary() CompactSummary {
	m.mu.Lock()
	defer m.mu.Unlock()

	s := CompactSummary{
		ByTool: make(map[string]ToolCompactSum),
	}
	for _, r := range m.Records {
		s.TotalOrig += r.OrigSize
		s.TotalComp += r.CompSize
		ts := s.ByTool[r.Tool]
		ts.Count++
		ts.Orig += r.OrigSize
		ts.Comp += r.CompSize
		s.ByTool[r.Tool] = ts
	}
	if s.TotalOrig > 0 {
		s.SavingsPct = float64(s.TotalOrig-s.TotalComp) / float64(s.TotalOrig) * 100
	}
	return s
}

// FormatStats returns a human-readable string of compaction stats.
func (m *CompactMetrics) FormatStats() string {
	s := m.Summary()
	if len(m.Records) == 0 {
		return "No compaction records in this session."
	}

	var b strings.Builder
	fmt.Fprintf(&b, "RTK Compactor Stats\n")
	fmt.Fprintf(&b, "═══════════════════\n")
	fmt.Fprintf(&b, "Total calls:    %d\n", len(m.Records))
	fmt.Fprintf(&b, "Original size:  %s\n", formatBytes(s.TotalOrig))
	fmt.Fprintf(&b, "Compacted size: %s\n", formatBytes(s.TotalComp))
	fmt.Fprintf(&b, "Savings:        %.1f%%\n\n", s.SavingsPct)

	fmt.Fprintf(&b, "By Tool:\n")
	for tool, ts := range s.ByTool {
		pct := float64(0)
		if ts.Orig > 0 {
			pct = float64(ts.Orig-ts.Comp) / float64(ts.Orig) * 100
		}
		fmt.Fprintf(&b, "  %-15s %3d calls  %s → %s  (%.0f%%)\n",
			tool, ts.Count, formatBytes(ts.Orig), formatBytes(ts.Comp), pct)
	}
	return b.String()
}

// Save persists metrics to a JSON file in the session directory.
func (m *CompactMetrics) Save(sessionDir string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if len(m.Records) == 0 {
		return nil
	}

	data := struct {
		Records []CompactRecord `json:"records"`
		Summary CompactSummary  `json:"summary"`
	}{
		Records: m.Records,
	}

	// Compute summary without lock (we already hold it).
	s := CompactSummary{ByTool: make(map[string]ToolCompactSum)}
	for _, r := range m.Records {
		s.TotalOrig += r.OrigSize
		s.TotalComp += r.CompSize
		ts := s.ByTool[r.Tool]
		ts.Count++
		ts.Orig += r.OrigSize
		ts.Comp += r.CompSize
		s.ByTool[r.Tool] = ts
	}
	if s.TotalOrig > 0 {
		s.SavingsPct = float64(s.TotalOrig-s.TotalComp) / float64(s.TotalOrig) * 100
	}
	data.Summary = s

	b, err := json.MarshalIndent(data, "", "  ")
	if err != nil {
		return err
	}

	path := filepath.Join(sessionDir, "compactor-metrics.json")
	return os.WriteFile(path, b, 0644)
}

func formatBytes(n int) string {
	switch {
	case n >= 1024*1024:
		return fmt.Sprintf("%.1fMB", float64(n)/1024/1024)
	case n >= 1024:
		return fmt.Sprintf("%.1fKB", float64(n)/1024)
	default:
		return fmt.Sprintf("%dB", n)
	}
}
