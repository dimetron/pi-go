package palace

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
)

// Errors of the predicate schema.
var (
	ErrNotUnique = errors.New("palace: kg: upsert needs a unique predicate")
)

// PredicateSchema holds the constraints a predicate can carry, after
// Tiramemsu's predicate-schema capability. Every flag is an ordinary,
// versioned statement about the predicate.
type PredicateSchema struct {
	// One: a subject holds at most one object at any valid time. Asserting a
	// new object retracts the overlapping old ones (kind "replaced"); their
	// layers are not carried over, since they described a different fact.
	One bool `json:"one,omitempty"`
	// Unique: at most one live subject holds a given object.
	Unique bool `json:"unique,omitempty"`
	// ValueType restricts objects to one kind: entity, string, int, number,
	// bool, time or statement. Empty allows any.
	ValueType string `json:"value_type,omitempty"`
	// SubjectType restricts subjects to entity or statement. Empty allows both.
	SubjectType string `json:"subject_type,omitempty"`
	// IsEdge is stored and reported; it marks entity-valued predicates as
	// relationships for query front ends. Nothing is checked.
	IsEdge bool `json:"is_edge,omitempty"`
	// Sensitive seals literal objects under their data subject's key, so that
	// erasing the subject destroys them (crypto-shredding). See kg_seal.go.
	Sensitive bool `json:"sensitive,omitempty"`
}

// schemaPredicates are the engine predicates that carry schema flags.
var schemaPredicates = []string{predCardinality, predUnique, predValueType, predSubjectType, predIsEdge, predSensitive}

// valueTypeTags maps a ValueType name to the object tags it admits. A sealed
// value is a literal whose kind is hidden, so every literal type admits it.
var valueTypeTags = map[string][]int{
	"entity":    {tagEntity},
	"statement": {tagStmt},
	"string":    {tagStr, tagSealed},
	"int":       {tagInt, tagSealed},
	"number":    {tagNum, tagSealed},
	"bool":      {tagBool, tagSealed},
	"time":      {tagTime, tagSealed},
}

var valueKindNames = map[ValueKind]string{
	KindEntity: "entity", KindStatement: "statement", KindString: "string", KindInt: "int",
	KindNumber: "number", KindBool: "bool", KindTime: "time", KindTx: "tx",
}

func isLiteralKind(k ValueKind) bool {
	switch k {
	case KindString, KindInt, KindNumber, KindBool, KindTime:
		return true
	}
	return false
}

// readSchema returns predicate p's live schema and, per flag predicate, the
// eids of the statements that set it.
func readSchema(ctx context.Context, q querier, p objectID) (PredicateSchema, map[string][]int64, error) {
	var sch PredicateSchema
	ids := map[string][]int64{}
	names := map[objectID]string{}
	var args []any
	for _, name := range schemaPredicates {
		o, ok, err := encodeValue(ctx, q, sysPred(name), false)
		if err != nil {
			return sch, nil, err
		}
		if ok {
			names[o] = name
			args = append(args, o)
		}
	}
	if len(args) == 0 {
		return sch, ids, nil
	}
	rows, err := q.QueryContext(ctx, "SELECT eid, p, o FROM kg_stmt WHERE s = ? AND p IN ("+
		placeholders(len(args))+") AND t_ret IS NULL ORDER BY eid", append([]any{p}, args...)...)
	if err != nil {
		return sch, nil, fmt.Errorf("palace: kg: read schema: %w", err)
	}
	type flag struct {
		eid  int64
		p, o objectID
	}
	var flags []flag
	var objs []objectID
	for rows.Next() {
		var f flag
		if err := rows.Scan(&f.eid, &f.p, &f.o); err != nil {
			rows.Close()
			return sch, nil, fmt.Errorf("palace: kg: scan schema: %w", err)
		}
		flags = append(flags, f)
		objs = append(objs, f.o)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return sch, nil, err
	}
	d := newDecoder()
	if err := d.load(ctx, q, objs); err != nil {
		return sch, nil, err
	}
	for _, f := range flags {
		name := names[f.p]
		ids[name] = append(ids[name], f.eid)
		switch name {
		case predCardinality:
			sch.One = true
		case predUnique:
			sch.Unique = true
		case predValueType:
			sch.ValueType = d.value(f.o).Text
		case predSubjectType:
			sch.SubjectType = d.value(f.o).Text
		case predIsEdge:
			sch.IsEdge = true
		case predSensitive:
			sch.Sensitive = true
		}
	}
	return sch, ids, nil
}

