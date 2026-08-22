package tools

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"google.golang.org/adk/v2/agent"
	"google.golang.org/adk/v2/tool"

	"github.com/dimetron/pi-go/internal/config"
)

// llmsFetchTimeout bounds the total time spent fetching a single llms.txt
// page. Documentation pages are small; anything slower is almost certainly a
// stalled connection or a denial-of-service attempt.
const llmsFetchTimeout = 30 * time.Second

// llmsMaxBytes caps the size of a fetched page. llms.txt indexes and their
// linked markdown pages are typically well under this; the cap prevents an
// oversized or malicious response from consuming unbounded context tokens.
const llmsMaxBytes = 2 * 1024 * 1024 // 2 MB

// llmsUserAgent identifies pi-go to remote documentation hosts. Some CDNs
// refuse requests without a UA.
const llmsUserAgent = "pi-go/1.0 (+https://github.com/dimetron/pi-go)"

// llmsClientOverride is a test-only override for the HTTP client used to
// fetch documentation. Production code leaves it nil and uses the toolset's
// default client. Tests that talk to httptest.NewTLSServer install
// srv.Client() so the test's self-signed cert is trusted.
var llmsClientOverride *http.Client

// LLMSInput defines the parameters for the fetch_docs tool.
type LLMSInput struct {
	// URL is the documentation URL to fetch. It must be an https:// URL on a
	// host that hosts one of the configured llms.txt sources.
	URL string `json:"url,omitempty"`
}

// LLMSOutput is the result of a fetch_docs call.
type LLMSOutput struct {
	Content string `json:"content,omitempty"`
	Error   string `json:"error,omitempty"`
}

// LLMSToolset implements tool.Toolset for llms.txt documentation sources.
type LLMSToolset struct {
	sources []config.LLMSSource
	client  *http.Client
}

// NewLLMSToolset creates a new llms.txt toolset from configuration.
func NewLLMSToolset(cfg *config.LLMSConfig) *LLMSToolset {
	ts := &LLMSToolset{client: &http.Client{Timeout: llmsFetchTimeout}}
	if cfg != nil {
		ts.sources = cfg.Sources
	}
	return ts
}

// Name returns the name of the toolset.
func (t *LLMSToolset) Name() string {
	return "llms"
}

// Tools returns the llms.txt tools available.
func (t *LLMSToolset) Tools(ctx agent.ReadonlyContext) ([]tool.Tool, error) {
	return LLMSTools(t), nil
}

// LLMSTools returns the llms.txt tools built from the toolset.
func LLMSTools(ts *LLMSToolset) []tool.Tool {
	t, err := NewLLMSToolFromSet(ts)
	if err != nil {
		return nil
	}
	return []tool.Tool{t}
}

// NewLLMSToolFromSet creates the fetch_docs ADK tool from a toolset.
func NewLLMSToolFromSet(ts *LLMSToolset) (tool.Tool, error) {
	desc := buildLLMSDescription(ts.sources)

	return newTool("fetch_docs", desc,
		func(ctx agent.Context, input LLMSInput) (LLMSOutput, error) {
			return ts.fetchDocs(ctx, input.URL)
		},
	)
}

// fetchDocs fetches a documentation URL, restricted to hosts that host one of
// the configured llms.txt sources. The response is returned as-is (llms.txt
// and its linked pages are markdown, so no HTML conversion is needed).
func (t *LLMSToolset) fetchDocs(ctx context.Context, rawURL string) (LLMSOutput, error) {
	rawURL = strings.TrimSpace(rawURL)
	if rawURL == "" {
		return LLMSOutput{Error: "url is required"}, nil
	}

	u, err := url.Parse(rawURL)
	if err != nil {
		return LLMSOutput{Error: fmt.Sprintf("parse url: %v", err)}, nil
	}
	if u.Scheme != "https" {
		return LLMSOutput{Error: "only https:// URLs are allowed"}, nil
	}
	if u.Host == "" {
		return LLMSOutput{Error: "url has no host"}, nil
	}

	// SSRF guard: reject loopback, private, link-local, multicast, and
	// unspecified destinations before dialing.
	if err := rejectPrivateHost(u.Hostname()); err != nil {
		return LLMSOutput{Error: fmt.Sprintf("url rejected: %v", err)}, nil
	}

	// Restrict to hosts that host a configured llms.txt source.
	if !t.hostAllowed(u.Hostname()) {
		return LLMSOutput{Error: fmt.Sprintf("host %q is not an allowed documentation source", u.Hostname())}, nil
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return LLMSOutput{Error: fmt.Sprintf("build request: %v", err)}, nil
	}
	req.Header.Set("User-Agent", llmsUserAgent)

	client := t.client
	if llmsClientOverride != nil {
		client = llmsClientOverride
	}
	resp, err := client.Do(req)
	if err != nil {
		return LLMSOutput{Error: fmt.Sprintf("fetch failed: %v", err)}, nil
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return LLMSOutput{Error: fmt.Sprintf("unexpected status %d", resp.StatusCode)}, nil
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, llmsMaxBytes))
	if err != nil {
		return LLMSOutput{Error: fmt.Sprintf("read body: %v", err)}, nil
	}
	return LLMSOutput{Content: string(body)}, nil
}

// hostAllowed reports whether hostname hosts one of the configured llms.txt
// sources. The comparison is on the hostname (port stripped) so a source
// configured as https://adk.dev/llms.txt allows fetching any page on adk.dev.
func (t *LLMSToolset) hostAllowed(hostname string) bool {
	for _, s := range t.sources {
		u, err := url.Parse(s.URL)
		if err != nil {
			continue
		}
		if strings.EqualFold(u.Hostname(), hostname) {
			return true
		}
	}
	return false
}

// buildLLMSDescription generates a dynamic tool description listing the
// configured documentation sources.
func buildLLMSDescription(sources []config.LLMSSource) string {
	var sb strings.Builder
	sb.WriteString("Fetch documentation from a configured llms.txt source. ")
	sb.WriteString("Parameters: url (the https:// URL to fetch). ")
	sb.WriteString("Only hosts that host a configured llms.txt source are allowed. ")
	if len(sources) == 0 {
		sb.WriteString("No llms.txt sources configured.")
		return sb.String()
	}
	sb.WriteString("Available sources: ")
	names := make([]string, 0, len(sources))
	for _, s := range sources {
		names = append(names, s.Name)
	}
	sb.WriteString(strings.Join(names, ", "))
	sb.WriteString(".")
	return sb.String()
}
