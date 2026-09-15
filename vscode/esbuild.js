const esbuild = require("esbuild");
const production = process.argv.includes("--production");
esbuild.build({
  entryPoints: ["src/extension.ts"],
  bundle: true,
  platform: "node",
  format: "cjs",
  external: ["vscode"],
  sourcemap: !production,
  minify: production,
  outfile: "dist/extension.js"
}).catch(() => process.exit(1));
