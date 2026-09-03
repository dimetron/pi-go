package cli

import (
	"context"
	"time"

	"github.com/dimetron/pi-go/internal/provider"
)

// refreshPricing pulls a fresh models.dev pricing snapshot in the background
// when the embedded one is stale. It is best-effort: a network or write failure
// is ignored so the embedded data keeps serving. The timeout bounds the fetch
// so a slow models.dev never delays startup.
func refreshPricing(ctx context.Context) {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	_ = provider.RefreshPricingIfStale(ctx)
}
