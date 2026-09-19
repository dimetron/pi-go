package tools

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"

	"google.golang.org/adk/v2/agent"
	"google.golang.org/adk/v2/tool"
)

// Web search is served by Ollama in two places, and they are not
// interchangeable — the paths differ, not just the host:
//
//   - A local daemon exposes /api/experimental/web_search and needs no API key
//     at all. The daemon authenticates with the identity from `ollama signin`,
//     so a signed-in machine can search without any key in the environment.
//   - ollama.com exposes /api/web_search, requires OLLAMA_API_KEY, and draws
//     down a per-account monthly search quota.
//
// The published docs describe only the second. The Ollama Go SDK's
// WebSearchExperimental method calls the first, and that path 404s on
// api.ollama.com, so the SDK method cannot be pointed at the cloud endpoint.
// That is why this tool speaks HTTP directly instead of reusing the SDK client
// the model provider already builds.
const (
	ollamaLocalSearchPath       = "/api/experimental/web_search"
	ollamaDefaultHost           = "http://localhost:11434"
	ollamaCloudSearchURLDefault = "https://ollama.com/api/web_search"

	// webSearchTimeout bounds one search. The local daemon usually answers in
	// well under a second (it is a proxy to a remote index), but a cold daemon
	// or a slow upstream can take several; 30s matches the llms.txt fetch.
	webSearchTimeout = 30 * time.Second

	// webSearchMaxResultsCap mirrors the API's own ceiling; the service rejects
	// anything larger, so clamping here turns a rejected call into a useful one.
	webSearchMaxResultsCap     = 10
	webSearchDefaultMaxResults = 5

	// webSearchMaxContentBytes caps the snippet kept per result. A single
	// result can return tens of kilobytes — a GitHub repository page comes back
	// as the entire rendered README — and five of those would spend most of a
	// context window on one call. Truncation is what keeps the tool usable on a
	// modest context; the URL is always kept intact so the model can fetch more.
	webSearchMaxContentBytes = 4000
)

// WebSearchInput defines the parameters for the web_search tool.
type WebSearchInput struct {
	// Query is the search query string.
	Query string `json:"query"`
	// MaxResults caps how many results come back (1-10, default 5).
	MaxResults int `json:"max_results,omitempty"`
}

// WebSearchResultItem is one search hit.
type WebSearchResultItem struct {
	Title   string `json:"title,omitempty"`
	URL     string `json:"url,omitempty"`
	Content string `json:"content,omitempty"`
}

// WebSearchOutput is the result of a web_search call.
type WebSearchOutput struct {
	Results []WebSearchResultItem `json:"results,omitempty"`
	// Source names the endpoint that answered ("local" or "cloud"), so a caller
	// reading a session log can tell which path a result came from.
	Source string `json:"source,omitempty"`
	Error  string `json:"error,omitempty"`
}

// webSearchResponse is the wire shape both endpoints answer with. The request
// shape is WebSearchInput itself — the JSON tags there already match the API,
// so a second identical struct would only be one more thing to keep in sync.
type webSearchResponse struct {
	Results []WebSearchResultItem `json:"results"`
	Error   string                `json:"error"`
}

// webSearchHTTPClient performs the request. Production leaves it nil and uses
// http.DefaultClient; tests talk to httptest servers and swap in their own.
var webSearchHTTPClient *http.Client

// ollamaCloudSearchURL is the cloud search endpoint, a variable so tests can
// point it at an httptest server; see webSearchHTTPClient for the same pattern.
var ollamaCloudSearchURL = ollamaCloudSearchURLDefault

// newWebSearchTool builds the web_search tool.
func newWebSearchTool() (tool.Tool, error) {
	return newTool("web_search", `Search the web for current information.

Use this when you need facts that are not in the repository and that you cannot
read from a file: a library's current release, a recent API change, an error
message you have not seen before, or anything else that changed after your
training data. Results are titles, URLs and content snippets, each with the URL
kept intact so you can fetch a page in full with a shell command if a snippet is
not enough.

Requires a running Ollama daemon, or OLLAMA_API_KEY for the ollama.com search
API. Prefer grep and read when the answer is already in the repository — a
search costs a network round trip and returns text you cannot verify against
the code.`, func(ctx agent.Context, input WebSearchInput) (WebSearchOutput, error) {
		return runWebSearch(ctx, input)
	})
}

