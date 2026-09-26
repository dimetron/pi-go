import { existsSync, readFileSync } from "node:fs";
import { join } from "node:path";
import { describe, expect, it } from "vitest";
// @ts-expect-error — plain .mjs build script without type declarations.
import { THEMES, THEMES_DIR, buildTheme, serialize } from "../scripts/themes.mjs";

interface ThemeSpec {
  id: string;
  label: string;
  flavor: string;
  file: string;
}
interface Theme {
  name: string;
  type: string;
  colors: Record<string, string>;
  tokenColors: { scope?: string | string[]; settings: { foreground?: string } }[];
}

const specs = THEMES as ThemeSpec[];
const pkg = JSON.parse(readFileSync(join(__dirname, "..", "package.json"), "utf8"));

function luminance(hex: string): number {
  const [r, g, b] = [1, 3, 5].map((i) => {
    const c = parseInt(hex.slice(i, i + 2), 16) / 255;
    return c <= 0.03928 ? c / 12.92 : ((c + 0.055) / 1.055) ** 2.4;
  });
  return 0.2126 * r + 0.7152 * g + 0.0722 * b;
}

function contrast(a: string, b: string): number {
  const [hi, lo] = [luminance(a), luminance(b)].sort((x, y) => y - x);
  return (hi + 0.05) / (lo + 0.05);
}

function scopeColor(theme: Theme, scope: string): string {
  const rule = theme.tokenColors.find((t) =>
    Array.isArray(t.scope) ? t.scope.includes(scope) : t.scope === scope,
  );
  if (!rule?.settings.foreground) throw new Error(`no rule for ${scope}`);
  return rule.settings.foreground.slice(0, 7);
}

describe("Pi-Go color themes", () => {
  it("contributes every generated theme from package.json", () => {
    const contributed = pkg.contributes.themes.map((t: { label: string; path: string }) => [t.label, t.path]);
    expect(contributed).toEqual(specs.map((s) => [s.label, `./themes/${s.file}`]));
    expect(pkg.categories).toContain("Themes");
  });

  it.each(specs)("$label on disk matches the generator (run `bun run themes`)", (spec) => {
    const path = join(THEMES_DIR, spec.file);
    expect(existsSync(path)).toBe(true);
    expect(readFileSync(path, "utf8")).toBe(serialize(buildTheme(spec)));
  });

  it.each(specs)("$label keeps readable text and syntax", (spec) => {
    const theme = buildTheme(spec) as Theme;
    const c = theme.colors;
    const bg = c["editor.background"];
    expect(theme.name).toBe(spec.label);
    // Body text: WCAG AAA. Syntax: AA. Comments are deliberately muted: 3:1.
    expect(contrast(c["editor.foreground"], bg)).toBeGreaterThanOrEqual(7);
    for (const scope of ["keyword", "entity.name.function", "string", "constant.numeric"]) {
      expect(contrast(scopeColor(theme, scope), bg), scope).toBeGreaterThanOrEqual(4.5);
    }
    expect(contrast(scopeColor(theme, "comment"), bg)).toBeGreaterThanOrEqual(3);
    expect(contrast(c["button.foreground"], c["button.background"])).toBeGreaterThanOrEqual(4.5);
    expect(contrast(c["sideBar.foreground"] ?? c["foreground"], c["sideBar.background"])).toBeGreaterThanOrEqual(7);
  });

  it.each(specs)("$label uses the Pi-Go palette, not Catppuccin's", (spec) => {
    const c = (buildTheme(spec) as Theme).colors;
    const brand = spec.id === "neon" ? { cyan: "#00f0ff", magenta: "#ff00aa", bg: "#0a0a12" } : { cyan: "#00758f", magenta: "#c2007f", bg: "#f7f8fc" };
    expect(c["editor.background"]).toBe(brand.bg);
    expect(c["editorCursor.foreground"]).toBe(brand.cyan);
    expect(c["terminal.ansiCyan"]).toBe(brand.cyan);
    expect(c["terminal.ansiMagenta"]).toBe(brand.magenta);
  });
});
