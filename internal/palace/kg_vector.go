package palace

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"math"
	"slices"
	"strings"
)

// Statement vectors and hybrid recall, after Tiramemsu's vector-retrieval and
// hybrid-recall capabilities (specs/memory/tiramemsu/phase2/design.md#H).
// Vectors are derived records in kg_stmt_vec: immutable, written inside a
// knowledge-graph transaction and visible from that transaction on. A
// statement with a sealed value is never embedded, so no plaintext of a
// sensitive value reaches an embedder or the vector table.

// Errors of vectors and recall.
var (
	ErrVectorConflict     = errors.New("palace: kg: a different vector is already stored for this statement and model")
	ErrBadVector          = errors.New("palace: kg: vector is not usable")
	ErrChannelUnavailable = errors.New("palace: kg: retrieval channel unavailable")
)

// rrfK is the reciprocal-rank-fusion constant.
const rrfK = 60

// embedAuthor is the transaction author of EmbedStatements.
const embedAuthor = "palace-embed"

// Scored is a statement with its cosine score and 1-based rank.
type Scored struct {
	Statement Statement `json:"statement"`
	Score     float64   `json:"score"`
	Rank      int       `json:"rank"`
}

// RecallHit is one fused result. TextRank and VectorRank are 1-based ranks in
// each channel; 0 means the channel did not return the statement. RRF is a
// reciprocal-rank-fusion score (k=60), not a similarity.
type RecallHit struct {
	Statement  Statement `json:"statement"`
	TextRank   int       `json:"text_rank"`
	VectorRank int       `json:"vector_rank"`
	RRF        float64   `json:"rrf"`
}

func encodeVec(v []float32) []byte {
	b := make([]byte, 4*len(v))
	for i, f := range v {
		binary.LittleEndian.PutUint32(b[4*i:], math.Float32bits(f))
	}
	return b
}

func decodeVec(b []byte) []float32 {
	v := make([]float32, len(b)/4)
	for i := range v {
		v[i] = math.Float32frombits(binary.LittleEndian.Uint32(b[4*i:]))
	}
	return v
}

// checkVector requires finite components and a non-zero norm.
func checkVector(v []float32) error {
	if len(v) == 0 {
		return fmt.Errorf("%w: empty", ErrBadVector)
	}
	var norm float64
	for _, f := range v {
		if math.IsNaN(float64(f)) || math.IsInf(float64(f), 0) {
			return fmt.Errorf("%w: non-finite component", ErrBadVector)
		}
		norm += float64(f) * float64(f)
	}
	if norm == 0 {
		return fmt.Errorf("%w: zero norm", ErrBadVector)
	}
	return nil
}

func modelEntity(model string) Value { return Ent("model:" + model) }

// modelDim returns the recorded dimension of an embedding model.
func modelDim(ctx context.Context, q querier, model string) (int, bool, error) {
	s, ok, err := encodeValue(ctx, q, modelEntity(model), false)
	if err != nil || !ok {
		return 0, false, err
	}
	p, ok, err := encodeValue(ctx, q, sysPred(predEmbeddingModel), false)
	if err != nil || !ok {
		return 0, false, err
	}
	var o objectID
	err = q.QueryRowContext(ctx,
		"SELECT o FROM kg_stmt WHERE s = ? AND p = ? AND t_ret IS NULL ORDER BY eid LIMIT 1", s, p).Scan(&o)
	if errNoRows(err) {
		return 0, false, nil
	}
	if err != nil {
		return 0, false, fmt.Errorf("palace: kg: model dimension: %w", err)
	}
	return int(o.payload()), true, nil
}

// requireDim checks dim against the model's recorded dimension, recording it
// on first use.
func (w *KGTx) requireDim(model string, dim int) error {
	rec, ok, err := modelDim(w.ctx, w.q, model)
	if err != nil {
		return err
	}
	if ok {
		if rec != dim {
			return fmt.Errorf("%w: model %q has dimension %d, got %d", ErrBadVector, model, rec, dim)
		}
		return nil
	}
	so, _, err := encodeValue(w.ctx, w.q, modelEntity(model), true)
	if err != nil {
		return err
	}
	_, _, err = w.engineWrite(so, predEmbeddingModel, Int(int64(dim)))
	return err
}

