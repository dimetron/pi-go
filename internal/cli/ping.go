package cli

import (
	"context"
	"crypto/tls"
	"fmt"
	"net"
	"net/http"
	"net/http/httptrace"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/dimetron/pi-go/internal/config"
	"github.com/dimetron/pi-go/internal/provider"
	"github.com/spf13/cobra"
	llmmodel "google.golang.org/adk/model"
	"google.golang.org/genai"
)

func newPingCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "ping",
		Short: "Check connectivity to the LLM provider (verbose trace)",
		Long:  "Performs a verbose connectivity check to the configured LLM provider, similar to curl -vvv. Shows DNS resolution, TCP connection, TLS handshake, HTTP request/response, and a minimal API call to verify the model is alive.",
		RunE:  runPing,
	}
	cmd.Flags().StringVar(&flagModel, "model", "", "LLM model to use")
	cmd.Flags().StringVar(&flagURL, "url", "", "Alternative base URL for the LLM API endpoint")
	cmd.Flags().BoolVar(&flagSmol, "smol", false, "Use the smol role")
	cmd.Flags().BoolVar(&flagSlow, "slow", false, "Use the slow role")
	cmd.Flags().BoolVar(&flagPlan, "plan", false, "Use the plan role")
	return cmd
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
	default:
		return "/"
	}
}

