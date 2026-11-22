package palace

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
)

// Saved answers, after Tiramemsu's saved-answer-invalidation capability. An
// answer keeps its query, view, rows, the statements the rows rest on, the
// predicates the query reads and an event checkpoint. Checking it replays
// the events after the checkpoint conservatively: anything that might change
// the rows marks it stale, and only a successful refresh clears that.
// Saved answers are derived records: saving, checking, refreshing and
// deleting them never writes a transaction, statement or term.

// ErrNoAnswer is returned for an unknown saved answer.
var ErrNoAnswer = errors.New("palace: kg: no saved answer by that name")

// sealedPlaceholder replaces a value decoded from a sealed term in a stored
// result, so a saved answer never keeps plaintext that erasure must destroy.
const sealedPlaceholder = "sys:Sealed"

// SavedAnswer is one saved answer and its status.
type SavedAnswer struct {
	Name       string             `json:"name"`
	Lang       string             `json:"lang"`
	Query      string             `json:"query"`
	View       string             `json:"view"`
	Vars       []string           `json:"vars,omitempty"`
	Rows       []map[string]Value `json:"rows"`
	Deps       []int64            `json:"deps,omitempty"`
	Checkpoint int64              `json:"checkpoint"`
	// Stale is empty while the answer is fresh, otherwise why it is not.
	Stale string `json:"stale,omitempty"`
}

// viewSpec is the stored form of a View.
type viewSpec struct {
	Mode    viewMode `json:"mode"`
	T       int64    `json:"t,omitempty"`
	ValidAt *int64   `json:"valid_at,omitempty"`
	Graphs  []string `json:"graphs,omitempty"`
}

func (v View) spec() viewSpec {
	return viewSpec{Mode: v.mode, T: v.t, ValidAt: v.validAt, Graphs: v.graphs}
}

func (kg *KnowledgeGraph) viewFrom(s viewSpec) View {
	return View{kg: kg, mode: s.Mode, t: s.T, validAt: s.ValidAt, graphs: s.Graphs}
}

type storedResult struct {
	Vars []string           `json:"vars"`
	Rows []map[string]Value `json:"rows"`
}

// derived runs fn in one SQL transaction that commits: the reads are one
// snapshot, and the only writes are to kg_saved_answer.
func (kg *KnowledgeGraph) derived(ctx context.Context, fn func(q querier) error) error {
	db, err := kg.database()
	if err != nil {
		return err
	}
	sqlTx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("palace: kg: begin: %w", err)
	}
	defer func() { _ = sqlTx.Rollback() }()
	if err := fn(sqlTx); err != nil {
		return err
	}
	if err := sqlTx.Commit(); err != nil {
		return fmt.Errorf("palace: kg: commit: %w", err)
	}
	return nil
}

func parseQuery(lang, text string) (*Query, error) {
	switch strings.ToLower(lang) {
	case "sparql":
		return ParseSPARQL(text)
	case "cypher":
		return ParseCypher(text)
	}
	return nil, fmt.Errorf("palace: kg: saved answers support sparql and cypher, not %q", lang)
}

// evaluate runs the answer's query in snapshot q and returns the stored
// fields. A future AsOf bound is frozen to the last committed transaction, so
// a saved as-of answer is really fixed.
func (kg *KnowledgeGraph) evaluate(ctx context.Context, q querier, lang, text string, v View) (
	res *QueryResult, spec viewSpec, last int64, err error) {
	parsed, err := parseQuery(lang, text)
	if err != nil {
		return nil, spec, 0, err
	}
	last, err = lastT(ctx, q)
	if err != nil {
		return nil, spec, 0, err
	}
	if v.mode == viewAsOf && v.t > last {
		v.t = last
	}
	res, err = v.selectQ(ctx, q, parsed, newMeter(ctx, Budget{}))
	if err != nil {
		return nil, spec, 0, err
	}
	if err := redactSealed(ctx, q, res); err != nil {
		return nil, spec, 0, err
	}
	return res, v.spec(), last, nil
}

