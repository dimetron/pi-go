package palace

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"
)

// objectID is a tagged value as stored in kg_stmt's s, p and o columns:
// payload<<4 | tag, as in Tiramemsu's ObjectId. Entities, strings and numbers
// carry a kg_term id; statements carry an eid; integers, booleans, times and
// transactions are inline. One integer per column keeps the indexes small and
// makes a statement reference just another value.
type objectID int64

const (
	tagEntity = 1
	tagStmt   = 2
	tagStr    = 3
	tagInt    = 4
	tagNum    = 5
	tagBool   = 6
	tagTime   = 7
	tagTx     = 8
	tagSealed = 9 // kg_term row: "<subject oid>:<base64 iv||ciphertext>"
)

func makeOID(payload int64, tag int) objectID { return objectID(payload<<4 | int64(tag)) }
func (o objectID) tag() int                   { return int(int64(o) & 0xF) }
func (o objectID) payload() int64             { return int64(o) >> 4 }

// ValueKind says what a Value holds.
type ValueKind uint8

// The kinds a Value can hold.
const (
	KindEntity ValueKind = iota + 1
	KindStatement
	KindString
	KindInt
	KindNumber
	KindBool
	KindTime
	KindTx
)

// Value is a subject, predicate or object of a statement.
type Value struct {
	Kind ValueKind `json:"kind"`
	// Text is the normalized entity id or the string value.
	Text string `json:"text,omitempty"`
	// Int is the integer, eid, transaction number, epoch ms (time) or 0/1 (bool).
	Int int64 `json:"int,omitempty"`
	// Num is the number value.
	Num float64 `json:"num,omitempty"`
}

// Ent is the entity with the given name, normalized as entity ids always are.
func Ent(name string) Value { return Value{Kind: KindEntity, Text: entityID(name)} }

// StmtRef refers to the statement with the given eid.
func StmtRef(eid int64) Value { return Value{Kind: KindStatement, Int: eid} }

// Str is a string literal.
func Str(s string) Value { return Value{Kind: KindString, Text: s} }

// Int is an integer literal.
func Int(i int64) Value { return Value{Kind: KindInt, Int: i} }

// Num is a number literal.
func Num(f float64) Value { return Value{Kind: KindNumber, Num: f} }

// Bool is a boolean literal.
func Bool(b bool) Value {
	if b {
		return Value{Kind: KindBool, Int: 1}
	}
	return Value{Kind: KindBool}
}

// TimeVal is a time literal at millisecond precision.
func TimeVal(t time.Time) Value { return Value{Kind: KindTime, Int: t.UnixMilli()} }

func txVal(t int64) Value { return Value{Kind: KindTx, Int: t} }

// engine predicates. They contain upper-case letters, and entityID lower-cases
// every name, so no user or legacy predicate can ever be equal to one.
const (
	predSupersedes  = "sys:Supersedes"
	predConfirmedBy = "sys:ConfirmedBy"
	predDerivedFrom = "sys:DerivedFrom"
	predCardinality = "sys:Cardinality"
	predUnique      = "sys:Unique"

	predInGraph        = "sys:InGraph"
	predValueType      = "sys:ValueType"
	predSubjectType    = "sys:SubjectType"
	predIsEdge         = "sys:IsEdge"
	predSensitive      = "sys:Sensitive"
	predRuleText       = "sys:RuleText"
	predEmbeddingModel = "sys:EmbeddingModel"
)

var enginePredicates = []string{
	predSupersedes, predConfirmedBy, predDerivedFrom, predCardinality, predUnique,
	predInGraph, predValueType, predSubjectType, predIsEdge, predSensitive, predRuleText, predEmbeddingModel,
}

func sysPred(name string) Value { return Value{Kind: KindEntity, Text: name} }

func isEnginePredicate(v Value) bool {
	if v.Kind != KindEntity {
		return false
	}
	for _, p := range enginePredicates {
		if v.Text == p {
			return true
		}
	}
	return false
}

// Layer predicates written by the palace itself.
const (
	predConfidence = "confidence"
	predSource     = "source"
	predSourceFile = "source_file"
)

// IsStatement reports whether v refers to a statement.
func (v Value) IsStatement() bool { return v.Kind == KindStatement }

// String renders v for people: the entity id, "e<eid>", the literal text.
func (v Value) String() string {
	switch v.Kind {
	case KindEntity, KindString:
		return v.Text
	case KindStatement:
		return "e" + strconv.FormatInt(v.Int, 10)
	case KindInt:
		return strconv.FormatInt(v.Int, 10)
	case KindNumber:
		return strconv.FormatFloat(v.Num, 'g', -1, 64)
	case KindBool:
		return strconv.FormatBool(v.Int != 0)
	case KindTime:
		return time.UnixMilli(v.Int).UTC().Format(time.RFC3339)
	case KindTx:
		return "tx" + strconv.FormatInt(v.Int, 10)
	}
	return ""
}

// querier is the part of *sql.Tx and *sql.DB the engine uses.
type querier interface {
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}

// termKey returns the dictionary tag and lexical form of a term-backed value.
func termKey(v Value) (tag int, lex string, num *float64, ok bool) {
	switch v.Kind {
	case KindEntity:
		return tagEntity, v.Text, nil, true
	case KindString:
		return tagStr, v.Text, nil, true
	case KindNumber:
		n := v.Num
		return tagNum, strconv.FormatFloat(n, 'g', -1, 64), &n, true
	}
	return 0, "", nil, false
}

