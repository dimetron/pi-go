package cli

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"maps"
	"net"
	"net/http"
	"net/http/httptrace"
	"net/url"
	"os"
	"slices"
	"strings"
	"time"

	"github.com/spf13/cobra"
	llmmodel "google.golang.org/adk/v2/model"
	"google.golang.org/genai"

	"github.com/dimetron/pi-go/internal/auth"
	"github.com/dimetron/pi-go/internal/config"
	"github.com/dimetron/pi-go/internal/httplog"
	"github.com/dimetron/pi-go/internal/provider"
)

// ANSI color codes for ping output.
const (
	colorGray   = "\033[90m" // dim gray for secondary info
	colorYellow = "\033[33m" // yellow for warnings
	colorBlue   = "\033[34m" // blue for phase headers
	colorGreen  = "\033[32m" // green for success
	colorReset  = "\033[0m"
)

// codexPingBaseURL is the ChatGPT backend used when OPENAI_API_KEY holds a
// codex OAuth token; the platform /v1/* endpoints reject those tokens.
const codexPingBaseURL = "https://chatgpt.com/backend-api/codex"

func newPingCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "ping [prompt...]",
		Short: "Check connectivity to the LLM provider (verbose trace)",
		Long: `Performs a verbose connectivity check to the configured LLM provider, similar to curl -vvv.
Shows DNS resolution, TCP connection, TLS handshake, HTTP request/response, and a model API call.

The default test sends "prompt-prompt" and expects "prompt-prompt". If a prompt is provided as positional args,
it is sent instead and the full response is displayed with all trace-level data.

Examples:
  pi ping                     # prompt-prompt connectivity test
  pi ping 2+2                 # custom prompt with full trace
  pi ping --smol Explain Go   # test smol role with custom prompt`,
		Args: cobra.ArbitraryArgs,
		RunE: runPing,
	}
	cmd.Flags().StringVar(&flagModel, "model", "", "LLM model to use")
	cmd.Flags().StringVar(&flagURL, "url", "", "Alternative base URL for the LLM API endpoint")
	cmd.Flags().StringArrayVar(&flagHeaders, "header", nil, "Extra HTTP header for LLM requests (key=value, repeatable)")
	if f := cmd.Flags().Lookup("header"); f != nil {
		f.NoOptDefVal = ""
	}
	cmd.Flags().BoolVar(&flagInsecure, "insecure", false, "Skip TLS certificate verification for LLM API calls")
	cmd.Flags().StringVar(&flagCACert, "ca-cert", "", "PEM bundle to trust for LLM API calls, in addition to the system roots")
	cmd.Flags().BoolVar(&flagSmol, "smol", false, "Use the smol role")
	cmd.Flags().BoolVar(&flagSlow, "slow", false, "Use the slow role")
	cmd.Flags().BoolVar(&flagPlan, "plan", false, "Use the plan role")
	return cmd
}

// HTTPDoer abstracts http.Client for testability.
type HTTPDoer interface {
	Do(req *http.Request) (*http.Response, error)
}

// defaultAPIBaseURL returns the default API base URL for a provider.
func defaultAPIBaseURL(providerName string) string {
	switch providerName {
	case "anthropic":
		return "https://api.anthropic.com"
	case "openai":
		return "https://api.openai.com"
	case "gemini":
		return "https://generativelanguage.googleapis.com"
	case "xai":
		return "https://api.x.ai"
	default:
		return ""
	}
}

// pingEndpoint returns the health-check URL path for a provider.
func pingEndpoint(providerName string) string {
	switch providerName {
	case "anthropic":
		return "/v1/messages"
	case "openai":
		return "/v1/models"
	case "gemini":
		return "/v1beta/models"
	case "xai":
		return "/v1/models"
	default:
		return "/"
	}
}

// pingEndpointForBaseURL returns the provider health-check path adjusted for a
// custom base URL that may already include the provider API version prefix.
func pingEndpointForBaseURL(providerName, baseURL string) string {
	endpoint := pingEndpoint(providerName)
	switch providerName {
	case "openai", "xai":
		if strings.HasSuffix(strings.TrimRight(baseURL, "/"), "/v1") {
			return "/models"
		}
	}
	return endpoint
}

// pingWriter emits one line of ping trace output.
type pingWriter func(format string, a ...any)

// pingTarget is the resolved endpoint a ping run exercises, along with the
// credentials and transport options needed to reach it.
type pingTarget struct {
	info      provider.Info
	apiKey    string
	baseURL   string
	targetURL string
	// fallbackURLs are tried in order when targetURL answers non-2xx. Azure
	// populates them because no single GET route is both universally served
	// and meaningful — see provider.AzureProbePaths.
	fallbackURLs []string
	opts         *provider.LLMOptions
	// codexBackend routes through the ChatGPT codex backend because the key is
	// an OAuth token the platform API would reject.
	codexBackend bool
}

// runPing walks the six diagnostic phases in order — resolve, DNS, TCP, TLS,
// HTTP, model — stopping at the first one that fails.
func runPing(cmd *cobra.Command, args []string) error {
	loadDotEnv()

	cfg, err := config.Load()
	if err != nil {
		return fmt.Errorf("loading config: %w", err)
	}

	target, err := resolvePingTarget(cfg)
	if err != nil {
		return err
	}

	w := pingWriter(func(format string, a ...any) { _, _ = fmt.Fprintf(os.Stderr, format, a...) })
	target.printHeader(w)

	// ping has no session log to write to, so --trace-http goes to the same
	// stream as the rest of its output. Without this the flag would be
	// accepted here and do nothing. It complements rather than duplicates the
	// dumpPing* output: that covers only the health-check probe and never
	// shows a body, while this covers the model API call underneath.
	if flagTraceHTTP {
		httplog.SetSink(pingTraceSink(w))
		defer httplog.SetSink(nil)
	}

	u, err := url.Parse(target.targetURL)
	if err != nil {
		w("* ERROR: invalid URL %q: %v\n", target.targetURL, err)
		return fmt.Errorf("invalid URL: %w", err)
	}
	host, port := u.Hostname(), pingPort(u)

	addrs, err := pingDNS(w, host)
	if err != nil {
		return err
	}
	if err := pingTCP(w, addrs[0], port); err != nil {
		return err
	}
	if u.Scheme == "https" {
		if err := pingTLS(w, host, port, target.opts.InsecureSkipTLS); err != nil {
			return err
		}
	}

	// The response body is read again during the verdict, so the request
	// context has to outlive doHTTP — keep it scoped to runPing.
	ctx, cancel := context.WithTimeout(cmd.Context(), 30*time.Second)
	defer cancel()

	resp, err := target.doHTTP(ctx, w)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()

	if !target.reportHTTPVerdict(w, resp) {
		w("* %s─── Model Ping ───%s\n", colorBlue, colorReset)
		w("* Skipped — endpoint not reachable\n")
		return nil
	}
	return target.pingModel(cmd.Context(), w, args)
}

