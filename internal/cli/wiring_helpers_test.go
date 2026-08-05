package cli

import (
	"context"
	"testing"

	"github.com/dimetron/pi-go/internal/config"
	"github.com/dimetron/pi-go/internal/tools"
)

func TestCompactorConfigFrom(t *testing.T) {
	t.Parallel()

	defaults := tools.DefaultCompactorConfig()
	disabled := false

	tests := []struct {
		name    string
		cfg     config.Config
		want    tools.CompactorConfig
		wantMsg string
	}{
		{
			name: "nil compactor keeps defaults",
			cfg:  config.Config{},
			want: defaults,
		},
		{
			name: "empty compactor keeps defaults",
			cfg:  config.Config{Compactor: &config.CompactorConfig{}},
			want: defaults,
		},
		{
			name: "explicit disable is honored",
			cfg:  config.Config{Compactor: &config.CompactorConfig{Enabled: &disabled}},
			want: func() tools.CompactorConfig {
				c := defaults
				c.Enabled = false
				return c
			}(),
		},
		{
			name: "overrides are applied",
			cfg: config.Config{Compactor: &config.CompactorConfig{
				SourceCodeFiltering: "aggressive",
				MaxChars:            1234,
				MaxLines:            56,
			}},
			want: func() tools.CompactorConfig {
				c := defaults
				c.SourceCodeFiltering = "aggressive"
				c.MaxChars = 1234
				c.MaxLines = 56
				return c
			}(),
		},
		{
			name: "zero values do not clobber defaults",
			cfg: config.Config{Compactor: &config.CompactorConfig{
				SourceCodeFiltering: "",
				MaxChars:            0,
				MaxLines:            0,
			}},
			want: defaults,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := compactorConfigFrom(tt.cfg)
			if got != tt.want {
				t.Errorf("compactorConfigFrom() = %+v, want %+v", got, tt.want)
			}
		})
	}
}

func TestMemoryAndPalaceEnabled(t *testing.T) {
	// Not parallel: mutates the package-level flagMemoryOff.
	yes, no := true, false

	tests := []struct {
		name       string
		cfg        config.Config
		memoryOff  bool
		wantMemory bool
		wantPalace bool
	}{
		{
			name:       "default is on",
			cfg:        config.Config{},
			wantMemory: true,
			wantPalace: true,
		},
		{
			name:       "explicit enable is on",
			cfg:        config.Config{Memory: &config.MemoryConfig{Enabled: &yes}, Palace: &config.PalaceConfig{Enabled: &yes}},
			wantMemory: true,
			wantPalace: true,
		},
		{
			name:       "explicit disable is off",
			cfg:        config.Config{Memory: &config.MemoryConfig{Enabled: &no}, Palace: &config.PalaceConfig{Enabled: &no}},
			wantMemory: false,
			wantPalace: false,
		},
		{
			name:       "memory-off flag overrides config",
			cfg:        config.Config{Memory: &config.MemoryConfig{Enabled: &yes}, Palace: &config.PalaceConfig{Enabled: &yes}},
			memoryOff:  true,
			wantMemory: false,
			wantPalace: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			orig := flagMemoryOff
			flagMemoryOff = tt.memoryOff
			t.Cleanup(func() { flagMemoryOff = orig })

			if got := memoryEnabled(tt.cfg); got != tt.wantMemory {
				t.Errorf("memoryEnabled() = %v, want %v", got, tt.wantMemory)
			}
			if got := palaceIsEnabled(tt.cfg); got != tt.wantPalace {
				t.Errorf("palaceIsEnabled() = %v, want %v", got, tt.wantPalace)
			}
		})
	}
}

func TestSetupMemoryDisabledIsSafe(t *testing.T) {
	orig := flagMemoryOff
	flagMemoryOff = true
	t.Cleanup(func() { flagMemoryOff = orig })

	store, worker, closeFn := setupMemory(t.Context(), config.Config{}, nil)
	if store != nil || worker != nil {
		t.Errorf("expected no store/worker when memory is off, got %v/%v", store, worker)
	}
	if closeFn == nil {
		t.Fatal("closer must never be nil — callers defer it unconditionally")
	}
	closeFn() // must not panic
}

