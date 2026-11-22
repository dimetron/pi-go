package palace

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"time"
)

// Fact bundles, after Tiramemsu's fact-bundles capability
// (specs/memory/tiramemsu/phase2/design.md#B and its review rows 6, 19, 20).
// A bundle is a statement with everything stated about it, as a JSON tree
// that names no local eid, so it can be imported into another palace.

// Errors of bundles.
var (
	ErrBundleCycle     = errors.New("palace: kg: bundle references a statement that is not imported yet")
	ErrSensitiveBundle = errors.New("palace: kg: bundle holds sealed values; export needs IncludeSensitive")
)

// BundleVersion is the bundle format version.
const BundleVersion = 1

// Bundle is a root statement and everything stated about it.
type Bundle struct {
	Version int        `json:"version"`
	Root    BundleStmt `json:"root"`
}

// BundleStmt is one statement of a bundle. Statements are imported in the
// order they are encountered: subject, object, the statement, then Layers.
type BundleStmt struct {
	// ID is bundle-local ("b1", "b2", ...), numbered in import order.
	ID     string       `json:"id"`
	S      BundleValue  `json:"s"`
	P      BundleValue  `json:"p"`
	O      BundleValue  `json:"o"`
	Valid  *BundleValid `json:"valid,omitempty"`
	Layers []BundleStmt `json:"layers,omitempty"`
}

// BundleValid is a valid-time interval; nil bounds are open.
type BundleValid struct {
	From *time.Time `json:"from,omitempty"`
	To   *time.Time `json:"to,omitempty"`
}

// BundleValue is an entity or literal (the embedded Value), an inlined
// statement (Stmt) or a statement already in the bundle (Ref).
type BundleValue struct {
	Value
	Stmt *BundleStmt `json:"stmt,omitempty"`
	Ref  string      `json:"ref,omitempty"`
	// Sensitive marks a value that was sealed at rest in the source.
	Sensitive bool `json:"sensitive,omitempty"`
}

// MarshalJSON leaves out the empty Value of a statement reference, so no
// local eid or meaningless kind is written.
func (b BundleValue) MarshalJSON() ([]byte, error) {
	if b.Stmt != nil || b.Ref != "" {
		return json.Marshal(struct {
			Stmt *BundleStmt `json:"stmt,omitempty"`
			Ref  string      `json:"ref,omitempty"`
		}{b.Stmt, b.Ref})
	}
	return json.Marshal(struct {
		Value
		Sensitive bool `json:"sensitive,omitempty"`
	}{b.Value, b.Sensitive})
}

// BundleOptions configures an export.
type BundleOptions struct {
	// IncludeSensitive allows sealed values; they are written in plaintext and
	// marked "sensitive": true.
	IncludeSensitive bool
}

// bundleSkipped are the engine predicates that name local eids or
// transactions and mean nothing elsewhere.
func bundleSkipped(ctx context.Context, q querier) (map[objectID]bool, error) {
	skip := map[objectID]bool{}
	for _, name := range []string{predSupersedes, predConfirmedBy, predDerivedFrom} {
		o, ok, err := encodeValue(ctx, q, sysPred(name), false)
		if err != nil {
			return nil, err
		}
		if ok {
			skip[o] = true
		}
	}
	return skip, nil
}

// Bundle exports statement eid as seen by v: the root, its visible dependents
// and the statements they reference, except engine bookkeeping. A past bundle
// comes from AsOf.
func (v View) Bundle(ctx context.Context, eid int64, opts BundleOptions) (*Bundle, error) {
	var out *Bundle
	err := v.kg.read(ctx, func(q querier) error {
		var err error
		out, err = v.bundle(ctx, q, eid, opts)
		return err
	})
	return out, err
}

type bundleMember struct {
	st  Statement
	row stmtRow
}

