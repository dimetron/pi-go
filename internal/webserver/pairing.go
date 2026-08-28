// Package webserver provides a web-based terminal interface for pi-go.
// It exposes the pi-go agent via a browser with xterm.js, authenticated
// through a pairing code flow.
// It exposes the pi-go agent via a browser with xterm.js, authenticated
// through a pairing code flow.
package webserver

import (
	"bytes"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	qrcode "github.com/skip2/go-qrcode"
)

// PairStatus represents the status of a pairing request.
type PairStatus string

const (
	PairStatusPending  PairStatus = "pending"
	PairStatusApproved PairStatus = "approved"
	PairStatusExpired  PairStatus = "expired"
	PairStatusUnknown  PairStatus = "unknown"
)

// PendingPair represents a pending pairing request.
type PendingPair struct {
	Code      string
	CreatedAt time.Time
	ExpiresAt time.Time
	Project   string
	Token     string // browser token to approve
}

// ApprovedPair represents an approved pairing token.
type ApprovedPair struct {
	Token      string
	Project    string
	ApprovedAt time.Time
}

// Default attempt limiting for Approve. A pairing code is 6 digits, so there
// are only 10^6 of them: an unthrottled /api/pair/submit is a puzzle an
// attacker solves in an afternoon. Spending the budget destroys every pending
// code and refuses submissions for the lockout window, which puts a guessing
// run back into the months.
const (
	defaultMaxApproveFailures = 5
	defaultApproveLockout     = time.Minute
)

// ErrTooManyAttempts is returned by Approve when the failure budget is spent
// and pairing is locked out. It is distinct from a plain wrong-code error so
// callers and tests can tell throttling from rejection.
var ErrTooManyAttempts = errors.New("too many pairing attempts")

// PairingManager handles pairing code generation, approval, and expiry.
type PairingManager struct {
	mu       sync.RWMutex
	timeout  time.Duration
	pending  map[string]*PendingPair  // code → pending info
	approved map[string]*ApprovedPair // token → approved info

	// Attempt limiting. maxFailures and lockout are fields rather than
	// constants only so tests can shorten them; nothing in production
	// overrides the defaults. failures counts rejected codes since the last
	// successful approval, and is deliberately *not* reset by CreatePair —
	// otherwise an attacker resets the budget by asking for a new code.
	maxFailures int
	lockout     time.Duration
	failures    int
	lockedUntil time.Time
}

// NewPairingManager creates a new PairingManager with the specified timeout.
func NewPairingManager(timeout time.Duration) *PairingManager {
	return &PairingManager{
		timeout:     timeout,
		pending:     make(map[string]*PendingPair),
		approved:    make(map[string]*ApprovedPair),
		maxFailures: defaultMaxApproveFailures,
		lockout:     defaultApproveLockout,
	}
}

// CreatePair generates a new 6-digit pairing code and returns the code,
// token, and QR data.
func (pm *PairingManager) CreatePair(project string) (code, token string, qrData []byte, err error) {
	return pm.CreatePairWithContext(project, "pi-go", "")
}

// CreatePairWithContext generates a pair and embeds server context in the QR payload.
func (pm *PairingManager) CreatePairWithContext(project, serverHost, pairURL string) (code, token string, qrData []byte, err error) {
	// Generate a 6-digit code that doesn't collide with one already in flight.
	// With 1M possible codes the per-call collision chance is tiny, but it is
	// observable in tight test loops; retrying up to a small cap absorbs it
	// without changing the visible behavior.
	const maxAttempts = 8
	pm.mu.Lock()
	var codeNum *big.Int
	for i := 0; i < maxAttempts; i++ {
		codeNum, err = rand.Int(rand.Reader, big.NewInt(1000000))
		if err != nil {
			pm.mu.Unlock()
			return "", "", nil, fmt.Errorf("generating code: %w", err)
		}
		code = fmt.Sprintf("%06d", codeNum.Int64())
		if _, exists := pm.pending[code]; !exists {
			break
		}
		code = ""
	}
	pm.mu.Unlock()
	if code == "" {
		return "", "", nil, fmt.Errorf("generating code: exhausted %d retries", maxAttempts)
	}

	// Generate token
	token = uuid.New().String()

	now := time.Now()
	pp := &PendingPair{
		Code:      code,
		CreatedAt: now,
		ExpiresAt: now.Add(pm.timeout),
		Project:   project,
		Token:     token,
	}

	pm.mu.Lock()
	pm.pending[code] = pp
	pm.mu.Unlock()

	if strings.TrimSpace(serverHost) == "" {
		serverHost = "pi-go"
	}

	qrPayload, err := buildQRPayload(code, serverHost, pairURL)
	if err != nil {
		return "", "", nil, fmt.Errorf("encoding QR data: %w", err)
	}

	qrData, err = GenerateQRCode(string(qrPayload))
	if err != nil {
		return "", "", nil, fmt.Errorf("generating QR image: %w", err)
	}

	return code, token, qrData, nil
}

// CheckStatus returns the current status of a pairing token.
func (pm *PairingManager) CheckStatus(token string) (PairStatus, error) {
	pm.mu.RLock()
	defer pm.mu.RUnlock()

	// Check if approved
	if _, ok := pm.approved[token]; ok {
		return PairStatusApproved, nil
	}

	// Check pending codes
	for _, pp := range pm.pending {
		if pp.Token == token {
			if time.Now().After(pp.ExpiresAt) {
				return PairStatusExpired, nil
			}
			return PairStatusPending, nil
		}
	}

	return PairStatusUnknown, fmt.Errorf("token not found")
}