// resolvePingTarget resolves the role, model, credentials and endpoint the ping
// runs against, applying the --model/--url/--header/--insecure flags.
func resolvePingTarget(cfg config.Config) (*pingTarget, error) {
	if flagModel != "" {
		cfg.Roles["default"] = config.RoleConfig{Model: flagModel}
	}

	info, baseURL, explicitBaseURL, err := resolvePingModelInfo(cfg, pingRoleFromFlags())
	if err != nil {
		return nil, err
	}

	baseURL, apiKey, codexBackend := resolvePingCredentials(info, baseURL, explicitBaseURL)
	endpoint, fallbacks := pingProbePaths(info, baseURL, codexBackend)

	opts := &provider.LLMOptions{
		ExtraHeaders: mergeExtraHeaders(cfg.ExtraHeaders, flagHeaders),
	}
	applyTransportOptions(opts, cfg)

	trimmed := strings.TrimRight(baseURL, "/")
	fallbackURLs := make([]string, 0, len(fallbacks))
	for _, p := range fallbacks {
		fallbackURLs = append(fallbackURLs, trimmed+p)
	}
	return &pingTarget{
		info:         info,
		apiKey:       apiKey,
		baseURL:      baseURL,
		targetURL:    trimmed + endpoint,
		fallbackURLs: fallbackURLs,
		codexBackend: codexBackend,
		opts:         opts,
	}, nil
}

// pingRoleFromFlags returns the role the --smol/--slow/--plan flags select.
func pingRoleFromFlags() string {
	switch {
	case flagSmol:
		return "smol"
	case flagSlow:
		return "slow"
	case flagPlan:
		return "plan"
	default:
		return "default"
	}
}

// resolvePingModelInfo resolves the role's model into provider info. It also
// returns the base URL the resolution used and whether that URL was configured
// rather than defaulted — the codex-token check needs to tell those apart.
func resolvePingModelInfo(cfg config.Config, activeRole string) (provider.Info, string, bool, error) {
	modelName, providerName, _, _, _, err := cfg.ResolveRole(activeRole)
	if err != nil {
		return provider.Info{}, "", false, fmt.Errorf("resolving model role: %w", err)
	}

	baseURL := flagURL
	explicitBaseURL := baseURL != ""
	if baseURL == "" && providerName != "" {
		baseURL = cfg.ResolveBaseURLs()[providerName]
		explicitBaseURL = baseURL != ""
	}

	info, err := provider.ResolveWithBaseURL(modelName, baseURL)
	if err != nil {
		return provider.Info{}, "", false, fmt.Errorf("resolving model: %w", err)
	}
	if providerName != "" {
		info.Provider = providerName
		info.Custom = baseURL != ""
	}
	if err := provider.ValidateModel(info); err != nil {
		return provider.Info{}, "", false, fmt.Errorf("model validation: %w", err)
	}
	return info, baseURL, explicitBaseURL, nil
}

// resolvePingCredentials settles the base URL and API key the probe uses, and
// reports whether the run has to go through the ChatGPT codex backend.
func resolvePingCredentials(info provider.Info, baseURL string, explicitBaseURL bool) (string, string, bool) {
	apiKey := config.APIKeys()[info.Provider]
	if info.Ollama {
		baseURL = provider.ResolveOllamaEndpoint(provider.OllamaRouting{
			Model:      info.Model,
			BaseURL:    baseURL,
			APIKey:     apiKey,
			ForceLocal: info.LocalOllama,
		})
	}
	// Azure resolves its endpoint and credential through the same helpers a
	// real run uses, so a passing ping means the settings a real run would pick
	// up are the ones that worked. config.APIKeys already covers all three key
	// vars; routing through provider.AzureAPIKey keeps ping and the client on
	// one chain that cannot drift. The endpoint is the real gap — ping ignored
	// AZURE_OPENAI_ENDPOINT entirely and saw only the configured base URL.
	if info.Provider == "azure" {
		baseURL = provider.AzureEndpoint(baseURL)
		apiKey = provider.AzureAPIKey(apiKey)
	}
	if baseURL == "" {
		baseURL = defaultAPIBaseURL(info.Provider)
	}

	// A codex ChatGPT OAuth token (JWT with the OpenAI auth claim) cannot
	// talk to api.openai.com — the platform API requires an sk- key with
	// api.responses.write scope. Redirect the health check and the model
	// ping to the ChatGPT backend (/codex/responses), mirroring pi-mono's
	// openai-codex-responses provider.
	codexBackend := info.Provider == "openai" && !explicitBaseURL && auth.IsCodexOAuthToken(apiKey)
	if codexBackend {
		baseURL = codexPingBaseURL
	}
	return baseURL, apiKey, codexBackend
}

// pingProbePaths returns the health-check path to probe, plus any paths to try
// when the first one does not answer 2xx.
func pingProbePaths(info provider.Info, baseURL string, codexBackend bool) (string, []string) {
	if codexBackend {
		// The codex backend only exposes POST /responses — use it as a
		// reachability target (it will 405 for GET, which we treat as
		// "server alive, endpoint requires POST").
		return "/responses", nil
	}
	if info.Provider == "azure" {
		// Same rules NewAzureOpenAI applies to real traffic: api-version on a
		// native resource, /models on a compat gateway. The first candidate is
		// the probe; the rest are tried only if it does not answer 2xx.
		paths := provider.AzureProbePaths(info.Model, "", baseURL)
		return paths[0], paths[1:]
	}
	return pingEndpointForBaseURL(info.Provider, baseURL), nil
}