// sealedChain reports whether a statement's object, or the object of any
// statement on its subject chain, is sealed.
func sealedChain(ctx context.Context, q querier, s, o objectID) (bool, error) {
	if o.tag() == tagSealed {
		return true, nil
	}
	for depth := 0; s.tag() == tagStmt; depth++ {
		if depth > 64 {
			return false, errors.New("palace: kg: statement subject chain too deep")
		}
		var next, obj objectID
		err := q.QueryRowContext(ctx, "SELECT s, o FROM kg_stmt WHERE eid = ?", s.payload()).Scan(&next, &obj)
		if errNoRows(err) {
			return false, nil
		}
		if err != nil {
			return false, fmt.Errorf("palace: kg: subject chain: %w", err)
		}
		if obj.tag() == tagSealed {
			return true, nil
		}
		s = next
	}
	return false, nil
}

type embedCandidate struct {
	eid  int64
	text string
}

// embedCandidates lists live, non-engine statements that lack a vector for
// model and are safe to embed, with their text, oldest first.
func embedCandidates(ctx context.Context, q querier, model string) ([]embedCandidate, error) {
	ef, eargs, err := engineFilter(ctx, q, "st.")
	if err != nil {
		return nil, err
	}
	rows, err := q.QueryContext(ctx, `SELECT st.eid, st.s, st.p, st.o FROM kg_stmt st
		WHERE st.t_ret IS NULL AND (st.o & 15) <> 9
		AND NOT EXISTS (SELECT 1 FROM kg_stmt_vec v WHERE v.eid = st.eid AND v.model = ?)`+ef+
		" ORDER BY st.eid", append([]any{model}, eargs...)...)
	if err != nil {
		return nil, fmt.Errorf("palace: kg: embed candidates: %w", err)
	}
	type row struct {
		eid     int64
		s, p, o objectID
	}
	var rs []row
	for rows.Next() {
		var r row
		if err := rows.Scan(&r.eid, &r.s, &r.p, &r.o); err != nil {
			rows.Close()
			return nil, err
		}
		rs = append(rs, r)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, err
	}
	var keep []row
	var ids []objectID
	for _, r := range rs {
		sealed, err := sealedChain(ctx, q, r.s, r.o)
		if err != nil {
			return nil, err
		}
		if sealed {
			continue
		}
		keep = append(keep, r)
		ids = append(ids, r.s, r.p, r.o)
	}
	d := newDecoder()
	if err := d.load(ctx, q, ids); err != nil {
		return nil, err
	}
	out := make([]embedCandidate, len(keep))
	for i, r := range keep {
		out[i] = embedCandidate{r.eid, d.value(r.s).String() + " " + d.value(r.p).String() + " " + d.value(r.o).String()}
	}
	return out, nil
}

func insertVector(w *KGTx, eid int64, model string, vec []float32) error {
	_, err := w.q.ExecContext(w.ctx, "INSERT INTO kg_stmt_vec (eid, model, dim, vec, t) VALUES (?, ?, ?, ?, ?)",
		eid, model, len(vec), encodeVec(vec), w.t)
	if err != nil {
		return fmt.Errorf("palace: kg: store vector: %w", err)
	}
	return nil
}

// EmbedStatements backfills a vector for model on every live, non-engine
// statement that has none (so a superseded statement's replacement is picked
// up), in one knowledge-graph transaction authored "palace-embed"; the vectors
// are visible from that transaction. Statements holding sealed values are
// skipped. It returns how many vectors were stored.
func (kg *KnowledgeGraph) EmbedStatements(ctx context.Context, emb Embedder, model string, batch int) (int, error) {
	if emb == nil {
		return 0, fmt.Errorf("%w: no embedder", ErrChannelUnavailable)
	}
	if strings.TrimSpace(model) == "" {
		return 0, errors.New("palace: kg: embedding model name is empty")
	}
	if batch <= 0 {
		batch = 64
	}
	var pending int
	if err := kg.read(ctx, func(q querier) error {
		c, err := embedCandidates(ctx, q, model)
		pending = len(c)
		return err
	}); err != nil || pending == 0 {
		return 0, err
	}
	var n int
	_, err := kg.Transact(ctx, TxOptions{Author: embedAuthor, Reason: "embed " + model}, func(w *KGTx) error {
		cands, err := embedCandidates(ctx, w.q, model)
		if err != nil {
			return err
		}
		for start := 0; start < len(cands); start += batch {
			chunk := cands[start:min(start+batch, len(cands))]
			texts := make([]string, len(chunk))
			for i, c := range chunk {
				texts[i] = c.text
			}
			vecs, err := emb.Embed(texts)
			if err != nil {
				return fmt.Errorf("palace: kg: embed: %w", err)
			}
			if len(vecs) != len(chunk) {
				return fmt.Errorf("%w: embedder returned %d vectors for %d texts", ErrBadVector, len(vecs), len(chunk))
			}
			for i, v := range vecs {
				if err := checkVector(v); err != nil {
					return err
				}
				if err := w.requireDim(model, len(v)); err != nil {
					return err
				}
				if err := insertVector(w, chunk[i].eid, model, v); err != nil {
					return err
				}
				n++
			}
		}
		return nil
	})
	if err != nil {
		return 0, err
	}
	return n, nil
}

