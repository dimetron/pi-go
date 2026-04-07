//go:build embedding

package palace

import (
	"os"
	"path/filepath"
	"testing"
)

func TestEmbedder_Integration(t *testing.T) {
	modelDir := os.Getenv("PALACE_MODEL_DIR")
	if modelDir == "" {
		homeDir, err := os.UserHomeDir()
		if err != nil {
			t.Fatal(err)
		}
		modelDir = filepath.Join(homeDir, ".pi-go", "models", "sentence-transformers", "all-MiniLM-L6-v2")
	}

	if _, err := os.Stat(modelDir); os.IsNotExist(err) {
		t.Skipf("model not found at %s; download with DownloadModel() or set PALACE_MODEL_DIR", modelDir)
	}

	emb, err := NewEmbedder(modelDir)
	if err != nil {
		t.Fatalf("NewEmbedder: %v", err)
	}
	defer emb.Close()

	t.Run("similar sentences", func(t *testing.T) {
		vecs, err := emb.Embed([]string{
			"the cat sat on the mat",
			"a cat was sitting on a rug",
		})
		if err != nil {
			t.Fatalf("Embed: %v", err)
		}
		if len(vecs) != 2 {
			t.Fatalf("got %d embeddings, want 2", len(vecs))
		}
		sim := CosineSimilarity(vecs[0], vecs[1])
		if sim < 0.8 {
			t.Fatalf("similar sentences: similarity %f, want >= 0.8", sim)
		}
		t.Logf("similar sentences cosine: %f", sim)
	})

	t.Run("dissimilar sentences", func(t *testing.T) {
		vecs, err := emb.Embed([]string{
			"the cat sat on the mat",
			"quantum mechanics describes wave-particle duality",
		})
		if err != nil {
			t.Fatalf("Embed: %v", err)
		}
		sim := CosineSimilarity(vecs[0], vecs[1])
		if sim > 0.3 {
			t.Fatalf("dissimilar sentences: similarity %f, want < 0.3", sim)
		}
		t.Logf("dissimilar sentences cosine: %f", sim)
	})

	t.Run("embedding dimensions", func(t *testing.T) {
		vecs, err := emb.Embed([]string{"hello world"})
		if err != nil {
			t.Fatalf("Embed: %v", err)
		}
		// all-MiniLM-L6-v2 produces 384-dimensional vectors
		if len(vecs[0]) != 384 {
			t.Fatalf("got %d dimensions, want 384", len(vecs[0]))
		}
	})
}
