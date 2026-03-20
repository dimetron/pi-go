package audit

// Severity represents the severity level of a finding.
type Severity int

const (
	SeverityInfo     Severity = iota // unusual but not dangerous
	SeverityWarning                  // suspicious, skill still loads
	SeverityCritical                 // dangerous, skill should be blocked
)

func (s Severity) String() string {
	switch s {
	case SeverityInfo:
		return "info"
	case SeverityWarning:
		return "warning"
	case SeverityCritical:
		return "critical"
	default:
		return "unknown"
	}
}

// charEntry describes a hidden Unicode character class.
type charEntry struct {
	Severity    Severity
	Description string
}

// charTable maps Unicode codepoints to their severity and description.
// Populated in init() for readability.
var charTable map[rune]charEntry

func init() {
	charTable = make(map[rune]charEntry)

	// Critical: Unicode tag characters (U+E0001-E007F) — used for invisible instructions.
	for r := rune(0xE0001); r <= 0xE007F; r++ {
		charTable[r] = charEntry{SeverityCritical, "Unicode tag character"}
	}

	// Critical: BiDi override characters — can reorder visible text.
	bidiOverrides := map[rune]string{
		0x202A: "Left-to-Right Embedding (LRE)",
		0x202B: "Right-to-Left Embedding (RLE)",
		0x202C: "Pop Directional Formatting (PDF)",
		0x202D: "Left-to-Right Override (LRO)",
		0x202E: "Right-to-Left Override (RLO)",
		0x2066: "Left-to-Right Isolate (LRI)",
		0x2067: "Right-to-Left Isolate (RLI)",
		0x2068: "First Strong Isolate (FSI)",
		0x2069: "Pop Directional Isolate (PDI)",
	}
	for r, desc := range bidiOverrides {
		charTable[r] = charEntry{SeverityCritical, "BiDi override: " + desc}
	}

	// Critical: Variation Selectors Supplement (U+E0100-E01EF) — Glassworm vector.
	for r := rune(0xE0100); r <= 0xE01EF; r++ {
		charTable[r] = charEntry{SeverityCritical, "Variation selector supplement (Glassworm vector)"}
	}

	// Warning: Zero-width characters (may hide content).
	zwChars := map[rune]string{
		0x200B: "Zero Width Space (ZWSP)",
		0x200C: "Zero Width Non-Joiner (ZWNJ)",
		0x200D: "Zero Width Joiner (ZWJ)",
		0xFEFF: "Byte Order Mark (BOM)",
	}
	for r, desc := range zwChars {
		charTable[r] = charEntry{SeverityWarning, desc}
	}

	// Warning: BiDi marks.
	bidiMarks := map[rune]string{
		0x200E: "Left-to-Right Mark (LRM)",
		0x200F: "Right-to-Left Mark (RLM)",
	}
	for r, desc := range bidiMarks {
		charTable[r] = charEntry{SeverityWarning, "BiDi mark: " + desc}
	}

	// Warning: Invisible math/format characters.
	invisibleChars := map[rune]string{
		0x2061: "Function Application",
		0x2062: "Invisible Times",
		0x2063: "Invisible Separator",
		0x2064: "Invisible Plus",
	}
	for r, desc := range invisibleChars {
		charTable[r] = charEntry{SeverityWarning, "Invisible operator: " + desc}
	}

	// Info: Unusual whitespace (not standard space/tab/newline).
	unusualWS := map[rune]string{
		0x00A0: "No-Break Space",
		0x2000: "En Quad",
		0x2001: "Em Quad",
		0x2002: "En Space",
		0x2003: "Em Space",
		0x2004: "Three-Per-Em Space",
		0x2005: "Four-Per-Em Space",
		0x2006: "Six-Per-Em Space",
		0x2007: "Figure Space",
		0x2008: "Punctuation Space",
		0x2009: "Thin Space",
		0x200A: "Hair Space",
		0x202F: "Narrow No-Break Space",
		0x205F: "Medium Mathematical Space",
		0x3000: "Ideographic Space",
	}
	for r, desc := range unusualWS {
		charTable[r] = charEntry{SeverityInfo, "Unusual whitespace: " + desc}
	}
}