// storedVector returns the vector stored for (eid, model).
func storedVector(ctx context.Context, q querier, eid int64, model string) ([]float32, bool, error) {
	var b []byte
	err := q.QueryRowContext(ctx, "SELECT vec FROM kg_stmt_vec WHERE eid = ? AND model = ?", eid, model).Scan(&b)
	if errNoRows(err) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, fmt.Errorf("palace: kg: read vector: %w", err)
	}
	return decodeVec(b), true, nil
}

// PutVector stores a caller-supplied vector for statement eid under model, in
// its own transaction. The same vector again is a no-op; a different one is
// ErrVectorConflict. The vector must be finite with a non-zero norm and match
// the model's recorded dimension (the first vector of a model records it). A
// statement holding a sealed value is refused.
func (kg *KnowledgeGraph) PutVector(ctx context.Context, eid int64, model string, vec []float32) error {
	if err := checkVector(vec); err != nil {
		return err
	}
	if strings.TrimSpace(model) == "" {
		return errors.New("palace: kg: embedding model name is empty")
	}
	same := func(q querier) (bool, error) {
		old, ok, err := storedVector(ctx, q, eid, model)
		if err != nil || !ok {
			return false, err
		}
		if !slices.Equal(old, vec) {
			return false, fmt.Errorf("%w: e%d", ErrVectorConflict, eid)
		}
		return true, nil
	}
	var done bool
	if err := kg.read(ctx, func(q querier) (err error) { done, err = same(q); return }); err != nil || done {
		return err
	}
	_, err := kg.Transact(ctx, TxOptions{Author: embedAuthor, Reason: "put vector"}, func(w *KGTx) error {
		if ok, err := same(w.q); err != nil || ok {
			return err
		}
		var s, o objectID
		err := w.q.QueryRowContext(w.ctx, "SELECT s, o FROM kg_stmt WHERE eid = ?", eid).Scan(&s, &o)
		if errNoRows(err) {
			return fmt.Errorf("palace: kg: no statement e%d", eid)
		}
		if err != nil {
			return fmt.Errorf("palace: kg: load statement: %w", err)
		}
		sealed, err := sealedChain(w.ctx, w.q, s, o)
		if err != nil {
			return err
		}
		if sealed {
			return fmt.Errorf("%w: e%d holds a sealed value", ErrBadVector, eid)
		}
		if err := w.requireDim(model, len(vec)); err != nil {
			return err
		}
		return insertVector(w, eid, model, vec)
	})
	return err
}

// channelCheck fails with ErrChannelUnavailable when there is no embedder or
// no vector at all for model.
func channelCheck(ctx context.Context, q querier, emb Embedder, model string) error {
	if emb == nil {
		return fmt.Errorf("%w: no embedder", ErrChannelUnavailable)
	}
	var n int
	if err := q.QueryRowContext(ctx, "SELECT count(*) FROM (SELECT 1 FROM kg_stmt_vec WHERE model = ? LIMIT 1)", model).Scan(&n); err != nil {
		return fmt.Errorf("palace: kg: vector channel: %w", err)
	}
	if n == 0 {
		return fmt.Errorf("%w: no vectors for model %q", ErrChannelUnavailable, model)
	}
	return nil
}

// VectorSearch ranks the statements v selects by exact cosine similarity to
// query, using vectors stored by the view's transaction bound (Now and
// History: the last committed one; AsOf(t): t). It fails with
// ErrChannelUnavailable when there is no embedder or no vector for model.
func (v View) VectorSearch(ctx context.Context, emb Embedder, model, query string, k int) ([]Scored, error) {
	var out []Scored
	err := v.kg.read(ctx, func(q querier) error {
		var err error
		out, err = v.vectorSearch(ctx, q, emb, model, query, k)
		return err
	})
	return out, err
}

