# Vendored third-party assets

## ghostty-web

- **Files**: `ghostty-web.js` (ESM bundle), `ghostty-web.LICENSE`
- **Version**: 0.4.0
- **Source**: https://github.com/coder/ghostty-web (MIT)
- **Fetched from**: `https://unpkg.com/ghostty-web@0.4.0/dist/ghostty-web.js`

Ghostty's VT parser compiled to WebAssembly, with an xterm.js-compatible API.
It replaced xterm.js 5.3.0 and its fit/web-links/unicode11 addons, which were
previously loaded from a CDN at page load.

### Why the bundle is 680KB and there is no .wasm file

The published ESM bundle **inlines `ghostty-vt.wasm` as a base64 data URL**
(~564KB of the 682KB total). `init()` takes no arguments and resolves the
module in this order:

1. the inlined `data:application/wasm;base64,…` URL
2. `./ghostty-vt.wasm` — relative to the *page*, not this module
3. `/ghostty-vt.wasm`

Candidate 1 always succeeds, so no separate `.wasm` file is shipped and no
route serves one. Note that candidates 2 and 3 resolve against the page URL:
if a future upgrade ships the wasm as a separate file, it must be served from
the site root, not from `/static/`.

Inlining costs roughly 160KB over a raw `.wasm` (base64 is ~33% larger) and
buys a single self-contained file with no MIME configuration and no extra
request. `pi serve` works offline as a result.

### Refreshing

```bash
curl -sSL https://unpkg.com/ghostty-web@<version>/dist/ghostty-web.js \
  -o internal/webserver/static/vendor/ghostty-web.js
curl -sSL https://raw.githubusercontent.com/coder/ghostty-web/main/LICENSE \
  -o internal/webserver/static/vendor/ghostty-web.LICENSE
```

Then check the export list still contains `init`, `Terminal`, `FitAddon` and
`UrlRegexProvider`, and re-read the wasm-resolution note above — if upstream
stops inlining, this file's assumptions change.
