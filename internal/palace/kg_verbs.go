package palace

import (
	"context"
	"database/sql"
	"fmt"
	"slices"
	"time"
)

// The memory verbs, after Tiramemsu's time-model.md#Operations. Every verb
// runs inside a KGTx; nothing here deletes or edits a stored statement except
// setting t_ret and ret_kind once.

type writeMode int

const (
	writeAssert writeMode = iota // idempotent, user checks
	writeCreate                  // always inserts, user checks
	writeEngine                  // idempotent, engine-owned predicate, no user checks
	writeLegacy                  // always inserts, migration only
)

// stmtRow is one kg_stmt row.
type stmtRow struct {
	eid        int64
	s, p, o    objectID
	tAdd       int64
	tRet       *int64
	vFrom, vTo *int64
	retKind    *int64
}

const stmtCols = "eid, s, p, o, t_add, t_ret, v_from, v_to, ret_kind"

func scanStmtRow(sc interface{ Scan(...any) error }) (stmtRow, error) {
	var (
		r                       stmtRow
		tRet, vFrom, vTo, rKind sql.NullInt64
	)
	if err := sc.Scan(&r.eid, &r.s, &r.p, &r.o, &r.tAdd, &tRet, &vFrom, &vTo, &rKind); err != nil {
		return r, err
	}
	r.tRet = nullPtr(tRet)
	r.vFrom = nullPtr(vFrom)
	r.vTo = nullPtr(vTo)
	r.retKind = nullPtr(rKind)
	return r, nil
}

func nullPtr(n sql.NullInt64) *int64 {
	if !n.Valid {
		return nil
	}
	v := n.Int64
	return &v
}

func loadRow(w *KGTx, eid int64) (*stmtRow, error) {
	r, err := scanStmtRow(w.q.QueryRowContext(w.ctx, "SELECT "+stmtCols+" FROM kg_stmt WHERE eid = ?", eid))
	if errNoRows(err) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("palace: kg: load statement: %w", err)
	}
	return &r, nil
}

// encodeUser interns a user-supplied triple. Engine predicates are refused
// and the predicate must be an entity.
func (w *KGTx) encodeUser(s, p, o Value) (objectID, objectID, objectID, error) {
	if isEnginePredicate(p) {
		return 0, 0, 0, fmt.Errorf("%w: %s", ErrReservedPredicate, p.Text)
	}
	if p.Kind != KindEntity {
		return 0, 0, 0, fmt.Errorf("palace: kg: predicate must be an entity, got %s", p)
	}
	if s.Kind != KindEntity && s.Kind != KindStatement {
		return 0, 0, 0, fmt.Errorf("palace: kg: subject must be an entity or a statement, got %s", s)
	}
	so, _, err := encodeValue(w.ctx, w.q, s, true)
	if err != nil {
		return 0, 0, 0, err
	}
	po, _, err := encodeValue(w.ctx, w.q, p, true)
	if err != nil {
		return 0, 0, 0, err
	}
	oo, err := w.encodeObject(so, po, o)
	if err != nil {
		return 0, 0, 0, err
	}
	return so, po, oo, nil
}

func (w *KGTx) encode3(s, p, o Value) (objectID, objectID, objectID, error) {
	so, _, err := encodeValue(w.ctx, w.q, s, true)
	if err != nil {
		return 0, 0, 0, err
	}
	po, _, err := encodeValue(w.ctx, w.q, p, true)
	if err != nil {
		return 0, 0, 0, err
	}
	oo, _, err := encodeValue(w.ctx, w.q, o, true)
	if err != nil {
		return 0, 0, 0, err
	}
	return so, po, oo, nil
}

// engineOID returns the id of an engine predicate or value term, interning it
// when create is set.
func (w *KGTx) engineOID(v Value, create bool) (objectID, bool, error) {
	return encodeValue(w.ctx, w.q, v, create)
}

// Assert writes (s, p, o) valid during v, or returns the live statement that
// already says the same thing over an overlapping interval. isNew reports
// which.
func (w *KGTx) Assert(s, p, o Value, v Valid) (eid int64, isNew bool, err error) {
	so, po, oo, err := w.encodeUser(s, p, o)
	if err != nil {
		return 0, false, err
	}
	from, to := v.ms()
	return w.write(so, po, oo, from, to, writeAssert)
}

