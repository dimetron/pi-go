package tools

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// newSearchTestServer stands in for one Ollama search endpoint and records what
// it was sent, so a test can assert on the wire rather than only on the output.
type searchRequest struct {
	Path          string
	Authorization string
	Body          string
}

func newSearchTestServer(t *testing.T, status int, body string, rec *searchRequest) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		if rec != nil {
			rec.Path = r.URL.Path
			rec.Authorization = r.Header.Get("Authorization")
			rec.Body = string(raw)
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)
	return srv
}

// withSearchEnv points both endpoints at test servers and restores the
// package-level overrides afterwards. OLLAMA_HOST decides the local URL,
// ollamaCloudSearchURL the cloud one.
func withSearchEnv(t *testing.T, localURL, cloudURL, apiKey string) {
	t.Helper()
	t.Setenv("OLLAMA_HOST", localURL)
	t.Setenv("OLLAMA_API_KEY", apiKey)
	prevClient := webSearchHTTPClient
	prevCloud := ollamaCloudSearchURL
	webSearchHTTPClient = nil
	if cloudURL != "" {
		ollamaCloudSearchURL = cloudURL
	}
	t.Cleanup(func() {
		webSearchHTTPClient = prevClient
		ollamaCloudSearchURL = prevCloud
	})
}

const twoResults = `{"results":[
  {"title":"First","url":"https://example.com/1","content":"alpha"},
  {"title":"Second","url":"https://example.com/2","content":"beta"}
]}`

func TestWebSearchLocalEndpointIsUsedAndNeedsNoKey(t *testing.T) {
	var rec searchRequest
	srv := newSearchTestServer(t, http.StatusOK, twoResults, &rec)
	withSearchEnv(t, srv.URL, "", "")

	out, err := runWebSearch(context.Background(), WebSearchInput{Query: "hello"})
	if err != nil {
		t.Fatalf("runWebSearch: %v", err)
	}
	if out.Error != "" {
		t.Fatalf("unexpected error: %s", out.Error)
	}
	if out.Source != "local" {
		t.Errorf("source = %q, want local", out.Source)
	}
	if len(out.Results) != 2 {
		t.Fatalf("results = %d, want 2", len(out.Results))
	}
	if out.Results[0].Title != "First" || out.Results[0].URL != "https://example.com/1" {
		t.Errorf("result[0] = %+v", out.Results[0])
	}
	// The local path has no credential to send; sending one would leak the
	// cloud key to a local process.
	if rec.Authorization != "" {
		t.Errorf("local request carried Authorization %q, want none", rec.Authorization)
	}
	if rec.Path != ollamaLocalSearchPath {
		t.Errorf("path = %q, want %q", rec.Path, ollamaLocalSearchPath)
	}
}

func TestWebSearchFallsBackToCloudWhenLocalFails(t *testing.T) {
	// A local endpoint that refuses connections: the URL points at a closed
	// server, which is what "no daemon running" looks like on the wire.
	dead := newSearchTestServer(t, http.StatusOK, twoResults, nil)
	deadURL := dead.URL
	dead.Close()

	var rec searchRequest
	cloud := newSearchTestServer(t, http.StatusOK, twoResults, &rec)
	withSearchEnv(t, deadURL, cloud.URL, "sk-test-key")

	out, err := runWebSearch(context.Background(), WebSearchInput{Query: "hello"})
	if err != nil {
		t.Fatalf("runWebSearch: %v", err)
	}
	if out.Source != "cloud" {
		t.Fatalf("source = %q, want cloud (local was unreachable)", out.Source)
	}
	if len(out.Results) != 2 {
		t.Fatalf("results = %d, want 2", len(out.Results))
	}
	if rec.Authorization != "Bearer sk-test-key" {
		t.Errorf("cloud Authorization = %q, want the bearer key", rec.Authorization)
	}
}

func TestWebSearchLocalWinsOverCloud(t *testing.T) {
	var localRec, cloudRec searchRequest
	local := newSearchTestServer(t, http.StatusOK, twoResults, &localRec)
	cloud := newSearchTestServer(t, http.StatusOK, twoResults, &cloudRec)
	withSearchEnv(t, local.URL, cloud.URL, "sk-test-key")

	out, err := runWebSearch(context.Background(), WebSearchInput{Query: "hello"})
	if err != nil {
		t.Fatalf("runWebSearch: %v", err)
	}
	if out.Source != "local" {
		t.Errorf("source = %q, want local", out.Source)
	}
	// Local is preferred because it spends no quota; reaching the cloud here
	// would burn a metered search for no reason.
	if cloudRec.Path != "" {
		t.Errorf("cloud endpoint was called (%q) despite a working local daemon", cloudRec.Path)
	}
}

