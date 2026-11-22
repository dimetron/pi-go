package palace

import (
	"context"
	"errors"
	"fmt"
	"strings"
)

// Named graphs, after Tiramemsu's named-graphs capability. A graph is an
// entity; membership is the layer statement (eid sys:InGraph g), so it has
// transaction and valid time like any fact, falls when the fact is retracted
// and is replayed when the fact is superseded. Reads without a graph see the
// default graph: the union of everything.

// ErrEmptyGraph is returned for a blank graph name.
var ErrEmptyGraph = errors.New("palace: kg: graph name is empty")

func graphValue(g string) (Value, error) {
	if strings.TrimSpace(g) == "" {
		return Value{}, ErrEmptyGraph
	}
	return Ent(g), nil
}

// AddToGraph puts live statement eid into graph g. It is idempotent and
// returns the membership statement.
func (w *KGTx) AddToGraph(eid int64, g string) (int64, bool, error) {
	gv, err := graphValue(g)
	if err != nil {
		return 0, false, err
	}
	if err := w.checkLive(makeOID(eid, tagStmt)); err != nil {
		return 0, false, err
	}
	return w.engineWrite(makeOID(eid, tagStmt), predInGraph, gv)
}

// RemoveFromGraph retracts statement eid's live membership of graph g. The
// statement itself stays. It returns false when eid was not a member.
func (w *KGTx) RemoveFromGraph(eid int64, g string) (bool, error) {
	gv, err := graphValue(g)
	if err != nil {
		return false, err
	}
	members, err := w.memberships(&eid, gv)
	if err != nil || len(members) == 0 {
		return false, err
	}
	for _, m := range members {
		if err := w.retract(m, RetExplicit, RetCascade); err != nil {
			return false, err
		}
	}
	return true, nil
}

// DropGraph retracts every live membership of graph g and returns how many.
// The member statements stay.
func (w *KGTx) DropGraph(g string) (int, error) {
	gv, err := graphValue(g)
	if err != nil {
		return 0, err
	}
	members, err := w.memberships(nil, gv)
	if err != nil {
		return 0, err
	}
	for _, m := range members {
		if live, err := w.isLive(m); err != nil {
			return 0, err
		} else if live {
			if err := w.retract(m, RetExplicit, RetCascade); err != nil {
				return 0, err
			}
		}
	}
	return len(members), nil
}

func (w *KGTx) memberships(eid *int64, g Value) ([]int64, error) {
	in, ok, err := w.engineOID(sysPred(predInGraph), false)
	if err != nil || !ok {
		return nil, err
	}
	gid, ok, err := encodeValue(w.ctx, w.q, g, false)
	if err != nil || !ok {
		return nil, err
	}
	query := "SELECT eid FROM kg_stmt WHERE p = ? AND o = ? AND t_ret IS NULL"
	args := []any{in, gid}
	if eid != nil {
		query += " AND s = ?"
		args = append(args, makeOID(*eid, tagStmt))
	}
	return collectInts(w.ctx, w.q, query+" ORDER BY eid", args)
}

// GraphInfo is one named graph with its member count in a view.
type GraphInfo struct {
	Name    string `json:"name"`
	Members int    `json:"members"`
}

// ListGraphs returns the graphs with at least one member visible in v.
func (v View) ListGraphs(ctx context.Context) ([]GraphInfo, error) {
	var out []GraphInfo
	err := v.kg.read(ctx, func(q querier) error {
		in, ok, err := encodeValue(ctx, q, sysPred(predInGraph), false)
		if err != nil || !ok {
			return err
		}
		cond, cargs := v.cond("")
		rows, err := q.QueryContext(ctx, "SELECT o, count(*) FROM kg_stmt WHERE p = ? AND "+cond+
			" GROUP BY o ORDER BY o", append([]any{in}, cargs...)...)
		if err != nil {
			return fmt.Errorf("palace: kg: list graphs: %w", err)
		}
		type row struct {
			o objectID
			n int
		}
		var rs []row
		var ids []objectID
		for rows.Next() {
			var r row
			if err := rows.Scan(&r.o, &r.n); err != nil {
				rows.Close()
				return err
			}
			rs = append(rs, r)
			ids = append(ids, r.o)
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return err
		}
		d := newDecoder()
		if err := d.load(ctx, q, ids); err != nil {
			return err
		}
		for _, r := range rs {
			out = append(out, GraphInfo{Name: d.value(r.o).String(), Members: r.n})
		}
		return nil
	})
	return out, err
}

// GraphsOf returns the graphs statement eid is a member of in v.
func (v View) GraphsOf(ctx context.Context, eid int64) ([]string, error) {
	var out []string
	err := v.kg.read(ctx, func(q querier) error {
		in, ok, err := encodeValue(ctx, q, sysPred(predInGraph), false)
		if err != nil || !ok {
			return err
		}
		cond, cargs := v.cond("")
		ids, err := collectInts(ctx, q, "SELECT o FROM kg_stmt WHERE s = ? AND p = ? AND "+cond+" ORDER BY o",
			append([]any{makeOID(eid, tagStmt), in}, cargs...))
		if err != nil {
			return err
		}
		oids := make([]objectID, len(ids))
		for i, id := range ids {
			oids[i] = objectID(id)
		}
		d := newDecoder()
		if err := d.load(ctx, q, oids); err != nil {
			return err
		}
		for _, o := range oids {
			out = append(out, d.value(o).String())
		}
		return nil
	})
	return out, err
}