// Schema returns predicate p's current schema.
func (kg *KnowledgeGraph) Schema(ctx context.Context, predicate string) (PredicateSchema, error) {
	var sch PredicateSchema
	err := kg.read(ctx, func(q querier) error {
		po, ok, err := encodeValue(ctx, q, Ent(predicate), false)
		if err != nil || !ok {
			return err
		}
		sch, _, err = readSchema(ctx, q, po)
		return err
	})
	return sch, err
}

// encodeObject is the one place a user object becomes an objectID. It checks
// valueType and subjectType (the first two schema checks, before anything is
// interned) and seals a literal of a sensitive predicate, so plaintext of a
// sensitive value never reaches kg_term or its text index.
func (w *KGTx) encodeObject(s, p objectID, o Value) (objectID, error) {
	sch, _, err := readSchema(w.ctx, w.q, p)
	if err != nil {
		return 0, err
	}
	if sch.ValueType != "" && valueKindNames[o.Kind] != sch.ValueType {
		return 0, fmt.Errorf("%w: value type %s, got %s", ErrSchemaViolation, sch.ValueType, valueKindNames[o.Kind])
	}
	if sch.SubjectType != "" && !subjectTypeOK(sch.SubjectType, s) {
		return 0, fmt.Errorf("%w: subject type %s", ErrSchemaViolation, sch.SubjectType)
	}
	if sch.Sensitive && isLiteralKind(o.Kind) {
		return w.seal(s, o)
	}
	oo, _, err := encodeValue(w.ctx, w.q, o, true)
	return oo, err
}

func subjectTypeOK(want string, s objectID) bool {
	switch want {
	case "entity":
		return s.tag() == tagEntity
	case "statement":
		return s.tag() == tagStmt
	}
	return true
}

// applySchema enforces p's unique and cardinality constraints (the last two
// checks) before (s, p, o) is written: a unique collision fails, and a
// cardinality-one predicate retracts the subject's other overlapping objects.
// The schema is read on every write, so a SetPredicate earlier in the same
// transaction takes effect at once.
func (w *KGTx) applySchema(s, p, o objectID, from, to *int64) error {
	sch, _, err := readSchema(w.ctx, w.q, p)
	if err != nil {
		return err
	}
	if sch.Unique {
		var other int64
		err := w.q.QueryRowContext(w.ctx,
			"SELECT eid FROM kg_stmt WHERE p = ? AND o = ? AND s <> ? AND t_ret IS NULL ORDER BY eid LIMIT 1",
			p, o, s).Scan(&other)
		if err == nil {
			return fmt.Errorf("%w (held by e%d)", ErrUniqueViolation, other)
		}
		if !errNoRows(err) {
			return fmt.Errorf("palace: kg: unique check: %w", err)
		}
	}
	if !sch.One {
		return nil
	}
	replaced, err := collectInts(w.ctx, w.q,
		"SELECT eid FROM kg_stmt WHERE s = ? AND p = ? AND o <> ? AND t_ret IS NULL AND "+overlapSQL+" ORDER BY eid",
		[]any{s, p, o, from, from, to, to})
	if err != nil {
		return err
	}
	for _, eid := range replaced {
		if live, err := w.isLive(eid); err != nil {
			return err
		} else if live {
			if err := w.retract(eid, RetCardinality, RetCascade); err != nil {
				return err
			}
		}
	}
	return nil
}

