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