func runPing(cmd *cobra.Command, args []string) error {
	loadDotEnv()

	cfg, err := config.Load()
	if err != nil {
		return fmt.Errorf("loading config: %w", err)
	}

	if flagModel != "" {
		cfg.Roles["default"] = config.RoleConfig{Model: flagModel}
	}

	activeRole := "default"
	switch {
	case flagSmol:
		activeRole = "smol"
	case flagSlow:
		activeRole = "slow"
	case flagPlan:
		activeRole = "plan"
	}

	modelName, providerName, err := cfg.ResolveRole(activeRole)
	if err != nil {
		return fmt.Errorf("resolving model role: %w", err)
	}

	info, err := provider.Resolve(modelName)
	if err != nil {
		return fmt.Errorf("resolving model: %w", err)
	}
	if providerName != "" {
		info.Provider = providerName
	}

	keys := config.APIKeys()
	apiKey := keys[info.Provider]

	baseURL := flagURL
	if baseURL == "" {
		baseURLs := config.BaseURLs()
		baseURL = baseURLs[info.Provider]
	}
	if baseURL == "" && info.Ollama {
		baseURL = "http://localhost:11434"
	}
	if baseURL == "" {
		baseURL = defaultAPIBaseURL(info.Provider)
	}

	out := os.Stderr
	w := func(format string, a ...any) { fmt.Fprintf(out, format, a...) }

	w("* pi-go ping\n")
	w("* Provider:  %s\n", info.Provider)
	w("* Model:     %s\n", info.Model)
	w("* Ollama:    %v\n", info.Ollama)
	if apiKey != "" {
		masked := apiKey
		if len(masked) > 8 {
			masked = masked[:4] + "..." + masked[len(masked)-4:]
		}
		w("* API Key:   %s\n", masked)
	} else {
		w("* API Key:   (not set)\n")
	}
	w("* Base URL:  %s\n", baseURL)
	w("*\n")

	// Parse the target URL.
	endpoint := pingEndpoint(info.Provider)
	targetURL := strings.TrimRight(baseURL, "/") + endpoint
	u, err := url.Parse(targetURL)
	if err != nil {
		w("* ERROR: invalid URL %q: %v\n", targetURL, err)
		return fmt.Errorf("invalid URL: %w", err)
	}

	host := u.Hostname()
	port := u.Port()
	if port == "" {
		if u.Scheme == "https" {
			port = "443"
		} else {
			port = "80"
		}
	}

	// Phase 1: DNS resolution.
	w("* ─── DNS Resolution ───\n")
	dnsStart := time.Now()
	addrs, dnsErr := net.LookupHost(host)
	dnsDur := time.Since(dnsStart)
	if dnsErr != nil {
		w("* DNS FAILED: %v  (%s)\n", dnsErr, dnsDur.Round(time.Millisecond))
		w("*\n* RESULT: connection issue — DNS resolution failed\n")
		return fmt.Errorf("DNS resolution failed: %w", dnsErr)
	}
	w("*   Resolved %s → %s  (%s)\n", host, strings.Join(addrs, ", "), dnsDur.Round(time.Millisecond))

	// Phase 2: TCP connection.
	w("* ─── TCP Connection ───\n")
	tcpAddr := net.JoinHostPort(addrs[0], port)
	tcpStart := time.Now()
	conn, tcpErr := net.DialTimeout("tcp", tcpAddr, 10*time.Second)
	tcpDur := time.Since(tcpStart)
	if tcpErr != nil {
		w("* TCP FAILED: %v  (%s)\n", tcpErr, tcpDur.Round(time.Millisecond))
		w("*\n* RESULT: connection issue — TCP connect failed to %s\n", tcpAddr)
		return fmt.Errorf("TCP connect failed: %w", tcpErr)
	}
	conn.Close()
	w("*   Connected to %s  (%s)\n", tcpAddr, tcpDur.Round(time.Millisecond))

	// Phase 3: TLS handshake (if https).
	if u.Scheme == "https" {
		w("* ─── TLS Handshake ───\n")
		tlsStart := time.Now()
		tlsConn, tlsErr := tls.DialWithDialer(
			&net.Dialer{Timeout: 10 * time.Second},
			"tcp", net.JoinHostPort(host, port),
			&tls.Config{ServerName: host},
		)
		tlsDur := time.Since(tlsStart)
		if tlsErr != nil {
			w("* TLS FAILED: %v  (%s)\n", tlsErr, tlsDur.Round(time.Millisecond))
			w("*\n* RESULT: connection issue — TLS handshake failed\n")
			return fmt.Errorf("TLS handshake failed: %w", tlsErr)
		}
		state := tlsConn.ConnectionState()
		tlsConn.Close()
		w("*   TLS %s, cipher %s  (%s)\n",
			tlsVersionString(state.Version),
			tls.CipherSuiteName(state.CipherSuite),
			tlsDur.Round(time.Millisecond))
		if len(state.PeerCertificates) > 0 {
			cert := state.PeerCertificates[0]
			w("*   Server cert: CN=%s, issuer=%s\n", cert.Subject.CommonName, cert.Issuer.CommonName)
			w("*   Valid: %s → %s\n", cert.NotBefore.Format("2006-01-02"), cert.NotAfter.Format("2006-01-02"))
		}
	}

	// Phase 4: HTTP request with trace.
	w("* ─── HTTP Request ───\n")

	ctx, cancel := context.WithTimeout(cmd.Context(), 30*time.Second)
	defer cancel()

	var (
		traceConnStart time.Time
		traceTLSStart  time.Time
		traceGotConn   time.Time
	)
	trace := &httptrace.ClientTrace{
		ConnectStart: func(_, _ string) { traceConnStart = time.Now() },
		ConnectDone: func(_, _ string, err error) {
			if err == nil {
				w("*   [trace] TCP connected (%s)\n", time.Since(traceConnStart).Round(time.Millisecond))
			}
		},
		TLSHandshakeStart: func() { traceTLSStart = time.Now() },
		TLSHandshakeDone: func(_ tls.ConnectionState, _ error) {
			w("*   [trace] TLS done (%s)\n", time.Since(traceTLSStart).Round(time.Millisecond))
		},
		GotConn: func(_ httptrace.GotConnInfo) { traceGotConn = time.Now() },
		GotFirstResponseByte: func() {
			if !traceGotConn.IsZero() {
				w("*   [trace] TTFB (%s)\n", time.Since(traceGotConn).Round(time.Millisecond))
			}
		},
	}
	ctx = httptrace.WithClientTrace(ctx, trace)

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, targetURL, nil)
	if err != nil {
		return fmt.Errorf("creating request: %w", err)
	}

	// Set auth headers.
	switch info.Provider {
	case "anthropic":
		if apiKey != "" {
			req.Header.Set("x-api-key", apiKey)
			req.Header.Set("anthropic-version", "2023-06-01")
		}
	case "openai":
		if apiKey != "" {
			req.Header.Set("Authorization", "Bearer "+apiKey)
		}
	case "gemini":
		if apiKey != "" {
			q := req.URL.Query()
			q.Set("key", apiKey)
			req.URL.RawQuery = q.Encode()
		}
	}
	req.Header.Set("User-Agent", "pi-go/"+Version)

	w("> %s %s HTTP/1.1\n", req.Method, req.URL.RequestURI())
	w("> Host: %s\n", req.URL.Host)
	for k, vs := range req.Header {
		for _, v := range vs {
			if strings.EqualFold(k, "Authorization") || strings.EqualFold(k, "X-Api-Key") {
				v = v[:min(10, len(v))] + "..."
			}
			w("> %s: %s\n", k, v)
		}
	}
	w(">\n")

	httpStart := time.Now()
	resp, httpErr := http.DefaultClient.Do(req)
	httpDur := time.Since(httpStart)

	if httpErr != nil {
		w("* HTTP FAILED: %v  (%s)\n", httpErr, httpDur.Round(time.Millisecond))
		w("*\n* RESULT: connection issue — HTTP request failed\n")
		return fmt.Errorf("HTTP request failed: %w", httpErr)
	}
	defer resp.Body.Close()

	w("< HTTP/%d.%d %s\n", resp.ProtoMajor, resp.ProtoMinor, resp.Status)
	for k, vs := range resp.Header {
		for _, v := range vs {
			w("< %s: %s\n", k, v)
		}
	}
	w("<\n")
	w("* Total HTTP time: %s\n", httpDur.Round(time.Millisecond))
	w("*\n")

	// Phase 5: HTTP Verdict.
	w("* ─── HTTP Result ───\n")
	httpAlive := false
	switch {
	case resp.StatusCode >= 200 && resp.StatusCode < 300:
		w("* ✓ Endpoint reachable via %s\n", info.Provider)
		w("* Status: %s\n", resp.Status)
		httpAlive = true
	case resp.StatusCode == 401 || resp.StatusCode == 403:
		w("* ✗ Authentication failed (HTTP %d)\n", resp.StatusCode)
		w("* The API endpoint is reachable but the API key is invalid or missing.\n")
		w("* Check %s\n", providerEnvVar(info.Provider))
	case resp.StatusCode == 404:
		w("* ✗ Endpoint not found (HTTP %d)\n", resp.StatusCode)
		w("* The server is reachable but the model endpoint was not found.\n")
		w("* Base URL: %s\n", baseURL)
	case resp.StatusCode == 405:
		// Method Not Allowed is fine for POST-only endpoints — server is alive.
		w("* ✓ Endpoint reachable via %s (endpoint requires POST)\n", info.Provider)
		w("* Status: %s\n", resp.Status)
		httpAlive = true
	case resp.StatusCode == 429:
		w("* ⚠ Rate limited (HTTP %d) — endpoint reachable but throttled\n", resp.StatusCode)
		httpAlive = true
	case resp.StatusCode >= 500:
		w("* ✗ Server error (HTTP %d) — provider may be experiencing issues\n", resp.StatusCode)
	default:
		w("* ? Unexpected status: %s\n", resp.Status)
	}
	w("*\n")

	// Phase 6: Model ping — send "ping" to the model and expect "pong".
	if !httpAlive {
		w("* ─── Model Ping ───\n")
		w("* Skipped — endpoint not reachable\n")
		return nil
	}

	w("* ─── Model Ping ───\n")
	w("* Sending test message to %s ...\n", info.Model)

	// If Ollama, first do a raw API test to bypass SDK.
	if info.Ollama {
		w("* ─── Raw Ollama API Test ───\n")
		rawReply, rawErr := ollamaRawPing(cmd.Context(), baseURL, info.Model)
		if rawErr != nil {
			w("* ✗ Raw Ollama API test FAILED: %v\n", rawErr)
		} else {
			w("* ✓ Raw Ollama API: %s\n", rawReply)
		}
		w("*\n")
	}

	llm, llmErr := provider.NewLLM(cmd.Context(), info, apiKey, baseURL, "none")
	if llmErr != nil {
		w("* ✗ Failed to create LLM client: %v\n", llmErr)
		return fmt.Errorf("creating LLM for ping: %w", llmErr)
	}

	pingCtx, pingCancel := context.WithTimeout(cmd.Context(), 60*time.Second)
	defer pingCancel()

	reply, pingErr := modelPing(pingCtx, llm)
	if pingErr != nil {
		w("* ✗ Model ping FAILED: %v\n", pingErr)
		return fmt.Errorf("model ping failed: %w", pingErr)
	}

	w("* Model replied: %q\n", reply)
	w("* ✓ Model %s is ALIVE\n", info.Model)

	return nil
}

