package palace

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
)

// Crypto-shredding, after Tiramemsu's time-model.md#Erasure. A literal object
// of a sensitive predicate is stored as a SEALED dictionary term encrypted
// with its data subject's key; erasing the subject destroys the key. No row is
// deleted, so never-forget still holds: statements, eids, times and history
// stay, and the sealed values decode as "sys:Erased".
//
// The cipher is deterministic, so one value of one subject is always the same
// term and assert stays idempotent: a synthetic IV (the first 12 bytes of
// HMAC-SHA256 under the MAC half of the key) feeds AES-256-GCM under the
// encryption half, with the data subject as associated data. Only the standard
// library is used.

// Errors of erasure.
var (
	ErrErased = errors.New("palace: kg: the data subject has been erased")
)

// ErasedText is how a sealed value decodes once its key is destroyed.
const ErasedText = "sys:Erased"

const sealKeySize = 64 // 32 bytes AES-256, 32 bytes HMAC-SHA256

// dataSubject follows statement subjects down to the node a statement is
// ultimately about. Annotations of a sensitive fact therefore share its key.
func dataSubject(ctx context.Context, q querier, s objectID) (objectID, error) {
	for depth := 0; s.tag() == tagStmt; depth++ {
		if depth > 64 {
			return 0, errors.New("palace: kg: statement subject chain too deep")
		}
		var next objectID
		if err := q.QueryRowContext(ctx, "SELECT s FROM kg_stmt WHERE eid = ?", s.payload()).Scan(&next); err != nil {
			return 0, fmt.Errorf("palace: kg: data subject of e%d: %w", s.payload(), err)
		}
		s = next
	}
	return s, nil
}

// readSealKey returns subject's key; destroyed is set once it was erased.
func readSealKey(ctx context.Context, q querier, subject objectID) (key []byte, found, destroyed bool, err error) {
	var d *int64
	err = q.QueryRowContext(ctx, "SELECT key, destroyed_t FROM kg_seal_key WHERE subject = ?", subject).Scan(&key, &d)
	if errNoRows(err) {
		return nil, false, false, nil
	}
	if err != nil {
		return nil, false, false, fmt.Errorf("palace: kg: read seal key: %w", err)
	}
	return key, true, d != nil, nil
}

// seal interns literal o as a sealed term under the key of s's data subject,
// creating the key on first use.
func (w *KGTx) seal(s objectID, o Value) (objectID, error) {
	subject, err := dataSubject(w.ctx, w.q, s)
	if err != nil {
		return 0, err
	}
	key, found, destroyed, err := readSealKey(w.ctx, w.q, subject)
	if err != nil {
		return 0, err
	}
	if destroyed {
		return 0, fmt.Errorf("%w: %d", ErrErased, subject)
	}
	if !found {
		key = make([]byte, sealKeySize)
		if _, err := rand.Read(key); err != nil {
			return 0, fmt.Errorf("palace: kg: new seal key: %w", err)
		}
		if _, err := w.q.ExecContext(w.ctx,
			"INSERT INTO kg_seal_key (subject, key, created_t) VALUES (?, ?, ?)", subject, key, w.t); err != nil {
			return 0, fmt.Errorf("palace: kg: store seal key: %w", err)
		}
	}
	lex, err := sealLex(key, subject, o)
	if err != nil {
		return 0, err
	}
	oo, _, err := internTerm(w.ctx, w.q, tagSealed, lex, nil, true)
	return oo, err
}

// sealedLookup returns the id of the sealed term v would be under subject's
// key, without creating anything. A read uses it to match a constant.
func sealedLookup(ctx context.Context, q querier, s objectID, v Value) (objectID, bool, error) {
	subject, err := dataSubject(ctx, q, s)
	if err != nil {
		return 0, false, err
	}
	key, found, destroyed, err := readSealKey(ctx, q, subject)
	if err != nil || !found || destroyed {
		return 0, false, err
	}
	lex, err := sealLex(key, subject, v)
	if err != nil {
		return 0, false, err
	}
	return internTerm(ctx, q, tagSealed, lex, nil, false)
}

func sealLex(key []byte, subject objectID, v Value) (string, error) {
	pt, err := json.Marshal(v)
	if err != nil {
		return "", fmt.Errorf("palace: kg: seal: %w", err)
	}
	gcm, err := sealCipher(key)
	if err != nil {
		return "", err
	}
	mac := hmac.New(sha256.New, key[32:])
	mac.Write(pt)
	iv := mac.Sum(nil)[:gcm.NonceSize()]
	aad := []byte(strconv.FormatInt(int64(subject), 10))
	ct := gcm.Seal(nil, iv, pt, aad)
	return string(aad) + ":" + base64.RawStdEncoding.EncodeToString(append(iv, ct...)), nil
}