// printHeader summarizes the resolved target before the phases begin.
func (t *pingTarget) printHeader(w pingWriter) {
	w("* pi-go ping\n")
	w("* %sProvider:%s  %s\n", colorBlue, colorReset, t.info.Provider)
	w("* %sModel:%s     %s\n", colorBlue, colorReset, t.info.Model)
	w("* %sOllama:%s    %v\n", colorBlue, colorReset, t.info.Ollama)
	if t.apiKey == "" {
		w("* %sAPI Key:%s   %s(not set)%s\n", colorBlue, colorReset, colorGray, colorReset)
	} else {
		w("* %sAPI Key:%s   %s\n", colorBlue, colorReset, maskAPIKey(t.apiKey))
	}
	w("* %sBase URL:%s  %s\n", colorBlue, colorReset, t.baseURL)
	w("*\n")
}

// maskAPIKey keeps the leading and trailing four characters of a key so it can
// be identified, and hides the rest. Keys too short to mask are left alone.
func maskAPIKey(key string) string {
	if len(key) <= 8 {
		return key
	}
	return key[:4] + "..." + key[len(key)-4:]
}

// pingPort returns the explicit port of u, or the default for its scheme.
func pingPort(u *url.URL) string {
	if port := u.Port(); port != "" {
		return port
	}
	if u.Scheme == "https" {
		return "443"
	}
	return "80"
}

// pingDNS resolves host and reports the addresses it maps to.
func pingDNS(w pingWriter, host string) ([]string, error) {
	w("* %s─── DNS Resolution ───%s\n", colorBlue, colorReset)
	start := time.Now()
	addrs, err := net.LookupHost(host)
	dur := time.Since(start)
	if err != nil {
		w("* DNS FAILED: %v  %s(%s)%s\n", err, colorGray, dur.Round(time.Millisecond), colorReset)
		w("*\n* RESULT: connection issue — DNS resolution failed\n")
		return nil, fmt.Errorf("DNS resolution failed: %w", err)
	}
	w("*   Resolved %s → %s  %s(%s)%s\n",
		host, strings.Join(addrs, ", "), colorGray, dur.Round(time.Millisecond), colorReset)
	return addrs, nil
}

// pingTCP opens and immediately closes a TCP connection, timing the handshake.
func pingTCP(w pingWriter, addr, port string) error {
	w("* %s─── TCP Connection ───%s\n", colorBlue, colorReset)
	tcpAddr := net.JoinHostPort(addr, port)
	start := time.Now()
	conn, err := net.DialTimeout("tcp", tcpAddr, 10*time.Second)
	dur := time.Since(start)
	if err != nil {
		w("* TCP FAILED: %v  %s(%s)%s\n", err, colorGray, dur.Round(time.Millisecond), colorReset)
		w("*\n* RESULT: connection issue — TCP connect failed to %s\n", tcpAddr)
		return fmt.Errorf("TCP connect failed: %w", err)
	}
	_ = conn.Close()
	w("*   Connected to %s  %s(%s)%s\n", tcpAddr, colorGray, dur.Round(time.Millisecond), colorReset)
	return nil
}

// pingTLS performs a TLS handshake and reports the negotiated parameters and
// the server certificate.
func pingTLS(w pingWriter, host, port string, insecure bool) error {
	w("* %s─── TLS Handshake ───%s\n", colorBlue, colorReset)
	start := time.Now()
	conn, err := tls.DialWithDialer(
		&net.Dialer{Timeout: 10 * time.Second},
		"tcp", net.JoinHostPort(host, port),
		&tls.Config{
			ServerName:         host,
			InsecureSkipVerify: insecure, //nolint:gosec // user-requested
		},
	)
	dur := time.Since(start)
	if err != nil {
		w("* TLS FAILED: %v  %s(%s)%s\n", err, colorGray, dur.Round(time.Millisecond), colorReset)
		w("*\n* RESULT: connection issue — TLS handshake failed\n")
		return fmt.Errorf("TLS handshake failed: %w", err)
	}
	state := conn.ConnectionState()
	_ = conn.Close()

	w("*   TLS %s, cipher %s  %s(%s)%s\n",
		tlsVersionString(state.Version),
		tls.CipherSuiteName(state.CipherSuite),
		colorGray, dur.Round(time.Millisecond), colorReset)
	if len(state.PeerCertificates) > 0 {
		cert := state.PeerCertificates[0]
		w("*   Server cert: %sCN=%s%s, issuer=%s\n",
			colorGray, cert.Subject.CommonName, colorReset, cert.Issuer.CommonName)
		w("*   %sValid:%s %s → %s\n", colorGray, colorReset,
			cert.NotBefore.Format("2006-01-02"), cert.NotAfter.Format("2006-01-02"))
	}
	return nil
}

// pingClientTrace reports connection milestones as the request progresses.
func pingClientTrace(w pingWriter) *httptrace.ClientTrace {
	var connStart, tlsStart, gotConn time.Time
	since := func(t time.Time) time.Duration { return time.Since(t).Round(time.Millisecond) }
	return &httptrace.ClientTrace{
		ConnectStart: func(_, _ string) { connStart = time.Now() },
		ConnectDone: func(_, _ string, err error) {
			if err == nil {
				w("*   %s[trace]%s TCP connected %s(%s)%s\n",
					colorGray, colorReset, colorGray, since(connStart), colorReset)
			}
		},
		TLSHandshakeStart: func() { tlsStart = time.Now() },
		TLSHandshakeDone: func(_ tls.ConnectionState, _ error) {
			w("*   %s[trace]%s TLS done %s(%s)%s\n",
				colorGray, colorReset, colorGray, since(tlsStart), colorReset)
		},
		GotConn: func(_ httptrace.GotConnInfo) { gotConn = time.Now() },
		GotFirstResponseByte: func() {
			if !gotConn.IsZero() {
				w("*   %s[trace]%s TTFB %s(%s)%s\n",
					colorGray, colorReset, colorGray, since(gotConn), colorReset)
			}
		},
	}
}