// Approve approves a pairing code and returns the associated token. Failed
// attempts are counted: once the budget is spent every pending pair is
// invalidated and further attempts are refused for the lockout window.
func (pm *PairingManager) Approve(code string) (string, error) {
	pm.mu.Lock()
	defer pm.mu.Unlock()

	now := time.Now()
	if now.Before(pm.lockedUntil) {
		return "", ErrTooManyAttempts
	}

	pp, ok := pm.pending[code]
	if !ok {
		return "", pm.recordFailure(now, fmt.Errorf("code not found"))
	}

	if now.After(pp.ExpiresAt) {
		delete(pm.pending, code)
		return "", pm.recordFailure(now, fmt.Errorf("code expired"))
	}

	// Create approved entry
	ap := &ApprovedPair{
		Token:      pp.Token,
		Project:    pp.Project,
		ApprovedAt: time.Now(),
	}
	pm.approved[pp.Token] = ap

	// Remove from pending
	delete(pm.pending, code)

	// A code that landed clears the budget: the operator is demonstrably in
	// the loop, so a later typo should not inherit an attacker's failures.
	pm.failures = 0

	return pp.Token, nil
}

// recordFailure counts one rejected code and returns the error the caller
// should report. Once the budget is spent it drops every pending pair — the
// codes being ground against are destroyed, not merely slowed — and opens a
// lockout window. Callers must hold pm.mu.
func (pm *PairingManager) recordFailure(now time.Time, cause error) error {
	pm.failures++
	if pm.failures < pm.maxFailures {
		return cause
	}

	pm.pending = make(map[string]*PendingPair)
	pm.failures = 0
	pm.lockedUntil = now.Add(pm.lockout)
	return ErrTooManyAttempts
}

// IsApproved checks if a token has been approved.
func (pm *PairingManager) IsApproved(token string) bool {
	pm.mu.RLock()
	defer pm.mu.RUnlock()

	_, ok := pm.approved[token]
	return ok
}

// GetProject returns the project path for an approved token.
func (pm *PairingManager) GetProject(token string) (string, error) {
	pm.mu.RLock()
	defer pm.mu.RUnlock()

	ap, ok := pm.approved[token]
	if !ok {
		return "", fmt.Errorf("token not approved")
	}

	return ap.Project, nil
}

// CleanupExpired removes expired pending codes.
func (pm *PairingManager) CleanupExpired() {
	pm.mu.Lock()
	defer pm.mu.Unlock()

	now := time.Now()
	for code, pp := range pm.pending {
		if now.After(pp.ExpiresAt) {
			delete(pm.pending, code)
		}
	}
}

// PairResponse is the JSON body returned by the pairing endpoints. It
// deliberately carries no token: /api/pair answers unauthenticated callers, so
// anything in here is public. The token stays server-side and reaches the
// operator out-of-band (the startup banner) and the browser as the pi_token
// cookie set once a code is submitted. The internal view that does carry the
// token is ServerV2.activePair.
type PairResponse struct {
	Code string `json:"code"`
	QR   string `json:"qr"` // base64 encoded PNG image
}

// StatusResponse represents the API response for status check.
type StatusResponse struct {
	Status    PairStatus `json:"status"`
	SessionID string     `json:"sessionID,omitempty"`
}

// GenerateQRCode generates a QR code image for the pairing data.
// Returns PNG data or an error.
func GenerateQRCode(data string) ([]byte, error) {
	png, err := qrcode.Encode(data, qrcode.Medium, 256)
	if err != nil {
		return nil, fmt.Errorf("encode QR PNG: %w", err)
	}
	if !bytes.HasPrefix(png, []byte("\x89PNG\r\n\x1a\n")) {
		return nil, fmt.Errorf("generated QR is not valid PNG")
	}
	return png, nil
}

// BuildPairQRCode builds a QR PNG for a pairing code and server context. The
// token is not part of the payload: the QR is rendered into the unauthenticated
// /api/pair response, so embedding the token there would leak it just as surely
// as a "token" JSON field.
func BuildPairQRCode(code, serverHost, pairURL string) ([]byte, error) {
	payload, err := buildQRPayload(code, serverHost, pairURL)
	if err != nil {
		return nil, fmt.Errorf("encoding QR payload: %w", err)
	}
	return GenerateQRCode(string(payload))
}

// ValidateCode checks if a code is valid (6 digits).
func ValidateCode(code string) bool {
	if len(code) != 6 {
		return false
	}
	for _, c := range code {
		if c < '0' || c > '9' {
			return false
		}
	}
	return true
}

// ParseQRData parses QR code data and extracts code and token. Payloads built
// by buildQRPayload no longer carry a token, so token comes back empty for
// them; the "code:token" form is still parsed for older clients.
func ParseQRData(data string) (code, token string, err error) {
	var qrInfo map[string]string
	if err := json.Unmarshal([]byte(data), &qrInfo); err != nil {
		// Try parsing as plain "code:token"
		parts := strings.Split(data, ":")
		if len(parts) == 2 {
			return parts[0], parts[1], nil
		}
		return "", "", fmt.Errorf("parsing QR data: %w", err)
	}
	return qrInfo["code"], qrInfo["token"], nil
}

func buildQRPayload(code, serverHost, pairURL string) ([]byte, error) {
	qrInfo := map[string]string{
		"code":   code,
		"server": serverHost,
	}
	if strings.TrimSpace(pairURL) != "" {
		qrInfo["url"] = pairURL
	}
	return json.Marshal(qrInfo)
}
