package palace

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"strings"

	"google.golang.org/adk/v2/agent"
	"google.golang.org/adk/v2/tool"
	"google.golang.org/adk/v2/tool/functiontool"
)

// kgEmbedModel names the palace's embedding backend for statement vectors,
// so vectors of different models never mix.
func (p *Palace) kgEmbedModel() string {
	if _, ok := p.embedder.(*ollamaEmbedder); ok {
		return "ollama/" + p.config.OllamaModel
	}
	return "local/" + filepath.Base(p.config.ModelPath)
}

// --- palace-kg-recall ---

// KGRecallToolInput defines parameters for the palace-kg-recall tool.
type KGRecallToolInput struct {
	// What to recall, in plain words.
	Query string `json:"query"`
	// Maximum facts (default 10).
	Limit int `json:"limit,omitempty"`
	// Only facts in this named graph.
	Graph string `json:"graph,omitempty"`
}

// KGRecallToolOutput contains the result.
type KGRecallToolOutput struct {
	Content string `json:"content"`
}

func newPalaceKGRecallTool(p *Palace) (tool.Tool, error) {
	return functiontool.New(functiontool.Config{
		Name: "palace-kg-recall",
		Description: "Recall knowledge-graph facts related to a question, by meaning and by words (hybrid: vector " +
			"similarity fused with text search). Use it when you do not know the exact entity name; use " +
			"palace-kg-query when you do. Each hit shows its rank in each channel.",
	}, func(ctx agent.Context, input KGRecallToolInput) (KGRecallToolOutput, error) {
		return palaceKGRecallHandler(ctx, p, input)
	})
}

func palaceKGRecallHandler(ctx context.Context, p *Palace, input KGRecallToolInput) (KGRecallToolOutput, error) {
	if strings.TrimSpace(input.Query) == "" {
		return KGRecallToolOutput{Content: "Error: query is required"}, nil
	}
	k := input.Limit
	if k <= 0 {
		k = 10
	}
	kg := p.KG()
	v := kg.Now()
	if input.Graph != "" {
		v = v.InGraph(input.Graph)
	}
	var sb strings.Builder
	var hits []RecallHit
	err := ErrChannelUnavailable
	if p.embedder != nil {
		model := p.kgEmbedModel()
		if _, err = kg.EmbedStatements(ctx, p.embedder, model, 256); err == nil {
			hits, err = v.Recall(ctx, p.embedder, model, input.Query, k)
		}
	}
	if errors.Is(err, ErrChannelUnavailable) {
		sb.WriteString("No embedding model is available, so this is text search only.\n")
		sts, terr := v.TextSearch(ctx, input.Query, k)
		if terr != nil {
			return KGRecallToolOutput{Content: fmt.Sprintf("Error: %v", terr)}, nil
		}
		hits = hits[:0]
		for i, st := range sts {
			hits = append(hits, RecallHit{Statement: st, TextRank: i + 1})
		}
	} else if err != nil {
		return KGRecallToolOutput{Content: fmt.Sprintf("Error: %v", err)}, nil
	}
	if len(hits) == 0 {
		sb.WriteString("Nothing recalled.")
		return KGRecallToolOutput{Content: sb.String()}, nil
	}
	for _, h := range hits {
		st := h.Statement
		fmt.Fprintf(&sb, "- e%d %s → %s → %s", st.EID, st.Subject, st.Predicate, st.Object)
		var ranks []string
		if h.TextRank > 0 {
			ranks = append(ranks, fmt.Sprintf("text #%d", h.TextRank))
		}
		if h.VectorRank > 0 {
			ranks = append(ranks, fmt.Sprintf("vector #%d", h.VectorRank))
		}
		fmt.Fprintf(&sb, " (%s)\n", strings.Join(ranks, ", "))
	}
	return KGRecallToolOutput{Content: sb.String()}, nil
}

// --- palace-kg-bundle ---

// KGBundleToolInput defines parameters for the palace-kg-bundle tool.
type KGBundleToolInput struct {
	// export (a fact with everything said about it, as JSON) or import.
	Action string `json:"action"`
	// export: the statement id, the number in e12.
	EID int64 `json:"eid,omitempty"`
	// export: the fact as believed then (transaction number or time).
	TxAsOf string `json:"tx_as_of,omitempty"`
	// import: a bundle produced by export.
	Bundle string `json:"bundle,omitempty"`
	// import: where it came from.
	Source string `json:"source,omitempty"`
}

// KGBundleToolOutput contains the result.
type KGBundleToolOutput struct {
	Content string `json:"content"`
}

func newPalaceKGBundleTool(p *Palace) (tool.Tool, error) {
	return functiontool.New(functiontool.Config{
		Name: "palace-kg-bundle",
		Description: "Move a fact between memories: export gives fact <eid> with its annotations (confidence, sources, " +
			"notes) and graph memberships as JSON; import writes such a bundle, reusing facts already known, so " +
			"importing twice changes nothing. Sensitive values are never exported.",
	}, func(ctx agent.Context, input KGBundleToolInput) (KGBundleToolOutput, error) {
		return palaceKGBundleHandler(ctx, p, input)
	})
}

func palaceKGBundleHandler(ctx context.Context, p *Palace, input KGBundleToolInput) (KGBundleToolOutput, error) {
	kg := p.KG()
	switch input.Action {
	case "export":
		if input.EID <= 0 {
			return KGBundleToolOutput{Content: "Error: eid is required"}, nil
		}
		v, err := kg.View(ctx, input.TxAsOf, "")
		if err != nil {
			return KGBundleToolOutput{Content: fmt.Sprintf("Error: %v", err)}, nil
		}
		b, err := v.Bundle(ctx, input.EID, BundleOptions{})
		if err != nil {
			return KGBundleToolOutput{Content: fmt.Sprintf("Error: %v", err)}, nil
		}
		if b == nil {
			return KGBundleToolOutput{Content: fmt.Sprintf("Error: e%d is not visible", input.EID)}, nil
		}
		data, err := json.Marshal(b)
		if err != nil {
			return KGBundleToolOutput{Content: fmt.Sprintf("Error: %v", err)}, nil
		}
		return KGBundleToolOutput{Content: string(data)}, nil
	case "import":
		var b Bundle
		if err := json.Unmarshal([]byte(input.Bundle), &b); err != nil {
			return KGBundleToolOutput{Content: fmt.Sprintf("Error: bundle is not valid JSON: %v", err)}, nil
		}
		author, session := txMeta(ctx)
		source := session
		if input.Source != "" {
			source = input.Source
		}
		var ids map[string]int64
		rep, err := kg.Transact(ctx, TxOptions{Author: author, Source: source, Reason: "import bundle"},
			func(w *KGTx) error {
				var err error
				ids, err = w.ImportBundle(&b)
				return err
			})
		if err != nil {
			return KGBundleToolOutput{Content: fmt.Sprintf("Error: %v", err)}, nil
		}
		return KGBundleToolOutput{Content: fmt.Sprintf("Imported %d statement(s): %d new, %d already known; root is e%d.",
			len(ids), len(rep.Asserted), len(rep.Existing), ids[b.Root.ID])}, nil
	}
	return KGBundleToolOutput{Content: "Error: action must be export or import"}, nil
}
