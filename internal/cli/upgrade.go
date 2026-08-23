package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/dimetron/pi-go/internal/notice"
)

const upgradeScriptURL = "https://raw.githubusercontent.com/dimetron/pi-go/main/scripts/install.sh"
const upgradeScriptURLWin = "https://raw.githubusercontent.com/dimetron/pi-go/main/scripts/install.ps1"
const latestReleaseURL = "https://api.github.com/repos/dimetron/pi-go/releases/latest"

type releaseInfo struct {
	TagName string `json:"tag_name"`
}

func checkForUpdate(ctx context.Context, currentVersion string) {
	if currentVersion == "" || currentVersion == "dev" || os.Getenv("PI_GO_UPDATE_CHECK") == "0" {
		return
	}

	ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()

	latest, err := fetchLatestVersion(ctx, http.DefaultClient, latestReleaseURL)
	if err != nil {
		return
	}
	if isNewerVersion(currentVersion, latest) {
		notice.Notifyf("update available: %s -> %s (run `pi upgrade`)", currentVersion, latest)
	}
}

func fetchLatestVersion(ctx context.Context, client *http.Client, url string) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return "", fmt.Errorf("creating release request: %w", err)
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("User-Agent", "pi-go/"+versionString())

	resp, err := client.Do(req)
	if err != nil {
		return "", fmt.Errorf("fetching latest release: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("latest release returned HTTP %d", resp.StatusCode)
	}

	var release releaseInfo
	if err := json.NewDecoder(resp.Body).Decode(&release); err != nil {
		return "", fmt.Errorf("decoding latest release: %w", err)
	}
	if release.TagName == "" {
		return "", fmt.Errorf("latest release missing tag_name")
	}
	return release.TagName, nil
}

func isNewerVersion(current, latest string) bool {
	currentParts := parseVersionParts(current)
	latestParts := parseVersionParts(latest)
	if len(currentParts) == 0 || len(latestParts) == 0 {
		return false
	}
	maxLen := len(currentParts)
	if len(latestParts) > maxLen {
		maxLen = len(latestParts)
	}
	for i := 0; i < maxLen; i++ {
		currentPart := 0
		if i < len(currentParts) {
			currentPart = currentParts[i]
		}
		latestPart := 0
		if i < len(latestParts) {
			latestPart = latestParts[i]
		}
		if latestPart != currentPart {
			return latestPart > currentPart
		}
	}
	return false
}

func parseVersionParts(version string) []int {
	version = normalizeVersion(version)
	if version == "" {
		return nil
	}
	parts := strings.Split(version, ".")
	parsed := make([]int, 0, len(parts))
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part == "" {
			return nil
		}
		n, err := strconv.Atoi(part)
		if err != nil {
			return nil
		}
		parsed = append(parsed, n)
	}
	return parsed
}

func normalizeVersion(version string) string {
	version = strings.TrimSpace(version)
	version = strings.TrimPrefix(version, "v")
	if i := strings.Index(version, "+"); i >= 0 {
		version = version[:i]
	}
	return version
}

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
