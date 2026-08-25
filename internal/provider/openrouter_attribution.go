package provider

import "net/http"

// App-attribution values sent to OpenRouter so usage is ranked under Pi-Go.
// HTTP-Referer is the required primary identifier — without it no app page is
// created and usage does not appear in rankings.
// See https://openrouter.ai/docs/app-attribution.
const (
	openrouterHTTPReferer   = "https://github.com/dimetron/pi-go"
	openrouterAppTitle      = "Pi-Go"
	openrouterAppCategories = "cli-agent"
)

// openrouterAppAttribution sets the OpenRouter app-attribution headers on h,
// for the raw-net/http request paths (model listing, context-window lookup).
func openrouterAppAttribution(h http.Header) {
	h.Set("HTTP-Referer", openrouterHTTPReferer)
	h.Set("X-OpenRouter-Title", openrouterAppTitle)
	h.Set("X-OpenRouter-Categories", openrouterAppCategories)
}