// setPingAuthHeaders applies the provider's authentication scheme to req.
// Providers that carry the key in the query string rewrite req.URL instead.
func setPingAuthHeaders(req *http.Request, providerName, apiKey string) {
	if apiKey == "" {
		return
	}
	switch providerName {
	case "anthropic":
		req.Header.Set("x-api-key", apiKey)
		req.Header.Set("anthropic-version", "2023-06-01")
	case "openai", "xai":
		req.Header.Set("Authorization", "Bearer "+apiKey)
	case "azure":
		req.Header.Set("Api-Key", apiKey)
	case "gemini":
		q := req.URL.Query()
		q.Set("key", apiKey)
		req.URL.RawQuery = q.Encode()
	}
}

// pingTraceSink renders --trace-http entries in the same curl -v style as the
// rest of ping's output, so a full exchange reads as one continuous transcript.
func pingTraceSink(w pingWriter) func(httplog.Entry) {
	return func(e httplog.Entry) {
		marker := ">"
		if e.Direction == httplog.DirectionResponse {
			marker = "<"
		}
		switch {
		case e.Err != "":
			w("%s%s [%d] transport error: %s%s\n", colorYellow, marker, e.Exchange, e.Err, colorReset)
			return
		case e.Direction == httplog.DirectionRequest:
			w("%s%s [%d] %s %s%s\n", colorBlue, marker, e.Exchange, e.Method, e.URL, colorReset)
		case e.Status != 0:
			w("%s%s [%d] %s %d (%s)%s\n", colorBlue, marker, e.Exchange, e.Proto, e.Status,
				e.Duration.Round(time.Millisecond), colorReset)
		}
		for _, k := range slices.Sorted(maps.Keys(e.Headers)) {
			for _, v := range e.Headers[k] {
				w("%s%s [%d] %s: %s%s\n", colorBlue, marker, e.Exchange, k, v, colorReset)
			}
		}
		if e.Body != "" {
			w("%s%s [%d] body: %s%s\n", colorGray, marker, e.Exchange, e.Body, colorReset)
		}
	}
}

// dumpPingRequest echoes the outgoing request curl -v style, masking credentials.
//
// Redaction is delegated to httplog rather than kept as a local list. The local
// one covered Authorization and X-Api-Key only, and so printed Azure keys in
// full — Azure authenticates with the header spelled `Api-Key`, which did not
// match either name. Anything that authenticates a request now has exactly one
// place to be declared.
func dumpPingRequest(w pingWriter, req *http.Request) {
	w("%s> %s %s HTTP/1.1%s\n", colorBlue, req.Method, req.URL.RequestURI(), colorReset)
	w("%s> Host: %s%s\n", colorBlue, req.URL.Host, colorReset)
	for k, vs := range httplog.Redact(req.Header) {
		for _, v := range vs {
			w("%s> %s: %s%s\n", colorBlue, k, v, colorReset)
		}
	}
	w("\n")
}

// dumpPingResponse echoes the response status line, headers and total time.
//
// Responses are redacted too: set-cookie and openai-organization identify the
// account, and ping output is what people paste into a bug report.
func dumpPingResponse(w pingWriter, resp *http.Response, dur time.Duration) {
	w("%s< HTTP/%d.%d %s%s\n", colorBlue, resp.ProtoMajor, resp.ProtoMinor, resp.Status, colorReset)
	for k, vs := range httplog.Redact(resp.Header) {
		for _, v := range vs {
			w("%s< %s: %s%s\n", colorBlue, k, v, colorReset)
		}
	}
	w("\n")
	w("* Total HTTP time: %s%s%s\n", colorGray, dur.Round(time.Millisecond), colorReset)
	w("*\n")
}

// doHTTP issues the traced health-check request, walking targetURL and then
// each fallback until one answers 2xx. The caller owns ctx and the returned
// response body.
//
// Only Azure supplies fallbacks: its two candidate routes trade off against
// each other (one is meaningful, the other is more widely served), so which
// one a given resource honors is not knowable ahead of the request.
func (t *pingTarget) doHTTP(ctx context.Context, w pingWriter) (*http.Response, error) {
	resp, err := t.doHTTPTo(ctx, w, t.targetURL)
	if err != nil {
		return nil, err
	}
	for _, next := range t.fallbackURLs {
		if resp.StatusCode >= 200 && resp.StatusCode < 300 {
			return resp, nil
		}
		w("* %s⚠ HTTP %d — retrying the probe at %s%s\n", colorYellow, resp.StatusCode, next, colorReset)
		_ = resp.Body.Close()
		// A transport error here is not "this route is unserved" — the first
		// request already reached the host, so the connection itself broke.
		// That is worth failing on rather than papering over with the next
		// candidate.
		fallbackResp, fallbackErr := t.doHTTPTo(ctx, w, next)
		if fallbackErr != nil {
			return nil, fallbackErr
		}
		t.targetURL = next
		resp = fallbackResp
	}
	return resp, nil
}

// doHTTPTo issues one traced GET against rawURL.
func (t *pingTarget) doHTTPTo(ctx context.Context, w pingWriter, rawURL string) (*http.Response, error) {
	w("* %s─── HTTP Request ───%s\n", colorBlue, colorReset)

	req, err := http.NewRequestWithContext(
		httptrace.WithClientTrace(ctx, pingClientTrace(w)),
		http.MethodGet, rawURL, nil)
	if err != nil {
		return nil, fmt.Errorf("creating request: %w", err)
	}
	setPingAuthHeaders(req, t.info.Provider, t.apiKey)
	for k, v := range t.opts.ExtraHeaders {
		req.Header.Set(k, v)
	}
	req.Header.Set("User-Agent", "pi-go/"+Version)
	dumpPingRequest(w, req)

	client, err := provider.BuildHTTPClient(t.opts, 30*time.Second)
	if err != nil {
		w("* HTTP CLIENT FAILED: %v\n", err)
		w("*\n* RESULT: configuration issue — could not build the HTTP client\n")
		return nil, err
	}

	start := time.Now()
	resp, err := client.Do(req)
	dur := time.Since(start)
	if err != nil {
		w("* HTTP FAILED: %v  %s(%s)%s\n", err, colorGray, dur.Round(time.Millisecond), colorReset)
		w("*\n* RESULT: connection issue — HTTP request failed\n")
		return nil, fmt.Errorf("HTTP request failed: %w", err)
	}
	dumpPingResponse(w, resp, dur)
	return resp, nil
}