// redactSealed replaces, in res, every literal value that the matched
// statements hold as a sealed term.
func redactSealed(ctx context.Context, q querier, res *QueryResult) error {
	if len(res.Deps) == 0 {
		return nil
	}
	sealed := map[Value]bool{}
	for start := 0; start < len(res.Deps); start += chunkSize {
		chunk := res.Deps[start:min(start+chunkSize, len(res.Deps))]
		args := make([]any, len(chunk))
		for i, e := range chunk {
			args[i] = e
		}
		sts, err := queryStatements(ctx, q, "WHERE st.eid IN ("+placeholders(len(args))+") AND (st.o & 15) = 9", args...)
		if err != nil {
			return err
		}
		for _, st := range sts {
			sealed[st.Object] = true
		}
	}
	if len(sealed) == 0 {
		return nil
	}
	for _, row := range res.Rows {
		for k, val := range row {
			if isLiteralKind(val.Kind) && sealed[val] {
				row[k] = Str(sealedPlaceholder)
			}
		}
	}
	return nil
}

// SaveAnswer evaluates the query on v and saves it under name, replacing an
// answer of the same name.
func (kg *KnowledgeGraph) SaveAnswer(ctx context.Context, name, lang, query string, v View) (*SavedAnswer, error) {
	if strings.TrimSpace(name) == "" {
		return nil, errors.New("palace: kg: saved answer needs a name")
	}
	var out *SavedAnswer
	err := kg.derived(ctx, func(q querier) error {
		res, spec, last, err := kg.evaluate(ctx, q, lang, query, v)
		if err != nil {
			return err
		}
		if err := writeAnswer(ctx, q, name, lang, query, spec, res, last); err != nil {
			return err
		}
		out = &SavedAnswer{Name: name, Lang: lang, Query: query, View: kg.viewFrom(spec).String(),
			Vars: res.Vars, Rows: res.Rows, Deps: res.Deps, Checkpoint: last}
		return nil
	})
	return out, err
}

func writeAnswer(ctx context.Context, q querier, name, lang, query string, spec viewSpec, res *QueryResult, last int64) error {
	view, _ := json.Marshal(spec)
	result, _ := json.Marshal(storedResult{Vars: res.Vars, Rows: res.Rows})
	deps, _ := json.Marshal(res.Deps)
	var preds []byte
	if res.Preds == nil {
		preds = []byte("null")
	} else {
		preds, _ = json.Marshal(res.Preds)
	}
	_, err := q.ExecContext(ctx, `INSERT INTO kg_saved_answer
		(name, lang, query, view, result, deps, preds, negative, checkpoint, stale)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, NULL)
		ON CONFLICT (name) DO UPDATE SET lang = excluded.lang, query = excluded.query, view = excluded.view,
			result = excluded.result, deps = excluded.deps, preds = excluded.preds,
			negative = excluded.negative, checkpoint = excluded.checkpoint, stale = NULL`,
		name, lang, query, string(view), string(result), string(deps), string(preds), res.Negative, last)
	if err != nil {
		return fmt.Errorf("palace: kg: save answer: %w", err)
	}
	return nil
}

type answerRow struct {
	name, lang, query string
	spec              viewSpec
	result            storedResult
	deps              []int64
	preds             []Value // nil = any predicate
	negative          bool
	checkpoint        int64
	stale             sql.NullString
}

func loadAnswer(ctx context.Context, q querier, name string) (*answerRow, error) {
	var (
		r                         answerRow
		view, result, deps, preds string
	)
	err := q.QueryRowContext(ctx, `SELECT name, lang, query, view, result, deps, preds, negative, checkpoint, stale
		FROM kg_saved_answer WHERE name = ?`, name).
		Scan(&r.name, &r.lang, &r.query, &view, &result, &deps, &preds, &r.negative, &r.checkpoint, &r.stale)
	if errNoRows(err) {
		return nil, fmt.Errorf("%w: %s", ErrNoAnswer, name)
	}
	if err != nil {
		return nil, fmt.Errorf("palace: kg: load answer: %w", err)
	}
	if err := json.Unmarshal([]byte(view), &r.spec); err != nil {
		return nil, fmt.Errorf("palace: kg: answer view: %w", err)
	}
	if result != "null" {
		if err := json.Unmarshal([]byte(result), &r.result); err != nil {
			return nil, fmt.Errorf("palace: kg: answer result: %w", err)
		}
	}
	if err := json.Unmarshal([]byte(deps), &r.deps); err != nil {
		return nil, fmt.Errorf("palace: kg: answer deps: %w", err)
	}
	if err := json.Unmarshal([]byte(preds), &r.preds); err != nil {
		return nil, fmt.Errorf("palace: kg: answer preds: %w", err)
	}
	return &r, nil
}

