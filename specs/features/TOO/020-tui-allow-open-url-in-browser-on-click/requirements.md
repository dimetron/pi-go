# Requirements

## Questions & Answers

**Q1: Click behavior — open immediately on left-click?**
A: Yes, plain left-click opens the URL (no modifier keys, no menu).

**Q2: What about clicks on non-link text?**
A: Click only opens when the cursor is over a hyperlink; otherwise normal text-selection behavior.

**Q3: Visual feedback on hover?**
A: Yes if simple — underline the hovered URL; click opens.

**Q4: Behavior when `browser.Open()` fails?**
A: Silently ignore (no error message).

**Q5: Interaction with copy-on-release drag selection?**
A: Open only on a click without drag — suppress opening if the click turned into a drag/selection.

## Summary

Add click-to-open-URL support to the TUI chat output:

1. **Hover underline**: when the mouse pointer rests over an http(s) URL in rendered chat output, show that URL underlined.
2. **Click-to-open**: a left mouse click (press + release without significant movement/drag) on a URL opens it in the system browser via `internal/browser.Open()`.
3. **Non-link clicks unchanged**: clicking non-hyperlink text keeps existing selection behavior.
4. **Drag suppression**: if the click starts a drag/selection, do not open the URL.
5. **Failure handling**: `browser.Open()` errors are silently ignored.
6. **Scope**: chat output only (where OSC 8 / URL regex rendering already lives in `internal/tui/chat.go`); no other TUI surfaces in v1.

## Acceptance Criteria

- Given chat output containing `https://example.com`, when the user hovers over it, then the URL renders underlined.
- When the user left-clicks (no drag) on the URL, then it is opened via `browser.Open` and no error is surfaced.
- When the user left-clicks and drags starting on the URL, then it is NOT opened and normal selection occurs.
- When the user clicks on non-URL text, then behavior is identical to today (selection).
- Given an environment with no browser handler, when a URL is clicked, then nothing visible happens (no crash, no error message).