// modelPing sends "ping" to the model with instructions to reply "pong".
// It tests both non-streaming and streaming modes, printing detailed event info.
func modelPing(ctx context.Context, llm llmmodel.LLM) (string, error) {
	w := func(format string, a ...any) { fmt.Fprintf(os.Stderr, format, a...) }

	req := &llmmodel.LLMRequest{
		Contents: []*genai.Content{
			genai.NewContentFromText("Say hello", genai.RoleUser),
		},
		Config: &genai.GenerateContentConfig{
			SystemInstruction: genai.NewContentFromText(
				"You are a connectivity test. When the user says \"ping\", reply with exactly \"pong\" and nothing else. For any other message, reply briefly.",
				genai.RoleUser,
			),
		},
	}

	// --- Non-streaming test ---
	w("*   [non-stream] Calling GenerateContent(stream=false)...\n")
	nsStart := time.Now()
	var nsResult strings.Builder
	nsEvents := 0
	for resp, err := range llm.GenerateContent(ctx, req, false) {
		nsEvents++
		if err != nil {
			w("*   [non-stream] ERROR at event %d: %v\n", nsEvents, err)
			return "", fmt.Errorf("non-streaming LLM error: %w", err)
		}
		w("*   [non-stream] event %d: partial=%v turnComplete=%v finish=%v",
			nsEvents, resp.Partial, resp.TurnComplete, resp.FinishReason)
		if resp.ErrorCode != "" {
			w(" errorCode=%s errorMsg=%s", resp.ErrorCode, resp.ErrorMessage)
		}
		if resp.UsageMetadata != nil {
			w(" tokens(in=%d out=%d)", resp.UsageMetadata.PromptTokenCount, resp.UsageMetadata.CandidatesTokenCount)
		}
		w("\n")
		if resp.Content != nil {
			w("*   [non-stream]   role=%s parts=%d\n", resp.Content.Role, len(resp.Content.Parts))
			for i, part := range resp.Content.Parts {
				if part.Text != "" {
					preview := part.Text
					if len(preview) > 120 {
						preview = preview[:120] + "..."
					}
					w("*   [non-stream]   part[%d] text(%d chars): %s\n", i, len(part.Text), preview)
					nsResult.WriteString(part.Text)
				}
				if part.FunctionCall != nil {
					w("*   [non-stream]   part[%d] tool_call: %s\n", i, part.FunctionCall.Name)
				}
				if part.Thought {
					w("*   [non-stream]   part[%d] thought=true\n", i)
				}
			}
		}
	}
	nsDur := time.Since(nsStart)
	w("*   [non-stream] Done: %d events, %s\n", nsEvents, nsDur.Round(time.Millisecond))

	// --- Streaming test ---
	w("*   [stream] Calling GenerateContent(stream=true)...\n")
	sStart := time.Now()
	var sResult strings.Builder
	sEvents := 0
	sThinkingChunks := 0
	sTextChunks := 0
	for resp, err := range llm.GenerateContent(ctx, req, true) {
		sEvents++
		if err != nil {
			w("*   [stream] ERROR at event %d: %v\n", sEvents, err)
			return nsResult.String(), fmt.Errorf("streaming LLM error: %w", err)
		}
		if resp.ErrorCode != "" {
			w("*   [stream] event %d: errorCode=%s errorMsg=%s\n", sEvents, resp.ErrorCode, resp.ErrorMessage)
			continue
		}
		if resp.Content != nil {
			role := resp.Content.Role
			for _, part := range resp.Content.Parts {
				if part.Text != "" {
					if role == "thinking" {
						sThinkingChunks++
					} else {
						sTextChunks++
						sResult.WriteString(part.Text)
					}
				}
			}
		}
		// Print summary for non-partial final event.
		if !resp.Partial {
			w("*   [stream] final event %d: turnComplete=%v finish=%v", sEvents, resp.TurnComplete, resp.FinishReason)
			if resp.UsageMetadata != nil {
				w(" tokens(in=%d out=%d)", resp.UsageMetadata.PromptTokenCount, resp.UsageMetadata.CandidatesTokenCount)
			}
			w("\n")
		}
	}
	sDur := time.Since(sStart)
	w("*   [stream] Done: %d events (%d thinking, %d text chunks), %s\n",
		sEvents, sThinkingChunks, sTextChunks, sDur.Round(time.Millisecond))

	if sResult.Len() > 0 {
		preview := sResult.String()
		if len(preview) > 200 {
			preview = preview[:200] + "..."
		}
		w("*   [stream] Reply: %s\n", preview)
	}

	// Return non-streaming result (or streaming if non-streaming was empty).
	reply := strings.TrimSpace(nsResult.String())
	if reply == "" {
		reply = strings.TrimSpace(sResult.String())
	}
	if reply == "" {
		return "", fmt.Errorf("model returned empty response in both streaming and non-streaming modes")
	}
	return reply, nil
}

