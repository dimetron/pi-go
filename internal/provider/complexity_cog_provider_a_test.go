package provider

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/google/jsonschema-go/jsonschema"
	"google.golang.org/adk/v2/model"
	"google.golang.org/genai"
)

// Goldens pinning the wire output of the Ollama and xAI translation layers.
//
// Every `want` literal in this file was captured by running these same cases
// against the pre-refactor source, so a mismatch means the refactor changed what
// pi sends to a model server — not that the new code disagrees with itself.
//
// Comparisons run over a canonical form (see cogPACanon) because
// ollamaapi.ToolPropertiesMap preserves insertion order and insertion happens
// while ranging a Go map: the emitted property order is already
// nondeterministic, and pinning it would only make the tests flake.

// cogPACanon renders any value as indented JSON with object keys sorted, by
// round-tripping through `any` so every object becomes a map[string]any (which
// encoding/json emits in sorted-key order). Array order is preserved.
func cogPACanon(t *testing.T, v any) string {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var generic any
	if err := json.Unmarshal(b, &generic); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	out, err := json.MarshalIndent(generic, "", "  ")
	if err != nil {
		t.Fatalf("re-marshal: %v", err)
	}
	return string(out)
}

// cogPAGolden compares a value against the golden recorded for this test under
// key (the test name, plus a suffix when one test pins more than one value).
// A missing golden prints the captured value in a form that can be pasted back
// into cogPAGoldens, which is how every literal below was produced.
func cogPAGolden(t *testing.T, key string, got any) {
	t.Helper()
	name := t.Name()
	if key != "" {
		name += "#" + key
	}
	g := cogPACanon(t, got)
	want, ok := cogPAGoldens[name]
	if !ok {
		t.Errorf("no golden recorded\n<<<CAPTURE %s\n%s\nCAPTURE>>>", name, g)
		return
	}
	if g != want {
		t.Errorf("golden %s mismatch\n--- got ---\n%s\n--- want ---\n%s", name, g, want)
	}
}

