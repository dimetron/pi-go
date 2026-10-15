package tools

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"
)

func TestHTMLToMarkdown_Document(t *testing.T) {
	const src = `<!DOCTYPE html>
<html>
<head>
<title>Ignored Title</title>
<style>p{color:red}</style>
<script>alert("nope")</script>
</head>
<body>
<script>alert("also-nope")</script>
<h1>Hello</h1>
<p>See <a href="https://example.com/docs">the docs</a> and <strong>this</strong>.</p>
<ul>
<li>one</li>
<li>two</li>
</ul>
<pre><code class="language-go">fmt.Println("hi")</code></pre>
</body>
</html>`

	const want = "# Hello\n\n" +
		"See [the docs](https://example.com/docs) and **this**.\n\n" +
		"- one\n" +
		"- two\n\n" +
		"```go\n" +
		"fmt.Println(\"hi\")\n" +
		"```"

	got := htmlToMarkdown(src, nil)
	if got != want {
		t.Fatalf("markdown =\n%q\nwant\n%q", got, want)
	}
	if strings.Contains(got, "nope") || strings.Contains(got, "color:red") || strings.Contains(got, "Ignored") {
		t.Fatalf("non-content leaked into markdown: %q", got)
	}
}

func TestHTMLToMarkdown_RelativeLinkAndInline(t *testing.T) {
	base := mustFetchURL(t, "https://example.com/a/b.html")
	got := htmlToMarkdown(`<p>use <code>go test</code> and <em>care</em>. <a href="guide">Guide</a> <a href="javascript:alert(1)">click</a> <a href="mailto:a@b.c">mail</a></p>`, base)
	want := "use `go test` and *care*. [Guide](https://example.com/a/guide) click [mail](mailto:a@b.c)"
	if got != want {
		t.Fatalf("markdown =\n%q\nwant\n%q", got, want)
	}
}

func TestHTMLToMarkdown_ListsTableQuote(t *testing.T) {
	got := htmlToMarkdown(`<ul><li>a<ul><li>b</li></ul></li><li>c</li></ul>`, nil)
	if got != "- a\n  - b\n- c" {
		t.Fatalf("nested list =\n%q", got)
	}

	got = htmlToMarkdown(`<ol start="3"><li>a</li><li>b</li></ol>`, nil)
	if got != "3. a\n4. b" {
		t.Fatalf("ordered list =\n%q", got)
	}

	got = htmlToMarkdown(`<table><tr><th>A</th><th>B</th></tr><tr><td>1</td><td>2</td></tr></table>`, nil)
	if got != "| A | B |\n| --- | --- |\n| 1 | 2 |" {
		t.Fatalf("table =\n%q", got)
	}

	got = htmlToMarkdown(`<blockquote><p>quoted</p></blockquote>`, nil)
	if got != "> quoted" {
		t.Fatalf("blockquote =\n%q", got)
	}
}

