package palace

import (
	"context"
	"fmt"
	"strings"

	"google.golang.org/adk/tool"
	"google.golang.org/adk/tool/functiontool"
)

// StatusInput is intentionally empty — palace-status takes no parameters.
type StatusInput struct{}

// StatusOutput contains the formatted palace status.
type StatusOutput struct {
	Content string `json:"content"`
}

func newPalaceStatusTool(p *Palace) (tool.Tool, error) {
	return functiontool.New(functiontool.Config{
		Name:        "palace-status",
		Description: "Show the current state of the memory palace: drawer count, wings, rooms, knowledge graph stats, and model status.",
	}, func(ctx tool.Context, _ StatusInput) (StatusOutput, error) {
		return palaceStatusHandler(ctx, p)
	})
}

func palaceStatusHandler(ctx context.Context, p *Palace) (StatusOutput, error) {
	st, err := p.Status(ctx)
	if err != nil {
		return StatusOutput{Content: fmt.Sprintf("Error: %v", err)}, nil
	}

	var sb strings.Builder
	fmt.Fprintf(&sb, "## Palace Status\n\n")
	fmt.Fprintf(&sb, "| Metric | Value |\n|--------|-------|\n")
	fmt.Fprintf(&sb, "| Drawers | %d |\n", st.DrawerCount)
	fmt.Fprintf(&sb, "| Wings | %d |\n", st.WingCount)
	fmt.Fprintf(&sb, "| Rooms | %d |\n", st.RoomCount)
	fmt.Fprintf(&sb, "| Model loaded | %v |\n", st.ModelLoaded)

	if st.KG != nil {
		fmt.Fprintf(&sb, "\n### Knowledge Graph\n\n")
		fmt.Fprintf(&sb, "| Metric | Value |\n|--------|-------|\n")
		fmt.Fprintf(&sb, "| Entities | %d |\n", st.KG.EntityCount)
		fmt.Fprintf(&sb, "| Triples | %d |\n", st.KG.TripleCount)
		fmt.Fprintf(&sb, "| Active triples | %d |\n", st.KG.ActiveTriples)
		if len(st.KG.Predicates) > 0 {
			fmt.Fprintf(&sb, "| Predicates | %s |\n", strings.Join(st.KG.Predicates, ", "))
		}
	}

	return StatusOutput{Content: sb.String()}, nil
}