func (v View) bundle(ctx context.Context, q querier, root int64, opts BundleOptions) (*Bundle, error) {
	rootRows, err := v.query(ctx, q, "WHERE st.eid = ?", root)
	if err != nil {
		return nil, err
	}
	rootVisible, err := visibleIn(ctx, q, v, root)
	if err != nil {
		return nil, err
	}
	if len(rootRows) == 0 || !rootVisible {
		return nil, fmt.Errorf("%w: e%d is not visible in %s", ErrNotLive, root, v)
	}
	skip, err := bundleSkipped(ctx, q)
	if err != nil {
		return nil, err
	}
	inGraph, _, err := encodeValue(ctx, q, sysPred(predInGraph), false)
	if err != nil {
		return nil, err
	}
	cond, cargs := v.cond("")
	deps, err := walkDependents(ctx, q, root, cond, cargs, 0)
	if err != nil {
		return nil, err
	}
	if len(v.graphs) > 0 {
		// The walk applies the transaction-time view; the graph scope is
		// applied per statement so nothing outside the graph is exported.
		in := deps[:0]
		for _, d := range deps {
			ok := d == root
			if !ok {
				if ok, err = visibleIn(ctx, q, v, d); err != nil {
					return nil, err
				}
			}
			if ok {
				in = append(in, d)
			}
		}
		deps = in
	}
	members := map[int64]*bundleMember{}
	load := func(eids []int64) error {
		for start := 0; start < len(eids); start += chunkSize {
			chunk := eids[start:min(start+chunkSize, len(eids))]
			ids := make([]any, len(chunk))
			for i, e := range chunk {
				ids[i] = e
			}
			sts, err := v.query(ctx, q, "WHERE st.eid IN ("+placeholders(len(ids))+")", ids...)
			if err != nil {
				return err
			}
			for _, st := range sts {
				r, err := scanStmtRow(q.QueryRowContext(ctx, "SELECT "+stmtCols+" FROM kg_stmt WHERE eid = ?", st.EID))
				if err != nil {
					return fmt.Errorf("palace: kg: load statement: %w", err)
				}
				members[st.EID] = &bundleMember{st: st, row: r}
			}
		}
		return nil
	}
	if err := load(deps); err != nil {
		return nil, err
	}
	// Drop engine bookkeeping, other engine predicates, and whatever stands
	// on a dropped statement.
	dropped := map[int64]bool{}
	for _, e := range deps {
		m := members[e]
		if m == nil {
			continue
		}
		if e == root {
			eng, err := isEngineOID(ctx, q, m.row.p)
			if err != nil {
				return nil, err
			}
			if eng {
				return nil, fmt.Errorf("%w: e%d is engine bookkeeping", ErrReservedPredicate, root)
			}
			continue
		}
		eng := false
		if !skip[m.row.p] && m.row.p != inGraph {
			if eng, err = isEngineOID(ctx, q, m.row.p); err != nil {
				return nil, err
			}
		}
		if skip[m.row.p] || eng {
			dropped[e] = true
			continue
		}
		for _, id := range []objectID{m.row.s, m.row.o} {
			if id.tag() == tagStmt && dropped[id.payload()] {
				dropped[e] = true
			}
		}
	}
	for e := range dropped {
		delete(members, e)
	}
	// Close over statement references of every member, without expanding the
	// dependents of reference-only members.
	queue := make([]int64, 0, len(members))
	for e := range members {
		queue = append(queue, e)
	}
	slices.Sort(queue)
	for len(queue) > 0 {
		e := queue[0]
		queue = queue[1:]
		m := members[e]
		for _, id := range []objectID{m.row.s, m.row.o} {
			if id.tag() != tagStmt || members[id.payload()] != nil {
				continue
			}
			ref := id.payload()
			if err := load([]int64{ref}); err != nil {
				return nil, err
			}
			rm := members[ref]
			refVisible := false
			if rm != nil {
				if refVisible, err = visibleIn(ctx, q, v, ref); err != nil {
					return nil, err
				}
			}
			if !refVisible {
				delete(members, ref)
				return nil, fmt.Errorf("%w: e%d, referenced by e%d, is not visible in %s", ErrNotLive, ref, e, v)
			}
			refEng := false
			if !skip[rm.row.p] {
				if refEng, err = isEngineOID(ctx, q, rm.row.p); err != nil {
					return nil, err
				}
			}
			if skip[rm.row.p] || refEng {
				return nil, fmt.Errorf("%w: e%d, referenced by e%d, is engine bookkeeping", ErrReservedPredicate, ref, e)
			}
			queue = append(queue, ref)
		}
	}
	if members[root] == nil {
		return nil, fmt.Errorf("palace: kg: e%d is not visible in %s", root, v)
	}
	// Sealed values.
	if !opts.IncludeSensitive {
		for _, m := range members {
			if m.row.o.tag() == tagSealed {
				return nil, fmt.Errorf("%w: e%d", ErrSensitiveBundle, m.st.EID)
			}
		}
	}
	b := &bundleBuilder{members: members, ids: map[int64]string{}, placed: map[int64]bool{}}
	rootNode := b.emit(root)
	for {
		var left []int64
		for e := range members {
			if !b.placed[e] {
				left = append(left, e)
			}
		}
		if len(left) == 0 {
			break
		}
		slices.Sort(left)
		rootNode.Layers = append(rootNode.Layers, *b.emit(left[0]))
	}
	return &Bundle{Version: BundleVersion, Root: *rootNode}, nil
}

