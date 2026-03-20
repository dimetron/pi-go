# Nanocoder Theme Format

**Source:** `source/config/themes.json` (787 lines, 39 themes)

## JSON Schema

```json
{
  "theme-slug": {
    "name": "theme-slug",
    "displayName": "Human Readable Name",
    "themeType": "dark" | "light",
    "colors": {
      "text": "#hex",        // primary text color
      "base": "#hex",        // background color
      "primary": "#hex",     // accent / user actions
      "tool": "#hex",        // tool calls and results
      "success": "#hex",     // success indicators
      "error": "#hex",       // error messages
      "secondary": "#hex",   // dim/secondary text
      "info": "#hex",        // informational highlights
      "warning": "#hex",     // warnings
      "diffAdded": "#hex",   // diff background for additions
      "diffRemoved": "#hex", // diff background for removals
      "diffAddedText": "#hex",   // diff text for additions
      "diffRemovedText": "#hex"  // diff text for removals
    }
  }
}
```

## Color Roles (13 total)

| Role | Usage in Nanocoder | pi-go Mapping |
|------|-------------------|---------------|
| `text` | Default text | Message content, general text |
| `base` | Background | Status bar bg, input bg |
| `primary` | User input, selected items, accents | User `>` prefix, selection |
| `tool` | Tool names, bash mode indicator | Tool bullets, tool names |
| `success` | Successful operations | Tool success dots |
| `error` | Error messages, failures | Errors, token limit |
| `secondary` | Dim text, hints, separators | Gray text, hints |
| `info` | Model labels, informational | Assistant bullets, status info |
| `warning` | Update notices, high context | Active tools, warnings |
| `diffAdded` | Diff add background | Edit tool: added lines bg |
| `diffRemoved` | Diff remove background | Edit tool: removed lines bg |
| `diffAddedText` | Diff add foreground | Edit tool: added lines text |
| `diffRemovedText` | Diff remove foreground | Edit tool: removed lines text |

## All 39 Themes

### Dark Themes (32)
tokyo-night, synthwave-84, forest-night, material-ocean, sunset-glow, nord-frost, neon-jungle, midnight-amethyst, desert-mirage, electric-storm, deep-sea, volcanic-ash, cyberpunk-mint, dracula, catppuccin-frappe, catppuccin-macchiato, catppuccin-mocha, gruvbox-dark, solarized-dark, one-dark, monokai, github-dark, rose-pine, rose-pine-moon, ayu-dark, ayu-mirage, night-owl, palenight, horizon, kanagawa, aurora-borealis, cherry-blossom

### Light Themes (7)
rose-pine-dawn, catppuccin-latte, gruvbox-light, solarized-light, one-light, github-light, ayu-light

## Go Integration Plan

The JSON can be embedded directly using `//go:embed themes.json` and deserialized into:

```go
type Theme struct {
    Name        string     `json:"name"`
    DisplayName string     `json:"displayName"`
    ThemeType   string     `json:"themeType"`
    Colors      ThemeColors `json:"colors"`
}

type ThemeColors struct {
    Text            string `json:"text"`
    Base            string `json:"base"`
    Primary         string `json:"primary"`
    Tool            string `json:"tool"`
    Success         string `json:"success"`
    Error           string `json:"error"`
    Secondary       string `json:"secondary"`
    Info            string `json:"info"`
    Warning         string `json:"warning"`
    DiffAdded       string `json:"diffAdded"`
    DiffRemoved     string `json:"diffRemoved"`
    DiffAddedText   string `json:"diffAddedText"`
    DiffRemovedText string `json:"diffRemovedText"`
}
```

lipgloss can consume hex colors directly: `lipgloss.Color("#bb9af7")`
