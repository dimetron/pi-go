// Pi-Go color themes, compiled with Catppuccin for VS Code.
//
// Catppuccin (https://github.com/catppuccin/vscode, MIT) derives ~560
// workbench colors, ~180 TextMate rules and its semantic-token rules from a
// 26-color palette. We keep that machinery and swap in the Pi-Go Design
// System palette (https://claude.ai/design/p/7009407c-e035-4157-bc52-4d4544f87be9):
// deep-space navy surfaces, cyan as the accent, magenta / purple / green /
// yellow / orange neon hues. A handful of workbench colors are then pinned
// to the brand's signatures (cyan cursor, magenta selection, gradient-era
// borders) that the palette mapping alone cannot express.
//
// Regenerate with `bun run themes` (or `node scripts/themes.mjs`). The
// generated JSON is committed; test/themes.test.ts fails when it drifts.

import { compile } from "@catppuccin/vscode";
import { writeFileSync } from "node:fs";
import { dirname, join } from "node:path";
import { fileURLToPath } from "node:url";

/**
 * Pi-Go palettes in Catppuccin's role names. Syntax roles as Catppuccin uses
 * them: mauve = keywords, blue = functions + UI accent, green = strings,
 * yellow = types, peach = numbers/constants, maroon = parameters,
 * lavender = properties, sky = operators + find highlights, overlay2 =
 * comments, red = errors, pink = the brand's magenta.
 */
export const PALETTES = {
  // Dark — the design system as specified (#0a0a12 page, #00f0ff primary).
  neon: {
    rosewater: "#ffc4e8",
    flamingo: "#ff8ad8",
    pink: "#ff00aa",
    mauve: "#b347ff",
    red: "#ff2e6e",
    maroon: "#ff7a9c",
    peach: "#ff6a00",
    yellow: "#ffe600",
    green: "#00ff88",
    teal: "#2dffd2",
    sky: "#5cd7ff",
    sapphire: "#36a3ff",
    blue: "#00f0ff",
    lavender: "#a8b4ff",
    text: "#e0e0f0",
    subtext1: "#c4c4dc",
    subtext0: "#a4a4c4",
    overlay2: "#8888aa",
    overlay1: "#6e6e92",
    overlay0: "#555577",
    surface2: "#3a3a58",
    surface1: "#262640",
    surface0: "#17172b",
    base: "#0a0a12",
    mantle: "#07070d",
    crust: "#040408",
  },
  // Light — same hues pushed to deeper inks so they clear WCAG AA on a
  // near-white page; the chat webview uses the same inks (media/chat.css).
  daylight: {
    rosewater: "#b3476f",
    flamingo: "#c0507a",
    pink: "#c2007f",
    mauve: "#7b2fd0",
    red: "#d1004f",
    maroon: "#b8385f",
    peach: "#c24e00",
    yellow: "#8f7200",
    green: "#00805a",
    teal: "#00806f",
    sky: "#0072b0",
    sapphire: "#005fbd",
    blue: "#00758f",
    lavender: "#4f5bd5",
    text: "#1b1e3a",
    subtext1: "#383c5e",
    subtext0: "#4c5174",
    overlay2: "#5f6488",
    overlay1: "#7b80a2",
    overlay0: "#969bba",
    surface2: "#b6bbd5",
    surface1: "#cdd1e5",
    surface0: "#dfe2f0",
    base: "#f7f8fc",
    mantle: "#eef0f7",
    crust: "#e4e7f2",
  },
};

