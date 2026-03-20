package memory

import "regexp"

var privateTagRe = regexp.MustCompile(`(?s)<private>.*?</private>`)

// StripPrivate replaces all <private>...</private> tags with [PRIVATE].
func StripPrivate(s string) string {
	return privateTagRe.ReplaceAllString(s, "[PRIVATE]")
}

// StripPrivateFromMap deep-walks a map and applies StripPrivate to all string values.
func StripPrivateFromMap(m map[string]any) map[string]any {
	if m == nil {
		return nil
	}
	out := make(map[string]any, len(m))
	for k, v := range m {
		out[k] = stripPrivateValue(v)
	}
	return out
}

func stripPrivateValue(v any) any {
	switch val := v.(type) {
	case string:
		return StripPrivate(val)
	case map[string]any:
		return StripPrivateFromMap(val)
	case []any:
		out := make([]any, len(val))
		for i, item := range val {
			out[i] = stripPrivateValue(item)
		}
		return out
	default:
		return v
	}
}