// cogPAGoldens holds the expected wire output, captured from the pre-refactor
// source. See the file comment.
var cogPAGoldens = map[string]string{
	"TestCogPAOllamaChatRequestGoldens/PI_OLLAMA_NUM_PREDICT_of_zero_removes_options_entirely": `{
  "messages": [
    {
      "content": "hi",
      "role": "user"
    }
  ],
  "model": "llama3",
  "options": null,
  "stream": false
}`,
	"TestCogPAOllamaChatRequestGoldens/PI_OLLAMA_NUM_PREDICT_overrides_the_default_cap": `{
  "messages": [
    {
      "content": "hi",
      "role": "user"
    }
  ],
  "model": "llama3",
  "options": {
    "num_predict": 128
  },
  "stream": false
}`,
	"TestCogPAOllamaChatRequestGoldens/an_assistant_tool_call_round_trip": `{
  "messages": [
    {
      "content": "read it",
      "role": "user"
    },
    {
      "content": "sure",
      "role": "assistant",
      "tool_calls": [
        {
          "function": {
            "arguments": {
              "path": "/tmp/x"
            },
            "index": 0,
            "name": "read"
          },
          "id": "c1"
        }
      ]
    },
    {
      "content": "contents",
      "role": "tool",
      "tool_call_id": "c1"
    }
  ],
  "model": "llama3",
  "options": {
    "num_predict": 16384
  },
  "stream": false
}`,
	"TestCogPAOllamaChatRequestGoldens/an_empty_tool_list_leaves_tools_off_the_wire": `{
  "messages": [
    {
      "content": "hi",
      "role": "user"
    }
  ],
  "model": "llama3",
  "options": {
    "num_predict": 16384
  },
  "stream": false
}`,
	"TestCogPAOllamaChatRequestGoldens/an_unparseable_num_predict_falls_back_to_the_default": `{
  "messages": [
    {
      "content": "hi",
      "role": "user"
    }
  ],
  "model": "llama3",
  "options": {
    "num_predict": 16384
  },
  "stream": false
}`,
	"TestCogPAOllamaChatRequestGoldens/no_contents_falls_back_to_a_Hello_turn": `{
  "messages": [
    {
      "content": "Hello",
      "role": "user"
    }
  ],
  "model": "llama3",
  "options": {
    "num_predict": 16384
  },
  "stream": false
}`,
	"TestCogPAOllamaChatRequestGoldens/nothink_applies_to_the_per-request_model_too": `{
  "messages": [
    {
      "content": "hi",
      "role": "user"
    }
  ],
  "model": "gemma-nothink",
  "options": {
    "num_predict": 16384
  },
  "stream": false,
  "think": false
}`,
	"TestCogPAOllamaChatRequestGoldens/nothink_model_forces_think_off_even_at_level_high": `{
  "messages": [
    {
      "content": "hi",
      "role": "user"
    }
  ],
  "model": "qwen3-NoThink:8b",
  "options": {
    "num_predict": 16384
  },
  "stream": false,
  "think": false
}`,
	"TestCogPAOllamaChatRequestGoldens/per-request_model_overrides_the_instance_model": `{
  "messages": [
    {
      "content": "hi",
      "role": "user"
    }
  ],
  "model": "qwen3:8b",
  "options": {
    "num_predict": 16384
  },
  "stream": false
}`,
	"TestCogPAOllamaChatRequestGoldens/plain_user_turn": `{
  "messages": [
    {
      "content": "hi",
      "role": "user"
    }
  ],
  "model": "llama3",
  "options": {
    "num_predict": 16384
  },
  "stream": false
}`,
	"TestCogPAOllamaChatRequestGoldens/sampling_knobs_join_num_predict_in_options": `{
  "messages": [
    {
      "content": "hi",
      "role": "user"
    }
  ],
  "model": "llama3",
  "options": {
    "frequency_penalty": 0.25,
    "num_predict": 16384,
    "presence_penalty": 0.5,
    "repeat_last_n": 256,
    "repeat_penalty": 1.3
  },
  "stream": false
}`,
	"TestCogPAOllamaChatRequestGoldens/sampling_knobs_survive_an_uncapped_num_predict": `{
  "messages": [
    {
      "content": "hi",
      "role": "user"
    }
  ],
  "model": "llama3",
  "options": {
    "repeat_penalty": 1.3
  },
  "stream": false
}`,
	"TestCogPAOllamaChatRequestGoldens/system_instruction_is_prepended_as_a_system_message": `{
  "messages": [
    {
      "content": "be terse",
      "role": "system"
    },
    {
      "content": "hi",
      "role": "user"
    }
  ],
  "model": "llama3",
  "options": {
    "num_predict": 16384
  },
  "stream": false
}`,
	"TestCogPAOllamaChatRequestGoldens/thinking_level_high": `{
  "messages": [
    {
      "content": "hi",
      "role": "user"
    }
  ],
  "model": "llama3",
  "options": {
    "num_predict": 16384
  },
  "stream": false,
  "think": "high"
}`,
	"TestCogPAOllamaChatRequestGoldens/thinking_level_none_sends_an_explicit_false": `{
  "messages": [
    {
      "content": "hi",
      "role": "user"
    }
  ],
  "model": "llama3",
  "options": {
    "num_predict": 16384
  },
  "stream": false,
  "think": false
}`,
	"TestCogPAOllamaChatRequestGoldens/tools_are_converted_onto_the_request": `{
  "messages": [
    {
      "content": "hi",
      "role": "user"
    }
  ],
  "model": "llama3",
  "options": {
    "num_predict": 16384
  },
  "stream": false,
  "tools": [
    {
      "function": {
        "description": "read a file",
        "name": "read",
        "parameters": {
          "properties": {
            "path": {
              "type": "string"
            }
          },
          "required": [
            "path"
          ],
          "type": "object"
        }
      },
      "type": "function"
    }
  ]
}`,
	"TestCogPAOllamaChatRequestGoldens/unparseable_sampling_knobs_are_ignored": `{
  "messages": [
    {
      "content": "hi",
      "role": "user"
    }
  ],
  "model": "llama3",
  "options": {
    "num_predict": 16384
  },
  "stream": false
}`,
	"TestCogPAOllamaChatRequestGoldens/unrecognized_thinking_level_omits_think": `{
  "messages": [
    {
      "content": "hi",
      "role": "user"
    }
  ],
  "model": "llama3",
  "options": {
    "num_predict": 16384
  },
  "stream": false
}`,
	"TestCogPAOllamaStreamingRequestGolden#request": `{
  "messages": [
    {
      "content": "hi",
      "role": "user"
    }
  ],
  "model": "llama3",
  "options": {
    "num_predict": 16384
  },
  "think": "low"
}`,
	"TestCogPAOllamaStreamingRequestGolden#text": `[
  "ok",
  "ok"
]`,
	"TestCogPAOllamaToolsGoldens/array_property_drops_its_items_schema": `[
  {
    "function": {
      "name": "batch",
      "parameters": {
        "properties": {
          "paths": {
            "description": "files",
            "type": "array"
          }
        },
        "type": "object"
      }
    },
    "type": "function"
  }
]`,
	"TestCogPAOllamaToolsGoldens/declaration_with_no_parameters": `[
  {
    "function": {
      "description": "current time",
      "name": "now",
      "parameters": {
        "properties": {},
        "type": "object"
      }
    },
    "type": "function"
  }
]`,
	"TestCogPAOllamaToolsGoldens/empty_object_schema": `[
  {
    "function": {
      "name": "ping",
      "parameters": {
        "properties": {},
        "type": "object"
      }
    },
    "type": "function"
  }
]`,
	"TestCogPAOllamaToolsGoldens/empty_required_list": `[
  {
    "function": {
      "name": "emptyreq",
      "parameters": {
        "properties": {
          "a": {
            "type": "string"
          }
        },
        "type": "object"
      }
    },
    "type": "function"
  }
]`,
	"TestCogPAOllamaToolsGoldens/enum_property": `[
  {
    "function": {
      "name": "mode",
      "parameters": {
        "properties": {
          "kind": {
            "enum": [
              "read",
              "write",
              "append"
            ],
            "type": "string"
          }
        },
        "required": [
          "kind"
        ],
        "type": "object"
      }
    },
    "type": "function"
  }
]`,
	"TestCogPAOllamaToolsGoldens/nested_object_property_drops_its_sub-properties": `[
  {
    "function": {
      "name": "config",
      "parameters": {
        "properties": {
          "opts": {
            "description": "options",
            "type": "object"
          }
        },
        "required": [
          "opts"
        ],
        "type": "object"
      }
    },
    "type": "function"
  }
]`,
	"TestCogPAOllamaToolsGoldens/nil_declaration_inside_a_tool_is_skipped": `null`,
	"TestCogPAOllamaToolsGoldens/nil_tool_entry_is_skipped":                `null`,
	"TestCogPAOllamaToolsGoldens/nil_tool_slice":                           `null`,
	"TestCogPAOllamaToolsGoldens/non-string_entries_in_required_are_dropped": `[
  {
    "function": {
      "name": "mixed",
      "parameters": {
        "properties": {
          "a": {
            "type": "string"
          }
        },
        "required": [
          "a"
        ],
        "type": "object"
      }
    },
    "type": "function"
  }
]`,
	"TestCogPAOllamaToolsGoldens/optional_field_is_not_listed_as_required": `[
  {
    "function": {
      "name": "grep",
      "parameters": {
        "properties": {
          "limit": {
            "description": "max hits",
            "type": "integer"
          }
        },
        "type": "object"
      }
    },
    "type": "function"
  }
]`,
	"TestCogPAOllamaToolsGoldens/properties_that_is_not_a_map_is_ignored": `[
  {
    "function": {
      "name": "badprops",
      "parameters": {
        "properties": {},
        "required": [
          "a"
        ],
        "type": "object"
      }
    },
    "type": "function"
  }
]`,
	"TestCogPAOllamaToolsGoldens/property_that_is_not_an_object_yields_a_bare_property": `[
  {
    "function": {
      "name": "odd",
      "parameters": {
        "properties": {
          "weird": {}
        },
        "type": "object"
      }
    },
    "type": "function"
  }
]`,
	"TestCogPAOllamaToolsGoldens/required_that_is_not_a_list_is_ignored": `[
  {
    "function": {
      "name": "badreq",
      "parameters": {
        "properties": {
          "a": {
            "type": "string"
          }
        },
        "type": "object"
      }
    },
    "type": "function"
  }
]`,
	"TestCogPAOllamaToolsGoldens/schema_that_is_not_an_object_at_all": `[
  {
    "function": {
      "name": "scalar",
      "parameters": {
        "properties": {},
        "type": "object"
      }
    },
    "type": "function"
  }
]`,
	"TestCogPAOllamaToolsGoldens/several_declarations_across_several_tools": `[
  {
    "function": {
      "name": "first",
      "parameters": {
        "properties": {},
        "type": "object"
      }
    },
    "type": "function"
  },
  {
    "function": {
      "description": "two",
      "name": "second",
      "parameters": {
        "properties": {},
        "type": "object"
      }
    },
    "type": "function"
  },
  {
    "function": {
      "name": "third",
      "parameters": {
        "properties": {},
        "type": "object"
      }
    },
    "type": "function"
  }
]`,
	"TestCogPAOllamaToolsGoldens/single_required_string_field": `[
  {
    "function": {
      "description": "read a file",
      "name": "read",
      "parameters": {
        "properties": {
          "path": {
            "description": "absolute path",
            "type": "string"
          }
        },
        "required": [
          "path"
        ],
        "type": "object"
      }
    },
    "type": "function"
  }
]`,
	"TestCogPAOllamaToolsGoldens/tool_with_nil_declarations_is_skipped": `null`,
	"TestCogPAOllamaToolsGoldens/typed_jsonschema.Schema_is_marshaled_through_JSON": `[
  {
    "function": {
      "description": "typed schema",
      "name": "typed",
      "parameters": {
        "properties": {
          "path": {
            "description": "file to read",
            "type": "string"
          }
        },
        "required": [
          "path"
        ],
        "type": "object"
      }
    },
    "type": "function"
  }
]`,
	"TestCogPAXAIInputConversionError": `[
  "xAI Responses API failed: Post \"nodial://127.0.0.1:1/responses\": unsupported protocol scheme \"nodial\""
]`,
	"TestCogPAXAINilRequest": `[
  "xAI responses: nil LLM request"
]`,
	"TestCogPAXAIRequestGoldens/an_empty_tool_list_leaves_tools_off_the_wire": `{
  "input": [
    {
      "content": "hi",
      "role": "user"
    }
  ],
  "model": "grok-4.6",
  "store": false
}`,
	"TestCogPAXAIRequestGoldens/function_declarations_reach_the_tools_field": `{
  "input": [
    {
      "content": "hi",
      "role": "user"
    }
  ],
  "model": "grok-4.6",
  "store": false,
  "tools": [
    {
      "description": "read a file",
      "name": "read",
      "parameters": {
        "properties": {
          "path": {
            "type": "string"
          }
        },
        "required": [
          "path"
        ],
        "type": "object"
      },
      "strict": false,
      "type": "function"
    }
  ]
}`,
	"TestCogPAXAIRequestGoldens/level_none_maps_to_low": `{
  "input": [
    {
      "content": "hi",
      "role": "user"
    }
  ],
  "model": "grok-4.6",
  "reasoning": {
    "effort": "low"
  },
  "store": false
}`,
	"TestCogPAXAIRequestGoldens/non-reasoning_model_never_gets_reasoning": `{
  "input": [
    {
      "content": "hi",
      "role": "user"
    }
  ],
  "model": "grok-4.20-0309-non-reasoning",
  "store": false
}`,
	"TestCogPAXAIRequestGoldens/per-request_model_gates_reasoning": `{
  "input": [
    {
      "content": "hi",
      "role": "user"
    }
  ],
  "model": "grok-4.20-0309-non-reasoning",
  "store": false
}`,
	"TestCogPAXAIRequestGoldens/per-request_model_overrides_the_instance_model": `{
  "input": [
    {
      "content": "hi",
      "role": "user"
    }
  ],
  "model": "grok-4.7",
  "reasoning": {
    "effort": "high"
  },
  "store": false
}`,
	"TestCogPAXAIRequestGoldens/plain_user_turn_with_no_reasoning_level": `{
  "input": [
    {
      "content": "hi",
      "role": "user"
    }
  ],
  "model": "grok-4.6",
  "store": false
}`,
	"TestCogPAXAIRequestGoldens/reasoning_effort_medium": `{
  "input": [
    {
      "content": "hi",
      "role": "user"
    }
  ],
  "model": "grok-4.6",
  "reasoning": {
    "effort": "medium"
  },
  "store": false
}`,
	"TestCogPAXAIRequestGoldens/system_instruction_becomes_instructions": `{
  "input": [
    {
      "content": "hi",
      "role": "user"
    }
  ],
  "instructions": "be terse",
  "model": "grok-4.6",
  "store": false
}`,
	"TestCogPAXAIRequestGoldens/unrecognized_level_omits_reasoning": `{
  "input": [
    {
      "content": "hi",
      "role": "user"
    }
  ],
  "model": "grok-4.6",
  "store": false
}`,
	"TestCogPAXAIServerSideToolsGolden": `[
  {
    "type": "web_search"
  },
  {
    "type": "x_search"
  },
  {
    "type": "code_interpreter"
  }
]`,
}

