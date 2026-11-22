package palace

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"
)

// Errors returned by knowledge-graph writes.
var (
	ErrNotLive           = errors.New("palace: kg: statement is not live")
	ErrReservedPredicate = errors.New("palace: kg: predicate is reserved for the engine")
	ErrEmptyInterval     = errors.New("palace: kg: valid_from must be before valid_to")
	ErrCascadeLimit      = errors.New("palace: kg: retraction cascade exceeds the limit")
	ErrUniqueViolation   = errors.New("palace: kg: another subject already holds this unique value")
	ErrSchemaViolation   = errors.New("palace: kg: live data violates the predicate schema")
	ErrNoSupport         = errors.New("palace: kg: a derived statement needs at least one support")
	ErrPatchIntoCascade  = errors.New("palace: kg: a correction cannot point into the statement's own layers")
	ErrHistoryConflicts  = errors.New("palace: kg: conflicts are not defined on the history view")
)

// DefaultMaxCascade bounds the statements one retraction may take with it.
const DefaultMaxCascade = 10_000

// TxOptions configures one knowledge-graph transaction.
type TxOptions struct {
	// Author, Source and Reason are recorded on the transaction.
	Author, Source, Reason string
	// DryRun runs the transaction and reports it without committing. The eids
	// in the report are provisional: they may be issued again later.
	DryRun bool
	// MaxCascade bounds a retraction's cascade; 0 means DefaultMaxCascade.
	MaxCascade int
}

// RetractKind says why a statement was retracted.
type RetractKind int

// Retraction kinds, stored in kg_stmt.ret_kind.
const (
	RetExplicit RetractKind = iota
	RetCascade
	RetSupersede
	RetCardinality
)

func (k RetractKind) String() string {
	switch k {
	case RetExplicit:
		return "retracted"
	case RetCascade:
		return "cascade"
	case RetSupersede:
		return "superseded"
	case RetCardinality:
		return "replaced"
	}
	return fmt.Sprintf("kind(%d)", int(k))
}

// Retraction is one statement a transaction retracted.
type Retraction struct {
	EID  int64       `json:"eid"`
	Kind RetractKind `json:"kind"`
}

// Supersession links a corrected statement to its replacement.
type Supersession struct {
	Old int64 `json:"old"`
	New int64 `json:"new"`
}

// TxReport is what one transaction did.
type TxReport struct {
	T          int64          `json:"t"`
	Instant    time.Time      `json:"instant"`
	Asserted   []int64        `json:"asserted,omitempty"`
	Existing   []int64        `json:"existing,omitempty"`
	Retracted  []Retraction   `json:"retracted,omitempty"`
	Superseded []Supersession `json:"superseded,omitempty"`
	Erased     []string       `json:"erased,omitempty"`
	DryRun     bool           `json:"dry_run,omitempty"`
}

// Valid is a valid-time interval [From, To); nil means unbounded.
type Valid struct {
	From, To *time.Time
}

func (v Valid) ms() (from, to *int64) {
	if v.From != nil {
		f := v.From.UnixMilli()
		from = &f
	}
	if v.To != nil {
		t := v.To.UnixMilli()
		to = &t
	}
	return from, to
}

func checkInterval(from, to *int64) error {
	if from != nil && to != nil && *from >= *to {
		return ErrEmptyInterval
	}
	return nil
}

// KGTx is an open knowledge-graph transaction. It is only valid inside the
// callback passed to KnowledgeGraph.Transact.
type KGTx struct {
	ctx        context.Context
	q          querier
	t          int64
	instant    int64
	maxCascade int
	report     *TxReport
}

// T is the transaction number.
func (w *KGTx) T() int64 { return w.t }

// beginWriter allocates the next transaction. The allocation is the first
// statement of the SQL transaction and is a write, so SQLite's write lock is
// taken before anything is read: two writers serialize on busy_timeout
// instead of racing for the same t.
func beginWriter(ctx context.Context, q querier, opts TxOptions, nowMs int64) (*KGTx, error) {
	var t, instant int64
	err := q.QueryRowContext(ctx, `INSERT INTO kg_tx (t, instant, author, source, reason)
		SELECT m.value + 1, max(?, COALESCE((SELECT max(instant) FROM kg_tx), 0) + 1), ?, ?, ?
		FROM kg_meta m WHERE m.key = 'last_t'
		RETURNING t, instant`, nowMs, opts.Author, opts.Source, opts.Reason).Scan(&t, &instant)
	if err != nil {
		return nil, fmt.Errorf("palace: kg: begin transaction: %w", err)
	}
	maxCascade := opts.MaxCascade
	if maxCascade <= 0 {
		maxCascade = DefaultMaxCascade
	}
	return &KGTx{
		ctx: ctx, q: q, t: t, instant: instant, maxCascade: maxCascade,
		report: &TxReport{T: t, Instant: time.UnixMilli(instant).UTC(), DryRun: opts.DryRun},
	}, nil
}

// finish advances the committed watermark to this transaction. It must be
// the last write before COMMIT.
func (w *KGTx) finish() error {
	if _, err := w.q.ExecContext(w.ctx, "UPDATE kg_meta SET value = ? WHERE key = 'last_t'", w.t); err != nil {
		return fmt.Errorf("palace: kg: advance watermark: %w", err)
	}
	return nil
}

// Transact runs fn in one knowledge-graph transaction: one t, one instant,
// all-or-nothing. fn's error rolls everything back.
func (kg *KnowledgeGraph) Transact(ctx context.Context, opts TxOptions, fn func(*KGTx) error) (*TxReport, error) {
	db, err := kg.database()
	if err != nil {
		return nil, err
	}
	sqlTx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("palace: kg: begin: %w", err)
	}
	defer func() { _ = sqlTx.Rollback() }()

	w, err := beginWriter(ctx, sqlTx, opts, kg.now().UnixMilli())
	if err != nil {
		return nil, err
	}
	if err := fn(w); err != nil {
		return nil, err
	}
	if opts.DryRun {
		return w.report, nil // deferred rollback discards it
	}
	if err := w.finish(); err != nil {
		return nil, err
	}
	if err := sqlTx.Commit(); err != nil {
		return nil, fmt.Errorf("palace: kg: commit: %w", err)
	}
	return w.report, nil
}

// read runs fn inside one SQL transaction so that every scan of a composite
// read (explain, conflicts, ...) sees the same snapshot.
func (kg *KnowledgeGraph) read(ctx context.Context, fn func(q querier) error) error {
	db, err := kg.database()
	if err != nil {
		return err
	}
	sqlTx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("palace: kg: begin read: %w", err)
	}
	defer func() { _ = sqlTx.Rollback() }()
	return fn(sqlTx)
}

// ErrKGUnavailable is returned when the store has no database behind it.
var ErrKGUnavailable = errors.New("palace: kg: the store has no database")

func (kg *KnowledgeGraph) database() (*sql.DB, error) {
	if db := kg.store.DB(); db != nil {
		return db, nil
	}
	return nil, ErrKGUnavailable
}

// lastT is the committed watermark.
func lastT(ctx context.Context, q querier) (int64, error) {
	var t int64
	if err := q.QueryRowContext(ctx, "SELECT value FROM kg_meta WHERE key = 'last_t'").Scan(&t); err != nil {
		return 0, fmt.Errorf("palace: kg: read watermark: %w", err)
	}
	return t, nil
}

// errNoRows reports a missing row without wrapping.
func errNoRows(err error) bool { return errors.Is(err, sql.ErrNoRows) }