func (v View) vectorSearch(ctx context.Context, q querier, emb Embedder, model, query string, k int) ([]Scored, error) {
	if err := channelCheck(ctx, q, emb, model); err != nil {
		return nil, err
	}
	if k <= 0 {
		k = 10
	}
	vecs, err := emb.Embed([]string{query})
	if err != nil {
		return nil, fmt.Errorf("palace: kg: embed query: %w", err)
	}
	if len(vecs) != 1 {
		return nil, fmt.Errorf("%w: embedder returned %d vectors for one query", ErrBadVector, len(vecs))
	}
	qv := vecs[0]
	if err := checkVector(qv); err != nil {
		return nil, err
	}
	if rec, ok, err := modelDim(ctx, q, model); err != nil {
		return nil, err
	} else if ok && rec != len(qv) {
		return nil, fmt.Errorf("%w: model %q has dimension %d, query has %d", ErrBadVector, model, rec, len(qv))
	}
	bound, err := lastT(ctx, q)
	if err != nil {
		return nil, err
	}
	if v.mode == viewAsOf && v.t < bound {
		bound = v.t
	}
	cond, cargs, err := v.condQ(ctx, q, "st.")
	if err != nil {
		return nil, err
	}
	ef, eargs, err := engineFilter(ctx, q, "st.")
	if err != nil {
		return nil, err
	}
	args := append(append([]any{model, bound}, cargs...), eargs...)
	rows, err := q.QueryContext(ctx, `SELECT v.eid, v.vec FROM kg_stmt_vec v JOIN kg_stmt st ON st.eid = v.eid
		WHERE v.model = ? AND v.t <= ? AND `+cond+ef, args...)
	if err != nil {
		return nil, fmt.Errorf("palace: kg: vector search: %w", err)
	}
	var qn float64
	for _, f := range qv {
		qn += float64(f) * float64(f)
	}
	qn = math.Sqrt(qn)
	type hit struct {
		eid   int64
		score float64
	}
	var hits []hit
	for rows.Next() {
		var (
			eid int64
			b   []byte
		)
		if err := rows.Scan(&eid, &b); err != nil {
			rows.Close()
			return nil, err
		}
		if len(b) != 4*len(qv) {
			continue
		}
		var dot, n float64
		for i, f := range decodeVec(b) {
			dot += float64(f) * float64(qv[i])
			n += float64(f) * float64(f)
		}
		if n == 0 {
			continue
		}
		hits = append(hits, hit{eid, dot / (math.Sqrt(n) * qn)})
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, err
	}
	slices.SortFunc(hits, func(a, b hit) int {
		switch {
		case a.score > b.score:
			return -1
		case a.score < b.score:
			return 1
		}
		return int(a.eid - b.eid)
	})
	if len(hits) > k {
		hits = hits[:k]
	}
	if len(hits) == 0 {
		return nil, nil
	}
	ids := make([]any, len(hits))
	for i, h := range hits {
		ids[i] = h.eid
	}
	sts, err := v.query(ctx, q, "WHERE st.eid IN ("+placeholders(len(ids))+")", ids...)
	if err != nil {
		return nil, err
	}
	byEID := make(map[int64]Statement, len(sts))
	for _, s := range sts {
		byEID[s.EID] = s
	}
	out := make([]Scored, 0, len(hits))
	for i, h := range hits {
		out = append(out, Scored{Statement: byEID[h.eid], Score: h.score, Rank: i + 1})
	}
	return out, nil
}

// Recall fuses TextSearch and VectorSearch by reciprocal rank (k=60) on one
// snapshot. Each hit reports its rank in each channel; RRF is the fused score
// and is not a similarity. A missing channel fails the call with
// ErrChannelUnavailable instead of silently degrading.
func (v View) Recall(ctx context.Context, emb Embedder, model, query string, k int) ([]RecallHit, error) {
	if k <= 0 {
		k = 10
	}
	pool := max(3*k, 50)
	var out []RecallHit
	err := v.kg.read(ctx, func(q querier) error {
		vs, err := v.vectorSearch(ctx, q, emb, model, query, pool)
		if err != nil {
			return err
		}
		ts, err := v.textSearch(ctx, q, query, pool)
		if err != nil {
			return err
		}
		byEID := map[int64]*RecallHit{}
		var order []int64
		hit := func(s Statement) *RecallHit {
			h, ok := byEID[s.EID]
			if !ok {
				h = &RecallHit{Statement: s}
				byEID[s.EID] = h
				order = append(order, s.EID)
			}
			return h
		}
		for i, s := range ts {
			h := hit(s)
			h.TextRank = i + 1
			h.RRF += 1 / float64(rrfK+i+1)
		}
		for _, s := range vs {
			h := hit(s.Statement)
			h.VectorRank = s.Rank
			h.RRF += 1 / float64(rrfK+s.Rank)
		}
		for _, e := range order {
			out = append(out, *byEID[e])
		}
		slices.SortStableFunc(out, func(a, b RecallHit) int {
			switch {
			case a.RRF > b.RRF:
				return -1
			case a.RRF < b.RRF:
				return 1
			}
			return int(a.Statement.EID - b.Statement.EID)
		})
		if len(out) > k {
			out = out[:k]
		}
		return nil
	})
	return out, err
}