// cogPATools wraps function declarations in the single-tool shape genai uses.
func cogPATools(fds ...*genai.FunctionDeclaration) []*genai.Tool {
	return []*genai.Tool{{FunctionDeclarations: fds}}
}

// ---------------------------------------------------------------------------
// ollamaGenaiToolsToOllama
// ---------------------------------------------------------------------------

func TestCogPAOllamaToolsGoldens(t *testing.T) {
	typed := &jsonschema.Schema{
		Type: "object",
		Properties: map[string]*jsonschema.Schema{
			"path": {Type: "string", Description: "file to read"},
		},
		Required: []string{"path"},
	}

	tests := []struct {
		name  string
		tools []*genai.Tool
	}{
		{
			name:  "nil tool slice",
			tools: nil,
		},
		{
			name:  "nil tool entry is skipped",
			tools: []*genai.Tool{nil},
		},
		{
			name:  "tool with nil declarations is skipped",
			tools: []*genai.Tool{{FunctionDeclarations: nil}},
		},
		{
			name:  "nil declaration inside a tool is skipped",
			tools: cogPATools(nil),
		},
		{
			name:  "declaration with no parameters",
			tools: cogPATools(&genai.FunctionDeclaration{Name: "now", Description: "current time"}),
		},
		{
			name: "empty object schema",
			tools: cogPATools(&genai.FunctionDeclaration{
				Name:                 "ping",
				ParametersJsonSchema: map[string]any{"type": "object"},
			}),
		},
		{
			name: "single required string field",
			tools: cogPATools(&genai.FunctionDeclaration{
				Name:        "read",
				Description: "read a file",
				ParametersJsonSchema: map[string]any{
					"type": "object",
					"properties": map[string]any{
						"path": map[string]any{"type": "string", "description": "absolute path"},
					},
					"required": []any{"path"},
				},
			}),
		},
		{
			name: "optional field is not listed as required",
			tools: cogPATools(&genai.FunctionDeclaration{
				Name: "grep",
				ParametersJsonSchema: map[string]any{
					"type": "object",
					"properties": map[string]any{
						"limit": map[string]any{"type": "integer", "description": "max hits"},
					},
				},
			}),
		},
		{
			name: "enum property",
			tools: cogPATools(&genai.FunctionDeclaration{
				Name: "mode",
				ParametersJsonSchema: map[string]any{
					"type": "object",
					"properties": map[string]any{
						"kind": map[string]any{
							"type": "string",
							"enum": []any{"read", "write", "append"},
						},
					},
					"required": []any{"kind"},
				},
			}),
		},
		{
			name: "array property drops its items schema",
			tools: cogPATools(&genai.FunctionDeclaration{
				Name: "batch",
				ParametersJsonSchema: map[string]any{
					"type": "object",
					"properties": map[string]any{
						"paths": map[string]any{
							"type":        "array",
							"description": "files",
							"items":       map[string]any{"type": "string"},
						},
					},
				},
			}),
		},
		{
			name: "nested object property drops its sub-properties",
			tools: cogPATools(&genai.FunctionDeclaration{
				Name: "config",
				ParametersJsonSchema: map[string]any{
					"type": "object",
					"properties": map[string]any{
						"opts": map[string]any{
							"type":        "object",
							"description": "options",
							"properties": map[string]any{
								"deep": map[string]any{"type": "boolean"},
							},
							"required": []any{"deep"},
						},
					},
					"required": []any{"opts"},
				},
			}),
		},
		{
			name: "property that is not an object yields a bare property",
			tools: cogPATools(&genai.FunctionDeclaration{
				Name: "odd",
				ParametersJsonSchema: map[string]any{
					"type":       "object",
					"properties": map[string]any{"weird": "not-a-schema"},
				},
			}),
		},
		{
			name: "non-string entries in required are dropped",
			tools: cogPATools(&genai.FunctionDeclaration{
				Name: "mixed",
				ParametersJsonSchema: map[string]any{
					"type":       "object",
					"properties": map[string]any{"a": map[string]any{"type": "string"}},
					"required":   []any{"a", 7, nil, true},
				},
			}),
		},
		{
			name: "required that is not a list is ignored",
			tools: cogPATools(&genai.FunctionDeclaration{
				Name: "badreq",
				ParametersJsonSchema: map[string]any{
					"type":       "object",
					"properties": map[string]any{"a": map[string]any{"type": "string"}},
					"required":   "a",
				},
			}),
		},
		{
			name: "empty required list",
			tools: cogPATools(&genai.FunctionDeclaration{
				Name: "emptyreq",
				ParametersJsonSchema: map[string]any{
					"type":       "object",
					"properties": map[string]any{"a": map[string]any{"type": "string"}},
					"required":   []any{},
				},
			}),
		},
		{
			name: "properties that is not a map is ignored",
			tools: cogPATools(&genai.FunctionDeclaration{
				Name: "badprops",
				ParametersJsonSchema: map[string]any{
					"type":       "object",
					"properties": []any{"a"},
					"required":   []any{"a"},
				},
			}),
		},
		{
			name: "schema that is not an object at all",
			tools: cogPATools(&genai.FunctionDeclaration{
				Name:                 "scalar",
				ParametersJsonSchema: 42,
			}),
		},
		{
			name: "typed jsonschema.Schema is marshaled through JSON",
			tools: cogPATools(&genai.FunctionDeclaration{
				Name:                 "typed",
				Description:          "typed schema",
				ParametersJsonSchema: typed,
			}),
		},
		{
			name: "several declarations across several tools",
			tools: []*genai.Tool{
				{FunctionDeclarations: []*genai.FunctionDeclaration{
					{Name: "first"},
					nil,
					{Name: "second", Description: "two"},
				}},
				nil,
				{FunctionDeclarations: []*genai.FunctionDeclaration{{Name: "third"}}},
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			cogPAGolden(t, "", ollamaGenaiToolsToOllama(tc.tools))
		})
	}
}