// ollamaRawPing tests the native Ollama API directly using the Ollama Go client.
func ollamaRawPing(ctx context.Context, baseURL, modelName string) (string, error) {
	w := func(format string, a ...any) { fmt.Fprintf(os.Stderr, format, a...) }

	// List available models first.
	models, err := provider.OllamaListModels(ctx, baseURL)
	if err != nil {
		return "", fmt.Errorf("list models: %w", err)
	}
	w("*   [raw] Available models: %s\n", strings.Join(models, ", "))

	// Check if our model is available.
	found := false
	for _, m := range models {
		if m == modelName || strings.HasPrefix(m, strings.Split(modelName, ":")[0]) {
			found = true
			break
		}
	}
	if !found {
		return "", fmt.Errorf("model %q not found in available models", modelName)
	}

	// Do a quick non-streaming chat.
	llm, err := provider.NewOllama(ctx, modelName, baseURL, "none")
	if err != nil {
		return "", fmt.Errorf("create client: %w", err)
	}

	req := &llmmodel.LLMRequest{
		Contents: []*genai.Content{
			genai.NewContentFromText("Say hello in one word", genai.RoleUser),
		},
	}

	var textContent string
	var thinkingChars int
	for resp, err := range llm.GenerateContent(ctx, req, false) {
		if err != nil {
			return "", fmt.Errorf("generate: %w", err)
		}
		if resp.Content != nil {
			for _, part := range resp.Content.Parts {
				if part.Text != "" {
					textContent += part.Text
				}
			}
		}
		if resp.UsageMetadata != nil {
			w("*   [raw] tokens(in=%d out=%d)\n",
				resp.UsageMetadata.PromptTokenCount, resp.UsageMetadata.CandidatesTokenCount)
		}
	}

	_ = thinkingChars
	if textContent == "" {
		return "", fmt.Errorf("model returned empty response")
	}
	return truncate(textContent, 200), nil
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
