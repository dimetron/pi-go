package main

import (
	"fmt"
	"os"

	"github.com/dimetron/pi-go/internal/sop"
)

// describeSOP prints the compiled shape of a built-in SOP.
func describeSOP(workDir, name string) int {
	def, err := sop.LoadDefinition(workDir, name)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	compiled, err := sop.Compile(def, sop.DescribeFactory{})
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	fmt.Print(compiled.Describe())
	return 0
}
