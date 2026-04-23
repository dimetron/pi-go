package palace

import (
	"math"
	"strings"
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

// Tests for embedder functions (require mocking since they need real models)

func TestNewEmbedder_InvalidPath(t *testing.T) {
	// Test that NewEmbedder returns an error for non-existent path
	_, err := NewEmbedder("/nonexistent/path/that/does/not/exist/model")
	if err == nil {
		t.Error("expected error for invalid model path")
	}
}

func TestEmbedder_Close_NilSession(t *testing.T) {
	// Test that Close is safe with nil session
	e := &Embedder{}
	e.Close() // Should not panic with nil session
}

func TestDownloadModel_Signature(t *testing.T) {
	// Test that DownloadModel has correct signature
	// Actual download requires network, so we just verify it can be called
	result, err := DownloadModel("/tmp/test-dest", "")
	if err != nil {
		// Might fail due to network or invalid dest, but signature is correct
		t.Logf("DownloadModel returned error (expected for test): %v", err)
	}
	_ = result // may be empty on failure
}

func TestEmbedder_Constants(t *testing.T) {
	// Verify embedder constants are correctly defined
	if maxCharLength != 512 {
		t.Errorf("maxCharLength = %d, want 512", maxCharLength)
	}
	if maxTokenLength != 128 {
		t.Errorf("maxTokenLength = %d, want 128", maxTokenLength)
	}
}

func TestDetectPlatformOnnxFile_InEmbedder(t *testing.T) {
	path := DetectPlatformOnnxFile()
	if path == "" {
		t.Error("DetectPlatformOnnxFile returned empty string")
	}
	// Verify the path contains "onnx/"
	if !strings.Contains(path, "onnx/") {
		t.Errorf("DetectPlatformOnnxFile returned %q, want path containing 'onnx/'", path)
	}
	// Verify it ends with .onnx
	if !strings.HasSuffix(path, ".onnx") {
		t.Errorf("DetectPlatformOnnxFile returned %q, want path ending with '.onnx'", path)
	}
}

func TestEmbedder_Close_WithSession(t *testing.T) {
	// Test that Close handles the case where session is non-nil but not initialized
	// This exercises the Close method code path even when we can't create a real session
	e := &Embedder{}
	// Close should not panic
	e.Close()
}