// A cloud error is reported as-is: the API answers a spent quota or a bad key
// with a useful message, and retrying the local endpoint would only hide it.
func TestWebSearchCloudErrorIsSurfaced(t *testing.T) {
	dead := newSearchTestServer(t, http.StatusOK, twoResults, nil)
	deadURL := dead.URL
	dead.Close()

	const quotaMsg = `{"error":"you have reached your monthly usage limit"}`
	cloud := newSearchTestServer(t, http.StatusTooManyRequests, quotaMsg, nil)
	withSearchEnv(t, deadURL, cloud.URL, "sk-test-key")

	out, err := runWebSearch(context.Background(), WebSearchInput{Query: "hello"})
	if err != nil {
		t.Fatalf("runWebSearch: %v", err)
	}
	if !strings.Contains(out.Error, "monthly usage limit") {
		t.Errorf("error = %q, want the API's own message", out.Error)
	}
	if out.Source != "cloud" {
		t.Errorf("source = %q, want cloud", out.Source)
	}
}

func TestWebSearchNoEndpointConfigured(t *testing.T) {
	dead := newSearchTestServer(t, http.StatusOK, twoResults, nil)
	deadURL := dead.URL
	dead.Close()
	withSearchEnv(t, deadURL, "", "") // no key

	out, err := runWebSearch(context.Background(), WebSearchInput{Query: "hello"})
	if err != nil {
		t.Fatalf("runWebSearch: %v", err)
	}
	if !strings.Contains(out.Error, "OLLAMA_API_KEY") {
		t.Errorf("error = %q, want it to name the missing key", out.Error)
	}
}

func TestWebSearchEmptyQueryIsRejected(t *testing.T) {
	out, err := runWebSearch(context.Background(), WebSearchInput{Query: "   "})
	if err != nil {
		t.Fatalf("runWebSearch: %v", err)
	}
	if out.Error != "query is required" {
		t.Errorf("error = %q, want query is required", out.Error)
	}
}

func TestWebSearchMaxResultsIsClamped(t *testing.T) {
	tests := []struct {
		name string
		in   int
		want int
	}{
		{"zero defaults to 5", 0, webSearchDefaultMaxResults},
		{"negative defaults to 5", -3, webSearchDefaultMaxResults},
		{"above the cap clamps to 10", 99, webSearchMaxResultsCap},
		{"in range is passed through", 3, 3},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var rec searchRequest
			srv := newSearchTestServer(t, http.StatusOK, twoResults, &rec)
			withSearchEnv(t, srv.URL, "", "")

			if _, err := runWebSearch(context.Background(), WebSearchInput{Query: "q", MaxResults: tt.in}); err != nil {
				t.Fatalf("runWebSearch: %v", err)
			}
			var sent WebSearchInput
			if err := json.Unmarshal([]byte(rec.Body), &sent); err != nil {
				t.Fatalf("decode sent body %q: %v", rec.Body, err)
			}
			if sent.MaxResults != tt.want {
				t.Errorf("max_results = %d, want %d", sent.MaxResults, tt.want)
			}
		})
	}
}

// An oversized snippet is a repository page returned whole, so the cap is what
// keeps one result from spending the context window.
func TestWebSearchTruncatesOversizedContent(t *testing.T) {
	huge := strings.Repeat("x", webSearchMaxContentBytes*3)
	body, err := json.Marshal(webSearchResponse{Results: []WebSearchResultItem{
		{Title: "Big", URL: "https://example.com/big", Content: huge},
	}})
	if err != nil {
		t.Fatal(err)
	}
	srv := newSearchTestServer(t, http.StatusOK, string(body), nil)
	withSearchEnv(t, srv.URL, "", "")

	out, err := runWebSearch(context.Background(), WebSearchInput{Query: "q"})
	if err != nil {
		t.Fatalf("runWebSearch: %v", err)
	}
	if len(out.Results) != 1 {
		t.Fatalf("results = %d, want 1", len(out.Results))
	}
	if got := len(out.Results[0].Content); got != webSearchMaxContentBytes {
		t.Errorf("content length = %d, want %d", got, webSearchMaxContentBytes)
	}
	// The URL survives truncation so the model can still fetch the full page.
	if out.Results[0].URL != "https://example.com/big" {
		t.Errorf("url = %q, want it preserved", out.Results[0].URL)
	}
}