func (kg *KnowledgeGraph) answerOf(r *answerRow) *SavedAnswer {
	return &SavedAnswer{Name: r.name, Lang: r.lang, Query: r.query, View: kg.viewFrom(r.spec).String(),
		Vars: r.result.Vars, Rows: r.result.Rows, Deps: r.deps, Checkpoint: r.checkpoint, Stale: r.stale.String}
}

// CheckAnswer replays the events after the answer's checkpoint. An answer on
// an as-of view is fixed and stays fresh. Otherwise it becomes stale when a
// statement it rests on is retracted, when a statement with a predicate it
// reads is asserted (it may add rows), when a query with OPTIONAL, NOT EXISTS
// or COUNT sees a retraction of such a statement (it may add or change rows),
// or, for a graph-scoped view, on any membership change. A fresh check
// advances the checkpoint; a stale answer keeps its checkpoint until it is
// refreshed. Checking is idempotent.
func (kg *KnowledgeGraph) CheckAnswer(ctx context.Context, name string) (*SavedAnswer, error) {
	var out *SavedAnswer
	err := kg.derived(ctx, func(q querier) error {
		r, err := loadAnswer(ctx, q, name)
		if err != nil {
			return err
		}
		if r.stale.Valid || r.spec.Mode == viewAsOf {
			out = kg.answerOf(r)
			return nil
		}
		last, err := lastT(ctx, q)
		if err != nil {
			return err
		}
		reason, err := staleReason(ctx, q, r, last)
		if err != nil {
			return err
		}
		if reason != "" {
			r.stale = sql.NullString{String: reason, Valid: true}
			_, err = q.ExecContext(ctx, "UPDATE kg_saved_answer SET stale = ? WHERE name = ?", reason, name)
		} else {
			r.checkpoint = last
			_, err = q.ExecContext(ctx, "UPDATE kg_saved_answer SET checkpoint = ? WHERE name = ?", last, name)
		}
		if err != nil {
			return fmt.Errorf("palace: kg: check answer: %w", err)
		}
		out = kg.answerOf(r)
		return nil
	})
	return out, err
}

func staleReason(ctx context.Context, q querier, r *answerRow, last int64) (string, error) {
	if last <= r.checkpoint {
		return "", nil
	}
	window := []any{r.checkpoint, last}
	// A statement the rows rest on was retracted.
	for start := 0; start < len(r.deps); start += chunkSize {
		chunk := r.deps[start:min(start+chunkSize, len(r.deps))]
		args := append([]any{}, window...)
		for _, e := range chunk {
			args = append(args, e)
		}
		var eid, t int64
		err := q.QueryRowContext(ctx, "SELECT eid, t_ret FROM kg_stmt WHERE t_ret > ? AND t_ret <= ? AND eid IN ("+
			placeholders(len(chunk))+") ORDER BY t_ret, eid LIMIT 1", args...).Scan(&eid, &t)
		if err == nil {
			return fmt.Sprintf("e%d retracted at tx%d", eid, t), nil
		}
		if !errNoRows(err) {
			return "", fmt.Errorf("palace: kg: check answer: %w", err)
		}
	}
	// Statements with a predicate the query reads (or any, when unknown).
	predCond, predArgs, ok, err := predicateFilter(ctx, q, r.preds)
	if err != nil {
		return "", err
	}
	if ok {
		eid, t, found, err := firstEvent(ctx, q, "t_add", predCond, append(append([]any{}, window...), predArgs...))
		if err != nil {
			return "", err
		}
		if found {
			return fmt.Sprintf("e%d asserted at tx%d may add rows", eid, t), nil
		}
		if r.negative {
			eid, t, found, err := firstEvent(ctx, q, "t_ret", predCond, append(append([]any{}, window...), predArgs...))
			if err != nil {
				return "", err
			}
			if found {
				return fmt.Sprintf("e%d retracted at tx%d may change rows", eid, t), nil
			}
		}
	}
	// A graph-scoped view depends on memberships.
	if len(r.spec.Graphs) > 0 {
		in, okIn, err := encodeValue(ctx, q, sysPred(predInGraph), false)
		if err != nil {
			return "", err
		}
		if okIn {
			for _, col := range []string{"t_add", "t_ret"} {
				eid, t, found, err := firstEvent(ctx, q, col, "p = ?", append(append([]any{}, window...), in))
				if err != nil {
					return "", err
				}
				if found {
					return fmt.Sprintf("graph membership e%d changed at tx%d", eid, t), nil
				}
			}
		}
	}
	return "", nil
}

