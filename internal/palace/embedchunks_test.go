package palace

import (
	"errors"
	"fmt"
	"testing"
)

// embedChunks indexes its results rather than appending them. That distinction
// is the point of the function: an earlier version built the result with
// append() and only on success, so a single failed batch shifted every later
// chunk onto someone else's vector. The wrong vector was then stored, silently,
// because the caller only reads embeddings[i].
//
// These tests pin the alignment, not the happy path — the happy path is the one
// that already worked.
func TestEmbedChunksAlignsVectorsToChunks(t *testing.T) {
	chunks := []chunkJob{
		{relPath: "a.md", content: "alpha"},
		{relPath: "b.md", content: "beta"},
		{relPath: "c.md", content: "gamma"},
	}
	emb := &fixedEmbedder{
		vecs: map[string][]float32{
			"alpha": {1, 0},
			"beta":  {0, 1},
			"gamma": {1, 1},
		},
	}
	p := NewWithStore(newTestStore(t), emb)

	got := embedChunks(p, &MineConfig{}, chunks)
	if len(got) != len(chunks) {
		t.Fatalf("returned %d vectors for %d chunks", len(got), len(chunks))
	}
	for i, want := range [][]float32{{1, 0}, {0, 1}, {1, 1}} {
		if len(got[i]) != 2 || got[i][0] != want[0] || got[i][1] != want[1] {
			t.Errorf("embeddings[%d] = %v, want %v (chunk %q)", i, got[i], want, chunks[i].content)
		}
	}
}

// A failed batch must leave nils at its own indices, not shift the chunks after
// it onto the vectors that did succeed. With every batch failing, every slot
// must be nil — a shifted result here would be non-nil.
func TestEmbedChunksFailedBatchLeavesNilsInPlace(t *testing.T) {
	chunks := []chunkJob{
		{relPath: "a.md", content: "alpha"},
		{relPath: "b.md", content: "beta"},
	}
	p := NewWithStore(newTestStore(t), &fixedEmbedder{err: errors.New("model down")})

	got := embedChunks(p, &MineConfig{}, chunks)
	if len(got) != len(chunks) {
		t.Fatalf("returned %d vectors for %d chunks", len(got), len(chunks))
	}
	for i, v := range got {
		if v != nil {
			t.Errorf("embeddings[%d] = %v, want nil: a failed batch must not produce a vector", i, v)
		}
	}
}

// A palace with no embedder must return nil, not a slice of empty vectors: the
// caller distinguishes "no embedder configured" from "embedding produced
// nothing" by the nil.
func TestEmbedChunksWithoutEmbedderReturnsNil(t *testing.T) {
	p := NewWithStore(newTestStore(t), nil)
	if got := embedChunks(p, &MineConfig{}, []chunkJob{{content: "x"}}); got != nil {
		t.Errorf("embedChunks without an embedder = %v, want nil", got)
	}
}

// The progress callback is optional. A nil Phase must not panic, because the
// non-interactive callers pass a bare MineConfig.
func TestEmbedChunksNilPhaseIsSafe(t *testing.T) {
	p := NewWithStore(newTestStore(t), &fixedEmbedder{fallback: []float32{1, 0}})
	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("embedChunks panicked with a nil Phase: %v", r)
		}
	}()
	got := embedChunks(p, &MineConfig{}, []chunkJob{{content: "x"}, {content: "y"}})
	if len(got) != 2 {
		t.Fatalf("returned %d vectors, want 2", len(got))
	}
}

// The phase callback receives the running chunk count and the total, which is
// what the progress line renders. The final report must carry the full total or
// the embed line would end below 100%.
func TestEmbedChunksReportsProgress(t *testing.T) {
	var reports int
	var lastDone, lastTotal int
	cfg := &MineConfig{Phase: func(stage, item string, done, total int) {
		if stage != "embed" {
			t.Errorf("stage = %q, want %q", stage, "embed")
		}
		reports++
		lastDone, lastTotal = done, total
	}}
	p := NewWithStore(newTestStore(t), &fixedEmbedder{fallback: []float32{1, 0}})

	chunks := []chunkJob{{content: "a"}, {content: "b"}, {content: "c"}}
	embedChunks(p, cfg, chunks)

	if reports == 0 {
		t.Fatal("Phase was never called")
	}
	if lastDone != len(chunks) || lastTotal != len(chunks) {
		t.Errorf("final report = %d/%d, want %d/%d", lastDone, lastTotal, len(chunks), len(chunks))
	}
}

// The case that actually separates an indexed result from an appended one: more
// than one batch, where an early batch succeeds and a later one fails. An
// append-on-success implementation returns only the vectors that succeeded, so
// every chunk after the failure reads its neighbour's vector; an indexed one
// returns the full length with nils in the failed range.
//
// The default backend batches at embedBatchSize (8) with one worker when no
// model path is set, so 10 chunks is exactly two batches.
func TestEmbedChunksPartialFailureDoesNotShiftLaterChunks(t *testing.T) {
	if embedBatchSize >= 10 {
		t.Skipf("embedBatchSize=%d would make this a single batch", embedBatchSize)
	}
	chunks := make([]chunkJob, 10)
	for i := range chunks {
		chunks[i] = chunkJob{relPath: fmt.Sprintf("f%d.md", i), content: fmt.Sprintf("chunk %d", i)}
	}
	p := NewWithStore(newTestStore(t), &failAfterEmbedder{succeed: 1, vec: []float32{7, 7}})

	got := embedChunks(p, &MineConfig{}, chunks)

	// The length is the assertion that catches the append bug: an appended
	// result is shorter than the chunk list, so the caller's embeddings[i]
	// reads past the end of what it got.
	if len(got) != len(chunks) {
		t.Fatalf("returned %d vectors for %d chunks: a failed batch must not shorten the result",
			len(got), len(chunks))
	}
	for i, v := range got {
		if i < embedBatchSize {
			if v == nil {
				t.Errorf("embeddings[%d] is nil, but its batch succeeded", i)
			}
			continue
		}
		if v != nil {
			t.Errorf("embeddings[%d] = %v, but its batch failed; a nil was expected", i, v)
		}
	}
}

// failAfterEmbedder succeeds for the first n *batches* and then errors. It
// counts batch invocations, not texts: Embed is called once per batch, so a
// per-text count would abort a whole batch partway and put the failure at a
// boundary the test did not choose.
type failAfterEmbedder struct {
	batches int
	succeed int
	vec     []float32
}

func (f *failAfterEmbedder) Embed(texts []string) ([][]float32, error) {
	f.batches++
	if f.batches > f.succeed {
		return nil, fmt.Errorf("embedder failed on batch %d", f.batches)
	}
	out := make([][]float32, len(texts))
	for i := range out {
		out[i] = f.vec
	}
	return out, nil
}

func (f *failAfterEmbedder) Close() {}