// reportHTTPVerdict classifies the health-check status and reports whether the
// endpoint is alive enough to continue on to the model ping. A 2xx from OpenAI
// also resolves the model alias against the returned model list.
func (t *pingTarget) reportHTTPVerdict(w pingWriter, resp *http.Response) bool {
	w("* %s─── HTTP Result ───%s\n", colorBlue, colorReset)
	defer w("*\n")

	// Custom Azure and proxy deployments routinely reject the plain GET probe;
	// when the user pointed us at one explicitly, keep going anyway.
	customAzure := t.info.Provider == "azure" && (flagURL != "" || len(t.opts.ExtraHeaders) > 0)

	switch code := resp.StatusCode; {
	case code >= 200 && code < 300:
		return t.verdictReachable(w, resp)

	case code == 401 || code == 403:
		return t.verdictUnauthorized(w, code, customAzure)

	case code == 404:
		return t.verdictNotFound(w, code)

	case code == 405:
		// Method Not Allowed is fine for POST-only endpoints — server is alive.
		w("* %s✓ Endpoint reachable via %s (endpoint requires POST)%s\n", colorGreen, t.info.Provider, colorReset)
		w("* Status: %s\n", resp.Status)
		return true

	case code == 422:
		return t.verdictUnprocessable(w, resp, customAzure)

	case code == 429:
		w("* %s⚠ Rate limited (HTTP %d) — endpoint reachable but throttled%s\n", colorYellow, code, colorReset)
		return true

	case code >= 500:
		w("* %s✗ Server error (HTTP %d) — provider may be experiencing issues%s\n", colorYellow, code, colorReset)
		return false

	default:
		w("* %s? Unexpected status: %s%s\n", colorYellow, resp.Status, colorReset)
		return false
	}
}

// verdictReachable reports a 2xx health check. For OpenAI the body is the model
// list, so it doubles as the place a model alias gets resolved to a real ID.
func (t *pingTarget) verdictReachable(w pingWriter, resp *http.Response) bool {
	w("* %s✓ Endpoint reachable via %s%s\n", colorGreen, t.info.Provider, colorReset)
	w("* Status: %s\n", resp.Status)
	if t.info.Provider == "openai" {
		if resolved, ok := resolveOpenAIModelFromList(resp, t.info.Model, w); ok {
			t.info.Model = resolved
		}
	}
	return true
}

// verdictUnauthorized reports a 401/403. A custom Azure or proxy endpoint is
// allowed to reject the GET probe and still be worth pinging.
func (t *pingTarget) verdictUnauthorized(w pingWriter, code int, customAzure bool) bool {
	if customAzure {
		w("* %s⚠ Received HTTP %d on health check, continuing with model ping (custom Azure/proxy setups may reject GET checks)%s\n", colorYellow, code, colorReset)
		return true
	}
	w("* %s✗ Authentication failed (HTTP %d)%s\n", colorYellow, code, colorReset)
	w("* The API endpoint is reachable but the API key is invalid or missing.\n")
	w("* Check %s\n", providerEnvVar(t.info.Provider))
	return false
}

// verdictNotFound reports a 404. Azure deployments routinely serve no GET
// health route at all, which says nothing about whether the model answers.
func (t *pingTarget) verdictNotFound(w pingWriter, code int) bool {
	if t.info.Provider == "azure" {
		w("* %s⚠ Endpoint returned 404 for health path, continuing with model ping (Azure/proxy endpoints often disable GET health routes)%s\n", colorYellow, colorReset)
		return true
	}
	w("* %s✗ Endpoint not found (HTTP %d)%s\n", colorYellow, code, colorReset)
	w("* The server is reachable but the model endpoint was not found.\n")
	w("* Base URL: %s\n", t.baseURL)
	return false
}

// verdictUnprocessable reports a 422, which on a proxy means the route exists
// but wants a structured POST rather than the bare GET probe.
func (t *pingTarget) verdictUnprocessable(w pingWriter, resp *http.Response, customAzure bool) bool {
	if customAzure {
		w("* %s⚠ Received HTTP 422 on health check, continuing with model ping (proxy endpoint expects structured POST requests)%s\n", colorYellow, colorReset)
		return true
	}
	w("* %s? Unexpected status: %s%s\n", colorYellow, resp.Status, colorReset)
	return false
}

// pingModel sends the prompt to the model and reports the reply. With no
// positional args this is the default Prompt:Prompt connectivity test.
func (t *pingTarget) pingModel(ctx context.Context, w pingWriter, args []string) error {
	prompt := strings.Join(args, " ")
	isPingPong := prompt == ""
	if isPingPong {
		prompt = "prompt-prompt"
	}

	w("* %s─── Model Ping ───%s\n", colorBlue, colorReset)
	w("* %sPrompt:%s    %q\n", colorBlue, colorReset, prompt)
	if isPingPong {
		w("* %sMode:%s      Prompt:Prompt connectivity test\n", colorBlue, colorReset)
	} else {
		w("* %sMode:%s      custom prompt (full trace)\n", colorBlue, colorReset)
	}
	w("* Sending to %s ...\n", t.info.Model)

	reply, err := t.sendPrompt(ctx, w, prompt, isPingPong)
	if err != nil {
		return err
	}
	t.reportReply(w, reply, isPingPong)
	return nil
}