// SetPredicate declares p's schema, replacing the previous one. It fails with
// ErrSchemaViolation when live data already breaks a new constraint, or when
// the combination is not supported (sensitive with unique: sealed values of
// different subjects never compare equal).
func (w *KGTx) SetPredicate(p Value, sch PredicateSchema) error {
	if isEnginePredicate(p) || p.Kind != KindEntity {
		return fmt.Errorf("%w: %s", ErrReservedPredicate, p)
	}
	if sch.ValueType != "" && valueTypeTags[sch.ValueType] == nil {
		return fmt.Errorf("palace: kg: unknown value type %q", sch.ValueType)
	}
	if sch.SubjectType != "" && sch.SubjectType != "entity" && sch.SubjectType != "statement" {
		return fmt.Errorf("palace: kg: unknown subject type %q", sch.SubjectType)
	}
	if sch.Sensitive && sch.Unique {
		return fmt.Errorf("%w: a predicate cannot be both sensitive and unique", ErrSchemaViolation)
	}
	po, _, err := encodeValue(w.ctx, w.q, p, true)
	if err != nil {
		return err
	}
	if err := w.checkSchemaAgainstData(po, sch); err != nil {
		return err
	}
	cur, ids, err := readSchema(w.ctx, w.q, po)
	if err != nil {
		return err
	}
	flags := []struct {
		name string
		want *Value
		same bool
	}{
		{predValueType, strFlag(sch.ValueType), cur.ValueType == sch.ValueType},
		{predSubjectType, strFlag(sch.SubjectType), cur.SubjectType == sch.SubjectType},
		{predUnique, boolFlag(sch.Unique, Bool(true)), cur.Unique == sch.Unique},
		{predCardinality, boolFlag(sch.One, Str("one")), cur.One == sch.One},
		{predIsEdge, boolFlag(sch.IsEdge, Bool(true)), cur.IsEdge == sch.IsEdge},
		{predSensitive, boolFlag(sch.Sensitive, Bool(true)), cur.Sensitive == sch.Sensitive},
	}
	for _, f := range flags {
		if f.same {
			continue
		}
		for _, eid := range ids[f.name] {
			if err := w.retract(eid, RetExplicit, RetCascade); err != nil {
				return err
			}
		}
		if f.want == nil {
			continue
		}
		if _, _, err := w.engineWrite(po, f.name, *f.want); err != nil {
			return err
		}
	}
	return nil
}

func strFlag(s string) *Value {
	if s == "" {
		return nil
	}
	v := Str(s)
	return &v
}

func boolFlag(on bool, v Value) *Value {
	if !on {
		return nil
	}
	return &v
}

// checkSchemaAgainstData rejects a schema that live statements of p already
// break, in the order the checks run on writes.
func (w *KGTx) checkSchemaAgainstData(po objectID, sch PredicateSchema) error {
	if sch.ValueType != "" {
		tags := valueTypeTags[sch.ValueType]
		if err := w.violatingTags(po, "o", tags, "value type "+sch.ValueType); err != nil {
			return err
		}
		if err := w.sealedKindViolations(po, sch.ValueType); err != nil {
			return err
		}
	}
	if sch.SubjectType != "" {
		tag := tagEntity
		if sch.SubjectType == "statement" {
			tag = tagStmt
		}
		if err := w.violatingTags(po, "s", []int{tag}, "subject type "+sch.SubjectType); err != nil {
			return err
		}
	}
	if sch.Unique {
		if err := w.violations(po, "a.o = b.o AND a.s < b.s", "unique"); err != nil {
			return err
		}
	}
	if sch.One {
		if err := w.violations(po, "a.s = b.s AND a.o < b.o AND "+
			"(a.v_from IS NULL OR b.v_to IS NULL OR a.v_from < b.v_to) AND "+
			"(b.v_from IS NULL OR a.v_to IS NULL OR b.v_from < a.v_to)", "cardinality one"); err != nil {
			return err
		}
	}
	if sch.Sensitive {
		// Plaintext literals already in the dictionary cannot be sealed after
		// the fact without rewriting history.
		eids, err := collectInts(w.ctx, w.q,
			"SELECT eid FROM kg_stmt WHERE p = ? AND (o & 15) IN (3, 4, 5, 6, 7) LIMIT 10", []any{po})
		if err != nil {
			return err
		}
		if len(eids) > 0 {
			return fmt.Errorf("%w: sensitive, but plaintext values exist in %v", ErrSchemaViolation, eids)
		}
		var vol int
		if err := w.q.QueryRowContext(w.ctx, "SELECT count(*) FROM kg_volatile WHERE key = ?", po).Scan(&vol); err != nil {
			return fmt.Errorf("palace: kg: volatile check: %w", err)
		}
		if vol > 0 {
			return fmt.Errorf("%w: sensitive, but %d volatile plaintext value(s) exist; clear them first", ErrSchemaViolation, vol)
		}
	}
	return nil
}