func TestRenderWebBody_ContentTypes(t *testing.T) {
	page := mustFetchURL(t, "https://example.com/docs/page")

	htmlBody := []byte(`<h1>Title</h1><p>Hi <a href="/guide">there</a></p>`)
	got, errMsg := renderWebBody("text/html; charset=utf-8", htmlBody, page)
	if errMsg != "" {
		t.Fatalf("html: %s", errMsg)
	}
	if got.contentType != "text/markdown" {
		t.Errorf("html content type = %q, want text/markdown", got.contentType)
	}
	if !strings.Contains(got.content, "# Title") || !strings.Contains(got.content, "[there](https://example.com/guide)") {
		t.Fatalf("html render = %q", got.content)
	}

	plain, errMsg := renderWebBody("text/plain", []byte("hello\n"), page)
	if errMsg != "" || plain.content != "hello\n" || plain.contentType != "text/plain" {
		t.Fatalf("plain = %+v err=%s", plain, errMsg)
	}

	js, errMsg := renderWebBody("application/json", []byte(`{"a":1}`), page)
	if errMsg != "" || js.content != `{"a":1}` || js.contentType != "application/json" {
		t.Fatalf("json = %+v err=%s", js, errMsg)
	}

	// A missing type is sniffed. A labeled binary type is refused.
	sniffed, errMsg := renderWebBody("", []byte("<p>Hi</p>"), page)
	if errMsg != "" || !strings.Contains(sniffed.content, "Hi") || sniffed.contentType != "text/markdown" {
		t.Fatalf("sniffed html = %+v err=%s", sniffed, errMsg)
	}
	sniffedJSON, errMsg := renderWebBody("application/octet-stream", []byte(`[1,2]`), page)
	if errMsg != "" || sniffedJSON.content != `[1,2]` || sniffedJSON.contentType != "application/json" {
		t.Fatalf("sniffed json = %+v err=%s", sniffedJSON, errMsg)
	}

	if _, errMsg = renderWebBody("image/png", []byte{0x89, 'P', 'N', 'G'}, page); errMsg == "" || !strings.Contains(errMsg, "image/png") {
		t.Fatalf("png error = %q, want unsupported image/png", errMsg)
	}
	png := []byte{0x89, 0x50, 0x4e, 0x47, 0x0d, 0x0a, 0x1a, 0x0a, 0xff, 0xfe}
	if _, errMsg = renderWebBody("application/octet-stream", png, page); errMsg == "" {
		t.Fatal("binary octet-stream was accepted")
	}

	latin, errMsg := renderWebBody("text/plain; charset=iso-8859-1", []byte{0x63, 0x61, 0x66, 0xe9}, page)
	if errMsg != "" {
		t.Fatal(errMsg)
	}
	if latin.content != "café" {
		t.Fatalf("charset = %q, want café", latin.content)
	}
}

func TestParseWebFetchURL(t *testing.T) {
	ok := []string{
		"https://example.com/a",
		"http://example.com",
		"HTTPS://example.com/a",
	}
	for _, raw := range ok {
		u, err := parseWebFetchURL(raw)
		if err != nil {
			t.Errorf("parse %q: %v", raw, err)
			continue
		}
		if u.Fragment != "" {
			t.Errorf("parse %q kept fragment %q", raw, u.Fragment)
		}
	}

	bad := []struct {
		raw  string
		want string
	}{
		{"", "url is required"},
		{"   ", "url is required"},
		{"file:///etc/passwd", "only http and https"},
		{"javascript:alert(1)", "only http and https"},
		{"ftp://example.com/a", "only http and https"},
		{"http://", "no host"},
		{"https://user:secret@example.com/a", "userinfo"},
	}
	for _, tt := range bad {
		_, err := parseWebFetchURL(tt.raw)
		if err == nil || !strings.Contains(err.Error(), tt.want) {
			t.Errorf("parse %q error = %v, want %q", tt.raw, err, tt.want)
		}
	}

	u, err := parseWebFetchURL("https://example.com/a#section")
	if err != nil {
		t.Fatal(err)
	}
	if u.Fragment != "" || strings.Contains(u.String(), "section") {
		t.Fatalf("fragment survived: %s", u)
	}
}

func TestRunWebFetch_RejectsPrivateAndUnsafeHosts(t *testing.T) {
	for _, raw := range []string{
		"http://127.0.0.1/",
		"http://127.0.0.1:9/",
		"http://[::1]/",
		"http://[::ffff:127.0.0.1]/",
		"http://10.1.2.3/",
		"http://169.254.169.254/latest/meta-data",
		"http://0.0.0.0/",
		"http://localhost/",
	} {
		out, err := runWebFetch(context.Background(), WebFetchInput{URL: raw})
		if err != nil {
			t.Fatalf("%s: %v", raw, err)
		}
		if out.Error == "" || out.Content != "" {
			t.Fatalf("%s: got %+v, want a rejection and no content", raw, out)
		}
	}
}