// sendPrompt dispatches the prompt through the client that suits the provider.
func (t *pingTarget) sendPrompt(ctx context.Context, w pingWriter, prompt string, isPingPong bool) (string, error) {
	// Ollama processes requests sequentially — running both a raw test and an
	// SDK test double-queues and makes the second request time out. Use the
	// native provider directly, with no artificial timeout.
	if t.info.Ollama {
		w("* %s─── Ollama Ping ───%s\n", colorBlue, colorReset)
		reply, err := ollamaPingFull(ctx, t.baseURL, t.info.Model, prompt, isPingPong, w)
		if err != nil {
			w("* %s✗ Ollama ping FAILED: %v%s\n", colorYellow, err, colorReset)
			return "", fmt.Errorf("model ping failed: %w", err)
		}
		return reply, nil
	}

	// For codex OAuth tokens, hand an empty baseURL to the provider so its
	// own auto-routing (chatgpt.com/codex/responses + required headers)
	// applies. Passing the rewritten ping baseURL would defeat that because
	// NewOpenAI treats any non-empty baseURL as caller-controlled.
	llmBaseURL := t.baseURL
	if t.codexBackend {
		llmBaseURL = ""
	}
	llm, err := provider.NewLLM(ctx, t.info, t.apiKey, llmBaseURL, "none", t.opts)
	if err != nil {
		w("* %s✗ Failed to create LLM client: %v%s\n", colorYellow, err, colorReset)
		return "", fmt.Errorf("creating LLM for ping: %w", err)
	}

	// Cloud providers get a 60s timeout.
	pingCtx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()

	reply, err := modelPing(pingCtx, llm, prompt, isPingPong, t.info.Provider)
	if err != nil {
		w("* %s✗ Model ping FAILED: %v%s\n", colorYellow, err, colorReset)
		return "", fmt.Errorf("model ping failed: %w", err)
	}
	return reply, nil
}

// reportReply prints the model's answer and whether the round trip counts as a
// successful liveness check. A Prompt:Prompt run that comes back without the
// expected word still proves the model is alive, so it only warns.
func (t *pingTarget) reportReply(w pingWriter, reply string, isPingPong bool) {
	w("* Model replied: %s%q%s\n", colorGray, reply, colorReset)
	switch {
	case !isPingPong:
		w("* %s✓ Model %s is ALIVE%s\n", colorGreen, t.info.Model, colorReset)
	case strings.Contains(strings.ToLower(reply), "prompt"):
		w("* %s✓ Prompt:Prompt OK — model %s is ALIVE%s\n", colorGreen, t.info.Model, colorReset)
	default:
		w("* %s⚠ Model responded but did not say \"Prompt\" (got %q)%s\n", colorYellow, reply, colorReset)
		w("* %s✓ Model %s is ALIVE (response unexpected)%s\n", colorGreen, t.info.Model, colorReset)
	}
}

func resolveOpenAIModelFromList(resp *http.Response, requested string, w func(string, ...any)) (string, bool) {
	if resp == nil || resp.Body == nil || requested == "" {
		return "", false
	}
	var payload struct {
		Data []struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		w("* %s⚠ Could not parse model list: %v%s\n", colorYellow, err, colorReset)
		return "", false
	}
	for _, item := range payload.Data {
		if item.ID == requested {
			return requested, false
		}
	}
	var matches []string
	for _, item := range payload.Data {
		if strings.HasPrefix(item.ID, requested) {
			matches = append(matches, item.ID)
		}
	}
	switch len(matches) {
	case 1:
		w("* %sModel alias:%s %s → %s\n", colorBlue, colorReset, requested, matches[0])
		return matches[0], true
	case 0:
		return "", false
	default:
		w("* %s⚠ Model %q matched multiple available models: %s%s\n", colorYellow, requested, strings.Join(matches, ", "), colorReset)
		return "", false
	}
}

// allowSoftNonStreamFailure reports whether a failed stream=false probe should
// continue into the stream=true probe. Azure/Autox Responses deployments often
// 404 non-streaming while streaming (what interactive chat uses) succeeds.
func allowSoftNonStreamFailure(provider string) bool {
	return provider == "azure"
}

// modelPing sends a prompt to the model and traces the full response.
// Used for cloud providers (Anthropic, OpenAI, Gemini, Azure).
// Tests both non-streaming and streaming modes with detailed event tracing.
// For Azure, a non-stream failure is soft: ping continues with stream=true,
// matching the path interactive chat actually exercises.
func modelPing(ctx context.Context, llm llmmodel.LLM, prompt string, isPingPong bool, provider string) (string, error) {
	w := pingWriter(func(format string, a ...any) { fmt.Fprintf(os.Stderr, format, a...) })

	req := newPingRequest(prompt, isPingPong)

	// --- Non-streaming test ---
	w("*   %s[non-stream]%s Calling GenerateContent(stream=false)...\n", colorGray, colorReset)
	nsStart := time.Now()
	nsText, nsEvents, nsErr := modelPingNonStream(ctx, llm, req, w)
	if nsErr != nil {
		if !allowSoftNonStreamFailure(provider) {
			return "", nsErr
		}
		w("*   %s⚠ Non-stream failed; Azure gateways often only serve stream=true for Responses — continuing with stream test%s\n",
			colorYellow, colorReset)
	} else {
		nsDur := time.Since(nsStart)
		w("*   %s[non-stream]%s Done: %d events, %s%s%s\n", colorGray, colorReset, nsEvents, colorGray, nsDur.Round(time.Millisecond), colorReset)
	}

	// --- Streaming test ---
	w("*   %s[stream]%s Calling GenerateContent(stream=true)...\n", colorGray, colorReset)
	sText, sErr := modelPingStream(ctx, llm, req, w)
	if sErr != nil {
		return nsText, sErr
	}

	// Return non-streaming result (or streaming if non-streaming was empty).
	reply := strings.TrimSpace(nsText)
	if reply == "" {
		reply = strings.TrimSpace(sText)
	}
	if reply == "" {
		return "", fmt.Errorf("model returned empty response in both streaming and non-streaming modes")
	}
	return reply, nil
}

// pingSystemMessage is the system instruction a connectivity probe sends. The
// Prompt:Prompt variant pins the expected reply so the check can be exact.
func pingSystemMessage(isPingPong bool) string {
	if isPingPong {
		return `You are a connectivity test. When the user says "prompt-prompt", reply with exactly "prompt-prompt" and nothing else.`
	}
	return "You are a connectivity test. Reply briefly and concisely."
}