// Create always writes a new statement, even when an identical live one
// exists (parallel edges).
func (w *KGTx) Create(s, p, o Value, v Valid) (int64, error) {
	return w.create(s, p, o, v, writeCreate)
}

func (w *KGTx) create(s, p, o Value, v Valid, mode writeMode) (int64, error) {
	var (
		so, po, oo objectID
		err        error
	)
	if mode == writeLegacy {
		so, po, oo, err = w.encode3(s, p, o)
	} else {
		so, po, oo, err = w.encodeUser(s, p, o)
	}
	if err != nil {
		return 0, err
	}
	from, to := v.ms()
	eid, _, err := w.write(so, po, oo, from, to, mode)
	return eid, err
}

// Annotate adds a layer: a statement about statement eid.
func (w *KGTx) Annotate(eid int64, p, o Value) (int64, bool, error) {
	return w.Assert(StmtRef(eid), p, o, Valid{})
}

func (w *KGTx) write(s, p, o objectID, from, to *int64, mode writeMode) (int64, bool, error) {
	if err := checkInterval(from, to); err != nil {
		return 0, false, err
	}
	userChecks := mode == writeAssert || mode == writeCreate
	if userChecks {
		if err := w.checkLive(s, o); err != nil {
			return 0, false, err
		}
	}
	if mode == writeAssert || mode == writeEngine {
		existing, err := w.findLive(s, p, o, from, to)
		if err != nil {
			return 0, false, err
		}
		if existing != 0 {
			w.report.Existing = append(w.report.Existing, existing)
			return existing, false, nil
		}
	}
	if userChecks {
		if err := w.applySchema(s, p, o, from, to); err != nil {
			return 0, false, err
		}
		// Cardinality replacement may have retracted an endpoint.
		if err := w.checkLive(s, o); err != nil {
			return 0, false, err
		}
	}
	eid, err := w.insert(s, p, o, from, to)
	return eid, err == nil, err
}

func (w *KGTx) insert(s, p, o objectID, from, to *int64) (int64, error) {
	var eid int64
	err := w.q.QueryRowContext(w.ctx,
		"INSERT INTO kg_stmt (s, p, o, t_add, v_from, v_to) VALUES (?, ?, ?, ?, ?, ?) RETURNING eid",
		s, p, o, w.t, from, to).Scan(&eid)
	if err != nil {
		return 0, fmt.Errorf("palace: kg: insert statement: %w", err)
	}
	w.report.Asserted = append(w.report.Asserted, eid)
	return eid, nil
}

// checkLive requires every statement-valued endpoint to exist and be live.
func (w *KGTx) checkLive(ids ...objectID) error {
	for _, id := range ids {
		if id.tag() != tagStmt {
			continue
		}
		ok, err := w.isLive(id.payload())
		if err != nil {
			return err
		}
		if !ok {
			return fmt.Errorf("%w: e%d", ErrNotLive, id.payload())
		}
	}
	return nil
}

func (w *KGTx) isLive(eid int64) (bool, error) {
	var n int
	err := w.q.QueryRowContext(w.ctx,
		"SELECT count(*) FROM kg_stmt WHERE eid = ? AND t_ret IS NULL", eid).Scan(&n)
	if err != nil {
		return false, fmt.Errorf("palace: kg: liveness: %w", err)
	}
	return n > 0, nil
}

// overlapSQL is true when row (v_from, v_to) overlaps the interval bound to
// the four parameters (from, from, to, to). NULL is unbounded.
const overlapSQL = "(? IS NULL OR v_to IS NULL OR ? < v_to) AND (v_from IS NULL OR ? IS NULL OR v_from < ?)"

func (w *KGTx) findLive(s, p, o objectID, from, to *int64) (int64, error) {
	var eid int64
	err := w.q.QueryRowContext(w.ctx,
		"SELECT eid FROM kg_stmt WHERE s = ? AND p = ? AND o = ? AND t_ret IS NULL AND "+overlapSQL+
			" ORDER BY eid LIMIT 1", s, p, o, from, from, to, to).Scan(&eid)
	if errNoRows(err) {
		return 0, nil
	}
	if err != nil {
		return 0, fmt.Errorf("palace: kg: find statement: %w", err)
	}
	return eid, nil
}