const maxInline = int64(1) << 59

// encodeValue turns v into its objectID. Term-backed values are interned when
// create is set; otherwise a missing term reports found=false, which a read
// treats as "matches nothing".
func encodeValue(ctx context.Context, q querier, v Value, create bool) (objectID, bool, error) {
	switch v.Kind {
	case KindStatement:
		if v.Int <= 0 {
			return 0, false, fmt.Errorf("palace: kg: invalid statement id %d", v.Int)
		}
		return makeOID(v.Int, tagStmt), true, nil
	case KindInt, KindTime:
		if v.Int >= maxInline || v.Int < -maxInline {
			return 0, false, fmt.Errorf("palace: kg: integer %d out of range", v.Int)
		}
		tag := tagInt
		if v.Kind == KindTime {
			tag = tagTime
		}
		return makeOID(v.Int, tag), true, nil
	case KindBool:
		return makeOID(v.Int&1, tagBool), true, nil
	case KindTx:
		return makeOID(v.Int, tagTx), true, nil
	}
	tag, lex, num, ok := termKey(v)
	if !ok {
		return 0, false, fmt.Errorf("palace: kg: unknown value kind %d", v.Kind)
	}
	if v.Kind == KindEntity && strings.TrimSpace(lex) == "" {
		return 0, false, errors.New("palace: kg: empty entity name")
	}
	return internTerm(ctx, q, tag, lex, num, create)
}

// internTerm returns the id of dictionary term (tag, lex), inserting it when
// create is set.
func internTerm(ctx context.Context, q querier, tag int, lex string, num *float64, create bool) (objectID, bool, error) {
	var id int64
	err := q.QueryRowContext(ctx, "SELECT id FROM kg_term WHERE tag = ? AND lex = ?", tag, lex).Scan(&id)
	switch {
	case err == nil:
		return makeOID(id, tag), true, nil
	case !errors.Is(err, sql.ErrNoRows):
		return 0, false, fmt.Errorf("palace: kg: look up term: %w", err)
	case !create:
		return 0, false, nil
	}
	err = q.QueryRowContext(ctx,
		"INSERT INTO kg_term (tag, lex, num) VALUES (?, ?, ?) RETURNING id", tag, lex, num).Scan(&id)
	if err != nil {
		return 0, false, fmt.Errorf("palace: kg: intern term: %w", err)
	}
	return makeOID(id, tag), true, nil
}

// decoder resolves objectIDs back to Values, loading dictionary terms in
// batches.
type decoder struct {
	terms map[int64]Value
	keys  map[objectID][]byte // seal keys by data subject; nil = destroyed
}

func newDecoder() *decoder { return &decoder{terms: map[int64]Value{}, keys: map[objectID][]byte{}} }

// load fetches every term the given ids reference that is not cached yet.
func (d *decoder) load(ctx context.Context, q querier, ids []objectID) error {
	var need []any
	seen := map[int64]bool{}
	for _, o := range ids {
		switch o.tag() {
		case tagEntity, tagStr, tagNum, tagSealed:
			id := o.payload()
			if _, ok := d.terms[id]; !ok && !seen[id] {
				seen[id] = true
				need = append(need, id)
			}
		}
	}
	for start := 0; start < len(need); start += chunkSize {
		chunk := need[start:min(start+chunkSize, len(need))]
		sealed := map[int64]string{}
		rows, err := q.QueryContext(ctx,
			"SELECT id, tag, lex, num FROM kg_term WHERE id IN ("+placeholders(len(chunk))+")", chunk...)
		if err != nil {
			return fmt.Errorf("palace: kg: load terms: %w", err)
		}
		for rows.Next() {
			var (
				id, tag int64
				lex     string
				num     sql.NullFloat64
			)
			if err := rows.Scan(&id, &tag, &lex, &num); err != nil {
				rows.Close()
				return fmt.Errorf("palace: kg: scan term: %w", err)
			}
			switch tag {
			case tagEntity:
				d.terms[id] = Value{Kind: KindEntity, Text: lex}
			case tagStr:
				d.terms[id] = Value{Kind: KindString, Text: lex}
			case tagNum:
				d.terms[id] = Value{Kind: KindNumber, Num: num.Float64}
			case tagSealed:
				sealed[id] = lex
			}
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return fmt.Errorf("palace: kg: load terms: %w", err)
		}
		for id, lex := range sealed {
			v, err := openSealed(ctx, q, lex, d.keys)
			if err != nil {
				return err
			}
			d.terms[id] = v
		}
	}
	return nil
}

// value decodes o; its term must have been loaded.
func (d *decoder) value(o objectID) Value {
	switch o.tag() {
	case tagStmt:
		return StmtRef(o.payload())
	case tagInt:
		return Int(o.payload())
	case tagBool:
		return Value{Kind: KindBool, Int: o.payload()}
	case tagTime:
		return Value{Kind: KindTime, Int: o.payload()}
	case tagTx:
		return txVal(o.payload())
	}
	return d.terms[o.payload()]
}

// chunkSize bounds the number of parameters in one IN (...) list.
const chunkSize = 500

func placeholders(n int) string {
	if n <= 0 {
		return ""
	}
	return strings.Repeat("?,", n-1) + "?"
}
