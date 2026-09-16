const esbuild = require("esbuild");
const production = process.argv.includes("--production");

// Two bundles from one script:
//  - dist/extension.js  — the node extension host (cjs, external "vscode")
//  - dist/webview.js    — the chat webview (iife, browser, fully bundled)
const extension = esbuild.build({
  entryPoints: ["src/extension.ts"],
  bundle: true,
  platform: "node",
  format: "cjs",
  external: ["vscode"],
  sourcemap: !production,
  minify: production,
  outfile: "dist/extension.js"
});

const webview = esbuild.build({
  entryPoints: ["src/webview/main.ts"],
  bundle: true,
  platform: "browser",
  format: "iife",
  target: ["es2020"],
  sourcemap: !production,
  minify: production,
  define: { "process.env.NODE_ENV": '"production"' },
  outfile: "dist/webview.js"
});

Promise.all([extension, webview]).catch(() => process.exit(1));