// --- retraction ---

// Retract ends the belief in statement eid and, in the same transaction,
// in everything that stands on it: statements whose subject or object is a
// retracted statement, and statements derived from one. It returns false when
// eid was already retracted.
func (w *KGTx) Retract(eid int64) (bool, error) {
	row, err := loadRow(w, eid)
	if err != nil {
		return false, err
	}
	if row == nil {
		return false, fmt.Errorf("%w: e%d does not exist", ErrNotLive, eid)
	}
	if row.tRet != nil {
		return false, nil
	}
	return true, w.retract(eid, RetExplicit, RetCascade)
}

func (w *KGTx) retract(root int64, rootKind, memberKind RetractKind) error {
	set, err := w.cascadeSet(root)
	if err != nil {
		return err
	}
	for _, eid := range set {
		kind := memberKind
		if eid == root {
			kind = rootKind
		}
		if _, err := w.q.ExecContext(w.ctx,
			"UPDATE kg_stmt SET t_ret = ?, ret_kind = ? WHERE eid = ? AND t_ret IS NULL", w.t, int(kind), eid); err != nil {
			return fmt.Errorf("palace: kg: retract e%d: %w", eid, err)
		}
		w.report.Retracted = append(w.report.Retracted, Retraction{EID: eid, Kind: kind})
	}
	return nil
}

// cascadeSet is the live dependents walk from root, bounded by MaxCascade.
func (w *KGTx) cascadeSet(root int64) ([]int64, error) {
	set, err := walkDependents(w.ctx, w.q, root, "t_ret IS NULL", nil, w.maxCascade)
	if err != nil {
		return nil, err
	}
	return set, nil
}

// --- supersede ---

// Patch is a correction applied by Supersede. Nil fields keep the old value.
type Patch struct {
	Object         *Value
	ValidFrom      *time.Time
	ValidTo        *time.Time
	ClearValidFrom bool
	ClearValidTo   bool
}

// Supersede corrects statement eid. It retracts the statement and its layers
// (kind "superseded"), writes the corrected statement under a new eid, replays
// every layer onto it under new eids, and records (new sys:Supersedes old).
// Derived statements that rested on the old version are not replayed: their
// input changed, so they must be derived again. Subject and predicate cannot
// change, because that would be a different fact.
func (w *KGTx) Supersede(eid int64, patch Patch) (int64, error) {
	root, err := loadRow(w, eid)
	if err != nil {
		return 0, err
	}
	if root == nil || root.tRet != nil {
		return 0, fmt.Errorf("%w: e%d", ErrNotLive, eid)
	}

	newO := root.o
	if patch.Object != nil {
		if newO, err = w.encodeObject(root.s, root.p, *patch.Object); err != nil {
			return 0, err
		}
	}
	from, to := root.vFrom, root.vTo
	if patch.ValidFrom != nil {
		f := patch.ValidFrom.UnixMilli()
		from = &f
	}
	if patch.ClearValidFrom {
		from = nil
	}
	if patch.ValidTo != nil {
		t := patch.ValidTo.UnixMilli()
		to = &t
	}
	if patch.ClearValidTo {
		to = nil
	}
	if err := checkInterval(from, to); err != nil {
		return 0, err
	}

	set, err := w.cascadeSet(eid)
	if err != nil {
		return 0, err
	}
	inSet := make(map[int64]bool, len(set))
	for _, e := range set {
		inSet[e] = true
	}
	if newO.tag() == tagStmt && inSet[newO.payload()] {
		return 0, ErrPatchIntoCascade
	}
	rows := make(map[int64]stmtRow, len(set))
	for _, e := range set {
		r, err := loadRow(w, e)
		if err != nil {
			return 0, err
		}
		rows[e] = *r
	}
	drop, err := w.supersedeDrops(eid, set, rows, inSet)
	if err != nil {
		return 0, err
	}

	if err := w.retract(eid, RetSupersede, RetSupersede); err != nil {
		return 0, err
	}

	// The corrected root goes through the same constraints as a new write.
	if err := w.applySchema(root.s, root.p, newO, from, to); err != nil {
		return 0, err
	}
	if err := w.checkLive(root.s, newO); err != nil {
		return 0, err
	}
	newRoot, err := w.insert(root.s, root.p, newO, from, to)
	if err != nil {
		return 0, err
	}
	sigma := map[int64]int64{eid: newRoot}
	if err := w.replay(set, rows, drop, inSet, sigma); err != nil {
		return 0, err
	}

	sup, _, err := w.engineOID(sysPred(predSupersedes), true)
	if err != nil {
		return 0, err
	}
	if _, _, err := w.write(makeOID(newRoot, tagStmt), sup, makeOID(eid, tagStmt), nil, nil, writeEngine); err != nil {
		return 0, err
	}
	w.report.Superseded = append(w.report.Superseded, Supersession{Old: eid, New: newRoot})
	return newRoot, nil
}

