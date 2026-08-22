package voicegemini

import "encoding/json"

// SanitizeSchema rewrites a JSON Schema into the OpenAPI-3.0 subset that
// google.ai.generativelanguage.v1beta.Schema accepts. That message is parsed by
// a strict proto-JSON parser: any field it does not know closes the live
// session with WebSocket 1007 at setup, before a single microphone byte is
// sent.
//
// Two constructs Go schema inference commonly emits are fatal there:
//
//   - "additionalProperties" — emitted for every struct; the field does not
//     exist in Gemini's Schema at all. Dropped at every depth. Nothing is lost
//     that Gemini could have enforced, and unknown arguments are still rejected
//     server-side when the call is decoded.
//   - an array-valued "type" — emitted for every pointer or slice field
//     ("type": ["null","array"]); Gemini's type is a single enum. Collapsed to
//     its non-null member, with "nullable": true when "null" was present.
//
// It is deliberately permissive and total: it strips only what it knows about
// and passes everything else through, including input that is not a JSON object
// at all. A keyword Gemini would reject but this function does not know must
// fail a guard test, not disable voice at startup.
//
// It operates on marshaled bytes and returns fresh bytes, so it can never
// mutate a schema value shared with pi's other tool paths.
func SanitizeSchema(raw json.RawMessage) (json.RawMessage, error) {
	var v any
	if err := json.Unmarshal(raw, &v); err != nil {
		return nil, err
	}
	node, ok := v.(map[string]any)
	if !ok {
		// null (a tool whose schema inference failed) or any other non-object:
		// nothing to rewrite.
		return raw, nil
	}
	sanitizeSchemaNode(node)
	return json.Marshal(node)
}

// sanitizeSchemaNode applies both rules to one schema object and recurses
// through every container the Gemini Schema field set can nest a schema in.
func sanitizeSchemaNode(node map[string]any) {
	delete(node, "additionalProperties")
	collapseTypeUnion(node)

	if props, ok := node["properties"].(map[string]any); ok {
		sanitizeSchemaMap(props)
	}
	if items, ok := node["items"].(map[string]any); ok {
		sanitizeSchemaNode(items)
	}
	if anyOf, ok := node["anyOf"].([]any); ok {
		sanitizeSchemaList(anyOf)
	}
}

// collapseTypeUnion rewrites an array-valued "type" into the single enum
// Gemini's Schema expects: the first non-null member wins, "null" becomes
// "nullable": true, and a union with no non-null member drops "type" entirely.
// A "type" that is not an array is left exactly as it is.
func collapseTypeUnion(node map[string]any) {
	types, ok := node["type"].([]any)
	if !ok {
		return
	}

	var nullable bool
	var kept any
	for _, t := range types {
		if s, _ := t.(string); s == "null" {
			nullable = true
			continue
		}
		if kept == nil {
			kept = t
		}
	}

	if kept != nil {
		node["type"] = kept
	} else {
		delete(node, "type")
	}
	if nullable {
		node["nullable"] = true
	}
}

// sanitizeSchemaMap sanitizes every value of m that is itself a schema object.
// Values of any other shape are left untouched — the function strips only what
// it knows about.
func sanitizeSchemaMap(m map[string]any) {
	for _, v := range m {
		if sub, ok := v.(map[string]any); ok {
			sanitizeSchemaNode(sub)
		}
	}
}

// sanitizeSchemaList sanitizes every element of l that is itself a schema
// object, with the same pass-through rule as sanitizeSchemaMap.
func sanitizeSchemaList(l []any) {
	for _, v := range l {
		if sub, ok := v.(map[string]any); ok {
			sanitizeSchemaNode(sub)
		}
	}
}