func TestWebSearchNoResultsIsReported(t *testing.T) {
	srv := newSearchTestServer(t, http.StatusOK, `{"results":[]}`, nil)
	withSearchEnv(t, srv.URL, "", "")

	out, err := runWebSearch(context.Background(), WebSearchInput{Query: "q"})
	if err != nil {
		t.Fatalf("runWebSearch: %v", err)
	}
	if out.Error != "no results returned" {
		t.Errorf("error = %q, want no results returned", out.Error)
	}
}

// OLLAMA_HOST is documented as both host:port and a full URL; the bare form
// must not be parsed as a path.
func TestWebSearchLocalHostNormalization(t *testing.T) {
	tests := []struct {
		env  string
		want string
	}{
		{"", ollamaDefaultHost},
		{"localhost:11434", "http://localhost:11434"},
		{"http://192.168.1.5:11434", "http://192.168.1.5:11434"},
		{"http://example.com:11434/", "http://example.com:11434"},
	}
	for _, tt := range tests {
		t.Run(tt.env, func(t *testing.T) {
			t.Setenv("OLLAMA_HOST", tt.env)
			if got := webSearchLocalHost(); got != tt.want {
				t.Errorf("webSearchLocalHost() = %q, want %q", got, tt.want)
			}
		})
	}
}

// The Run wrapper is the path the model actually takes, so invoke through it
// rather than only through runWebSearch.
func TestWebSearchViaRun(t *testing.T) {
	var rec searchRequest
	srv := newSearchTestServer(t, http.StatusOK, twoResults, &rec)
	withSearchEnv(t, srv.URL, "", "")

	tl, err := newWebSearchTool()
	if err != nil {
		t.Fatal(err)
	}
	out := runTool(t, tl, map[string]any{"query": "hello"})
	if got := out["source"]; got != "local" {
		t.Errorf("source = %v, want local", got)
	}
	results, ok := out["results"].([]any)
	if !ok || len(results) != 2 {
		t.Fatalf("results = %#v, want 2 entries", out["results"])
	}
}

// A non-2xx answer that is not JSON must still produce a usable message. The
// real API wraps its errors in JSON, but a proxy or load balancer in the path
// answers with HTML, and that must not reach the model as a bare decode error.
func TestWebSearchNonJSONErrorStatus(t *testing.T) {
	srv := newSearchTestServer(t, http.StatusBadGateway, "<html>502 Bad Gateway</html>", nil)
	withSearchEnv(t, srv.URL, "", "")

	out, err := runWebSearch(context.Background(), WebSearchInput{Query: "q"})
	if err != nil {
		t.Fatalf("runWebSearch: %v", err)
	}
	if !strings.Contains(out.Error, "502") {
		t.Errorf("error = %q, want it to carry the status", out.Error)
	}
}

// Same guard for a 2xx body that is not JSON: a truncated or malformed
// response is a decode failure, and the message must say so.
func TestWebSearchMalformedSuccessBody(t *testing.T) {
	srv := newSearchTestServer(t, http.StatusOK, "not json at all", nil)
	withSearchEnv(t, srv.URL, "", "")

	out, err := runWebSearch(context.Background(), WebSearchInput{Query: "q"})
	if err != nil {
		t.Fatalf("runWebSearch: %v", err)
	}
	if !strings.Contains(out.Error, "decode") {
		t.Errorf("error = %q, want a decode failure", out.Error)
	}
}

// A 2xx body with no results is "no results", not an error from the API.
func TestWebSearchEmptyBodyIsNoResults(t *testing.T) {
	srv := newSearchTestServer(t, http.StatusOK, "{}", nil)
	withSearchEnv(t, srv.URL, "", "")

	out, err := runWebSearch(context.Background(), WebSearchInput{Query: "q"})
	if err != nil {
		t.Fatalf("runWebSearch: %v", err)
	}
	if out.Error != "no results returned" {
		t.Errorf("error = %q, want no results returned", out.Error)
	}
}

