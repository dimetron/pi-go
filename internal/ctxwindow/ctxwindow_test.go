package ctxwindow

import (
	"context"
	"testing"

	"github.com/dimetron/pi-go/internal/config"
	"github.com/dimetron/pi-go/internal/provider"
)

func TestResolve(t *testing.T) {
	t.Parallel()

	info := provider.Info{Provider: "anthropic", Model: "claude-sonnet-4-6"}
	catalog := provider.ContextWindowSizeFor(info.Provider, info.Model)

	tests := []struct {
		name string
		cfg  config.Config
		want int64
	}{
		{name: "catalog size when config says nothing", cfg: config.Config{}, want: catalog},
		{name: "explicit config value wins", cfg: config.Config{ContextWindow: 4242}, want: 4242},
		{name: "zero config value does not override", cfg: config.Config{ContextWindow: 0}, want: catalog},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := Resolve(context.Background(), tt.cfg, info, "")
			if got != tt.want {
				t.Errorf("Resolve = %d, want %d", got, tt.want)
			}
		})
	}
}
