//go:build windows

package cli

import (
	"context"
	"errors"

	"github.com/dimetron/pi-go/internal/voicegemini"
	"github.com/dimetron/pi-go/internal/webserver"
)

// enableServeVoice refuses --voice on Windows before anything is resolved or
// dialed. The browser voice session drives pi through a PTY, and
// github.com/creack/pty has no Windows implementation, so the server side is
// compiled out (internal/webserver/voice_windows.go); failing here keeps the
// operator from exporting a key and opening a listener for a feature that
// cannot start.
func enableServeVoice(context.Context, *webserver.ServerV2, ...voicegemini.Option) error {
	return errors.New("--voice is not supported on Windows: browser voice drives pi through a PTY, which has no Windows implementation")
}