// visibleIn reports whether eid passes the view's full condition (including
// its graph restriction).
func visibleIn(ctx context.Context, q querier, v View, eid int64) (bool, error) {
	cond, args, err := v.condQ(ctx, q, "st.")
	if err != nil {
		return false, err
	}
	var n int
	if err := q.QueryRowContext(ctx, "SELECT count(*) FROM kg_stmt st WHERE st.eid = ? AND "+cond,
		append([]any{eid}, args...)...).Scan(&n); err != nil {
		return false, fmt.Errorf("palace: kg: bundle visibility: %w", err)
	}
	return n > 0, nil
}

func isEngineOID(ctx context.Context, q querier, p objectID) (bool, error) {
	d := newDecoder()
	if err := d.load(ctx, q, []objectID{p}); err != nil {
		return false, err
	}
	return isEnginePredicate(d.value(p)), nil
}

// bundleBuilder lays the members out as a tree whose depth-first walk is a
// valid import order: a statement is written after its subject, its object
// and its parent, and refers to an earlier statement by id.
type bundleBuilder struct {
	members map[int64]*bundleMember
	ids     map[int64]string
	placed  map[int64]bool // asserted by the simulated import so far
	n       int
}

func (b *bundleBuilder) value(id objectID, v Value, sensitive bool) BundleValue {
	if id.tag() == tagStmt {
		e := id.payload()
		if b.placed[e] {
			return BundleValue{Ref: b.ids[e]}
		}
		return BundleValue{Stmt: b.emit(e)}
	}
	return BundleValue{Value: v, Sensitive: sensitive}
}

func (b *bundleBuilder) emit(eid int64) *BundleStmt {
	m := b.members[eid]
	node := &BundleStmt{}
	node.S = b.value(m.row.s, m.st.Subject, false)
	node.P = BundleValue{Value: m.st.Predicate}
	node.O = b.value(m.row.o, m.st.Object, m.row.o.tag() == tagSealed)
	if m.st.ValidFrom != nil || m.st.ValidTo != nil {
		node.Valid = &BundleValid{From: m.st.ValidFrom, To: m.st.ValidTo}
	}
	b.n++
	node.ID = fmt.Sprintf("b%d", b.n)
	b.ids[eid] = node.ID
	b.placed[eid] = true
	// Layers: statements about this one whose own references are written.
	var layers []int64
	for e, lm := range b.members {
		if !b.placed[e] && lm.row.s == makeOID(eid, tagStmt) {
			layers = append(layers, e)
		}
	}
	slices.Sort(layers)
	for _, e := range layers {
		lm := b.members[e]
		if b.placed[e] || (lm.row.o.tag() == tagStmt && !b.placed[lm.row.o.payload()]) {
			continue
		}
		node.Layers = append(node.Layers, *b.emit(e))
	}
	return node
}

// --- import ---

type bundleCheck struct {
	ctx     context.Context
	q       querier
	seen    map[string]bool
	sensTag map[string]bool
}

// ImportBundle writes bundle b through idempotent Assert, depth-first, and
// returns the eid each bundle id became. Importing the same bundle again
// changes nothing. The whole bundle is validated before anything is written:
// a reference to an id not yet imported is ErrBundleCycle; any engine
// predicate other than sys:InGraph is rejected; a sensitive value needs a
// sensitive target predicate (ErrSchemaViolation), and is then sealed by the
// normal write path.
func (w *KGTx) ImportBundle(b *Bundle) (map[string]int64, error) {
	if b == nil || b.Version != BundleVersion {
		return nil, errors.New("palace: kg: unsupported bundle version")
	}
	c := &bundleCheck{ctx: w.ctx, q: w.q, seen: map[string]bool{}, sensTag: map[string]bool{}}
	if err := c.stmt(&b.Root); err != nil {
		return nil, err
	}
	ids := map[string]int64{}
	if _, err := w.importStmt(&b.Root, ids); err != nil {
		return nil, err
	}
	return ids, nil
}