// TestCogPAOllamaToolsRequiredIsNilNotEmpty pins the distinction the golden's
// JSON cannot show: Required stays a nil slice when the schema carries no
// usable required list, rather than becoming an empty non-nil slice.
func TestCogPAOllamaToolsRequiredIsNilNotEmpty(t *testing.T) {
	cases := map[string]any{
		"absent":     map[string]any{"type": "object"},
		"empty list": map[string]any{"type": "object", "required": []any{}},
		"wrong type": map[string]any{"type": "object", "required": "a"},
		"no strings": map[string]any{"type": "object", "required": []any{1, 2}},
		"nil schema": nil,
	}
	for name, schema := range cases {
		t.Run(name, func(t *testing.T) {
			out := ollamaGenaiToolsToOllama(cogPATools(&genai.FunctionDeclaration{
				Name:                 "f",
				ParametersJsonSchema: schema,
			}))
			if len(out) != 1 {
				t.Fatalf("len(out) = %d, want 1", len(out))
			}
			if got := out[0].Function.Parameters.Required; got != nil {
				t.Errorf("Required = %#v, want nil", got)
			}
			if out[0].Function.Parameters.Properties == nil {
				t.Error("Properties map must always be allocated")
			}
		})
	}
}

// ---------------------------------------------------------------------------
// (*ollamaModel).GenerateContent — the request that reaches /api/chat
// ---------------------------------------------------------------------------