// predicateFilter turns the saved predicates into a SQL condition. ok is
// false when none of them exists in the dictionary yet and they are not
// "any", in which case nothing can match.
func predicateFilter(ctx context.Context, q querier, preds []Value) (string, []any, bool, error) {
	if preds == nil {
		return "(p & 15) = 1", nil, true, nil
	}
	var args []any
	for _, p := range preds {
		o, found, err := encodeValue(ctx, q, p, false)
		if err != nil {
			return "", nil, false, err
		}
		if found {
			args = append(args, o)
		}
	}
	if len(args) == 0 {
		// The predicates are unknown now; an assert interns them first.
		return "0", nil, false, nil
	}
	return "p IN (" + placeholders(len(args)) + ")", args, true, nil
}

func firstEvent(ctx context.Context, q querier, col, cond string, args []any) (int64, int64, bool, error) {
	var eid, t int64
	err := q.QueryRowContext(ctx, "SELECT eid, "+col+" FROM kg_stmt WHERE "+col+" > ? AND "+col+" <= ? AND "+cond+
		" ORDER BY "+col+", eid LIMIT 1", args...).Scan(&eid, &t)
	if errNoRows(err) {
		return 0, 0, false, nil
	}
	if err != nil {
		return 0, 0, false, fmt.Errorf("palace: kg: check answer: %w", err)
	}
	return eid, t, true, nil
}

// RefreshAnswer re-evaluates a saved answer. Only success clears its stale
// mark and moves its checkpoint; a failure leaves it as it was.
func (kg *KnowledgeGraph) RefreshAnswer(ctx context.Context, name string) (*SavedAnswer, error) {
	var out *SavedAnswer
	err := kg.derived(ctx, func(q querier) error {
		r, err := loadAnswer(ctx, q, name)
		if err != nil {
			return err
		}
		res, spec, last, err := kg.evaluate(ctx, q, r.lang, r.query, kg.viewFrom(r.spec))
		if err != nil {
			return err
		}
		if err := writeAnswer(ctx, q, name, r.lang, r.query, spec, res, last); err != nil {
			return err
		}
		out = &SavedAnswer{Name: name, Lang: r.lang, Query: r.query, View: kg.viewFrom(spec).String(),
			Vars: res.Vars, Rows: res.Rows, Deps: res.Deps, Checkpoint: last}
		return nil
	})
	return out, err
}

// DeleteAnswer removes a saved answer. Deleting an unknown one succeeds.
func (kg *KnowledgeGraph) DeleteAnswer(ctx context.Context, name string) error {
	return kg.derived(ctx, func(q querier) error {
		if _, err := q.ExecContext(ctx, "DELETE FROM kg_saved_answer WHERE name = ?", name); err != nil {
			return fmt.Errorf("palace: kg: delete answer: %w", err)
		}
		return nil
	})
}

// ListAnswers returns every saved answer, by name.
func (kg *KnowledgeGraph) ListAnswers(ctx context.Context) ([]SavedAnswer, error) {
	var out []SavedAnswer
	err := kg.read(ctx, func(q querier) error {
		rows, err := q.QueryContext(ctx, "SELECT name FROM kg_saved_answer ORDER BY name")
		if err != nil {
			return fmt.Errorf("palace: kg: list answers: %w", err)
		}
		var names []string
		for rows.Next() {
			var n string
			if err := rows.Scan(&n); err != nil {
				rows.Close()
				return err
			}
			names = append(names, n)
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return err
		}
		for _, n := range names {
			r, err := loadAnswer(ctx, q, n)
			if err != nil {
				return err
			}
			out = append(out, *kg.answerOf(r))
		}
		return nil
	})
	return out, err
}
