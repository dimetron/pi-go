// Package webserver provides a web-based terminal interface for pi-go.
// It exposes the pi-go agent via a browser with xterm.js, authenticated
// through a pairing code flow.
// It exposes the pi-go agent via a browser with xterm.js, authenticated
// through a pairing code flow.
package webserver

import (
	"crypto/rand"
	"errors"
	"fmt"
	"math/big"
	"sync"
	"time"

	"github.com/google/uuid"
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

// defaultMaxApproveFailures is how many wrong codes pairing tolerates before
// it gives up entirely.
//
// A pairing code is 6 digits, so there are only 10^6 of them: an unthrottled
// /api/pair/submit is a puzzle an attacker solves in an afternoon. A timed
// lockout would only slow that down. Spending the budget instead destroys every
// pending code and signals the server to stop, so a guessing run gets three
// tries and then has nothing left to talk to.
//
// Three is deliberately below the threshold where a human is still fumbling: an
// operator mistyping a code they can see on screen gets a second chance, and a
// third, and that is enough. The cost of being wrong is a restart, not a
// lockout the attacker can also trigger to deny service.
const defaultMaxApproveFailures = 3

// ErrTooManyAttempts is returned by Approve once the failure budget is spent.
// It is distinct from a plain wrong-code error so callers and tests can tell a
// terminal refusal from an ordinary rejection.
var ErrTooManyAttempts = errors.New("too many pairing attempts")

// PairingManager handles pairing code generation, approval, and expiry.
type PairingManager struct {
	mu       sync.RWMutex
	timeout  time.Duration
	pending  map[string]*PendingPair  // code → pending info
	approved map[string]*ApprovedPair // token → approved info

	// Attempt limiting. maxFailures is a field rather than
	// constants only so tests can shorten them; nothing in production
	// overrides the defaults. failures counts rejected codes since the last
	// successful approval, and is deliberately *not* reset by CreatePair —
	// otherwise an attacker resets the budget by asking for a new code.
	maxFailures int
	failures    int
	lockedOut   bool
	lockedOutCh chan struct{}
	lockedOnce  sync.Once
}

// NewPairingManager creates a new PairingManager with the specified timeout.
func NewPairingManager(timeout time.Duration) *PairingManager {
	return &PairingManager{
		timeout:     timeout,
		pending:     make(map[string]*PendingPair),
		approved:    make(map[string]*ApprovedPair),
		maxFailures: defaultMaxApproveFailures,
		lockedOutCh: make(chan struct{}),
	}
}

// CreatePair generates a new 6-digit pairing code and returns the code,
// token.
func (pm *PairingManager) CreatePair(project string) (code, token string, err error) {
	return pm.CreatePairWithContext(project)
}

// CreatePairWithContext generates a pair. The context parameters the QR payload
// once needed are gone with it; the signature is kept so callers read the same.
func (pm *PairingManager) CreatePairWithContext(project string) (code, token string, err error) {
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
			return "", "", fmt.Errorf("generating code: %w", err)
		}
		code = fmt.Sprintf("%06d", codeNum.Int64())
		if _, exists := pm.pending[code]; !exists {
			break
		}
		code = ""
	}
	pm.mu.Unlock()
	if code == "" {
		return "", "", fmt.Errorf("generating code: exhausted %d retries", maxAttempts)
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

	return code, token, nil
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
// invalidated and every later attempt is refused, including a correct code.
func (pm *PairingManager) Approve(code string) (string, error) {
	pm.mu.Lock()
	defer pm.mu.Unlock()

	now := time.Now()
	if pm.lockedOut {
		return "", ErrTooManyAttempts
	}

	pp, ok := pm.pending[code]
	if !ok {
		return "", pm.recordFailure(fmt.Errorf("code not found"))
	}

	if now.After(pp.ExpiresAt) {
		delete(pm.pending, code)
		return "", pm.recordFailure(fmt.Errorf("code expired"))
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
// codes being ground against are destroyed, not merely slowed — and closes the
// lockout channel so the server can stop. Callers must hold pm.mu.
func (pm *PairingManager) recordFailure(cause error) error {
	pm.failures++
	if pm.failures < pm.maxFailures {
		return cause
	}

	pm.pending = make(map[string]*PendingPair)
	pm.lockedOut = true
	// Closing under pm.mu is safe: nothing in the close path takes the lock.
	pm.lockedOnce.Do(func() { close(pm.lockedOutCh) })
	return ErrTooManyAttempts
}

// LockedOut returns a channel closed once pairing has spent its failure
// budget. The server selects on it to shut down, which is what makes three
// attempts a hard stop rather than a pause an attacker can wait out.
func (pm *PairingManager) LockedOut() <-chan struct{} {
	return pm.lockedOutCh
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
}

// StatusResponse represents the API response for status check.
type StatusResponse struct {
	Status    PairStatus `json:"status"`
	SessionID string     `json:"sessionID,omitempty"`
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
