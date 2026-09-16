import { fileURLToPath } from "node:url";
import { defineConfig } from "vitest/config";

export default defineConfig({
  resolve: {
    alias: [
      {
        // Tests run the extension-host sources in plain node against a stub
        // "vscode" module (there is no VS Code process here): route every
        // `import ... from "vscode"` in src/** to the shared test double. The
        // shipped tsconfigs are untouched — they still resolve the real
        // @types/vscode for typechecking.
        find: /^vscode$/,
        replacement: fileURLToPath(new URL("./test/mocks/vscode.ts", import.meta.url)),
      },
    ],
  },
  test: {
    include: ["test/**/*.test.ts"],
    environment: "node",
    coverage: {
      provider: "v8",
      all: true,
      include: ["src/**/*.ts"],
      // Excluded: src/webview/** is compiled by tsconfig.web.json and runs in
      // the browser context of a webview (DOM, no vscode API) — exercising it
      // needs a DOM harness that a node-side unit suite does not provide.
      exclude: ["src/webview/**"],
      thresholds: {
        lines: 90,
        statements: 90,
        functions: 80,
        branches: 75,
        autoUpdate: false,
      },
    },
  },
});