// A non-2xx status with a valid-JSON body that carries no error field still has
// to be reported; otherwise a bare 500 would look like an empty result set.
func TestWebSearchErrorStatusWithoutErrorMessage(t *testing.T) {
	srv := newSearchTestServer(t, http.StatusInternalServerError, `{"results":[]}`, nil)
	withSearchEnv(t, srv.URL, "", "")

	out, err := runWebSearch(context.Background(), WebSearchInput{Query: "q"})
	if err != nil {
		t.Fatalf("runWebSearch: %v", err)
	}
	if !strings.Contains(out.Error, "500") {
		t.Errorf("error = %q, want it to carry the status", out.Error)
	}
}

// A request that cannot be built short-circuits before any network call.
func TestWebSearchBadURLIsReported(t *testing.T) {
	srv := newSearchTestServer(t, http.StatusOK, twoResults, nil)
	withSearchEnv(t, srv.URL, "", "")

	out, err := runWebSearch(context.Background(), WebSearchInput{Query: "q"})
	if err != nil {
		t.Fatalf("runWebSearch: %v", err)
	}
	if out.Error != "" {
		t.Fatalf("baseline failed: %s", out.Error)
	}

	// A control character in the URL makes http.NewRequestWithContext fail
	// before the request is sent.
	t.Setenv("OLLAMA_HOST", "http://exa\x7fmple.com")
	out, err = runWebSearch(context.Background(), WebSearchInput{Query: "q"})
	if err != nil {
		t.Fatalf("runWebSearch: %v", err)
	}
	if out.Error == "" {
		t.Error("expected an error for an unbuildable URL")
	}
}

// With a key present and the local endpoint unreachable, the transport failure
// from both endpoints is what gets reported.
func TestWebSearchBothEndpointsUnreachableWithKey(t *testing.T) {
	dead := newSearchTestServer(t, http.StatusOK, twoResults, nil)
	deadURL := dead.URL
	dead.Close()

	withSearchEnv(t, deadURL, deadURL, "sk-test-key")
	out, err := runWebSearch(context.Background(), WebSearchInput{Query: "q"})
	if err != nil {
		t.Fatalf("runWebSearch: %v", err)
	}
	if !strings.Contains(out.Error, "web search failed") {
		t.Errorf("error = %q, want the transport failure", out.Error)
	}
	// With a key set, the message must not claim the key is missing.
	if strings.Contains(out.Error, "OLLAMA_API_KEY is not set") {
		t.Errorf("error = %q wrongly blames a missing key", out.Error)
	}
}

// A body that fails mid-read is the case the bounded read exists for: a
// connection dropped partway must not surface as a partial decode.
func TestWebSearchBodyReadError(t *testing.T) {
	t.Setenv("OLLAMA_API_KEY", "")
	t.Setenv("OLLAMA_HOST", "http://example.invalid")

	prev := webSearchHTTPClient
	webSearchHTTPClient = &http.Client{Transport: errReadTransport{}}
	t.Cleanup(func() { webSearchHTTPClient = prev })

	out, err := runWebSearch(context.Background(), WebSearchInput{Query: "q"})
	if err != nil {
		t.Fatalf("runWebSearch: %v", err)
	}
	if !strings.Contains(out.Error, "read response") {
		t.Errorf("error = %q, want a read failure", out.Error)
	}
}

// errReadTransport returns a well-formed response whose body fails on Read,
// standing in for a connection dropped mid-stream. The failing reader is the
// package's existing errReader (llms_test.go).
type errReadTransport struct{}

func (errReadTransport) RoundTrip(*http.Request) (*http.Response, error) {
	return &http.Response{
		StatusCode: http.StatusOK,
		Status:     "200 OK",
		Header:     make(http.Header),
		Body:       io.NopCloser(errReader{}),
	}, nil
}

func TestWebSearchToolDeclaration(t *testing.T) {
	tool, err := newWebSearchTool()
	if err != nil {
		t.Fatal(err)
	}
	if tool.Name() != "web_search" {
		t.Errorf("name = %q, want web_search", tool.Name())
	}
	if tool.Description() == "" {
		t.Error("description is empty; the model needs it to know when to search")
	}
}
