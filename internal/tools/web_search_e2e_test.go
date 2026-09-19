//go:build e2e

package tools

import (
	"context"
	"os"
	"testing"
)

// These tests talk to a real Ollama endpoint, which is what makes them worth
// having: the local and cloud paths are different URLs, and a unit test with a
// stub server cannot notice that the endpoint a release actually serves has
// moved. The path drift that produced this file — the Go SDK posting to
// /api/experimental/web_search, which 404s on api.ollama.com — is invisible to
// httptest and obvious here.
//
//	PI_LIVE_SEARCH=1 go test -tags e2e ./internal/tools/ -run TestE2EWebSearch -v
//
// Skipped without PI_LIVE_SEARCH, so a bare `make test-e2e` does not depend on
// a daemon being up or on spending search quota.

func e2eSearchEnabled(t *testing.T) {
	t.Helper()
	if os.Getenv("PI_LIVE_SEARCH") == "" {
		t.Skip("set PI_LIVE_SEARCH=1 to run against a real Ollama endpoint")
	}
}

func TestE2EWebSearchLocalDaemon(t *testing.T) {
	e2eSearchEnabled(t)
	t.Setenv("OLLAMA_HOST", "") // force the default localhost:11434

	// The local daemon is the path that needs no credential, so it is the one
	// that should work on a developer machine with no key exported.
	t.Setenv("OLLAMA_API_KEY", "")
	out, err := runWebSearch(context.Background(), WebSearchInput{Query: "ollama release notes", MaxResults: 3})
	if err != nil {
		t.Fatalf("runWebSearch: %v", err)
	}
	if out.Error != "" {
		t.Skipf("no local daemon available: %s", out.Error)
	}
	if out.Source != "local" {
		t.Errorf("source = %q, want local", out.Source)
	}
	if len(out.Results) == 0 {
		t.Fatal("no results")
	}
	for i, r := range out.Results {
		if r.URL == "" {
			t.Errorf("result[%d] has no url", i)
		}
		if len(r.Content) > webSearchMaxContentBytes {
			t.Errorf("result[%d] content = %d bytes, over the %d cap", i, len(r.Content), webSearchMaxContentBytes)
		}
		t.Logf("[%d] %s | %s | %d bytes", i, r.Title, r.URL, len(r.Content))
	}
}

// The cloud path needs OLLAMA_API_KEY and draws on a monthly quota, so it is
// opt-in twice over.
func TestE2EWebSearchCloud(t *testing.T) {
	e2eSearchEnabled(t)
	if os.Getenv("OLLAMA_API_KEY") == "" {
		t.Skip("OLLAMA_API_KEY not set")
	}
	if os.Getenv("PI_LIVE_SEARCH_CLOUD") == "" {
		t.Skip("set PI_LIVE_SEARCH_CLOUD=1 to spend search quota")
	}

	// Point the local endpoint at a closed port so the fallback is exercised
	// rather than short-circuited.
	t.Setenv("OLLAMA_HOST", "http://127.0.0.1:1")
	out, err := runWebSearch(context.Background(), WebSearchInput{Query: "ollama release notes", MaxResults: 2})
	if err != nil {
		t.Fatalf("runWebSearch: %v", err)
	}
	if out.Source != "cloud" {
		t.Fatalf("source = %q, want cloud; error = %q", out.Source, out.Error)
	}
	if out.Error != "" {
		t.Fatalf("cloud search failed: %s", out.Error)
	}
	if len(out.Results) == 0 {
		t.Fatal("no results")
	}
	t.Logf("cloud returned %d results", len(out.Results))
}