// cogPAOllamaCapture serves one non-streaming chat reply and records the request
// body that produced it.
func cogPAOllamaCapture(t *testing.T, body *map[string]any) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(raw, body)
		w.Header().Set("Content-Type", "application/x-ndjson")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"model":             "m",
			"message":           map[string]any{"role": "assistant", "content": "ok"},
			"done":              true,
			"done_reason":       "stop",
			"prompt_eval_count": 3,
			"eval_count":        4,
		})
	}))
	t.Cleanup(srv.Close)
	return srv
}

// cogPAClearOllamaEnv unsets every knob ollamaChatRequest reads so a golden does
// not depend on the ambient environment.
func cogPAClearOllamaEnv(t *testing.T) {
	t.Helper()
	for _, k := range []string{
		"PI_OLLAMA_NUM_PREDICT",
		"PI_OLLAMA_REPEAT_PENALTY",
		"PI_OLLAMA_REPEAT_LAST_N",
		"PI_OLLAMA_PRESENCE_PENALTY",
		"PI_OLLAMA_FREQUENCY_PENALTY",
	} {
		t.Setenv(k, "")
	}
}

func TestCogPAOllamaChatRequestGoldens(t *testing.T) {
	toolReq := cogPATools(&genai.FunctionDeclaration{
		Name:        "read",
		Description: "read a file",
		ParametersJsonSchema: map[string]any{
			"type":       "object",
			"properties": map[string]any{"path": map[string]any{"type": "string"}},
			"required":   []any{"path"},
		},
	})

	tests := []struct {
		name     string
		model    string
		thinking string
		env      map[string]string
		req      *model.LLMRequest
	}{
		{
			name:  "plain user turn",
			model: "llama3",
			req: &model.LLMRequest{
				Contents: []*genai.Content{{Role: "user", Parts: []*genai.Part{{Text: "hi"}}}},
			},
		},
		{
			name:  "no contents falls back to a Hello turn",
			model: "llama3",
			req:   &model.LLMRequest{},
		},
		{
			name:  "system instruction is prepended as a system message",
			model: "llama3",
			req: &model.LLMRequest{
				Contents: []*genai.Content{{Role: "user", Parts: []*genai.Part{{Text: "hi"}}}},
				Config: &genai.GenerateContentConfig{
					SystemInstruction: &genai.Content{Parts: []*genai.Part{{Text: "be terse"}}},
				},
			},
		},
		{
			name:  "per-request model overrides the instance model",
			model: "llama3",
			req: &model.LLMRequest{
				Model:    "qwen3:8b",
				Contents: []*genai.Content{{Role: "user", Parts: []*genai.Part{{Text: "hi"}}}},
			},
		},
		{
			name:     "thinking level high",
			model:    "llama3",
			thinking: "high",
			req: &model.LLMRequest{
				Contents: []*genai.Content{{Role: "user", Parts: []*genai.Part{{Text: "hi"}}}},
			},
		},
		{
			name:     "thinking level none sends an explicit false",
			model:    "llama3",
			thinking: "none",
			req: &model.LLMRequest{
				Contents: []*genai.Content{{Role: "user", Parts: []*genai.Part{{Text: "hi"}}}},
			},
		},
		{
			name:     "unrecognized thinking level omits think",
			model:    "llama3",
			thinking: "turbo",
			req: &model.LLMRequest{
				Contents: []*genai.Content{{Role: "user", Parts: []*genai.Part{{Text: "hi"}}}},
			},
		},
		{
			name:     "nothink model forces think off even at level high",
			model:    "qwen3-NoThink:8b",
			thinking: "high",
			req: &model.LLMRequest{
				Contents: []*genai.Content{{Role: "user", Parts: []*genai.Part{{Text: "hi"}}}},
			},
		},
		{
			name:     "nothink applies to the per-request model too",
			model:    "llama3",
			thinking: "medium",
			req: &model.LLMRequest{
				Model:    "gemma-nothink",
				Contents: []*genai.Content{{Role: "user", Parts: []*genai.Part{{Text: "hi"}}}},
			},
		},
		{
			name:  "PI_OLLAMA_NUM_PREDICT overrides the default cap",
			model: "llama3",
			env:   map[string]string{"PI_OLLAMA_NUM_PREDICT": "128"},
			req: &model.LLMRequest{
				Contents: []*genai.Content{{Role: "user", Parts: []*genai.Part{{Text: "hi"}}}},
			},
		},
		{
			name:  "PI_OLLAMA_NUM_PREDICT of zero removes options entirely",
			model: "llama3",
			env:   map[string]string{"PI_OLLAMA_NUM_PREDICT": "0"},
			req: &model.LLMRequest{
				Contents: []*genai.Content{{Role: "user", Parts: []*genai.Part{{Text: "hi"}}}},
			},
		},
		{
			name:  "an unparseable num_predict falls back to the default",
			model: "llama3",
			env:   map[string]string{"PI_OLLAMA_NUM_PREDICT": "lots"},
			req: &model.LLMRequest{
				Contents: []*genai.Content{{Role: "user", Parts: []*genai.Part{{Text: "hi"}}}},
			},
		},
		{
			name:  "sampling knobs join num_predict in options",
			model: "llama3",
			env: map[string]string{
				"PI_OLLAMA_REPEAT_PENALTY":    "1.3",
				"PI_OLLAMA_REPEAT_LAST_N":     "256",
				"PI_OLLAMA_PRESENCE_PENALTY":  "0.5",
				"PI_OLLAMA_FREQUENCY_PENALTY": "0.25",
			},
			req: &model.LLMRequest{
				Contents: []*genai.Content{{Role: "user", Parts: []*genai.Part{{Text: "hi"}}}},
			},
		},
		{
			name:  "sampling knobs survive an uncapped num_predict",
			model: "llama3",
			env: map[string]string{
				"PI_OLLAMA_NUM_PREDICT":    "-1",
				"PI_OLLAMA_REPEAT_PENALTY": "1.3",
			},
			req: &model.LLMRequest{
				Contents: []*genai.Content{{Role: "user", Parts: []*genai.Part{{Text: "hi"}}}},
			},
		},
		{
			name:  "unparseable sampling knobs are ignored",
			model: "llama3",
			env: map[string]string{
				"PI_OLLAMA_REPEAT_PENALTY": "high",
				"PI_OLLAMA_REPEAT_LAST_N":  "many",
			},
			req: &model.LLMRequest{
				Contents: []*genai.Content{{Role: "user", Parts: []*genai.Part{{Text: "hi"}}}},
			},
		},
		{
			name:  "tools are converted onto the request",
			model: "llama3",
			req: &model.LLMRequest{
				Contents: []*genai.Content{{Role: "user", Parts: []*genai.Part{{Text: "hi"}}}},
				Config:   &genai.GenerateContentConfig{Tools: toolReq},
			},
		},
		{
			name:  "an empty tool list leaves tools off the wire",
			model: "llama3",
			req: &model.LLMRequest{
				Contents: []*genai.Content{{Role: "user", Parts: []*genai.Part{{Text: "hi"}}}},
				Config:   &genai.GenerateContentConfig{Tools: []*genai.Tool{}},
			},
		},
		{
			name:  "an assistant tool call round trip",
			model: "llama3",
			req: &model.LLMRequest{
				Contents: []*genai.Content{
					{Role: "user", Parts: []*genai.Part{{Text: "read it"}}},
					{Role: "model", Parts: []*genai.Part{
						{Text: "sure"},
						{FunctionCall: &genai.FunctionCall{ID: "c1", Name: "read", Args: map[string]any{"path": "/tmp/x"}}},
					}},
					{Role: "user", Parts: []*genai.Part{
						{FunctionResponse: &genai.FunctionResponse{ID: "c1", Name: "read", Response: map[string]any{"result": "contents"}}},
					}},
				},
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			cogPAClearOllamaEnv(t)
			for k, v := range tc.env {
				t.Setenv(k, v)
			}
			var body map[string]any
			srv := cogPAOllamaCapture(t, &body)
			llm, err := NewOllama(context.Background(), OllamaRouting{Model: tc.model, APIKey: "", BaseURL: srv.URL}, tc.thinking, nil)
			if err != nil {
				t.Fatalf("NewOllama: %v", err)
			}
			for _, err := range llm.GenerateContent(context.Background(), tc.req, false) {
				if err != nil {
					t.Fatalf("GenerateContent: %v", err)
				}
			}
			cogPAGolden(t, "", body)
		})
	}
}

