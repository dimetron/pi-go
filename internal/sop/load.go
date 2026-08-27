package sop

import (
	_ "embed"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"

	"github.com/dimetron/pi-go/internal/sop/validate"
)

//go:embed plan.sop.yaml
var defaultPlanSOP []byte

//go:embed run.sop.yaml
var defaultRunSOP []byte

// LoadDefinition returns the declarative SOP named `name` ("plan" or "run").
//
// Resolution mirrors LoadPDD: project .pi-go/sops/<name>.sop.yaml → global
// ~/.pi-go/sops/<name>.sop.yaml → embedded default. An override that fails to
// parse or lint is reported rather than silently ignored — a SOP that does not
// compile is a SOP that would schedule nothing.
func LoadDefinition(workDir, name string) (*Definition, error) {
	data, source := resolveDefinition(workDir, name)
	def, err := ParseDefinition(data)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", source, err)
	}
	if findings := LintDefinition(def); !findings.OK() {
		return nil, fmt.Errorf("%s is not a valid SOP:\n%s", source, findings.Format())
	}
	slog.Debug("loaded declarative SOP", "name", name, "source", source, "stages", len(def.Stages))
	return def, nil
}

// LintDefinitionFile parses and lints a SOP file without installing it, so a
// user editing an override can check it.
func LintDefinitionFile(path string) (validate.Findings, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	def, err := ParseDefinition(data)
	if err != nil {
		return nil, err
	}
	return LintDefinition(def), nil
}

func resolveDefinition(workDir, name string) (data []byte, source string) {
	file := name + ".sop.yaml"

	projectPath := filepath.Join(workDir, ".pi-go", "sops", file)
	if b, err := os.ReadFile(projectPath); err == nil {
		return b, projectPath
	}
	if home, err := os.UserHomeDir(); err == nil {
		globalPath := filepath.Join(home, ".pi-go", "sops", file)
		if b, err := os.ReadFile(globalPath); err == nil {
			return b, globalPath
		}
	}
	switch name {
	case "run":
		return defaultRunSOP, "embedded run.sop.yaml"
	default:
		return defaultPlanSOP, "embedded plan.sop.yaml"
	}
}
