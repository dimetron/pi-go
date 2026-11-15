//go:build !unix

package provider

import "errors"

// launchFoundationModelsServer is unsupported off Unix: `fm` is Apple's
// Foundation Models CLI and ships only for macOS, so there is no server to
// start. The provider itself still builds and resolves on every platform — a
// session recorded on a Mac stays readable elsewhere — only the lazy autostart
// is unavailable.
func launchFoundationModelsServer() (<-chan error, error) {
	return nil, errors.New("automatic `fm serve` start is only supported on macOS")
}