// TestCogPAOllamaStreamingRequestGolden pins the streaming branch of
// GenerateContent, which differs from the non-streaming one only in the stream
// flag it sends.
func TestCogPAOllamaStreamingRequestGolden(t *testing.T) {
	cogPAClearOllamaEnv(t)
	var body map[string]any
	srv := cogPAOllamaCapture(t, &body)
	llm, err := NewOllama(context.Background(), OllamaRouting{Model: "llama3", APIKey: "", BaseURL: srv.URL}, "low", nil)
	if err != nil {
		t.Fatalf("NewOllama: %v", err)
	}
	req := &model.LLMRequest{
		Contents: []*genai.Content{{Role: "user", Parts: []*genai.Part{{Text: "hi"}}}},
	}
	var texts []string
	for resp, err := range llm.GenerateContent(context.Background(), req, true) {
		if err != nil {
			t.Fatalf("GenerateContent: %v", err)
		}
		if resp != nil && resp.Content != nil {
			for _, p := range resp.Content.Parts {
				texts = append(texts, p.Text)
			}
		}
	}
	cogPAGolden(t, "request", body)
	cogPAGolden(t, "text", texts)
}

// ---------------------------------------------------------------------------
// (*xaiModel).GenerateContent — the request that reaches /responses
// ---------------------------------------------------------------------------

func cogPAXAICapture(t *testing.T, body *map[string]any) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(raw, body)
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"id":     "resp_x",
			"object": "response",
			"status": "completed",
			"model":  "grok-4.6",
			"output": []map[string]any{{
				"type":   "message",
				"id":     "msg_x",
				"role":   "assistant",
				"status": "completed",
				"content": []map[string]any{{
					"type":        "output_text",
					"text":        "hello",
					"annotations": []any{},
				}},
			}},
			"usage": map[string]any{"input_tokens": 1, "output_tokens": 2, "total_tokens": 3},
		})
	}))
	t.Cleanup(srv.Close)
	return srv
}

