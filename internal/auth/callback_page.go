package auth

import (
	"html"
	"io"
	"net/http"
	"strings"
)

// callbackPageTemplate renders the OAuth authorization-result page shown in
// the user's browser after the identity provider redirects back to the local
// loopback callback server. Visual language matches https://pi-go.sh/ (dark
// neon theme, grid and scanline overlays, Orbitron/Rajdhani type). All CSS is
// embedded so the page renders offline; the webfont link degrades gracefully.
const callbackPageTemplate = `<!DOCTYPE html>
<html lang="en">
<head>
  <meta charset="utf-8">
  <meta name="viewport" content="width=device-width, initial-scale=1">
  <meta name="color-scheme" content="dark">
  <title>pi-go — {{TITLE}}</title>
  <link rel="icon" href="data:,">
  <link rel="preconnect" href="https://fonts.googleapis.com">
  <link rel="preconnect" href="https://fonts.gstatic.com" crossorigin>
  <link href="https://fonts.googleapis.com/css2?family=Orbitron:wght@700&family=Rajdhani:wght@500;600&display=swap" rel="stylesheet">
  <style>
    :root {
      --bg: #0a0a12;
      --card: rgba(15, 15, 30, 0.72);
      --text: #e0e0f0;
      --muted: #8888aa;
      --cyan: #00f0ff;
      --magenta: #ff00aa;
      --green: #00ff88;
      --border: rgba(0, 240, 255, 0.25);
      --glow: 0 0 20px rgba(0, 240, 255, 0.22), 0 0 60px rgba(0, 240, 255, 0.08);
    }

    * { margin: 0; padding: 0; box-sizing: border-box; }

    html, body { height: 100%; }

    body {
      display: flex;
      align-items: center;
      justify-content: center;
      background: var(--bg);
      color: var(--text);
      font-family: 'Rajdhani', 'Segoe UI', system-ui, sans-serif;
      line-height: 1.6;
      -webkit-font-smoothing: antialiased;
    }

    /* Grid background */
    body::before {
      content: '';
      position: fixed;
      inset: 0;
      background:
        linear-gradient(rgba(0, 240, 255, 0.03) 1px, transparent 1px),
        linear-gradient(90deg, rgba(0, 240, 255, 0.03) 1px, transparent 1px);
      background-size: 60px 60px;
    }

    /* Scanline overlay */
    body::after {
      content: '';
      position: fixed;
      inset: 0;
      background: repeating-linear-gradient(0deg,
        transparent, transparent 2px,
        rgba(0, 0, 0, 0.05) 2px, rgba(0, 0, 0, 0.05) 4px);
      pointer-events: none;
    }

    .card {
      position: relative;
      z-index: 1;
      display: flex;
      flex-direction: column;
      align-items: center;
      gap: 0.9rem;
      max-width: 92vw;
      padding: 3rem 3.5rem;
      text-align: center;
      background: var(--card);
      border: 1px solid var(--border);
      border-radius: 4px;
      backdrop-filter: blur(10px);
      -webkit-backdrop-filter: blur(10px);
      box-shadow: var(--glow);
      animation: rise 0.45s ease-out;
    }

    .card.err {
      border-color: rgba(255, 0, 170, 0.35);
      box-shadow: 0 0 20px rgba(255, 0, 170, 0.25), 0 0 60px rgba(255, 0, 170, 0.08);
    }

    .mark {
      display: flex;
      align-items: center;
      justify-content: center;
      width: 64px;
      height: 64px;
      border-radius: 50%;
      font-family: 'Orbitron', monospace;
      font-size: 1.7rem;
      color: var(--green);
      border: 1px solid var(--green);
      box-shadow: 0 0 20px rgba(0, 255, 136, 0.35), inset 0 0 12px rgba(0, 255, 136, 0.12);
    }

    .err .mark {
      color: var(--magenta);
      border-color: var(--magenta);
      box-shadow: 0 0 20px rgba(255, 0, 170, 0.35), inset 0 0 12px rgba(255, 0, 170, 0.12);
    }

    h1 {
      font-family: 'Orbitron', 'Rajdhani', sans-serif;
      font-size: 1.25rem;
      font-weight: 700;
      letter-spacing: 1px;
    }

    p { color: var(--muted); font-weight: 500; }

    .brand {
      margin-top: 0.8rem;
      font-family: 'Orbitron', monospace;
      font-size: 0.95rem;
      font-weight: 700;
      letter-spacing: 1px;
      color: var(--cyan);
      text-decoration: none;
      text-shadow: 0 0 10px rgba(0, 240, 255, 0.5), 0 0 30px rgba(0, 240, 255, 0.2);
    }

    .brand span { color: var(--magenta); }

    @keyframes rise {
      from { opacity: 0; transform: translateY(10px); }
      to { opacity: 1; transform: none; }
    }
  </style>
</head>
<body>
  <main class="card{{CLASS}}">
    <div class="mark">{{MARK}}</div>
    <h1>{{TITLE}}</h1>
    {{SUB}}<a class="brand" href="https://pi-go.sh/">PI-GO<span>.SH</span></a>
  </main>
  <script>window.close()</script>
</body>
</html>
`

// RenderCallbackPage writes the OAuth authorization-result page in the
// pi-go.sh visual style. ok selects the success (green check) or failure
// (magenta cross) variant. title and detail are HTML-escaped before
// rendering; detail may be empty.
func RenderCallbackPage(w http.ResponseWriter, status int, ok bool, title, detail string) {
	variant, mark := "", "✓"
	if !ok {
		variant, mark = " err", "✗"
	}

	sub := ""
	if detail != "" {
		sub = "<p>" + html.EscapeString(detail) + "</p>\n    "
	}

	page := strings.NewReplacer(
		"{{CLASS}}", variant,
		"{{MARK}}", mark,
		"{{TITLE}}", html.EscapeString(title),
		"{{SUB}}", sub,
	).Replace(callbackPageTemplate)

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(status)
	_, _ = io.WriteString(w, page)
}
