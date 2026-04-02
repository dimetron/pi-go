package subagent

import "os/exec"

// setPlatformAttrs is a no-op on Windows; exec.CommandContext already
// kills the process when the context is cancelled.
func setPlatformAttrs(cmd *exec.Cmd) {
	// On Windows there is no process-group signal mechanism equivalent
	// to Unix Setpgid + Kill(-pgid). The default CommandContext
	// behaviour (TerminateProcess) is sufficient.
}