func TestCogPAXAIRequestGoldens(t *testing.T) {
	tools := cogPATools(&genai.FunctionDeclaration{
		Name:        "read",
		Description: "read a file",
		ParametersJsonSchema: map[string]any{
			"type":       "object",
			"properties": map[string]any{"path": map[string]any{"type": "string"}},
			"required":   []any{"path"},
		},
	})

	tests := []struct {
		name     string
		model    string
		thinking string
		req      *model.LLMRequest
	}{
		{
			name:  "plain user turn with no reasoning level",
			model: "grok-4.6",
			req: &model.LLMRequest{
				Contents: []*genai.Content{{Role: "user", Parts: []*genai.Part{{Text: "hi"}}}},
			},
		},
		{
			name:     "reasoning effort medium",
			model:    "grok-4.6",
			thinking: "medium",
			req: &model.LLMRequest{
				Contents: []*genai.Content{{Role: "user", Parts: []*genai.Part{{Text: "hi"}}}},
			},
		},
		{
			name:     "level none maps to low",
			model:    "grok-4.6",
			thinking: "none",
			req: &model.LLMRequest{
				Contents: []*genai.Content{{Role: "user", Parts: []*genai.Part{{Text: "hi"}}}},
			},
		},
		{
			name:     "unrecognized level omits reasoning",
			model:    "grok-4.6",
			thinking: "turbo",
			req: &model.LLMRequest{
				Contents: []*genai.Content{{Role: "user", Parts: []*genai.Part{{Text: "hi"}}}},
			},
		},
		{
			name:     "non-reasoning model never gets reasoning",
			model:    "grok-4.20-0309-non-reasoning",
			thinking: "high",
			req: &model.LLMRequest{
				Contents: []*genai.Content{{Role: "user", Parts: []*genai.Part{{Text: "hi"}}}},
			},
		},
		{
			name:     "per-request model gates reasoning",
			model:    "grok-4.6",
			thinking: "high",
			req: &model.LLMRequest{
				Model:    "grok-4.20-0309-non-reasoning",
				Contents: []*genai.Content{{Role: "user", Parts: []*genai.Part{{Text: "hi"}}}},
			},
		},
		{
			name:     "per-request model overrides the instance model",
			model:    "grok-4.6",
			thinking: "high",
			req: &model.LLMRequest{
				Model:    "grok-4.7",
				Contents: []*genai.Content{{Role: "user", Parts: []*genai.Part{{Text: "hi"}}}},
			},
		},
		{
			name:  "system instruction becomes instructions",
			model: "grok-4.6",
			req: &model.LLMRequest{
				Contents: []*genai.Content{{Role: "user", Parts: []*genai.Part{{Text: "hi"}}}},
				Config: &genai.GenerateContentConfig{
					SystemInstruction: &genai.Content{Parts: []*genai.Part{{Text: "be terse"}}},
				},
			},
		},
		{
			name:  "function declarations reach the tools field",
			model: "grok-4.6",
			req: &model.LLMRequest{
				Contents: []*genai.Content{{Role: "user", Parts: []*genai.Part{{Text: "hi"}}}},
				Config:   &genai.GenerateContentConfig{Tools: tools},
			},
		},
		{
			name:  "an empty tool list leaves tools off the wire",
			model: "grok-4.6",
			req: &model.LLMRequest{
				Contents: []*genai.Content{{Role: "user", Parts: []*genai.Part{{Text: "hi"}}}},
				Config:   &genai.GenerateContentConfig{Tools: []*genai.Tool{}},
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("PI_XAI_TOOLS", "")
			var body map[string]any
			srv := cogPAXAICapture(t, &body)
			llm, err := NewXAI(context.Background(), tc.model, "k", srv.URL, tc.thinking, nil)
			if err != nil {
				t.Fatalf("NewXAI: %v", err)
			}
			for _, err := range llm.GenerateContent(context.Background(), tc.req, false) {
				if err != nil {
					t.Fatalf("GenerateContent: %v", err)
				}
			}
			cogPAGolden(t, "", body)
		})
	}
}

// TestCogPAXAIServerSideToolsGolden pins the opt-in that appends xAI's built-in
// tools alongside the caller's function declarations.
func TestCogPAXAIServerSideToolsGolden(t *testing.T) {
	t.Setenv("PI_XAI_TOOLS", "")
	var body map[string]any
	srv := cogPAXAICapture(t, &body)
	llm, err := NewXAI(context.Background(), "grok-4.6", "k", srv.URL, "", &LLMOptions{EnableXAITools: true})
	if err != nil {
		t.Fatalf("NewXAI: %v", err)
	}
	req := &model.LLMRequest{
		Contents: []*genai.Content{{Role: "user", Parts: []*genai.Part{{Text: "hi"}}}},
	}
	for _, err := range llm.GenerateContent(context.Background(), req, false) {
		if err != nil {
			t.Fatalf("GenerateContent: %v", err)
		}
	}
	cogPAGolden(t, "", body["tools"])
}

// TestCogPAXAINilRequest pins the nil-request guard, which yields an error and
// never reaches the network.
func TestCogPAXAINilRequest(t *testing.T) {
	llm, err := NewXAI(context.Background(), "grok-4.6", "k", "http://127.0.0.1:1", "", nil)
	if err != nil {
		t.Fatalf("NewXAI: %v", err)
	}
	var errs []string
	for resp, err := range llm.GenerateContent(context.Background(), nil, false) {
		if err != nil {
			errs = append(errs, err.Error())
		}
		if resp != nil {
			t.Errorf("unexpected response %+v", resp)
		}
	}
	cogPAGolden(t, "", errs)
}

// TestCogPAXAIInputConversionError pins the wrapping of an input-conversion
// failure, which happens before any request is built.
//
// The base URL carries a scheme net/http refuses, so the failure is reported
// by the client itself rather than by the OS's dialer: a refused TCP connect
// reads "connect: connection refused" on Linux but "connectex: No connection
// could be made ..." on Windows, and a golden cannot hold both.
func TestCogPAXAIInputConversionError(t *testing.T) {
	llm, err := NewXAI(context.Background(), "grok-4.6", "k", "nodial://127.0.0.1:1", "", nil)
	if err != nil {
		t.Fatalf("NewXAI: %v", err)
	}
	req := &model.LLMRequest{
		Contents: []*genai.Content{{
			Role:  "user",
			Parts: []*genai.Part{{InlineData: &genai.Blob{MIMEType: "application/x-unsupported", Data: []byte("x")}}},
		}},
	}
	var errs []string
	for _, err := range llm.GenerateContent(context.Background(), req, false) {
		if err != nil {
			errs = append(errs, err.Error())
		}
	}
	cogPAGolden(t, "", errs)
}

