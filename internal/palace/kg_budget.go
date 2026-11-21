package palace

import (
	"context"
	"errors"
	"fmt"
)

// Query budgets, after Tiramemsu's query-budgets capability. A Budget covers
// a whole operation: every lookup of a query, path search, rule evaluation or
// recall draws from the same counter. Exceeding it fails the operation; a
// truncated result is never returned as complete. Deadlines and cancellation
// come from the context and are checked in every loop. The zero Budget is no
// budget: the operation behaves exactly as without one.

// ErrBudget is wrapped by every budget failure.
var ErrBudget = errors.New("palace: kg: budget exceeded")

// Budget bounds one operation. Zero fields are unlimited.
type Budget struct {
	// MaxRows bounds the statements read plus the rows produced.
	MaxRows int `json:"max_rows,omitempty"`
	// MaxBytes bounds the decoded literal bytes read and produced.
	MaxBytes int `json:"max_bytes,omitempty"`
}

// meter is the running account of one operation's budget.
type meter struct {
	ctx         context.Context
	b           Budget
	rows, bytes int
}

func newMeter(ctx context.Context, b Budget) *meter { return &meter{ctx: ctx, b: b} }

// check reports cancellation or an expired deadline.
func (m *meter) check() error {
	if err := m.ctx.Err(); err != nil {
		return fmt.Errorf("palace: kg: %w", err)
	}
	return nil
}

// take charges rows and bytes, failing once a limit is passed.
func (m *meter) take(rows, bytes int) error {
	if err := m.check(); err != nil {
		return err
	}
	m.rows += rows
	m.bytes += bytes
	if m.b.MaxRows > 0 && m.rows > m.b.MaxRows {
		return fmt.Errorf("%w: more than %d rows", ErrBudget, m.b.MaxRows)
	}
	if m.b.MaxBytes > 0 && m.bytes > m.b.MaxBytes {
		return fmt.Errorf("%w: more than %d bytes", ErrBudget, m.b.MaxBytes)
	}
	return nil
}

// takeStatements charges a batch of statements read.
func (m *meter) takeStatements(sts []Statement) error {
	n := 0
	for _, st := range sts {
		n += valueBytes(st.Subject) + valueBytes(st.Object)
	}
	return m.take(len(sts), n)
}

// valueBytes is the decoded size a value contributes to MaxBytes.
func valueBytes(v Value) int {
	switch v.Kind {
	case KindEntity, KindString:
		return len(v.Text)
	}
	return 8
}