func (w *KGTx) violatingTags(po objectID, col string, allowed []int, what string) error {
	args := []any{po}
	for _, t := range allowed {
		args = append(args, t)
	}
	eids, err := collectInts(w.ctx, w.q, "SELECT eid FROM kg_stmt WHERE p = ? AND t_ret IS NULL AND ("+col+
		" & 15) NOT IN ("+placeholders(len(allowed))+") ORDER BY eid LIMIT 10", args)
	if err != nil {
		return err
	}
	if len(eids) > 0 {
		return fmt.Errorf("%w: %s broken by %v", ErrSchemaViolation, what, eids)
	}
	return nil
}

func (w *KGTx) violations(p objectID, join, what string) error {
	rows, err := w.q.QueryContext(w.ctx,
		"SELECT a.eid, b.eid FROM kg_stmt a JOIN kg_stmt b ON a.p = b.p AND "+join+
			" WHERE a.p = ? AND a.t_ret IS NULL AND b.t_ret IS NULL ORDER BY a.eid, b.eid LIMIT 10", p)
	if err != nil {
		return fmt.Errorf("palace: kg: schema violations: %w", err)
	}
	defer rows.Close()
	var pairs []string
	for rows.Next() {
		var a, b int64
		if err := rows.Scan(&a, &b); err != nil {
			return fmt.Errorf("palace: kg: scan violations: %w", err)
		}
		pairs = append(pairs, fmt.Sprintf("e%d/e%d", a, b))
	}
	if err := rows.Err(); err != nil {
		return err
	}
	if len(pairs) > 0 {
		return fmt.Errorf("%w: %s broken by %v", ErrSchemaViolation, what, pairs)
	}
	return nil
}

// engineWrite idempotently asserts (s, <engine predicate>, o) without user
// checks. It is how the engine records schema flags, graph membership, rule
// texts and embedding models.
func (w *KGTx) engineWrite(s objectID, pred string, o Value) (int64, bool, error) {
	pp, _, err := w.engineOID(sysPred(pred), true)
	if err != nil {
		return 0, false, err
	}
	oo, _, err := encodeValue(w.ctx, w.q, o, true)
	if err != nil {
		return 0, false, err
	}
	return w.write(s, pp, oo, nil, nil, writeEngine)
}

// Upsert returns the subject of the live statement (n p o) on a unique
// predicate p, or creates a fresh node n and asserts (n p o). On a predicate
// that is not unique it fails with ErrNotUnique.
func (w *KGTx) Upsert(p, o Value) (node Value, eid int64, isNew bool, err error) {
	if isEnginePredicate(p) || p.Kind != KindEntity {
		return Value{}, 0, false, fmt.Errorf("%w: %s", ErrReservedPredicate, p)
	}
	po, _, err := encodeValue(w.ctx, w.q, p, true)
	if err != nil {
		return Value{}, 0, false, err
	}
	sch, _, err := readSchema(w.ctx, w.q, po)
	if err != nil {
		return Value{}, 0, false, err
	}
	if !sch.Unique {
		return Value{}, 0, false, fmt.Errorf("%w: %s", ErrNotUnique, p)
	}
	if oo, ok, err := encodeValue(w.ctx, w.q, o, false); err != nil {
		return Value{}, 0, false, err
	} else if ok {
		var (
			e int64
			s objectID
		)
		err := w.q.QueryRowContext(w.ctx,
			"SELECT eid, s FROM kg_stmt WHERE p = ? AND o = ? AND t_ret IS NULL ORDER BY eid LIMIT 1", po, oo).Scan(&e, &s)
		if err == nil {
			d := newDecoder()
			if err := d.load(w.ctx, w.q, []objectID{s}); err != nil {
				return Value{}, 0, false, err
			}
			w.report.Existing = append(w.report.Existing, e)
			return d.value(s), e, false, nil
		}
		if !errNoRows(err) {
			return Value{}, 0, false, fmt.Errorf("palace: kg: upsert: %w", err)
		}
	}
	node = Ent("node:" + uuid.NewString())
	eid, _, err = w.Assert(node, p, o, Valid{})
	return node, eid, true, err
}

