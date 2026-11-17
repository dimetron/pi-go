package provider

import (
	"encoding/json"
	"slices"

	"google.golang.org/genai"
)

// fmSchemaKeywordsDrop lists the JSON Schema keywords that `fm serve` rejects.
//
// Apple does not take a JSON Schema: it compiles one into a Swift
// GenerationSchema, and anything outside that model is a hard 400 —
// "Invalid request: The data couldn't be read because it isn't in the correct
// format." There is no field named in the error, so the only way to find these
// is to bisect the tool list, which is what was done against a running server:
//
//	oneOf            → 400
//	anyOf            → 200 (so it is rewritten rather than dropped)
//	$ref / $defs     → 400 "contains undefined references"
//	additionalProperties: {} → 400
//	"type": ["null", T]      → 400
//	"array" with no "items"  → 400 "because it is missing"
//	"items": {}              → 400
//	"pattern"        → 500 "A generation guide with an unsupported pattern was used."
//	property with "default": null and no "type" → 400
//
// Those are dropped below; anyOf/oneOf are collapsed first, because the common
// shape is ["T", "null"] from an optional parameter and the useful branch is the
// one that is not null.
var fmSchemaKeywordsDrop = map[string]bool{
	"$schema": true,
	"$defs":   true,
	"$ref":    true,
	"allOf":   true,
	"not":     true,
	"pattern": true,
}

// fmSanitizeSchema rewrites one JSON Schema into the subset `fm serve` accepts.
//
// The transformation is deliberately conservative: it only removes what Apple
// rejects and fills in what Apple requires, so a schema that already worked is
// left alone. Nothing here invents constraints — a dropped bound means the model
// is unconstrained on that field, which is what a server without that keyword
// can express anyway.
func fmSanitizeSchema(schema any) any {
	switch s := schema.(type) {
	case map[string]any:
		return fmSanitizeSchemaMap(s)
	case []any:
		// A list appears where keywords take several schemas (required,
		// enum, or a union branch list before it is collapsed). Recurse
		// element-wise so a nested object is rewritten too; marshaling the
		// slice wholesale would re-enter this function with the same []any
		// and never terminate.
		out := make([]any, len(s))
		for i, v := range s {
			out[i] = fmSanitizeSchema(v)
		}
		return out
	case nil:
		return nil
	default:
		// A typed schema (e.g. a struct) arrives as something else; round-trip it
		// through JSON so the same rewrite applies.
		data, err := json.Marshal(schema)
		if err != nil {
			return schema
		}
		var decoded any
		if json.Unmarshal(data, &decoded) != nil {
			return schema
		}
		// Guard against a value whose decoded form is again this branch: only
		// reachable for a scalar, which needs no rewriting anyway.
		switch decoded.(type) {
		case map[string]any, []any:
			return fmSanitizeSchema(decoded)
		}
		return decoded
	}
}