// newPingRequest builds the single-turn request both the cloud and the Ollama
// ping paths send.
func newPingRequest(prompt string, isPingPong bool) *llmmodel.LLMRequest {
	return &llmmodel.LLMRequest{
		Contents: []*genai.Content{
			genai.NewContentFromText(prompt, genai.RoleUser),
		},
		Config: &genai.GenerateContentConfig{
			SystemInstruction: genai.NewContentFromText(pingSystemMessage(isPingPong), genai.RoleUser),
		},
	}
}

// modelPingNonStream runs the stream=false probe, tracing every event, and
// returns the text collected so far plus the number of events seen. The text is
// meaningful even when err is non-nil: a soft failure keeps what arrived before
// the error.
func modelPingNonStream(ctx context.Context, llm llmmodel.LLM, req *llmmodel.LLMRequest, w pingWriter) (string, int, error) {
	var result strings.Builder
	events := 0
	for resp, err := range llm.GenerateContent(ctx, req, false) {
		events++
		if err != nil {
			w("*   %s[non-stream]%s ERROR at event %d: %v\n", colorGray, colorReset, events, err)
			return result.String(), events, fmt.Errorf("non-streaming LLM error: %w", err)
		}
		logPingNonStreamEvent(w, resp, events)
		logPingNonStreamContent(w, resp, &result)
	}
	return result.String(), events, nil
}

// logPingNonStreamEvent prints the one-line header for a non-streamed event.
func logPingNonStreamEvent(w pingWriter, resp *llmmodel.LLMResponse, event int) {
	w("*   %s[non-stream]%s event %d: partial=%v turnComplete=%v finish=%v",
		colorGray, colorReset, event, resp.Partial, resp.TurnComplete, resp.FinishReason)
	if resp.ErrorCode != "" {
		w(" errorCode=%s errorMsg=%s", resp.ErrorCode, resp.ErrorMessage)
	}
	if resp.UsageMetadata != nil {
		w(" tokens(in=%d out=%d)", resp.UsageMetadata.PromptTokenCount, resp.UsageMetadata.CandidatesTokenCount)
	}
	w("\n")
}

// logPingNonStreamContent prints each part of a non-streamed event and appends
// its text to out.
func logPingNonStreamContent(w pingWriter, resp *llmmodel.LLMResponse, out *strings.Builder) {
	if resp.Content == nil {
		return
	}
	w("*   %s[non-stream]%s   role=%s parts=%d\n", colorGray, colorReset, resp.Content.Role, len(resp.Content.Parts))
	for i, part := range resp.Content.Parts {
		if part.Text != "" {
			w("*   %s[non-stream]%s   part[%d] text(%d chars): %s\n", colorGray, colorReset, i, len(part.Text), truncate(part.Text, 120))
			out.WriteString(part.Text)
		}
		if part.FunctionCall != nil {
			w("*   %s[non-stream]%s   part[%d] tool_call: %s\n", colorGray, colorReset, i, part.FunctionCall.Name)
		}
		if part.Thought {
			w("*   %s[non-stream]%s   part[%d] thought=true\n", colorGray, colorReset, i)
		}
	}
}

// modelPingStream runs the stream=true probe and returns the model text it
// accumulated. Thinking chunks are counted but never join the reply.
func modelPingStream(ctx context.Context, llm llmmodel.LLM, req *llmmodel.LLMRequest, w pingWriter) (string, error) {
	sStart := time.Now()
	var result strings.Builder
	events, thinkingChunks, textChunks := 0, 0, 0
	for resp, err := range llm.GenerateContent(ctx, req, true) {
		events++
		if err != nil {
			w("*   %s[stream]%s ERROR at event %d: %v\n", colorGray, colorReset, events, err)
			return result.String(), fmt.Errorf("streaming LLM error: %w", err)
		}
		if resp.ErrorCode != "" {
			w("*   %s[stream]%s event %d: errorCode=%s errorMsg=%s\n", colorGray, colorReset, events, resp.ErrorCode, resp.ErrorMessage)
			continue
		}
		thinking, text := accumulatePingStreamParts(resp, &result)
		thinkingChunks += thinking
		textChunks += text
		// Print summary for non-partial final event.
		if !resp.Partial {
			logPingStreamFinalEvent(w, resp, events)
		}
	}
	sDur := time.Since(sStart)
	w("*   %s[stream]%s Done: %d events (%d thinking, %d text chunks), %s%s%s\n",
		colorGray, colorReset, events, thinkingChunks, textChunks, colorGray, sDur.Round(time.Millisecond), colorReset)

	reply := result.String()
	if reply != "" {
		w("*   %s[stream]%s Reply: %s\n", colorGray, colorReset, truncate(reply, 200))
	}
	return reply, nil
}

// accumulatePingStreamParts appends the model text of one streamed event to out
// and reports how many thinking and text chunks the event carried.
func accumulatePingStreamParts(resp *llmmodel.LLMResponse, out *strings.Builder) (thinking, text int) {
	if resp.Content == nil {
		return 0, 0
	}
	role := resp.Content.Role
	for _, part := range resp.Content.Parts {
		if part.Text == "" {
			continue
		}
		if role == "thinking" {
			thinking++
			continue
		}
		text++
		out.WriteString(part.Text)
	}
	return thinking, text
}

// logPingStreamFinalEvent prints the summary line for a non-partial event.
func logPingStreamFinalEvent(w pingWriter, resp *llmmodel.LLMResponse, event int) {
	w("*   %s[stream]%s final event %d: turnComplete=%v finish=%v", colorGray, colorReset, event, resp.TurnComplete, resp.FinishReason)
	if resp.UsageMetadata != nil {
		w(" tokens(in=%d out=%d)", resp.UsageMetadata.PromptTokenCount, resp.UsageMetadata.CandidatesTokenCount)
	}
	w("\n")
}