func (c *bundleCheck) stmt(s *BundleStmt) error {
	if s.ID == "" {
		return errors.New("palace: kg: bundle statement has no id")
	}
	if c.seen[s.ID] {
		return fmt.Errorf("palace: kg: bundle id %q is used twice", s.ID)
	}
	if err := c.value(&s.S, false); err != nil {
		return err
	}
	if s.P.Stmt != nil || s.P.Ref != "" || s.P.Kind != KindEntity {
		return fmt.Errorf("palace: kg: bundle %s: predicate must be an entity", s.ID)
	}
	if isEnginePredicate(s.P.Value) && s.P.Text != predInGraph {
		return fmt.Errorf("%w: bundle %s uses %s", ErrReservedPredicate, s.ID, s.P.Text)
	}
	if s.P.Text == predInGraph {
		if s.S.Stmt == nil && s.S.Ref == "" || s.O.Kind != KindEntity || s.O.Stmt != nil || s.O.Ref != "" {
			return fmt.Errorf("palace: kg: bundle %s: a membership is (statement, sys:InGraph, graph)", s.ID)
		}
	}
	if err := c.value(&s.O, true); err != nil {
		return err
	}
	if s.O.Sensitive {
		po, ok, err := encodeValue(c.ctx, c.q, s.P.Value, false)
		if err != nil {
			return err
		}
		sens := false
		if ok {
			if sens, err = isSensitive(c.ctx, c.q, po); err != nil {
				return err
			}
		}
		if !sens {
			return fmt.Errorf("%w: bundle %s: sensitive value for predicate %s, which is not sensitive",
				ErrSchemaViolation, s.ID, s.P.Text)
		}
	}
	if s.Valid != nil {
		if err := checkInterval(Valid{From: s.Valid.From, To: s.Valid.To}.ms()); err != nil {
			return fmt.Errorf("bundle %s: %w", s.ID, err)
		}
	}
	c.seen[s.ID] = true
	for i := range s.Layers {
		if err := c.stmt(&s.Layers[i]); err != nil {
			return err
		}
	}
	return nil
}

func (c *bundleCheck) value(v *BundleValue, object bool) error {
	switch {
	case v.Stmt != nil && v.Ref != "":
		return errors.New("palace: kg: bundle value has both stmt and ref")
	case v.Stmt != nil:
		return c.stmt(v.Stmt)
	case v.Ref != "":
		if !c.seen[v.Ref] {
			return fmt.Errorf("%w: %q", ErrBundleCycle, v.Ref)
		}
		return nil
	}
	switch v.Kind {
	case KindEntity:
	case KindString, KindInt, KindNumber, KindBool, KindTime:
		if !object {
			return errors.New("palace: kg: bundle subject must be an entity or a statement")
		}
	default:
		return fmt.Errorf("palace: kg: bundle value has unusable kind %d", v.Kind)
	}
	if v.Sensitive && (!object || !isLiteralKind(v.Kind)) {
		return errors.New("palace: kg: only a literal object can be sensitive")
	}
	return nil
}

func (w *KGTx) bundleValue(v *BundleValue, ids map[string]int64) (Value, error) {
	switch {
	case v.Stmt != nil:
		e, err := w.importStmt(v.Stmt, ids)
		return StmtRef(e), err
	case v.Ref != "":
		e, ok := ids[v.Ref]
		if !ok {
			return Value{}, fmt.Errorf("%w: %q", ErrBundleCycle, v.Ref)
		}
		return StmtRef(e), nil
	}
	return v.Value, nil
}

func (w *KGTx) importStmt(s *BundleStmt, ids map[string]int64) (int64, error) {
	sv, err := w.bundleValue(&s.S, ids)
	if err != nil {
		return 0, err
	}
	ov, err := w.bundleValue(&s.O, ids)
	if err != nil {
		return 0, err
	}
	var eid int64
	if s.P.Text == predInGraph {
		eid, _, err = w.AddToGraph(sv.Int, ov.Text)
	} else {
		var valid Valid
		if s.Valid != nil {
			valid = Valid{From: s.Valid.From, To: s.Valid.To}
		}
		eid, _, err = w.Assert(sv, s.P.Value, ov, valid)
	}
	if err != nil {
		return 0, fmt.Errorf("palace: kg: import bundle %s: %w", s.ID, err)
	}
	ids[s.ID] = eid
	for i := range s.Layers {
		if _, err := w.importStmt(&s.Layers[i], ids); err != nil {
			return 0, err
		}
	}
	return eid, nil
}