// supersedeDrops selects the members of the cascade set that are not replayed:
// derived statements (other than the root) whose support is in the set, and,
// transitively, everything that stands on a dropped statement.
func (w *KGTx) supersedeDrops(root int64, set []int64, rows map[int64]stmtRow, inSet map[int64]bool) (map[int64]bool, error) {
	drop := map[int64]bool{}
	derived, ok, err := w.engineOID(sysPred(predDerivedFrom), false)
	if err != nil || !ok {
		return drop, err
	}
	for _, e := range set {
		r := rows[e]
		if r.p != derived || r.s.tag() != tagStmt || r.o.tag() != tagStmt {
			continue
		}
		if d := r.s.payload(); d != root && inSet[d] && inSet[r.o.payload()] {
			drop[d] = true
		}
	}
	for changed := len(drop) > 0; changed; {
		changed = false
		for _, e := range set {
			if drop[e] || e == root {
				continue
			}
			r := rows[e]
			if (r.s.tag() == tagStmt && drop[r.s.payload()]) || (r.o.tag() == tagStmt && drop[r.o.payload()]) {
				drop[e] = true
				changed = true
			}
		}
	}
	return drop, nil
}

// replay inserts the retained members of a superseded set under new eids, in
// an order where every statement is written after the retained statements it
// references.
func (w *KGTx) replay(set []int64, rows map[int64]stmtRow, drop, inSet map[int64]bool, sigma map[int64]int64) error {
	var pending []int64
	for _, e := range set {
		if _, done := sigma[e]; !done && !drop[e] {
			pending = append(pending, e)
		}
	}
	mapRef := func(o objectID) (objectID, bool) {
		if o.tag() != tagStmt || !inSet[o.payload()] {
			return o, true
		}
		n, ok := sigma[o.payload()]
		return makeOID(n, tagStmt), ok
	}
	for len(pending) > 0 {
		var next []int64
		for _, e := range pending {
			r := rows[e]
			s, okS := mapRef(r.s)
			o, okO := mapRef(r.o)
			if !okS || !okO {
				next = append(next, e)
				continue
			}
			// External endpoints must still be live (a cardinality
			// replacement of the corrected root may have retracted one).
			if err := w.checkLive(s, o); err != nil {
				return err
			}
			n, err := w.insert(s, r.p, o, r.vFrom, r.vTo)
			if err != nil {
				return err
			}
			sigma[e] = n
		}
		if len(next) == len(pending) {
			return fmt.Errorf("palace: kg: cannot replay cyclic layers %v", next)
		}
		pending = next
	}
	return nil
}

// --- confirm and derive ---

// Confirm records that this transaction's source corroborates live statement
// eid: (eid sys:ConfirmedBy tx). Confirming twice in one transaction is one
// confirmation.
func (w *KGTx) Confirm(eid int64) (int64, error) {
	if err := w.checkLive(makeOID(eid, tagStmt)); err != nil {
		return 0, err
	}
	p, _, err := w.engineOID(sysPred(predConfirmedBy), true)
	if err != nil {
		return 0, err
	}
	link, _, err := w.write(makeOID(eid, tagStmt), p, makeOID(w.t, tagTx), nil, nil, writeEngine)
	return link, err
}

