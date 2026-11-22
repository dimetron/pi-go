package palace

import (
	"context"
	"errors"
	"fmt"
	"time"
)

// Volatile state, after Tiramemsu's volatile-state capability: high-churn
// values (last seen, counters, per-turn scores) that do not deserve history.
// They are plain upserts outside the graph: no eid, no valid time, no layers,
// no events, and visible only in the now view. Writes are part of their
// transaction, so a failed or dry-run transaction leaves them unchanged.

// ErrSensitiveVolatile is returned for a volatile write on a sensitive key:
// it would store plaintext that erasure cannot destroy.
var ErrSensitiveVolatile = errors.New("palace: kg: a sensitive predicate cannot hold volatile values")

func (w *KGTx) volatileKey(s, key Value) (objectID, objectID, error) {
	if s.Kind != KindEntity && s.Kind != KindStatement {
		return 0, 0, fmt.Errorf("palace: kg: volatile subject must be an entity or a statement, got %s", s)
	}
	if key.Kind != KindEntity || isEnginePredicate(key) {
		return 0, 0, fmt.Errorf("palace: kg: volatile key must be a user entity, got %s", key)
	}
	so, _, err := encodeValue(w.ctx, w.q, s, true)
	if err != nil {
		return 0, 0, err
	}
	ko, _, err := encodeValue(w.ctx, w.q, key, true)
	if err != nil {
		return 0, 0, err
	}
	return so, ko, nil
}

// SetVolatile sets (s, key) to o, replacing any previous value. updated_at is
// this transaction's instant.
func (w *KGTx) SetVolatile(s, key, o Value) error {
	so, ko, err := w.volatileKey(s, key)
	if err != nil {
		return err
	}
	if sens, err := isSensitive(w.ctx, w.q, ko); err != nil {
		return err
	} else if sens {
		return fmt.Errorf("%w: %s", ErrSensitiveVolatile, key)
	}
	oo, _, err := encodeValue(w.ctx, w.q, o, true)
	if err != nil {
		return err
	}
	_, err = w.q.ExecContext(w.ctx, `INSERT INTO kg_volatile (s, key, o, updated_at) VALUES (?, ?, ?, ?)
		ON CONFLICT (s, key) DO UPDATE SET o = excluded.o, updated_at = excluded.updated_at`, so, ko, oo, w.instant)
	if err != nil {
		return fmt.Errorf("palace: kg: set volatile: %w", err)
	}
	return nil
}

// ClearVolatile removes (s, key). Clearing an absent value succeeds.
func (w *KGTx) ClearVolatile(s, key Value) error {
	so, ok1, err := encodeValue(w.ctx, w.q, s, false)
	if err != nil {
		return err
	}
	ko, ok2, err := encodeValue(w.ctx, w.q, key, false)
	if err != nil || !ok1 || !ok2 {
		return err
	}
	if _, err := w.q.ExecContext(w.ctx, "DELETE FROM kg_volatile WHERE s = ? AND key = ?", so, ko); err != nil {
		return fmt.Errorf("palace: kg: clear volatile: %w", err)
	}
	return nil
}

// Resolved is one value of a subject's key and where it came from.
type Resolved struct {
	Value Value `json:"value"`
	// EID is the statement that holds it; 0 for a volatile value.
	EID int64 `json:"eid,omitempty"`
	// UpdatedAt is set for a volatile value.
	UpdatedAt *time.Time `json:"updated_at,omitempty"`
}

// Resolve returns the values of s's key in v: the objects of the statements
// (s key ?) the view selects; if there are none and v is the plain now view,
// the volatile value. Under time travel volatile values are absent, never
// wrong.
func (v View) Resolve(ctx context.Context, s, key Value) ([]Resolved, error) {
	var out []Resolved
	err := v.kg.read(ctx, func(q querier) error {
		sts, err := v.match(ctx, q, Pattern{Subject: &s, Predicate: &key})
		if err != nil {
			return err
		}
		for _, st := range sts {
			out = append(out, Resolved{Value: st.Object, EID: st.EID})
		}
		if len(out) > 0 || v.mode != viewNow || v.validAt != nil || len(v.graphs) > 0 {
			return nil
		}
		so, ok1, err := encodeValue(ctx, q, s, false)
		if err != nil {
			return err
		}
		ko, ok2, err := encodeValue(ctx, q, key, false)
		if err != nil || !ok1 || !ok2 {
			return err
		}
		var (
			o  objectID
			at int64
		)
		err = q.QueryRowContext(ctx, "SELECT o, updated_at FROM kg_volatile WHERE s = ? AND key = ?", so, ko).Scan(&o, &at)
		if errNoRows(err) {
			return nil
		}
		if err != nil {
			return fmt.Errorf("palace: kg: resolve volatile: %w", err)
		}
		d := newDecoder()
		if err := d.load(ctx, q, []objectID{o}); err != nil {
			return err
		}
		t := time.UnixMilli(at).UTC()
		out = append(out, Resolved{Value: d.value(o), UpdatedAt: &t})
		return nil
	})
	return out, err
}
