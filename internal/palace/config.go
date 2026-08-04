package palace

const (
	defaultDeduplicationThreshold = float32(0.9)
	defaultDBPath                 = "palace.db"
)

// PalaceConfig holds configuration for the Palace system.
type PalaceConfig struct {
	DBPath                 string
	ModelPath              string
	IdentityFile           string
	DeduplicationThreshold float32
	L1TopK                 int
	L1MaxChars             int
	L2MaxDrawers           int
	L2MaxCharsPerDrawer    int

	// UseOllama selects the Ollama daemon for embedding instead of the
	// in-process model. Default on: it is an order of magnitude faster and
	// retrieves better. See DefaultOllamaEmbedModel for the measurements.
	UseOllama bool
	// OllamaURL is the daemon address; empty means DefaultOllamaURL.
	OllamaURL string
	// OllamaModel is the embedding model; empty means DefaultOllamaEmbedModel.
	OllamaModel string
}

// DefaultConfig returns a PalaceConfig with sensible defaults.
func DefaultConfig() PalaceConfig {
	return PalaceConfig{
		DBPath:                 defaultDBPath,
		DeduplicationThreshold: defaultDeduplicationThreshold,
		L1TopK:                 15,
		L1MaxChars:             3200,
		L2MaxDrawers:           10,
		L2MaxCharsPerDrawer:    300,
		UseOllama:              true,
		OllamaURL:              DefaultOllamaURL,
		OllamaModel:            DefaultOllamaEmbedModel,
	}
}

// Option is a functional option for configuring a Palace.
type Option func(*PalaceConfig)

// WithDBPath sets the database file path.
func WithDBPath(path string) Option {
	return func(c *PalaceConfig) { c.DBPath = path }
}

// WithModelPath sets the path to the embedding model directory.
func WithModelPath(path string) Option {
	return func(c *PalaceConfig) { c.ModelPath = path }
}

// WithIdentityFile sets the path to the L0 identity file.
func WithIdentityFile(path string) Option {
	return func(c *PalaceConfig) { c.IdentityFile = path }
}

// WithDeduplicationThreshold sets the cosine similarity threshold for duplicate detection.
func WithDeduplicationThreshold(t float32) Option {
	return func(c *PalaceConfig) { c.DeduplicationThreshold = t }
}

// WithOllamaEmbedder points the palace at an Ollama daemon for embedding.
// Empty arguments fall back to DefaultOllamaURL and DefaultOllamaEmbedModel.
func WithOllamaEmbedder(baseURL, model string) Option {
	return func(c *PalaceConfig) {
		c.UseOllama = true
		if baseURL != "" {
			c.OllamaURL = baseURL
		}
		if model != "" {
			c.OllamaModel = model
		}
	}
}

// WithLocalEmbedder forces the in-process model, bypassing Ollama. Used when no
// daemon is available and the caller would rather be slow than fail.
func WithLocalEmbedder() Option {
	return func(c *PalaceConfig) { c.UseOllama = false }
}