func fmSanitizeSchemaMap(schema map[string]any) any {
	// Collapse anyOf/oneOf onto their first non-null branch. An optional
	// parameter is routinely spelled as ["T", "null"], and dropping the whole
	// keyword would leave the property untyped — which Apple rejects too.
	node := schema
	for _, combinator := range []string{"anyOf", "oneOf"} {
		branches, ok := schema[combinator].([]any)
		if !ok {
			continue
		}
		if branch := fmFirstNonNullBranch(branches); branch != nil {
			merged := make(map[string]any, len(schema)+len(branch))
			for k, v := range schema {
				if k != "anyOf" && k != "oneOf" {
					merged[k] = v
				}
			}
			for k, v := range branch {
				merged[k] = v
			}
			node = merged
		}
		break
	}

	out := make(map[string]any, len(node))
	for k, v := range node {
		switch k {
		case "anyOf", "oneOf":
			continue
		case "additionalProperties":
			// Only the boolean form is understood; a schema form is rejected.
			if b, ok := v.(bool); ok {
				out[k] = b
			}
			continue
		case "type":
			// A union type is rejected; keep the first non-null member.
			if list, ok := v.([]any); ok {
				out[k] = fmFirstNonNullType(list)
				if out[k] == nil {
					delete(out, "type")
				}
				continue
			}
			out[k] = v
			continue
		case "default":
			// A null default on a property with no other type information
			// leaves it untyped, which Apple cannot compile.
			if v == nil {
				continue
			}
			out[k] = v
			continue
		case "properties":
			// The keys here are parameter names, not keywords: a parameter
			// called "pattern" (find, ripgrep) must survive even though the
			// keyword of that name is dropped. Only the values are schemas.
			if props, ok := v.(map[string]any); ok {
				sanitized := make(map[string]any, len(props))
				for name, prop := range props {
					sanitized[name] = fmSanitizeSchema(prop)
				}
				out[k] = sanitized
				continue
			}
		}
		if fmSchemaKeywordsDrop[k] {
			continue
		}
		out[k] = fmSanitizeSchema(v)
	}

	// An object must carry properties, and every property must be typed. A
	// property can lose its type when it was written as a bare anyOf of nulls.
	if out["type"] == "object" {
		props, _ := out["properties"].(map[string]any)
		if props == nil {
			props = map[string]any{}
		}
		typed := make(map[string]any, len(props))
		for name, prop := range props {
			if isTyped, ok := prop.(map[string]any); ok {
				if _, hasType := isTyped["type"]; hasType {
					typed[name] = prop
					continue
				}
			}
			// A property that survived untyped would fail the whole request;
			// the least-wrong repair is the loosest type Apple accepts.
			typed[name] = map[string]any{"type": "string"}
		}
		out["properties"] = typed
		if required, ok := out["required"].([]any); ok {
			kept := make([]any, 0, len(required))
			for _, r := range required {
				if name, ok := r.(string); ok {
					if _, present := typed[name]; present {
						kept = append(kept, name)
					}
				}
			}
			if len(kept) == 0 {
				delete(out, "required")
			} else {
				out["required"] = kept
			}
		}
		out["x-order"] = fmPropertyOrder(typed, out["required"])
	}

	// An array must say what it holds.
	if out["type"] == "array" {
		if items, ok := out["items"]; !ok || items == nil {
			out["items"] = map[string]any{"type": "string"}
		} else if m, ok := items.(map[string]any); ok && len(m) == 0 {
			out["items"] = map[string]any{"type": "string"}
		}
	}

	return out
}

// fmPropertyOrder returns the "x-order" for an object: the order in which the
// model generates its properties. Without it fm serve warns and generates them
// alphabetically, so edit's new_string comes before its old_string. The source
// order is gone by the time the schema is a map, so this puts the required
// properties first, in the order they are listed, then the rest by name.
func fmPropertyOrder(props map[string]any, required any) []any {
	order := make([]any, 0, len(props))
	seen := make(map[string]bool, len(props))
	if list, ok := required.([]any); ok {
		for _, r := range list {
			name, ok := r.(string)
			if !ok {
				continue
			}
			order = append(order, name)
			seen[name] = true
		}
	}
	rest := make([]string, 0, len(props))
	for name := range props {
		if !seen[name] {
			rest = append(rest, name)
		}
	}
	slices.Sort(rest)
	for _, name := range rest {
		order = append(order, name)
	}
	return order
}

// fmFirstNonNullBranch returns the first branch whose type is not "null".
func fmFirstNonNullBranch(branches []any) map[string]any {
	for _, b := range branches {
		m, ok := b.(map[string]any)
		if !ok || m["type"] == "null" {
			continue
		}
		return m
	}
	return nil
}

// fmFirstNonNullType returns the first member of a type union that is not null.
func fmFirstNonNullType(types []any) any {
	for _, t := range types {
		if name, ok := t.(string); ok && name != "null" {
			return name
		}
	}
	return nil
}

// fmSanitizeTools rewrites every function declaration's parameter schema into
// the subset `fm serve` accepts.
//
// The declarations are rewritten in place so the caller's request carries the
// sanitized form for any later inspection, and the number of tools is preserved:
// a tool whose schema cannot be expressed is repaired, never silently dropped,
// because a tool the model is never offered is a capability the user loses
// without being told.
func fmSanitizeTools(cfg *genai.GenerateContentConfig) {
	if cfg == nil {
		return
	}
	for _, t := range cfg.Tools {
		if t == nil {
			continue
		}
		for _, fd := range t.FunctionDeclarations {
			if fd == nil || fd.ParametersJsonSchema == nil {
				continue
			}
			fd.ParametersJsonSchema = fmSanitizeSchema(fd.ParametersJsonSchema)
		}
	}
}
