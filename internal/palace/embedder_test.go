package palace

import (
	"math"
	"testing"
)

func TestCosineSimilarity_Identical(t *testing.T) {
	v := []float32{1, 2, 3, 4, 5}
	sim := CosineSimilarity(v, v)
	if diff := math.Abs(float64(sim) - 1.0); diff > 1e-6 {
		t.Fatalf("identical vectors: got %f, want 1.0", sim)
	}
}

func TestCosineSimilarity_Orthogonal(t *testing.T) {
	a := []float32{1, 0, 0}
	b := []float32{0, 1, 0}
	sim := CosineSimilarity(a, b)
	if diff := math.Abs(float64(sim)); diff > 1e-6 {
		t.Fatalf("orthogonal vectors: got %f, want 0.0", sim)
	}
}

func TestCosineSimilarity_Opposite(t *testing.T) {
	a := []float32{1, 2, 3}
	b := []float32{-1, -2, -3}
	sim := CosineSimilarity(a, b)
	if diff := math.Abs(float64(sim) + 1.0); diff > 1e-6 {
		t.Fatalf("opposite vectors: got %f, want -1.0", sim)
	}
}

func TestCosineSimilarity_ZeroVector(t *testing.T) {
	a := []float32{1, 2, 3}
	b := []float32{0, 0, 0}
	sim := CosineSimilarity(a, b)
	if sim != 0 {
		t.Fatalf("zero vector: got %f, want 0.0", sim)
	}
}

func TestCosineSimilarity_DifferentLengths(t *testing.T) {
	a := []float32{1, 2}
	b := []float32{1, 2, 3}
	sim := CosineSimilarity(a, b)
	if sim != 0 {
		t.Fatalf("different length vectors: got %f, want 0.0", sim)
	}
}

func TestCosineSimilarity_Empty(t *testing.T) {
	sim := CosineSimilarity(nil, nil)
	if sim != 0 {
		t.Fatalf("nil vectors: got %f, want 0.0", sim)
	}
}

func TestFindDuplicates(t *testing.T) {
	query := []float32{1, 0, 0}
	candidates := []EmbeddingRow{
		{DrawerID: "close", Embedding: []float32{0.99, 0.1, 0}},
		{DrawerID: "far", Embedding: []float32{0, 0, 1}},
		{DrawerID: "medium", Embedding: []float32{0.7, 0.7, 0}},
	}
	dupes := FindDuplicates(query, candidates, 0.9)

	if len(dupes) != 1 {
		t.Fatalf("got %d duplicates, want 1", len(dupes))
	}
	if dupes[0].ExistingID != "close" {
		t.Fatalf("got duplicate %q, want %q", dupes[0].ExistingID, "close")
	}
	if dupes[0].Similarity < 0.9 {
		t.Fatalf("got similarity %f, want >= 0.9", dupes[0].Similarity)
	}
}

func TestFindDuplicates_NoneAboveThreshold(t *testing.T) {
	query := []float32{1, 0, 0}
	candidates := []EmbeddingRow{
		{DrawerID: "far", Embedding: []float32{0, 0, 1}},
	}
	dupes := FindDuplicates(query, candidates, 0.9)
	if len(dupes) != 0 {
		t.Fatalf("got %d duplicates, want 0", len(dupes))
	}
}

func TestFindDuplicates_EmptyCandidates(t *testing.T) {
	query := []float32{1, 0, 0}
	dupes := FindDuplicates(query, nil, 0.9)
	if len(dupes) != 0 {
		t.Fatalf("got %d duplicates, want 0", len(dupes))
	}
}

func TestRankBySimilarity(t *testing.T) {
	query := []float32{1, 0, 0}
	candidates := []EmbeddingRow{
		{DrawerID: "far", Embedding: []float32{0, 0, 1}},
		{DrawerID: "close", Embedding: []float32{0.99, 0.1, 0}},
		{DrawerID: "medium", Embedding: []float32{0.7, 0.7, 0}},
	}
	ranked := RankBySimilarity(query, candidates, 2)

	if len(ranked) != 2 {
		t.Fatalf("got %d results, want 2", len(ranked))
	}
	if ranked[0].DrawerID != "close" {
		t.Fatalf("rank 0: got %q, want %q", ranked[0].DrawerID, "close")
	}
	if ranked[1].DrawerID != "medium" {
		t.Fatalf("rank 1: got %q, want %q", ranked[1].DrawerID, "medium")
	}
	if ranked[0].Similarity <= ranked[1].Similarity {
		t.Fatal("results not sorted by similarity descending")
	}
}

func TestRankBySimilarity_ZeroLimit(t *testing.T) {
	query := []float32{1, 0, 0}
	candidates := []EmbeddingRow{
		{DrawerID: "a", Embedding: []float32{1, 0, 0}},
		{DrawerID: "b", Embedding: []float32{0, 1, 0}},
	}
	ranked := RankBySimilarity(query, candidates, 0)
	if len(ranked) != 2 {
		t.Fatalf("limit 0 should return all: got %d, want 2", len(ranked))
	}
}

func TestRankBySimilarity_LimitExceedsCandidates(t *testing.T) {
	query := []float32{1, 0, 0}
	candidates := []EmbeddingRow{
		{DrawerID: "a", Embedding: []float32{1, 0, 0}},
	}
	ranked := RankBySimilarity(query, candidates, 10)
	if len(ranked) != 1 {
		t.Fatalf("got %d results, want 1", len(ranked))
	}
}

func TestMarshalUnmarshalEmbedding_Roundtrip(t *testing.T) {
	original := []float32{0.1, -0.5, 3.14, 0, -1e10, 1e-10}
	encoded := MarshalEmbedding(original)
	decoded := UnmarshalEmbedding(encoded)

	if len(decoded) != len(original) {
		t.Fatalf("length mismatch: got %d, want %d", len(decoded), len(original))
	}
	for i := range original {
		if decoded[i] != original[i] {
			t.Fatalf("index %d: got %f, want %f", i, decoded[i], original[i])
		}
	}
}

func TestMarshalEmbedding_Nil(t *testing.T) {
	if b := MarshalEmbedding(nil); b != nil {
		t.Fatalf("nil input: got %v, want nil", b)
	}
	if v := UnmarshalEmbedding(nil); v != nil {
		t.Fatalf("nil input: got %v, want nil", v)
	}
}
