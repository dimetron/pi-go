// Pi-Go color themes, compiled with Catppuccin for VS Code.
//
// Catppuccin (https://github.com/catppuccin/vscode, MIT) derives ~560
// workbench colors, ~180 TextMate rules and its semantic-token rules from a
// 26-color palette. Both Pi-Go themes use Catppuccin's own palettes as-is
// (https://catppuccin.com/palette/, read from @catppuccin/palette rather than
// copied): Mocha for the dark theme, Latte for the light one. Catppuccin's
// derived UI, syntax and terminal colors are kept; Pi-Go only picks the accent
// and adds a few brand touches in Catppuccin hues — Sky/Blue where the old
// neon theme used cyan, Pink where it used magenta.
//
// The theme labels stay "Pi-Go Neon" / "Pi-Go Daylight": VS Code stores the
// selected theme by label, so renaming would silently reset it for users.
//
// Regenerate with `bun run themes` (or `node scripts/themes.mjs`). The
// generated JSON is committed; test/themes.test.ts fails when it drifts.

import { flavors } from "@catppuccin/palette";
import { compile } from "@catppuccin/vscode";
import { writeFileSync } from "node:fs";
import { dirname, join } from "node:path";
import { fileURLToPath } from "node:url";

/** A Catppuccin flavor as { role: "#rrggbb" }. */
function flavorHexes(flavor) {
  return Object.fromEntries(Object.entries(flavors[flavor].colors).map(([role, c]) => [role, c.hex]));
}

/** The colors Catppuccin draws text with (syntax, links, diagnostics). */
export const ACCENT_ROLES = [
  "rosewater", "flamingo", "pink", "mauve", "red", "maroon", "peach",
  "yellow", "green", "teal", "sky", "sapphire", "blue", "lavender",
];

/** WCAG AA for normal text; test/themes.test.ts holds syntax to it. */
export const AA = 4.5;

function luminance(hex) {
  const [r, g, b] = [1, 3, 5].map((i) => {
    const c = parseInt(hex.slice(i, i + 2), 16) / 255;
    return c <= 0.03928 ? c / 12.92 : ((c + 0.055) / 1.055) ** 2.4;
  });
  return 0.2126 * r + 0.7152 * g + 0.0722 * b;
}

export function contrast(a, b) {
  const [hi, lo] = [luminance(a), luminance(b)].sort((x, y) => y - x);
  return (hi + 0.05) / (lo + 0.05);
}

/** Darken hex in 1% steps (same hue, same channel ratios) until it reaches
 *  AA on bg. A color that already passes comes back unchanged. */
function inkForAA(hex, bg) {
  const rgb = [1, 3, 5].map((i) => parseInt(hex.slice(i, i + 2), 16));
  for (let k = 100; k > 0; k--) {
    const out = `#${rgb.map((v) => Math.round((v * k) / 100).toString(16).padStart(2, "0")).join("")}`;
    if (contrast(out, bg) >= AA) return out;
  }
  return hex;
}

/**
 * The palette each theme renders with: Catppuccin's, except that an accent
 * color that would fail AA as text on the flavor's base is deepened just
 * enough to pass. Mocha's pastels all pass on its dark base, so the dark
 * theme is Mocha exactly. Latte trades contrast for softness (green strings
 * are 2.96:1), so its failing inks are deepened; surfaces and text are
 * untouched.
 */
function readablePalette(flavor) {
  const hexes = flavorHexes(flavor);
  const out = { ...hexes };
  for (const role of ACCENT_ROLES) out[role] = inkForAA(hexes[role], hexes.base);
  return out;
}

export const PALETTES = {
  neon: readablePalette("mocha"),
  daylight: readablePalette("latte"),
};

/** Only the colors that differ from the published flavor, for colorOverrides. */
function overrides(id, flavor) {
  const published = flavorHexes(flavor);
  return Object.fromEntries(Object.entries(PALETTES[id]).filter(([role, hex]) => published[role] !== hex));
}

/**
 * Brand touches over Catppuccin's derived colors, all in the flavor's own
 * hues. Kept deliberately small: everything else is Catppuccin's choice.
 */
function signatures(id) {
  const p = PALETTES[id];
  const accent = id === "neon" ? p.sky : p.blue;
  const onPink = id === "neon" ? p.crust : p.base;
  // Catppuccin letters Latte buttons in Crust, 3.9:1 on Blue; take the first
  // light ink that reaches AA instead.
  const onAccent = [p.crust, p.base, "#ffffff"].find((ink) => contrast(ink, accent) >= AA) ?? "#ffffff";
  return {
    "button.foreground": onAccent,
    "editorCursor.foreground": accent,
    "terminalCursor.foreground": accent,
    "badge.background": p.pink,
    "badge.foreground": onPink,
    "activityBarBadge.background": p.pink,
    "activityBarBadge.foreground": onPink,
    "textLink.activeForeground": p.pink,
    "chat.requestBorder": `${accent}33`,
    "chat.requestBackground": `${accent}0d`,
  };
}

/** The themes this extension contributes, in package.json order. Latte's Sky
 *  is too light to carry button text (2.6:1 on base), so the light theme
 *  accents with Blue, which Catppuccin designs for that role. */
export const THEMES = [
  { id: "neon", label: "Pi-Go Neon", flavor: "mocha", accent: "sky", file: "pi-go-neon-color-theme.json" },
  { id: "daylight", label: "Pi-Go Daylight", flavor: "latte", accent: "blue", file: "pi-go-daylight-color-theme.json" },
];

/** Compile one Pi-Go theme to the JSON VS Code loads. */
export function buildTheme({ id, label, flavor, accent }) {
  const theme = compile(flavor, {
    accent,
    italicComments: true,
    italicKeywords: false,
    boldKeywords: true,
    workbenchMode: "default",
    bracketMode: "rainbow",
    extraBordersEnabled: true,
    colorOverrides: { [flavor]: overrides(id, flavor) },
    customUIColors: { [flavor]: signatures(id) },
  });
  return {
    $schema: "vscode://schemas/color-theme",
    name: label,
    type: theme.type,
    semanticHighlighting: theme.semanticHighlighting,
    colors: theme.colors,
    semanticTokenColors: theme.semanticTokenColors,
    tokenColors: theme.tokenColors,
  };
}

export function serialize(theme) {
  return `${JSON.stringify(theme, null, 2)}\n`;
}

export const THEMES_DIR = join(dirname(fileURLToPath(import.meta.url)), "..", "themes");

if (process.argv[1] === fileURLToPath(import.meta.url)) {
  for (const spec of THEMES) {
    const path = join(THEMES_DIR, spec.file);
    writeFileSync(path, serialize(buildTheme(spec)));
    console.log(`wrote ${path}`);
  }
}