// ollamaPingFull performs a complete Ollama ping: lists models, checks availability,
// then runs non-streaming and streaming tests using the native Ollama provider.
// No artificial timeout — local models can be slow (thinking, model loading).
func ollamaPingFull(ctx context.Context, baseURL, modelName, prompt string, isPingPong bool, w func(string, ...any)) (string, error) {
	// Step 1: List available models and confirm ours is one of them.
	if err := ollamaEnsureModel(ctx, baseURL, modelName, w); err != nil {
		return "", err
	}

	// Step 2: Create native Ollama LLM.
	// baseURL is already settled by the caller, so it decides the endpoint on
	// its own — see rule 1 in ResolveOllamaEndpoint.
	llm, err := provider.NewOllama(ctx, provider.OllamaRouting{
		Model:   modelName,
		BaseURL: baseURL,
	}, "none", nil)
	if err != nil {
		return "", fmt.Errorf("create client: %w", err)
	}

	req := newPingRequest(prompt, isPingPong)

	// Step 3: Non-streaming test.
	nsText, err := ollamaPingNonStream(ctx, llm, req, w)
	if err != nil {
		return "", err
	}

	// Step 4: Streaming test.
	sText, err := ollamaPingStream(ctx, llm, req, w)
	if err != nil {
		// A stream failure after a usable non-streaming reply still proves the
		// model answered, so report that rather than the transport error.
		if nsText != "" {
			w("*   %s[stream]%s Falling back to non-streaming result\n", colorGray, colorReset)
			return nsText, nil
		}
		return "", err
	}

	// Return non-streaming result (preferred) or streaming fallback.
	reply := nsText
	if reply == "" {
		reply = sText
	}
	if reply == "" {
		return "", fmt.Errorf("model returned empty response in both modes")
	}
	return reply, nil
}

// ollamaEnsureModel reports the daemon's model list and fails when modelName is
// not served by it. A tag-less prefix match counts as a hit, because a model
// pulled as "llama3:8b" is routinely asked for as "llama3".
func ollamaEnsureModel(ctx context.Context, baseURL, modelName string, w pingWriter) error {
	models, err := provider.OllamaListModels(ctx, baseURL)
	if err != nil {
		return fmt.Errorf("list models: %w", err)
	}
	w("*   Available models: %s\n", strings.Join(models, ", "))

	modelBase := strings.Split(modelName, ":")[0]
	for _, m := range models {
		if m == modelName || strings.HasPrefix(m, modelBase) {
			w("*   Model %s: %sfound ✓%s\n", modelName, colorGreen, colorReset)
			return nil
		}
	}
	return fmt.Errorf("model %q not found in available models", modelName)
}

// ollamaPingNonStream runs the stream=false Ollama probe and returns the
// trimmed reply.
func ollamaPingNonStream(ctx context.Context, llm llmmodel.LLM, req *llmmodel.LLMRequest, w pingWriter) (string, error) {
	w("*   %s[non-stream]%s Calling Ollama chat (stream=false)...\n", colorGray, colorReset)
	nsStart := time.Now()
	var nsResult strings.Builder
	for resp, err := range llm.GenerateContent(ctx, req, false) {
		if err != nil {
			w("*   %s[non-stream]%s ERROR: %v\n", colorGray, colorReset, err)
			return "", fmt.Errorf("non-streaming: %w", err)
		}
		appendOllamaPingText(resp, &nsResult, false)
		logOllamaPingUsage(w, "non-stream", resp)
	}
	nsDur := time.Since(nsStart)
	nsText := strings.TrimSpace(nsResult.String())
	w("*   %s[non-stream]%s Done %s(%s)%s: %s\n", colorGray, colorReset, colorGray, nsDur.Round(time.Millisecond), colorReset, truncate(nsText, 120))
	return nsText, nil
}

// ollamaPingStream runs the stream=true Ollama probe and returns the trimmed
// reply, excluding thinking chunks.
func ollamaPingStream(ctx context.Context, llm llmmodel.LLM, req *llmmodel.LLMRequest, w pingWriter) (string, error) {
	w("*   %s[stream]%s Calling Ollama chat (stream=true)...\n", colorGray, colorReset)
	sStart := time.Now()
	var sResult strings.Builder
	sChunks := 0
	for resp, err := range llm.GenerateContent(ctx, req, true) {
		if err != nil {
			w("*   %s[stream]%s ERROR: %v\n", colorGray, colorReset, err)
			return "", fmt.Errorf("streaming: %w", err)
		}
		sChunks += appendOllamaPingText(resp, &sResult, true)
		logOllamaPingUsage(w, "stream", resp)
	}
	sDur := time.Since(sStart)
	sText := strings.TrimSpace(sResult.String())
	w("*   %s[stream]%s Done %s(%s)%s, %d chunks: %s\n", colorGray, colorReset, colorGray, sDur.Round(time.Millisecond), colorReset, sChunks, truncate(sText, 120))
	return sText, nil
}

// appendOllamaPingText appends the text parts of one event to out and returns
// how many chunks it contributed.
//
// skipThinking drops thinking-role output. Only the streaming probe sets it:
// the non-streaming call returns one assembled message and has always taken
// every text part it carries, so filtering there would change what a reasoning
// model reports as its reply.
func appendOllamaPingText(resp *llmmodel.LLMResponse, out *strings.Builder, skipThinking bool) int {
	if resp.Content == nil {
		return 0
	}
	chunks := 0
	for _, part := range resp.Content.Parts {
		if part.Text == "" || (skipThinking && resp.Content.Role == "thinking") {
			continue
		}
		out.WriteString(part.Text)
		chunks++
	}
	return chunks
}

// logOllamaPingUsage prints the token counts an event carried, if any.
func logOllamaPingUsage(w pingWriter, label string, resp *llmmodel.LLMResponse) {
	if resp.UsageMetadata == nil {
		return
	}
	w("*   %s[%s]%s tokens(in=%d out=%d)\n",
		colorGray, label, colorReset, resp.UsageMetadata.PromptTokenCount, resp.UsageMetadata.CandidatesTokenCount)
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}

func tlsVersionString(v uint16) string {
	switch v {
	case tls.VersionTLS10:
		return "1.0"
	case tls.VersionTLS11:
		return "1.1"
	case tls.VersionTLS12:
		return "1.2"
	case tls.VersionTLS13:
		return "1.3"
	default:
		return fmt.Sprintf("0x%04x", v)
	}
}
