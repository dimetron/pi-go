package palace

import (
	"context"
	"fmt"
	"strings"

	"google.golang.org/adk/v2/agent"
	"google.golang.org/adk/v2/tool"
	"google.golang.org/adk/v2/tool/functiontool"
)

// --- palace-kg-select ---

// KGSelectToolInput defines parameters for the palace-kg-select tool.
type KGSelectToolInput struct {
	// sparql (default) or cypher.
	Lang string `json:"lang,omitempty"`
	// The query, e.g. SELECT ?who WHERE { ?who <works_on> <auth> } or
	// MATCH (a)-[r:works_on]->(b) WHERE r.confidence > 0.5 RETURN a, b.
	Query string `json:"query,omitempty"`
	// What was believed then: a transaction number or a time.
	TxAsOf string `json:"tx_as_of,omitempty"`
	// Only facts valid at this time.
	AsOf string `json:"as_of,omitempty"`
	// Only facts in this named graph.
	Graph string `json:"graph,omitempty"`
	// Fail instead of returning more than this many rows (default 200).
	MaxRows int `json:"max_rows,omitempty"`
	// Save the answer under this name, to check later whether it still holds.
	SaveAs string `json:"save_as,omitempty"`
	// Check a saved answer by name instead of running a query.
	Check string `json:"check,omitempty"`
}

// KGSelectToolOutput contains the result.
type KGSelectToolOutput struct {
	Content string `json:"content"`
}

func newPalaceKGSelectTool(p *Palace) (tool.Tool, error) {
	return functiontool.New(functiontool.Config{
		Name: "palace-kg-select",
		Description: "Query the knowledge graph with a pattern language: a SPARQL subset (SELECT … WHERE { triple " +
			"patterns, OPTIONAL, UNION, FILTER, FILTER NOT EXISTS, GRAPH }, ORDER BY, LIMIT, COUNT) or a read-only " +
			"Cypher subset (MATCH (a)-[r:p]->(b), OPTIONAL MATCH, WHERE, RETURN, r.confidence for a fact's layer). " +
			"Names are entity names (<works_on>, <alice>). Works on past beliefs with tx_as_of. save_as keeps the " +
			"answer; check=<name> later says whether it is still fresh. For rules use palace-kg-rules.",
	}, func(ctx agent.Context, input KGSelectToolInput) (KGSelectToolOutput, error) {
		return palaceKGSelectHandler(ctx, p, input)
	})
}

func palaceKGSelectHandler(ctx context.Context, p *Palace, input KGSelectToolInput) (KGSelectToolOutput, error) {
	kg := p.KG()
	if input.Check != "" {
		a, err := kg.CheckAnswer(ctx, input.Check)
		if err != nil {
			return KGSelectToolOutput{Content: fmt.Sprintf("Error: %v", err)}, nil
		}
		if a.Stale == "" {
			return KGSelectToolOutput{Content: fmt.Sprintf("Saved answer %s is still fresh (%d rows, checked to tx%d).\n%s",
				a.Name, len(a.Rows), a.Checkpoint, FormatRows(a.Vars, a.Rows))}, nil
		}
		return KGSelectToolOutput{Content: fmt.Sprintf("Saved answer %s is stale: %s. Run the query again.", a.Name, a.Stale)}, nil
	}
	if strings.TrimSpace(input.Query) == "" {
		return KGSelectToolOutput{Content: "Error: query is required (or check, to check a saved answer)"}, nil
	}
	lang := strings.ToLower(input.Lang)
	if lang == "" {
		lang = "sparql"
	}
	v, err := kg.View(ctx, input.TxAsOf, input.AsOf)
	if err != nil {
		return KGSelectToolOutput{Content: fmt.Sprintf("Error: %v", err)}, nil
	}
	if input.Graph != "" {
		v = v.InGraph(input.Graph)
	}
	maxRows := input.MaxRows
	if maxRows <= 0 {
		maxRows = 200
	}
	if input.SaveAs != "" {
		a, err := kg.SaveAnswer(ctx, input.SaveAs, lang, input.Query, v)
		if err != nil {
			return KGSelectToolOutput{Content: fmt.Sprintf("Error: %v", err)}, nil
		}
		return KGSelectToolOutput{Content: fmt.Sprintf("%d row(s), %s, saved as %s:\n%s",
			len(a.Rows), a.View, a.Name, FormatRows(a.Vars, a.Rows))}, nil
	}
	// The budget counts statements read as well as rows returned.
	res, err := v.RunQuery(ctx, lang, input.Query, QueryOptions{Budget: Budget{MaxRows: maxRows * 50}})
	if err != nil {
		return KGSelectToolOutput{Content: fmt.Sprintf("Error: %v", err)}, nil
	}
	if len(res.Rows) > maxRows {
		return KGSelectToolOutput{Content: fmt.Sprintf("Error: %d rows, more than max_rows=%d; add LIMIT or narrow the query",
			len(res.Rows), maxRows)}, nil
	}
	return KGSelectToolOutput{Content: fmt.Sprintf("%d row(s), %s:\n%s", len(res.Rows), v, FormatRows(res.Vars, res.Rows))}, nil
}

// FormatRows renders query rows as a markdown table.
func FormatRows(vars []string, rows []map[string]Value) string {
	if len(rows) == 0 {
		return "(no rows)"
	}
	if len(vars) == 0 {
		vars = ElpiVars(rows)
	}
	var sb strings.Builder
	sb.WriteString("| " + strings.Join(vars, " | ") + " |\n|")
	for range vars {
		sb.WriteString("---|")
	}
	sb.WriteString("\n")
	for _, r := range rows {
		cells := make([]string, len(vars))
		for i, k := range vars {
			if v, ok := r[k]; ok {
				cells[i] = v.String()
			}
		}
		sb.WriteString("| " + strings.Join(cells, " | ") + " |\n")
	}
	return sb.String()
}