func TestWebFetchDialAllowsProxyAddress(t *testing.T) {
	// The page host is public; the dial address is a local proxy. Refusing it
	// as loopback would make web_fetch unusable behind HTTP_PROXY.
	ctx := context.WithValue(context.Background(), webFetchOriginKey{}, "example.com")
	_, err := webFetchDialContext(ctx, "tcp", "127.0.0.1:1")
	if err == nil {
		t.Fatal("expected the proxy dial to fail")
	}
	if strings.Contains(err.Error(), "loopback") || strings.Contains(err.Error(), "private") {
		t.Fatalf("proxy address was refused as a private destination: %v", err)
	}
}

func TestWebFetchDialRejectsPrivateIP(t *testing.T) {
	_, err := webFetchDialContext(context.Background(), "tcp", "10.1.2.3:80")
	if err == nil || !strings.Contains(err.Error(), "private") {
		t.Fatalf("dial error = %v, want a private-address refusal before connect", err)
	}
	_, err = webFetchDialContext(context.Background(), "tcp", "169.254.169.254:80")
	if err == nil || !strings.Contains(err.Error(), "link-local") {
		t.Fatalf("metadata dial error = %v, want link-local refusal", err)
	}
}

func TestRunWebFetch_HTMLPlainAndJSON(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/page", func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("User-Agent") == "" {
			t.Errorf("request has no User-Agent")
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write([]byte(`<h1>Hello</h1><p>See <a href="/guide">the guide</a>.</p><script>secret()</script>`))
	})
	mux.HandleFunc("/note.txt", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		_, _ = w.Write([]byte("plain body"))
	})
	mux.HandleFunc("/data.json", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"ok":true}`))
	})
	mux.HandleFunc("/pic.png", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "image/png")
		_, _ = w.Write([]byte{0x89, 'P', 'N', 'G'})
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	withAllowedTestHosts(t, "127.0.0.1")

	page, err := runWebFetch(context.Background(), WebFetchInput{URL: srv.URL + "/page"})
	if err != nil {
		t.Fatal(err)
	}
	if page.Error != "" {
		t.Fatal(page.Error)
	}
	if page.Status != http.StatusOK || page.ContentType != "text/markdown" {
		t.Fatalf("page status/type = %d %q", page.Status, page.ContentType)
	}
	if !strings.Contains(page.Content, "# Hello") || !strings.Contains(page.Content, "[the guide]("+srv.URL+"/guide)") {
		t.Fatalf("page content = %q", page.Content)
	}
	if strings.Contains(page.Content, "secret") {
		t.Fatalf("script leaked: %q", page.Content)
	}

	note, err := runWebFetch(context.Background(), WebFetchInput{URL: srv.URL + "/note.txt"})
	if err != nil || note.Content != "plain body" || note.ContentType != "text/plain" {
		t.Fatalf("note = %+v err=%v", note, err)
	}
	data, err := runWebFetch(context.Background(), WebFetchInput{URL: srv.URL + "/data.json"})
	if err != nil || data.Content != `{"ok":true}` || data.ContentType != "application/json" {
		t.Fatalf("json = %+v err=%v", data, err)
	}
	pic, err := runWebFetch(context.Background(), WebFetchInput{URL: srv.URL + "/pic.png"})
	if err != nil || pic.Error == "" || pic.Content != "" {
		t.Fatalf("png = %+v err=%v", pic, err)
	}
}

func TestRunWebFetch_TruncatesBody(t *testing.T) {
	payload := strings.Repeat("a", webFetchMaxBytes+64)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		_, _ = w.Write([]byte(payload))
	}))
	t.Cleanup(srv.Close)
	withAllowedTestHosts(t, "127.0.0.1")

	out, err := runWebFetch(context.Background(), WebFetchInput{URL: srv.URL})
	if err != nil {
		t.Fatal(err)
	}
	if out.Error != "" {
		t.Fatal(out.Error)
	}
	if !out.Truncated {
		t.Fatal("expected truncated")
	}
	if len(out.Content) != webFetchMaxBytes || out.Content != strings.Repeat("a", webFetchMaxBytes) {
		t.Fatalf("content len = %d, want the %d byte cap", len(out.Content), webFetchMaxBytes)
	}
}

func TestRunWebFetch_Timeout(t *testing.T) {
	started := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		close(started)
		<-r.Context().Done()
	}))
	t.Cleanup(srv.Close)
	withAllowedTestHosts(t, "127.0.0.1")

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	start := time.Now()
	out, err := runWebFetch(ctx, WebFetchInput{URL: srv.URL})
	elapsed := time.Since(start)
	if err != nil {
		t.Fatal(err)
	}
	if out.Error == "" {
		t.Fatal("expected a timeout error")
	}
	if elapsed > 5*time.Second {
		t.Fatalf("fetch took %s; the deadline did not stop it", elapsed)
	}
	select {
	case <-started:
	default:
		t.Fatal("handler never saw the request")
	}
}

func TestRunWebFetch_Redirects(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/start", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/final", http.StatusFound)
	})
	mux.HandleFunc("/final", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		_, _ = w.Write([]byte("landed"))
	})
	mux.HandleFunc("/loop", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/loop", http.StatusFound)
	})
	mux.HandleFunc("/bounce", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "http://10.1.2.3/secret", http.StatusFound)
	})
	mux.HandleFunc("/file", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Location", "file:///etc/passwd")
		w.WriteHeader(http.StatusFound)
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	withAllowedTestHosts(t, "127.0.0.1")

	ok, err := runWebFetch(context.Background(), WebFetchInput{URL: srv.URL + "/start"})
	if err != nil {
		t.Fatal(err)
	}
	if ok.Error != "" || ok.Content != "landed" || !strings.HasSuffix(ok.URL, "/final") {
		t.Fatalf("redirect = %+v", ok)
	}

	loop, err := runWebFetch(context.Background(), WebFetchInput{URL: srv.URL + "/loop"})
	if err != nil {
		t.Fatal(err)
	}
	if loop.Content != "" || !strings.Contains(loop.Error, "stopped after") {
		t.Fatalf("loop = %+v", loop)
	}

	bounce, err := runWebFetch(context.Background(), WebFetchInput{URL: srv.URL + "/bounce"})
	if err != nil {
		t.Fatal(err)
	}
	if bounce.Content != "" || !strings.Contains(bounce.Error, "private") {
		t.Fatalf("bounce = %+v", bounce)
	}

	fileHop, err := runWebFetch(context.Background(), WebFetchInput{URL: srv.URL + "/file"})
	if err != nil {
		t.Fatal(err)
	}
	if fileHop.Content != "" || fileHop.Error == "" {
		t.Fatalf("file redirect = %+v", fileHop)
	}
}

func TestRunWebFetch_HTTPError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "missing", http.StatusNotFound)
	}))
	t.Cleanup(srv.Close)
	withAllowedTestHosts(t, "127.0.0.1")

	out, err := runWebFetch(context.Background(), WebFetchInput{URL: srv.URL})
	if err != nil {
		t.Fatal(err)
	}
	if out.Status != http.StatusNotFound || !strings.Contains(out.Error, "404") || out.Content != "" {
		t.Fatalf("status error = %+v", out)
	}
}

func TestWebFetchToolDeclaration(t *testing.T) {
	tool, err := newWebFetchTool()
	if err != nil {
		t.Fatal(err)
	}
	if tool.Name() != "web_fetch" {
		t.Errorf("name = %q, want web_fetch", tool.Name())
	}
	desc := tool.Description()
	for _, want := range []string{"markdown", "100 KiB", "30s", "truncated"} {
		if !strings.Contains(desc, want) {
			t.Errorf("description missing %q:\n%s", want, desc)
		}
	}
}

func mustFetchURL(t *testing.T, raw string) *url.URL {
	t.Helper()
	u, err := parseWebFetchURL(raw)
	if err != nil {
		t.Fatal(err)
	}
	return u
}
