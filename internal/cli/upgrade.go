package cli

import (
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"runtime"

	"github.com/spf13/cobra"
)

const upgradeScriptURL = "https://raw.githubusercontent.com/dimetron/pi-go/main/scripts/install.sh"
const upgradeScriptURLWin = "https://raw.githubusercontent.com/dimetron/pi-go/main/scripts/install.ps1"

func newUpgradeCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "upgrade",
		Short: "Upgrade pi-go to the latest version",
		Long: `Downloads and runs the official install script to upgrade pi-go to the latest version.

The script detects your platform and installs the binary to the appropriate location.
Run with sudo if the default location requires elevated permissions.`,
		Args: cobra.NoArgs,
		RunE: runUpgrade,
	}
	return cmd
}

func runUpgrade(cmd *cobra.Command, _ []string) error {
	fmt.Fprintln(os.Stderr, "Upgrading pi-go...")

	var scriptURL string
	if runtime.GOOS == "windows" {
		scriptURL = upgradeScriptURLWin
		fmt.Fprintln(os.Stderr, "Detected Windows — using PowerShell install script.")
	} else {
		scriptURL = upgradeScriptURL
	}

	if runtime.GOOS == "windows" {
		return runUpgradePowerShell(scriptURL)
	}
	return runUpgradeShell(scriptURL)
}

func runUpgradeShell(scriptURL string) error {
	cmd := exec.Command("sh", "-c", fmt.Sprintf("curl -fsSL %s | bash", scriptURL))
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

func runUpgradePowerShell(scriptURL string) error {
	resp, err := http.Get(scriptURL)
	if err != nil {
		return fmt.Errorf("fetching install script: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("install script returned HTTP %d", resp.StatusCode)
	}

	tmp, err := os.CreateTemp("", "pi-upgrade-*.ps1")
	if err != nil {
		return fmt.Errorf("creating temp script: %w", err)
	}
	defer os.Remove(tmp.Name())
	defer tmp.Close()

	if _, err := tmp.ReadFrom(resp.Body); err != nil {
		return fmt.Errorf("reading script: %w", err)
	}

	execCmd := exec.Command("powershell.exe", "-ExecutionPolicy", "Bypass", "-File", tmp.Name())
	execCmd.Stdin = os.Stdin
	execCmd.Stdout = os.Stdout
	execCmd.Stderr = os.Stderr
	return execCmd.Run()
}
