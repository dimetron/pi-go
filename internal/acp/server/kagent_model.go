package server

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"github.com/dimetron/pi-go/internal/provider"
)

// kagentOllamaCloudURL is Ollama's hosted API, used when a cloud endpoint has to
// be named explicitly because the model's tag cannot carry ":cloud".
const kagentOllamaCloudURL = "https://api.ollama.com"

// kagentOllamaBaseEnv is where the kagent compiler puts an Ollama ModelConfig's
// host. The compiled ADK config has no host field for Ollama (only options), so
// the env var is the real channel; the JSON `ollama.host` key is read too in
// case a controller build adds it.
const kagentOllamaBaseEnv = "OLLAMA_API_BASE"

// kagentModelSpec is the model the AgentTemplate's ModelConfig compiled to. The
// compiler serializes with proto field names (snake_case) and one provider
// object next to `model`. Unknown fields are ignored on purpose.
type kagentModelSpec struct {
	Model     string `json:"model"`
	Anthropic *struct {
		BaseURL string `json:"base_url"`
	} `json:"anthropic"`
	OpenAI *struct {
		BaseURL string `json:"base_url"`
	} `json:"openai"`
	Mistral *struct {
		BaseURL string `json:"base_url"`
	} `json:"mistral"`
	// Gemini compiles a base_url too (ADKGemini.base_url), so a ModelConfig
	// pointing at a proxy keeps its endpoint — and resolving it as custom also
	// skips catalog validation, which a proxied deployment needs.
	Gemini *struct {
		BaseURL string `json:"base_url"`
	} `json:"gemini"`
	Ollama *struct {
		Host string `json:"host"`
	} `json:"ollama"`
	// Providers pi-go has no routing for. They are only detected so the log can
	// say why the model was not applied.
	AzureOpenAI     *json.RawMessage `json:"azure_openai"`
	GeminiVertexAI  *json.RawMessage `json:"gemini_vertex_ai"`
	GeminiAnthropic *json.RawMessage `json:"gemini_anthropic"`
	Bedrock         *json.RawMessage `json:"bedrock"`
	SAPAICore       *json.RawMessage `json:"sap_ai_core"`
	Foundry         *json.RawMessage `json:"foundry"`
}

// KagentModel is a model choice mapped onto pi-go's own routing: Model is a name
// provider.Resolve understands and BaseURL is the optional endpoint override.
type KagentModel struct {
	Model   string
	BaseURL string
}

// KagentModelFromEnv maps `agent.model` of KAGENT_CONFIG_JSON to pi-go routing.
//
// ok is false when there is nothing to apply: no config, a malformed config, no
// model, or a provider pi-go cannot route. In the last case reason says why, so
// the caller can warn and keep its default instead of failing startup. Headers,
// TLS settings and provider options in the config are deliberately ignored:
// pi-go sets its own, and the header values are credential placeholders for the
// egress gateway, not something to copy around.
func KagentModelFromEnv(getenv func(string) string) (m KagentModel, ok bool, reason string) {
	if getenv == nil {
		getenv = os.Getenv
	}
	raw := strings.TrimSpace(getenv(kagentConfigEnv))
	if raw == "" {
		return KagentModel{}, false, ""
	}
	var cfg struct {
		Agent struct {
			Model *kagentModelSpec `json:"model"`
		} `json:"agent"`
	}
	if err := json.Unmarshal([]byte(raw), &cfg); err != nil || cfg.Agent.Model == nil {
		return KagentModel{}, false, ""
	}
	spec := cfg.Agent.Model
	name := strings.TrimSpace(spec.Model)
	if name == "" {
		return KagentModel{}, false, ""
	}

	switch {
	case spec.Ollama != nil:
		host := strings.TrimSpace(spec.Ollama.Host)
		if host == "" {
			host = strings.TrimSpace(getenv(kagentOllamaBaseEnv))
		}
		m = ollamaKagentModel(name, host)
	case spec.OpenAI != nil:
		m = KagentModel{Model: withPrefix("openai/", name), BaseURL: strings.TrimSpace(spec.OpenAI.BaseURL)}
	case spec.Anthropic != nil:
		m = KagentModel{Model: withPrefix("anthropic/", name), BaseURL: strings.TrimSpace(spec.Anthropic.BaseURL)}
	case spec.Mistral != nil:
		m = KagentModel{Model: withPrefix("mistral/", name), BaseURL: strings.TrimSpace(spec.Mistral.BaseURL)}
	case spec.Gemini != nil:
		m = KagentModel{Model: name, BaseURL: strings.TrimSpace(spec.Gemini.BaseURL)}
	default:
		return KagentModel{}, false, fmt.Sprintf("provider of model %q is not supported by pi-go", name)
	}
	info, err := provider.ResolveWithBaseURL(m.Model, m.BaseURL)
	if err != nil {
		return KagentModel{}, false, fmt.Sprintf("model %q does not resolve: %v", name, err)
	}
	// ResolveWithBaseURL only picks a provider. The session build runs
	// ValidateModel as well (runtime.go:resolveSessionProvider), and that
	// rejects a model pi-go's catalog does not know — which would fail every
	// turn rather than fall back to the default. kagent's own model catalog
	// drifts from pi-go's embedded snapshots, so a ModelConfig naming a model
	// only kagent knows is a normal case, not a broken template.
	if err := provider.ValidateModel(info); err != nil {
		return KagentModel{}, false, fmt.Sprintf("model %q is not in pi-go's catalog: %v", name, err)
	}
	return m, true, ""
}

// ollamaKagentModel maps an Ollama ModelConfig onto the same routing kagent
// itself applies (models.OllamaReachesCloud): an explicit host that is not
// ollama.com is a daemon; otherwise the model name decides, being either a
// catalog name api.ollama.com serves or one carrying the :cloud/-cloud tag.
//
// An empty host does not mean cloud. kagent leaves OLLAMA_API_BASE unset when
// the operator named no host, and the runtime it substitutes applies the
// model-name rule to that absence — so treating an empty host as cloud sends
// local models (qwen3:8b, llama3.3) to api.ollama.com, where they do not exist.
func ollamaKagentModel(name, host string) KagentModel {
	if host != "" && !provider.IsOllamaCloudEndpoint(host) {
		return KagentModel{Model: withPrefix("ollama/", name), BaseURL: host}
	}
	if provider.IsOllamaCloudModel(name) || provider.IsOllamaCloudCatalogModel(name) {
		// The cloud API accepts the base catalog name; a :cloud tag is the
		// local daemon's proxy form, not something to add. Name the endpoint so
		// the key is not what decides the destination.
		return KagentModel{Model: withPrefix("ollama/", name), BaseURL: kagentOllamaCloudURL}
	}
	// Neither a cloud name nor a host: the daemon, as kagent routes it.
	return KagentModel{Model: withPrefix("ollama/", name)}
}

func withPrefix(prefix, name string) string {
	if strings.HasPrefix(strings.ToLower(name), prefix) {
		return name
	}
	return prefix + name
}