func sealCipher(key []byte) (cipher.AEAD, error) {
	if len(key) != sealKeySize {
		return nil, errors.New("palace: kg: bad seal key")
	}
	block, err := aes.NewCipher(key[:32])
	if err != nil {
		return nil, fmt.Errorf("palace: kg: seal cipher: %w", err)
	}
	return cipher.NewGCM(block)
}

// openSealed decrypts a sealed term's lex. A destroyed or missing key gives
// the erased marker.
func openSealed(ctx context.Context, q querier, lex string, keys map[objectID][]byte) (Value, error) {
	subjText, b64, ok := strings.Cut(lex, ":")
	if !ok {
		return Value{}, errors.New("palace: kg: malformed sealed term")
	}
	n, err := strconv.ParseInt(subjText, 10, 64)
	if err != nil {
		return Value{}, fmt.Errorf("palace: kg: malformed sealed term: %w", err)
	}
	subject := objectID(n)
	key, cached := keys[subject]
	if !cached {
		k, found, destroyed, err := readSealKey(ctx, q, subject)
		if err != nil {
			return Value{}, err
		}
		if found && !destroyed {
			key = k
		}
		keys[subject] = key
	}
	if key == nil {
		return Str(ErasedText), nil
	}
	raw, err := base64.RawStdEncoding.DecodeString(b64)
	if err != nil {
		return Value{}, fmt.Errorf("palace: kg: malformed sealed term: %w", err)
	}
	gcm, err := sealCipher(key)
	if err != nil {
		return Value{}, err
	}
	if len(raw) < gcm.NonceSize() {
		return Value{}, errors.New("palace: kg: malformed sealed term")
	}
	pt, err := gcm.Open(nil, raw[:gcm.NonceSize()], raw[gcm.NonceSize():], []byte(subjText))
	if err != nil {
		return Value{}, fmt.Errorf("palace: kg: unseal: %w", err)
	}
	var v Value
	if err := json.Unmarshal(pt, &v); err != nil {
		return Value{}, fmt.Errorf("palace: kg: unseal: %w", err)
	}
	return v, nil
}

// isSensitive reports whether predicate p is marked sensitive.
func isSensitive(ctx context.Context, q querier, p objectID) (bool, error) {
	sch, _, err := readSchema(ctx, q, p)
	return sch.Sensitive, err
}

// Erase destroys subject's seal key in this transaction. Every sealed value
// of the subject, and of every statement about its statements, decodes as
// sys:Erased from now on. Saved answers are marked stale and lose their stored
// rows, since those may hold decrypted values. It returns false when the
// subject has no live key.
func (w *KGTx) Erase(subject Value) (bool, error) {
	so, ok, err := encodeValue(w.ctx, w.q, subject, false)
	if err != nil || !ok {
		return false, err
	}
	res, err := w.q.ExecContext(w.ctx,
		"UPDATE kg_seal_key SET key = zeroblob(64), destroyed_t = ? WHERE subject = ? AND destroyed_t IS NULL", w.t, so)
	if err != nil {
		return false, fmt.Errorf("palace: kg: erase: %w", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return false, nil
	}
	if _, err := w.q.ExecContext(w.ctx,
		"UPDATE kg_saved_answer SET result = 'null', stale = ?", fmt.Sprintf("erased at tx%d", w.t)); err != nil {
		return false, fmt.Errorf("palace: kg: erase saved answers: %w", err)
	}
	w.report.Erased = append(w.report.Erased, subject.String())
	return true, nil
}

// EraseReport is the result of KnowledgeGraph.Erase.
type EraseReport struct {
	Tx *TxReport `json:"tx"`
	// Erased is false when the subject had no live key.
	Erased bool `json:"erased"`
	// Checkpointed is true when the WAL was checkpointed and truncated after
	// the commit, so the old key bytes are gone from the file. A busy reader
	// can prevent that; the bytes then leave at the next checkpoint.
	Checkpointed bool `json:"checkpointed"`
}

// Erase destroys subject's key in its own transaction and then checkpoints
// the WAL, so that the destroyed key leaves the file (secure_delete zeroes
// freed pages). Backups taken earlier still hold the key; that is documented,
// not solved.
func (kg *KnowledgeGraph) Erase(ctx context.Context, opts TxOptions, subject string) (*EraseReport, error) {
	out := &EraseReport{}
	rep, err := kg.Transact(ctx, opts, func(w *KGTx) error {
		ok, err := w.Erase(Ent(subject))
		out.Erased = ok
		return err
	})
	if err != nil {
		return nil, err
	}
	out.Tx = rep
	if opts.DryRun || !out.Erased {
		return out, nil
	}
	db, err := kg.database()
	if err != nil {
		return nil, err
	}
	var busy, logFrames, done int64
	if err := db.QueryRowContext(ctx, "PRAGMA wal_checkpoint(TRUNCATE)").Scan(&busy, &logFrames, &done); err != nil {
		return out, nil //nolint:nilerr // the erasure committed; the checkpoint is best effort
	}
	out.Checkpointed = busy == 0
	return out, nil
}