func TestSetupPalaceDisabledIsSafe(t *testing.T) {
	orig := flagMemoryOff
	flagMemoryOff = true
	t.Cleanup(func() { flagMemoryOff = orig })

	palaceTools, wakeUp, closeFn := setupPalace(config.Config{}, nil)
	if len(palaceTools) != 0 {
		t.Errorf("expected no tools when palace is off, got %d", len(palaceTools))
	}
	if wakeUp != "" {
		t.Errorf("expected no wake-up context, got %q", wakeUp)
	}
	if closeFn == nil {
		t.Fatal("closer must never be nil — callers defer it unconditionally")
	}
	closeFn() // must not panic
}

func TestSetupPalaceMissingDBIsNotAnError(t *testing.T) {
	orig := flagMemoryOff
	flagMemoryOff = false
	t.Cleanup(func() { flagMemoryOff = orig })

	cfg := config.Config{Palace: &config.PalaceConfig{DBPath: t.TempDir() + "/does-not-exist.db"}}
	palaceTools, wakeUp, closeFn := setupPalace(cfg, nil)

	if len(palaceTools) != 0 || wakeUp != "" {
		t.Errorf("a missing palace DB should contribute nothing, got %d tools / %q", len(palaceTools), wakeUp)
	}
	if closeFn == nil {
		t.Fatal("closer must never be nil")
	}
	closeFn()
}

func TestBuildToolsets(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		cfg     config.Config
		wantLen int
	}{
		{
			name:    "no mcp and no a2a yields nothing",
			cfg:     config.Config{},
			wantLen: 0,
		},
		{
			name:    "empty mcp server list yields nothing",
			cfg:     config.Config{MCP: &config.MCPConfig{}},
			wantLen: 0,
		},
		{
			name:    "a2a agents add one toolset",
			cfg:     config.Config{A2A: &config.A2AConfig{Agents: []config.A2AAgentConfig{{Name: "peer", URL: "http://localhost:9999"}}}},
			wantLen: 1,
		},
		{
			name:    "empty a2a agent list yields nothing",
			cfg:     config.Config{A2A: &config.A2AConfig{}},
			wantLen: 0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := buildToolsets(tt.cfg); len(got) != tt.wantLen {
				t.Errorf("buildToolsets() returned %d toolsets, want %d", len(got), tt.wantLen)
			}
		})
	}
}

func TestBuildMCPServerConfigs(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		cfg     config.Config
		wantLen int
	}{
		{"nil mcp", config.Config{}, 0},
		{"empty servers", config.Config{MCP: &config.MCPConfig{}}, 0},
		{
			"maps every server",
			config.Config{MCP: &config.MCPConfig{Servers: []config.MCPServer{
				{Name: "a", Command: "cmd-a"},
				{Name: "b", URL: "http://b"},
			}}},
			2,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := buildMCPServerConfigs(tt.cfg)
			if len(got) != tt.wantLen {
				t.Fatalf("buildMCPServerConfigs() returned %d, want %d", len(got), tt.wantLen)
			}
			if tt.cfg.MCP == nil {
				return
			}
			for i, want := range tt.cfg.MCP.Servers {
				if got[i].Name != want.Name || got[i].Command != want.Command || got[i].URL != want.URL {
					t.Errorf("server %d = %+v, want name/command/url from %+v", i, got[i], want)
				}
			}
		})
	}
}

func TestMemoryInstructionContextWithoutStore(t *testing.T) {
	t.Parallel()
	if got := memoryInstructionContext(context.Background(), nil, config.Config{}, "/tmp"); got != "" {
		t.Errorf("expected empty context with no store, got %q", got)
	}
}

func TestDispatchModeWithoutPromptIsNoOp(t *testing.T) {
	t.Parallel()
	for _, mode := range []string{"print", "json"} {
		t.Run(mode, func(t *testing.T) {
			t.Parallel()
			// A nil agent is safe here precisely because an empty prompt must
			// return before the agent is ever touched.
			if err := dispatchMode(context.Background(), mode, "", nil, "sess", nil, "test-model", config.Config{}, nil); err != nil {
				t.Errorf("dispatchMode(%q, empty prompt) = %v, want nil", mode, err)
			}
		})
	}
}
