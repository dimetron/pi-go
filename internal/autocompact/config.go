package autocompact

import (
	"github.com/dimetron/pi-go/internal/config"
	pisession "github.com/dimetron/pi-go/internal/session"
)

// ConfigFrom maps the user's config.json settings onto an AutoCompactConfig,
// leaving any field the user did not set at its default. Every numeric field
// is guarded by its own zero-check, so an unset field keeps its default rather
// than compacting at 0%.
func ConfigFrom(cfg config.Config) pisession.AutoCompactConfig {
	acCfg := pisession.DefaultAutoCompactConfig()
	if cfg.AutoCompact == nil {
		return acCfg
	}
	if cfg.AutoCompact.Enabled != nil {
		acCfg.Enabled = *cfg.AutoCompact.Enabled
	}
	if cfg.AutoCompact.ShedPercent > 0 {
		acCfg.ShedPercent = cfg.AutoCompact.ShedPercent
	}
	if cfg.AutoCompact.SummarizePercent > 0 {
		acCfg.SummarizePercent = cfg.AutoCompact.SummarizePercent
	}
	if cfg.AutoCompact.KeepUserMessageTokens > 0 {
		acCfg.KeepUserMessageTokens = cfg.AutoCompact.KeepUserMessageTokens
	}
	if cfg.AutoCompact.KeepRecentEvents > 0 {
		acCfg.KeepRecentEvents = cfg.AutoCompact.KeepRecentEvents
	}
	return acCfg
}