/** Brand signatures layered over Catppuccin's derived UI colors. */
const SIGNATURES = {
  neon: {
    "editorCursor.foreground": "#00f0ff",
    "terminalCursor.foreground": "#00f0ff",
    "selection.background": "#ff00aa55",
    "editor.selectionBackground": "#ff00aa40",
    "editor.inactiveSelectionBackground": "#ff00aa20",
    "terminal.selectionBackground": "#ff00aa40",
    "focusBorder": "#00f0ff80",
    "tab.activeBorderTop": "#00f0ff",
    "tab.activeBorder": "#00000000",
    "panelTitle.activeBorder": "#00f0ff",
    "activityBar.activeBorder": "#00f0ff",
    "button.background": "#00f0ff",
    "button.hoverBackground": "#7ff8ff",
    "button.foreground": "#0a0a12",
    "badge.background": "#ff00aa",
    "badge.foreground": "#0a0a12",
    "activityBarBadge.background": "#ff00aa",
    "activityBarBadge.foreground": "#0a0a12",
    "statusBarItem.remoteBackground": "#00f0ff",
    "statusBarItem.remoteForeground": "#0a0a12",
    "progressBar.background": "#00f0ff",
    "editorLineNumber.activeForeground": "#00f0ff",
    "list.highlightForeground": "#00f0ff",
    "textLink.foreground": "#00f0ff",
    "textLink.activeForeground": "#ff00aa",
    "errorForeground": "#ff2e6e",
    "chat.requestBorder": "#00f0ff26",
    "chat.requestBackground": "#00f0ff0d",
  },
  daylight: {
    "editorCursor.foreground": "#00758f",
    "terminalCursor.foreground": "#00758f",
    "selection.background": "#c2007f33",
    "editor.selectionBackground": "#c2007f26",
    "editor.inactiveSelectionBackground": "#c2007f14",
    "terminal.selectionBackground": "#c2007f26",
    "tab.activeBorderTop": "#00758f",
    "tab.activeBorder": "#00000000",
    "panelTitle.activeBorder": "#00758f",
    "activityBar.activeBorder": "#00758f",
    "button.foreground": "#ffffff",
    "badge.background": "#c2007f",
    "badge.foreground": "#ffffff",
    "activityBarBadge.background": "#c2007f",
    "activityBarBadge.foreground": "#ffffff",
    "editorLineNumber.activeForeground": "#00758f",
    "textLink.activeForeground": "#c2007f",
    "chat.requestBorder": "#00758f33",
    "chat.requestBackground": "#00758f0d",
  },
};

/** Bright ANSI variants; the normal ones come straight from the palette. */
const ANSI_BRIGHT = {
  neon: { red: "#ff5577", green: "#5cffb0", yellow: "#fff27a", blue: "#7fa2ff", magenta: "#ff5cc8", cyan: "#7ff8ff", white: "#ffffff" },
  daylight: { red: "#e8336e", green: "#00a070", yellow: "#a88600", blue: "#2d7fe0", magenta: "#d63399", cyan: "#0099bb", white: "#383c5e" },
};

/**
 * Terminal colors. Catppuccin reads these from its own ANSI table, which
 * colorOverrides does not reach, so they are derived from the Pi-Go palette
 * here or the terminal keeps Catppuccin's pastels.
 */
function ansiColors(id) {
  const p = PALETTES[id];
  const bright = ANSI_BRIGHT[id];
  const dark = id !== "daylight";
  return {
    "terminal.ansiBlack": dark ? p.surface1 : p.subtext1,
    "terminal.ansiRed": p.red,
    "terminal.ansiGreen": p.green,
    "terminal.ansiYellow": p.yellow,
    "terminal.ansiBlue": p.sapphire,
    "terminal.ansiMagenta": p.pink,
    "terminal.ansiCyan": p.blue,
    "terminal.ansiWhite": dark ? p.subtext1 : p.surface2,
    "terminal.ansiBrightBlack": dark ? p.surface2 : p.subtext0,
    "terminal.ansiBrightRed": bright.red,
    "terminal.ansiBrightGreen": bright.green,
    "terminal.ansiBrightYellow": bright.yellow,
    "terminal.ansiBrightBlue": bright.blue,
    "terminal.ansiBrightMagenta": bright.magenta,
    "terminal.ansiBrightCyan": bright.cyan,
    "terminal.ansiBrightWhite": bright.white,
  };
}

/** The themes this extension contributes, in package.json order. */
export const THEMES = [
  { id: "neon", label: "Pi-Go Neon", flavor: "mocha", file: "pi-go-neon-color-theme.json" },
  { id: "daylight", label: "Pi-Go Daylight", flavor: "latte", file: "pi-go-daylight-color-theme.json" },
];

/** Compile one Pi-Go theme to the JSON VS Code loads. */
export function buildTheme({ id, label, flavor }) {
  const theme = compile(flavor, {
    accent: "blue",
    italicComments: true,
    italicKeywords: false,
    boldKeywords: true,
    workbenchMode: "default",
    bracketMode: "rainbow",
    extraBordersEnabled: true,
    colorOverrides: { [flavor]: PALETTES[id] },
    customUIColors: { [flavor]: { ...ansiColors(id), ...SIGNATURES[id] } },
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