// runWebSearch validates the input and dispatches the search.
func runWebSearch(ctx context.Context, input WebSearchInput) (WebSearchOutput, error) {
	query := strings.TrimSpace(input.Query)
	if query == "" {
		return WebSearchOutput{Error: "query is required"}, nil
	}

	maxResults := input.MaxResults
	if maxResults <= 0 {
		maxResults = webSearchDefaultMaxResults
	}
	if maxResults > webSearchMaxResultsCap {
		maxResults = webSearchMaxResultsCap
	}

	candidates := webSearchEndpoints()
	hasCloudKey := strings.TrimSpace(os.Getenv("OLLAMA_API_KEY")) != ""

	// Try each endpoint in order. A transport failure moves to the next one; an
	// HTTP response — including an error status — ends the loop, because the
	// endpoint answered and its answer is the real result.
	var lastErr error
	for _, c := range candidates {
		resp, err := doWebSearch(ctx, c, query, maxResults)
		if err != nil {
			lastErr = err
			continue
		}
		if resp.Error != "" {
			return WebSearchOutput{Error: resp.Error, Source: c.name}, nil
		}
		out := WebSearchOutput{Source: c.name}
		for _, r := range resp.Results {
			out.Results = append(out.Results, WebSearchResultItem{
				Title:   r.Title,
				URL:     r.URL,
				Content: utf8SafeCut(r.Content, webSearchMaxContentBytes),
			})
		}
		if len(out.Results) == 0 {
			out.Error = "no results returned"
		}
		return out, nil
	}

	// Every endpoint failed at the transport level. When there is no key, the
	// bare dial error ("connection refused") reads as a transient network
	// problem, so name the two things that actually fix it.
	if !hasCloudKey {
		return WebSearchOutput{Error: fmt.Sprintf(
			"web search is unavailable: no Ollama daemon at %s and OLLAMA_API_KEY is not set (last error: %v)",
			webSearchLocalHost(), lastErr)}, nil
	}
	return WebSearchOutput{Error: fmt.Sprintf("web search failed: %v", lastErr)}, nil
}

// webSearchEndpoint is one place a search can be sent.
type webSearchEndpoint struct {
	name   string // "local" or "cloud", reported back as the source
	url    string
	apiKey string // empty for the local daemon
}

// webSearchLocalHost is the base URL of the local Ollama daemon, honoring
// OLLAMA_HOST the way the rest of pi-go does.
func webSearchLocalHost() string {
	host := strings.TrimSpace(os.Getenv("OLLAMA_HOST"))
	if host == "" {
		return ollamaDefaultHost
	}
	// OLLAMA_HOST is documented both as a bare host:port and as a full URL.
	// Without a scheme, url.Parse would read the host as a path.
	if !strings.Contains(host, "://") {
		host = "http://" + host
	}
	return strings.TrimRight(host, "/")
}

// webSearchEndpoints returns the endpoints to try, in order: the local daemon
// first, then ollama.com when a key is present.
//
// Local is first because it needs no credential and spends no quota — the
// daemon routes the search itself on the machine's own `ollama signin`
// identity. The cloud endpoint is a fallback for the environments that have no
// daemon: CI runners, dev containers, and VS Code remotes, where a local
// Ollama is not running but OLLAMA_API_KEY is set.
func webSearchEndpoints() []webSearchEndpoint {
	out := []webSearchEndpoint{{
		name: "local",
		url:  webSearchLocalHost() + ollamaLocalSearchPath,
	}}
	if key := strings.TrimSpace(os.Getenv("OLLAMA_API_KEY")); key != "" {
		out = append(out, webSearchEndpoint{
			name:   "cloud",
			url:    ollamaCloudSearchURL,
			apiKey: key,
		})
	}
	return out
}

// doWebSearch performs one search against one endpoint.
func doWebSearch(ctx context.Context, ep webSearchEndpoint, query string, maxResults int) (*webSearchResponse, error) {
	body, err := json.Marshal(WebSearchInput{Query: query, MaxResults: maxResults})
	if err != nil {
		return nil, fmt.Errorf("encode request: %w", err)
	}

	ctx, cancel := context.WithTimeout(ctx, webSearchTimeout)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, ep.url, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("build request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	if ep.apiKey != "" {
		req.Header.Set("Authorization", "Bearer "+ep.apiKey)
	}

	client := webSearchHTTPClient
	if client == nil {
		client = http.DefaultClient
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()

	// The API answers with {"error": "..."} on a rejection — a spent quota, a
	// bad key — and that message is far more useful to the model than the
	// status code alone.
	//
	// The body is read through a limit because it is remote input: a 5-arg call
	// to io.ReadAll on an attacker-influenced stream is an unbounded allocation.
	raw, err := io.ReadAll(io.LimitReader(resp.Body, webSearchMaxBodyBytes))
	if err != nil {
		return nil, fmt.Errorf("read response: %w", err)
	}

	var out webSearchResponse
	if err := json.Unmarshal(raw, &out); err != nil {
		if resp.StatusCode/100 != 2 {
			return nil, fmt.Errorf("search API returned %s", resp.Status)
		}
		return nil, fmt.Errorf("decode response: %w", err)
	}
	if out.Error == "" && resp.StatusCode/100 != 2 {
		out.Error = fmt.Sprintf("search API returned %s", resp.Status)
	}
	return &out, nil
}

// webSearchMaxBodyBytes bounds how much of a search response is read into
// memory. Five untruncated results can exceed a megabyte; truncation per result
// happens later, but the read itself still has to be bounded.
const webSearchMaxBodyBytes = 4 << 20 // 4MiB
