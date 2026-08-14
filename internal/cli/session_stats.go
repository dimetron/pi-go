package cli

import (
	"encoding/json"
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"github.com/dimetron/pi-go/internal/tools"
)

// newSessionStatsCmd exposes the session-stats analysis on the command line.
//
// The same analysis is already an agent tool, but a nightly health check should
// not need a model in the loop to run: it reads local files, makes no LLM call,
// and its output is deterministic. A CLI front door makes it schedulable and
// keeps the skill from shelling out to a second implementation.
func newSessionStatsCmd() *cobra.Command {
	var (
		hours         int
		highToolCalls int
		highTurns     int
		all           bool
		sessionDir    string
		asJSON        bool
	)

	cmd := &cobra.Command{
		Use:   "session-stats",
		Short: "Analyze recent sessions for anomalies, failures and token waste",
		Long: `Scan session events for a time window and report what went wrong, what it cost,
and whether the observation and palace pipelines are still recording.

Reads ~/.pi-go/sessions/*/events.jsonl. No LLM call, no network.`,
		Example: `  pi session-stats                 # last 24h
  pi session-stats --hours 72      # wider window
  pi session-stats --all           # include sessions with no anomalies
  pi session-stats --json          # machine-readable, for a cron wrapper`,
		Args: cobra.NoArgs,
		RunE: func(_ *cobra.Command, _ []string) error {
			out, err := tools.SessionStats(tools.SessionStatsInput{
				Hours:         hours,
				HighToolCalls: highToolCalls,
				HighTurns:     highTurns,
				All:           all,
				SessionDir:    sessionDir,
			})
			if err != nil {
				return fmt.Errorf("session-stats: %w", err)
			}

			if asJSON {
				enc := json.NewEncoder(os.Stdout)
				enc.SetIndent("", "  ")
				// Content is the human report; a JSON consumer wants the
				// numbers it can threshold on, not the markdown.
				return enc.Encode(map[string]any{
					"hours":              hours,
					"total_sessions":     out.TotalSessions,
					"anomalous_sessions": out.AnomalousSessions,
					"aborted_runs":       out.AbortedRuns,
					"reclaimable_tokens": out.ReclaimableTokens,
					"prompt_tokens":      out.PromptTokens,
				})
			}

			fmt.Print(out.Content)
			return nil
		},
	}

	cmd.Flags().IntVar(&hours, "hours", 24, "Lookback window in hours")
	cmd.Flags().IntVar(&highToolCalls, "high-tool-calls", 20, "Tool calls above which a session is flagged")
	cmd.Flags().IntVar(&highTurns, "high-turns", 5, "Turns above which a session is flagged")
	cmd.Flags().BoolVar(&all, "all", false, "Include sessions with no anomalies")
	cmd.Flags().StringVar(&sessionDir, "session-dir", "", "Session directory (default: ~/.pi-go/sessions)")
	cmd.Flags().BoolVar(&asJSON, "json", false, "Emit the summary counters as JSON")

	return cmd
}