// UpsertDerived returns the node n of a live key (n p o) derived from exactly
// support, or a fresh node whose key is written through Derive. A key that was
// asserted independently, or derived from other support, is never reused, so a
// rule engine's invented nodes are stable across runs and fall with their
// support (Tiramemsu D51).
func (w *KGTx) UpsertDerived(p, o Value, support []int64) (node Value, eid int64, isNew bool, err error) {
	if len(support) == 0 {
		return Value{}, 0, false, ErrNoSupport
	}
	sup := normalizeSupport(support)
	po, okP, err := encodeValue(w.ctx, w.q, p, false)
	if err != nil {
		return Value{}, 0, false, err
	}
	if okP {
		if sens, err := isSensitive(w.ctx, w.q, po); err != nil {
			return Value{}, 0, false, err
		} else if sens {
			return Value{}, 0, false, fmt.Errorf("%w: derived upsert on sensitive predicate %s", ErrSchemaViolation, p)
		}
	}
	oo, okO, err := encodeValue(w.ctx, w.q, o, false)
	if err != nil {
		return Value{}, 0, false, err
	}
	derived, okD, err := w.engineOID(sysPred(predDerivedFrom), false)
	if err != nil {
		return Value{}, 0, false, err
	}
	if okP && okO && okD {
		cands, err := collectInts(w.ctx, w.q,
			"SELECT eid FROM kg_stmt WHERE p = ? AND o = ? AND (s & 15) = 1 AND t_ret IS NULL ORDER BY eid",
			[]any{po, oo})
		if err != nil {
			return Value{}, 0, false, err
		}
		for _, c := range cands {
			have, err := liveSupports(w.ctx, w.q, c, derived, "t_ret IS NULL", nil)
			if err != nil {
				return Value{}, 0, false, err
			}
			if equalInts(have, sup) {
				row, err := loadRow(w, c)
				if err != nil {
					return Value{}, 0, false, err
				}
				d := newDecoder()
				if err := d.load(w.ctx, w.q, []objectID{row.s}); err != nil {
					return Value{}, 0, false, err
				}
				w.report.Existing = append(w.report.Existing, c)
				return d.value(row.s), c, false, nil
			}
		}
	}
	node = Ent("skolem:" + uuid.NewString())
	eid, _, err = w.Derive(node, p, o, Valid{}, sup)
	return node, eid, true, err
}

func equalInts(a, b []int64) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// ensureOne makes p cardinality-one while keeping its other schema flags.
func (w *KGTx) ensureOne(p Value) error {
	po, _, err := encodeValue(w.ctx, w.q, p, true)
	if err != nil {
		return err
	}
	sch, _, err := readSchema(w.ctx, w.q, po)
	if err != nil || sch.One {
		return err
	}
	sch.One = true
	return w.SetPredicate(p, sch)
}

// sealedKindViolations decrypts the live sealed objects of p and rejects a
// value type they do not have. Erased values cannot be checked and pass.
func (w *KGTx) sealedKindViolations(po objectID, valueType string) error {
	rows, err := collectInts(w.ctx, w.q,
		"SELECT o FROM kg_stmt WHERE p = ? AND t_ret IS NULL AND (o & 15) = 9", []any{po})
	if err != nil || len(rows) == 0 {
		return err
	}
	ids := make([]objectID, len(rows))
	for i, o := range rows {
		ids[i] = objectID(o)
	}
	d := newDecoder()
	if err := d.load(w.ctx, w.q, ids); err != nil {
		return err
	}
	for _, o := range ids {
		v := d.value(o)
		if v.Kind == KindString && v.Text == ErasedText {
			continue
		}
		if valueKindNames[v.Kind] != valueType {
			return fmt.Errorf("%w: value type %s broken by a sealed %s value", ErrSchemaViolation, valueType, valueKindNames[v.Kind])
		}
	}
	return nil
}
