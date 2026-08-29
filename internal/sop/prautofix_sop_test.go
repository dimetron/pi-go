package sop

import "testing"

func TestPRAutofixSOPLints(t *testing.T) {
	def, err := LoadEmbeddedDefinition("pr-autofix")
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if def.SOP != "pr-autofix" {
		t.Fatalf("sop name = %q", def.SOP)
	}
	if f := LintDefinition(def); !f.OK() {
		t.Fatalf("lint findings:\n%s", f.Format())
	}
	if _, err := Compile(def, DescribeFactory{}); err != nil {
		t.Fatalf("compile: %v", err)
	}
}