// TestCogPAXAISendErrorShapes pins how a transport failure is reported: as a
// yielded error in the non-streaming path and as a STREAM_ERROR response in the
// streaming one.
func TestCogPAXAISendErrorShapes(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte(`{"error":{"message":"boom"}}`))
	}))
	t.Cleanup(srv.Close)

	llm, err := NewXAI(context.Background(), "grok-4.6", "k", srv.URL, "", nil)
	if err != nil {
		t.Fatalf("NewXAI: %v", err)
	}
	req := &model.LLMRequest{
		Contents: []*genai.Content{{Role: "user", Parts: []*genai.Part{{Text: "hi"}}}},
	}

	var sawErr bool
	for _, err := range llm.GenerateContent(context.Background(), req, false) {
		if err != nil {
			sawErr = true
		}
	}
	if !sawErr {
		t.Error("non-streaming failure must yield an error")
	}

	var codes []string
	for resp := range llm.GenerateContent(context.Background(), req, true) {
		if resp != nil && resp.ErrorCode != "" {
			codes = append(codes, resp.ErrorCode)
		}
	}
	if len(codes) == 0 || codes[len(codes)-1] != "STREAM_ERROR" {
		t.Errorf("streaming failure codes = %v, want a trailing STREAM_ERROR", codes)
	}
}

// ---------------------------------------------------------------------------
// OllamaContextWindowSize
// ---------------------------------------------------------------------------

// cogPAShowServer serves one /api/show reply built from the given parameters
// block and model_info map.
func cogPAShowServer(t *testing.T, parameters string, modelInfo map[string]any) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"parameters": parameters,
			"model_info": modelInfo,
		})
	}))
	t.Cleanup(srv.Close)
	return srv
}

func TestCogPAOllamaContextWindowSize(t *testing.T) {
	tests := []struct {
		name       string
		parameters string
		modelInfo  map[string]any
		want       int64
	}{
		{
			name:       "num_ctx parameter is used",
			parameters: "stop \"<eot>\"\nnum_ctx 32768\ntemperature 0.7",
			want:       32768,
		},
		{
			name:       "num_ctx beats the native context length",
			parameters: "num_ctx 8192",
			modelInfo:  map[string]any{"llama.context_length": 131072},
			want:       8192,
		},
		{
			name:       "a zero num_ctx is returned rather than falling through",
			parameters: "num_ctx 0",
			modelInfo:  map[string]any{"llama.context_length": 131072},
			want:       0,
		},
		{
			name:       "an unparseable num_ctx falls through to model_info",
			parameters: "num_ctx lots",
			modelInfo:  map[string]any{"llama.context_length": 4096},
			want:       4096,
		},
		{
			name:       "a num_ctx line with extra fields is ignored",
			parameters: "num_ctx 4096 extra",
			modelInfo:  map[string]any{"llama.context_length": 2048},
			want:       2048,
		},
		{
			name:       "a later num_ctx is reached when an earlier one is unparseable",
			parameters: "num_ctx x\nnum_ctx 1234",
			want:       1234,
		},
		{
			name:      "native context length from model_info",
			modelInfo: map[string]any{"qwen3.context_length": 262144},
			want:      262144,
		},
		{
			name:      "a key that does not end in .context_length is ignored",
			modelInfo: map[string]any{"qwen3.context_length_hint": 999},
			want:      0,
		},
		{
			name:      "a non-numeric context_length yields zero",
			modelInfo: map[string]any{"qwen3.context_length": "big"},
			want:      0,
		},
		{
			name: "nothing to go on yields zero",
			want: 0,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			srv := cogPAShowServer(t, tc.parameters, tc.modelInfo)
			if got := OllamaContextWindowSize(context.Background(), srv.URL, "m"); got != tc.want {
				t.Errorf("OllamaContextWindowSize = %d, want %d", got, tc.want)
			}
		})
	}
}

func TestCogPAOllamaContextWindowSizeFailures(t *testing.T) {
	t.Run("unparseable base URL", func(t *testing.T) {
		if got := OllamaContextWindowSize(context.Background(), "http://[::1", "m"); got != 0 {
			t.Errorf("got %d, want 0", got)
		}
	})
	t.Run("server error", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusInternalServerError)
		}))
		t.Cleanup(srv.Close)
		if got := OllamaContextWindowSize(context.Background(), srv.URL, "m"); got != 0 {
			t.Errorf("got %d, want 0", got)
		}
	})
}

// TestCogPAOllamaNativeContextLengthIntegerValues covers the integer arms of the
// model_info type switch. They are unreachable through the HTTP path above,
// where every number arrives decoded as a float64, so they are exercised
// directly instead of through OllamaContextWindowSize.
func TestCogPAOllamaNativeContextLengthIntegerValues(t *testing.T) {
	tests := []struct {
		name string
		info map[string]any
		want int64
	}{
		{name: "int64", info: map[string]any{"a.context_length": int64(65536)}, want: 65536},
		{name: "int", info: map[string]any{"a.context_length": 4096}, want: 4096},
		{name: "float64", info: map[string]any{"a.context_length": float64(8192)}, want: 8192},
		{name: "unusable type", info: map[string]any{"a.context_length": []int{1}}, want: 0},
		{name: "no matching key", info: map[string]any{"a.embedding_length": 1}, want: 0},
		{name: "empty", info: nil, want: 0},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := ollamaNativeContextLength(tc.info); got != tc.want {
				t.Errorf("ollamaNativeContextLength = %d, want %d", got, tc.want)
			}
		})
	}
}
