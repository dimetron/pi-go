// Shared helpers for the cli package tests.
package cli

import (
	"testing"
	"time"
)

// resetGlobalFlags clears package-level CLI flags so tests do not leak
// state into one another.
func resetGlobalFlags(t *testing.T) {
	t.Helper()
	orig := struct {
		model, mode, session, socket, url, system, pprof, pprofPort string
		headers                                                     []string
		cont, insecure, smol, slow, plan, memOff                    bool
		loginModel                                                  string
		serveAddr, serveProject, serveModel, serveURL               string
		serveHeaders                                                []string
		servePairing                                                time.Duration
		serveInsecure                                               bool
	}{
		flagModel, flagMode, flagSession, flagSocket, flagURL, flagSystem, flagPprof, flagPprofPort,
		flagHeaders,
		flagContinue, flagInsecure, flagSmol, flagSlow, flagPlan, flagMemoryOff,
		flagLoginModel,
		flagServeAddr, flagServeProject, flagServeModel, flagServeURL,
		flagServeHeaders,
		flagServePairingTimeout,
		flagServeInsecure,
	}
	t.Cleanup(func() {
		flagModel = orig.model
		flagMode = orig.mode
		flagSession = orig.session
		flagSocket = orig.socket
		flagURL = orig.url
		flagSystem = orig.system
		flagPprof = orig.pprof
		flagPprofPort = orig.pprofPort
		flagHeaders = orig.headers
		flagContinue = orig.cont
		flagInsecure = orig.insecure
		flagSmol = orig.smol
		flagSlow = orig.slow
		flagPlan = orig.plan
		flagMemoryOff = orig.memOff
		flagLoginModel = orig.loginModel
		flagServeAddr = orig.serveAddr
		flagServeProject = orig.serveProject
		flagServeModel = orig.serveModel
		flagServeURL = orig.serveURL
		flagServeHeaders = orig.serveHeaders
		flagServePairingTimeout = orig.servePairing
		flagServeInsecure = orig.serveInsecure
	})

	flagModel = ""
	flagMode = ""
	flagSession = ""
	flagSocket = "/tmp/pi-go.sock"
	flagURL = ""
	flagSystem = ""
	flagPprof = ""
	flagPprofPort = "6060"
	flagHeaders = nil
	flagContinue = false
	flagInsecure = false
	flagSmol = false
	flagSlow = false
	flagPlan = false
	flagMemoryOff = false
	flagLoginModel = ""
	flagServeAddr = ":8080"
	flagServeProject = ""
	flagServeModel = ""
	flagServeURL = ""
	flagServeHeaders = nil
	flagServePairingTimeout = 5 * time.Minute
	flagServeInsecure = false
}

// nowJSON returns current time in RFC3339 as a JSON string.
func nowJSON() string {
	return `"` + nowTimeStr() + `"`
}

func nowTimeStr() string {
	return time.Now().Format(time.RFC3339)
}