// Derive writes a conclusion that rests on every statement in support. If any
// support is later retracted or corrected, the conclusion is retracted with
// it. It returns an existing statement only when one with the same content,
// an overlapping interval and exactly the same live supports exists; an
// independently asserted identical fact is never reused.
func (w *KGTx) Derive(s, p, o Value, v Valid, support []int64) (int64, bool, error) {
	if len(support) == 0 {
		return 0, false, ErrNoSupport
	}
	sup := normalizeSupport(support)
	supIDs := make([]objectID, len(sup))
	for i, e := range sup {
		supIDs[i] = makeOID(e, tagStmt)
	}
	if err := w.checkLive(supIDs...); err != nil {
		return 0, false, err
	}
	so, po, oo, err := w.encodeUser(s, p, o)
	if err != nil {
		return 0, false, err
	}
	from, to := v.ms()
	if err := checkInterval(from, to); err != nil {
		return 0, false, err
	}
	if err := w.checkLive(so, oo); err != nil {
		return 0, false, err
	}
	derived, _, err := w.engineOID(sysPred(predDerivedFrom), true)
	if err != nil {
		return 0, false, err
	}
	if existing, err := w.findDerivation(so, po, oo, from, to, derived, sup); err != nil || existing != 0 {
		if existing != 0 {
			w.report.Existing = append(w.report.Existing, existing)
		}
		return existing, false, err
	}
	if err := w.applySchema(so, po, oo, from, to); err != nil {
		return 0, false, err
	}
	if err := w.checkLive(append([]objectID{so, oo}, supIDs...)...); err != nil {
		return 0, false, err
	}
	eid, err := w.insert(so, po, oo, from, to)
	if err != nil {
		return 0, false, err
	}
	for _, x := range supIDs {
		if _, err := w.insert(makeOID(eid, tagStmt), derived, x, nil, nil); err != nil {
			return 0, false, err
		}
	}
	return eid, true, nil
}

// normalizeSupport sorts and de-duplicates a support set.
func normalizeSupport(support []int64) []int64 {
	sup := slices.Clone(support)
	slices.Sort(sup)
	return slices.Compact(sup)
}

func (w *KGTx) findDerivation(s, p, o objectID, from, to *int64, derived objectID, sup []int64) (int64, error) {
	rows, err := w.q.QueryContext(w.ctx,
		"SELECT eid FROM kg_stmt WHERE s = ? AND p = ? AND o = ? AND t_ret IS NULL AND "+overlapSQL+" ORDER BY eid",
		s, p, o, from, from, to, to)
	if err != nil {
		return 0, fmt.Errorf("palace: kg: find derivation: %w", err)
	}
	var candidates []int64
	for rows.Next() {
		var eid int64
		if err := rows.Scan(&eid); err != nil {
			rows.Close()
			return 0, err
		}
		candidates = append(candidates, eid)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return 0, err
	}
	for _, c := range candidates {
		have, err := liveSupports(w.ctx, w.q, c, derived, "t_ret IS NULL", nil)
		if err != nil {
			return 0, err
		}
		if slices.Equal(have, sup) {
			return c, nil
		}
	}
	return 0, nil
}

// liveSupports returns the sorted eids statement eid was derived from, as
// visible under the given view predicate.
func liveSupports(ctx context.Context, q querier, eid int64, derived objectID, view string, args []any) ([]int64, error) {
	rows, err := q.QueryContext(ctx,
		"SELECT o FROM kg_stmt WHERE s = ? AND p = ? AND "+view+" ORDER BY o",
		append([]any{makeOID(eid, tagStmt), derived}, args...)...)
	if err != nil {
		return nil, fmt.Errorf("palace: kg: read supports: %w", err)
	}
	defer rows.Close()
	var out []int64
	for rows.Next() {
		var o objectID
		if err := rows.Scan(&o); err != nil {
			return nil, err
		}
		if o.tag() == tagStmt {
			out = append(out, o.payload())
		}
	}
	slices.Sort(out)
	return out, rows.Err()
}
