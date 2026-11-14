// Command modelsdoc regenerates MODELS.md from the same catalogs `pi model
// list` prints.
//
// MODELS.md is a reference document, and a hand-maintained one rots: it sat
// a month out of date because regenerating it meant pasting nine provider
// tables by hand. This does the pasting.
//
// Usage:
//
//	go run ./hack/modelsdoc              # rewrite MODELS.md
//	go run ./hack/modelsdoc -dry-run     # print it instead
//	go run ./hack/modelsdoc -out FILE
//
// Two sections are not taken from a live provider:
//
//   - Agentgateway: the live gateway answers with whatever the local
//     config.yaml routes to plus every model it can reach, which is machine
//     state, not repo truth. The section lists the routing patterns declared
//     in hack/agentgateway/config.yaml.
//   - Azure: there is no listing API reachable with an API key, so the
//     embedded deployment catalog is the source.
//
// A provider whose key is missing keeps the section already in the document,
// so a missing key leaves one stale section rather than deleting it.
package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"gopkg.in/yaml.v3"

	"github.com/dimetron/pi-go/internal/provider"
)

// providerSpec is one row of the provider table and one section of the
// document.
//
// keepNotes and the note label differ per provider because the source columns
// differ: Gemini's extra column is a human-readable display name, while the
// context-carrying providers report capabilities. For Anthropic, OpenAI and
// xAI the extra column is the owner, which is just the provider's own name, so
// it is dropped.
type providerSpec struct {
	name    string // provider key passed to the CLI
	anchor  string // markdown anchor of the section heading
	title   string // section heading text
	intro   string // line under the heading; "" for none
	count   string // "models", "routes", or "deployments"
	notes   string // header for the notes column; "" drops the column
	hasCtx  bool   // render a context column
	credits string // credentials cell in the overview table
}

var providers = []providerSpec{
	{name: "anthropic", anchor: "anthropic", title: "Anthropic", intro: "API key: `ANTHROPIC_API_KEY`.", count: "models", credits: "`ANTHROPIC_API_KEY`"},
	{name: "openai", anchor: "openai", title: "OpenAI", intro: "API key: `OPENAI_API_KEY`.", count: "models", credits: "`OPENAI_API_KEY`"},
	{name: "gemini", anchor: "google-gemini", title: "Google Gemini", intro: "API key: `GEMINI_API_KEY`.", count: "models", notes: "Display name", credits: "`GEMINI_API_KEY`"},
	{name: "mistral", anchor: "mistral", title: "Mistral", intro: "API key: `MISTRAL_API_KEY`.", count: "models", notes: "Capabilities", hasCtx: true, credits: "`MISTRAL_API_KEY`"},
	{name: "xai", anchor: "xai", title: "xAI", intro: "API key: `XAI_API_KEY`.", count: "models", credits: "`XAI_API_KEY`"},
	{name: "ollama", anchor: "ollama", title: "Ollama", intro: "Local daemon, plus Ollama Cloud (`*:cloud` tags). No API key for local models.", count: "models", notes: "Capabilities", hasCtx: true, credits: "local daemon; no key needed"},
	{name: "openrouter", anchor: "openrouter", title: "OpenRouter", intro: "API key: `OPENROUTER_API_KEY`.", count: "models", credits: "`OPENROUTER_API_KEY`"},
	{name: "agentgateway", anchor: "agentgateway", title: "Agentgateway", count: "routes", hasCtx: true, credits: "local gateway"},
	{name: "azure", anchor: "azure", title: "Azure", count: "deployments", hasCtx: true, credits: "`AZUREOPENAI_API_KEY`; names are per-subscription"},
}

const header = `# Supported Models

Models available through ` + "`pi model list`" + `, organized by provider. Every model
each provider currently serves is listed below. Prices are USD per 1M tokens
(input/output), from the embedded models.dev snapshot; a ` + "`—`" + ` means the
snapshot has no entry for that model.
`

const preamble = `Azure has no live model-listing API — enumerating deployments needs ARM
credentials, so ` + "`pi model list azure`" + ` prints the embedded catalog shown below.

The CLI also reports an owner per model. It is shown only for Gemini, where it
is the human-readable display name; Anthropic and xAI report nothing useful
(the provider name itself), and OpenAI reports ` + "`openai-internal`" + ` for just
` + "`text-embedding-ada-002`" + `, ` + "`tts-1`" + ` and ` + "`whisper-1`" + `.
`

const footer = `## Updating This File

` + "```bash" + `
make generate-models-md   # regenerate every section from live providers
` + "```" + `

The Agentgateway section is generated from ` + "`hack/agentgateway/config.yaml`" + `
rather than the live gateway: the gateway answers with whatever the local
config routes to, which is machine state rather than repo truth.
`

// liveFetcher returns a provider's models. It is a field so tests can supply
// fixtures without a network or an API key.
type liveFetcher func(name string) ([]provider.ModelInfo, error)

func main() {
	var (
		out    = flag.String("out", "MODELS.md", "path to write")
		dryRun = flag.Bool("dry-run", false, "print the document instead of writing it")
		root   = flag.String("root", ".", "repository root")
		binary = flag.String("pi", "", "pi binary to query; built when empty")
	)
	flag.Parse()

	fetch, cleanup, err := binaryFetcher(*root, *binary)
	if err != nil {
		fmt.Fprintf(os.Stderr, "modelsdoc: %v\n", err)
		os.Exit(1)
	}
	defer cleanup()

	doc, err := generate(*root, fetch)
	if err != nil {
		fmt.Fprintf(os.Stderr, "modelsdoc: %v\n", err)
		os.Exit(1)
	}
	if *dryRun {
		fmt.Print(doc)
		return
	}
	path := filepath.Join(*root, *out)
	if err := os.WriteFile(path, []byte(doc), 0o644); err != nil {
		fmt.Fprintf(os.Stderr, "modelsdoc: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("wrote %s\n", *out)
}

// binaryFetcher returns a fetcher that shells out to `pi model list -o json`,
// plus a cleanup that removes the binary when this call built it.
//
// Shelling out rather than calling provider.ListModels directly is deliberate:
// the CLI applies filtering and enrichment the document must agree with, and
// duplicating that here would let the two drift.
func binaryFetcher(root, given string) (liveFetcher, func(), error) {
	cleanup := func() {}
	bin := given
	if bin == "" {
		dir, err := os.MkdirTemp("", "modelsdoc-")
		if err != nil {
			return nil, cleanup, err
		}
		bin = filepath.Join(dir, "pi")
		cleanup = func() { os.RemoveAll(dir) }
		fmt.Fprintln(os.Stderr, "building pi...")
		build := exec.Command("go", "build", "-o", bin, "./cmd/pi")
		build.Dir = root
		build.Stdout, build.Stderr = os.Stderr, os.Stderr
		if err := build.Run(); err != nil {
			cleanup()
			return nil, func() {}, fmt.Errorf("building pi: %w", err)
		}
	}

	fetch := func(name string) ([]provider.ModelInfo, error) {
		var stdout, stderr bytes.Buffer
		cmd := exec.Command(bin, "model", "list", name, "-o", "json")
		cmd.Dir = root
		cmd.Stdout, cmd.Stderr = &stdout, &stderr
		if err := cmd.Run(); err != nil {
			msg := strings.TrimSpace(stderr.String())
			if msg == "" {
				msg = err.Error()
			}
			return nil, errors.New(msg)
		}
		var doc struct {
			Models []provider.ModelInfo `json:"models"`
		}
		if err := json.NewDecoder(&stdout).Decode(&doc); err != nil {
			return nil, fmt.Errorf("decoding %s listing: %w", name, err)
		}
		return doc.Models, nil
	}
	return fetch, cleanup, nil
}

// generate builds the whole document. Sections whose provider is unavailable
// are carried over from the existing document at root when one is present.
func generate(root string, fetch liveFetcher) (string, error) {
	existing := readExistingSections(root)

	sections := make(map[string]string, len(providers))
	counts := make(map[string]int, len(providers))
	var order []string
	// liveSections counts the providers actually fetched. Azure and
	// agentgateway come from files in the repo and always succeed, so they
	// cannot tell whether any credentials worked; if no live provider did,
	// the run failed and must not overwrite a good document.
	liveSections := 0
	for _, spec := range providers {
		var (
			section string
			count   int
			err     error
		)
		fromFiles := true
		switch spec.name {
		case "azure":
			section, count = azureSection(spec)
		case "agentgateway":
			section, count, err = agentgatewaySection(root, spec)
		default:
			fromFiles = false
			section, count, err = liveSection(spec, fetch)
		}
		if err != nil || section == "" {
			if err != nil {
				fmt.Fprintf(os.Stderr, "skip %s: %v\n", spec.name, err)
			}
			// A carried-over section is still a section, but it was not
			// fetched, so it does not count as a live success.
			if prior, ok := existing[spec.anchor]; ok {
				sections[spec.name] = prior.body
				counts[spec.name] = prior.count
				order = append(order, spec.name)
				fmt.Fprintf(os.Stderr, "  keeping existing %s section\n", spec.name)
			}
			continue
		}
		sections[spec.name] = section
		counts[spec.name] = count
		order = append(order, spec.name)
		if !fromFiles {
			liveSections++
		}
	}

	if len(order) == 0 {
		return "", errors.New("no provider sections could be generated")
	}
	if liveSections == 0 && len(existing) == 0 {
		return "", errors.New("no provider could be fetched and there is no existing document to keep")
	}

	var b strings.Builder
	b.WriteString(header)
	fmt.Fprintf(&b, "\nLast updated: %s\n\n", time.Now().UTC().Format("2006-01-02"))
	b.WriteString("## Providers\n\n")
	b.WriteString("| Provider | Models | Credentials |\n|---|---|---|\n")
	for _, spec := range providers {
		n, ok := counts[spec.name]
		if !ok {
			continue
		}
		fmt.Fprintf(&b, "| [%s](#%s) | %d %s | %s |\n", spec.title, spec.anchor, n, spec.count, spec.credits)
	}
	b.WriteString("\n")
	b.WriteString(preamble)
	b.WriteString("\n")
	for _, name := range order {
		for _, spec := range providers {
			if spec.name == name {
				b.WriteString(sections[name])
				break
			}
		}
	}
	b.WriteString(footer)
	return b.String(), nil
}

// existingSection is a section parsed back out of the document on disk, so a
// provider that cannot be fetched keeps the content it already had.
type existingSection struct {
	body  string
	count int
}

// readExistingSections splits MODELS.md into sections keyed by anchor. An
// unreadable or absent document yields no sections, which is not an error:
// every section is then regenerated or skipped.
func readExistingSections(root string) map[string]existingSection {
	b, err := os.ReadFile(filepath.Join(root, "MODELS.md"))
	if err != nil {
		return nil
	}
	out := map[string]existingSection{}
	var (
		anchor  string
		heading string
		title   string
		lines   []string
		count   int
	)
	flush := func() {
		if anchor == "" {
			return
		}
		// The heading is part of the body: a carried-over section is written
		// back verbatim, so losing it would drop the provider's title.
		body := strings.Join(lines, "\n")
		if heading != "" {
			body = heading + "\n" + body
		}
		out[anchor] = existingSection{body: body, count: count}
	}
	for _, line := range strings.Split(string(b), "\n") {
		if rest, ok := strings.CutPrefix(line, "## "); ok {
			flush()
			lines = nil
			anchor, title, count = "", rest, 0
			heading = line
			// "Title — 12 models": the anchor is the title slug.
			if i := strings.LastIndex(rest, " — "); i >= 0 {
				title = rest[:i]
				_, _ = fmt.Sscanf(rest[i+len(" — "):], "%d", &count)
			}
			anchor = slug(title)
			continue
		}
		if anchor == "" {
			continue
		}
		lines = append(lines, line)
	}
	flush()
	delete(out, "updating-this-file")
	return out
}

// slug lowercases a heading into the anchor GitHub generates for it.
func slug(title string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(title) {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			b.WriteRune(r)
		case r == ' ':
			b.WriteRune('-')
		}
	}
	return b.String()
}

// liveSection renders one provider from its live listing. A provider that
// returns no models is treated as unavailable rather than rendered empty.
func liveSection(spec providerSpec, fetch liveFetcher) (string, int, error) {
	models, err := fetch(spec.name)
	if err != nil {
		return "", 0, err
	}
	if len(models) == 0 {
		return "", 0, errors.New("provider returned no models")
	}

	// Newest first; models with no date sort last, ties by ID.
	sort.SliceStable(models, func(i, j int) bool {
		if models[i].ReleaseDate != models[j].ReleaseDate {
			return models[i].ReleaseDate > models[j].ReleaseDate
		}
		return models[i].ID < models[j].ID
	})

	heads := []string{"Model", "Release", "Price (in/out per 1M)"}
	if spec.hasCtx {
		heads = append(heads, "Context")
	}
	if spec.notes != "" {
		heads = append(heads, spec.notes)
	}

	rows := make([][]string, 0, len(models))
	for _, m := range models {
		row := []string{m.ID, dash(m.ReleaseDate), dash(stripUnit(modelPrice(spec.name, m.ID)))}
		if spec.hasCtx {
			ctx := ""
			if m.ContextWindow > 0 {
				ctx = humanTokens(m.ContextWindow)
			}
			row = append(row, ctx)
		}
		if spec.notes != "" {
			if spec.name == "gemini" {
				row = append(row, m.DisplayName)
			} else {
				row = append(row, strings.Join(m.Capabilities, ", "))
			}
		}
		rows = append(rows, row)
	}

	// Drop a column that is empty in every row: a wall of em dashes is noise,
	// and the document's convention is to leave such a column out.
	keep := make([]int, 0, len(heads))
	for i := range heads {
		if i == 0 {
			keep = append(keep, i)
			continue
		}
		for _, row := range rows {
			if row[i] != "" && row[i] != "—" {
				keep = append(keep, i)
				break
			}
		}
	}

	var b strings.Builder
	fmt.Fprintf(&b, "## %s — %d %s\n\n", spec.title, len(models), spec.count)
	if spec.intro != "" {
		b.WriteString(spec.intro)
		b.WriteString("\n\n")
	}
	writeTable(&b, heads, keep, rows)
	return b.String(), len(models), nil
}

// writeTable renders a markdown table keeping only the columns in keep.
func writeTable(b *strings.Builder, heads []string, keep []int, rows [][]string) {
	kept := make([]string, len(keep))
	for i, c := range keep {
		kept[i] = heads[c]
	}
	b.WriteString("| " + strings.Join(kept, " | ") + " |\n")
	b.WriteString("|" + strings.Repeat("---|", len(keep)) + "\n")
	for _, row := range rows {
		cells := make([]string, len(keep))
		for i, c := range keep {
			cells[i] = row[c]
		}
		b.WriteString("| " + strings.Join(cells, " | ") + " |\n")
	}
	b.WriteString("\n")
}

// azureSection renders the embedded deployment catalog. It is not a live
// query: Azure has no endpoint that enumerates deployments for an API key.
func azureSection(spec providerSpec) (string, int) {
	deployments := provider.AzureDeployments()
	var b strings.Builder
	fmt.Fprintf(&b, "## %s — %d %s\n\n", spec.title, len(deployments), spec.count)
	b.WriteString("Deployment names are chosen per subscription; select one with\n")
	b.WriteString("`--model azure/<deployment>`. An uncataloged name still works — its window\n")
	b.WriteString("falls back to the OpenAI entry it is named after, or override it with\n")
	b.WriteString("`\"contextWindow\"` in `config.json`.\n\n")
	b.WriteString("| Deployment | Context |\n|---|---|\n")
	for _, d := range deployments {
		fmt.Fprintf(&b, "| %s | %s |\n", d.Name, humanTokens(d.ContextWindow))
	}
	b.WriteString("\n")
	return b.String(), len(deployments)
}

// agentgatewayConfig is the slice of the gateway config the document reports:
// the routes and virtual models declared under the top-level `llm` section.
// virtualModels is a sibling of models there, not nested inside one.
type agentgatewayConfig struct {
	LLM struct {
		Models []struct {
			Name string `yaml:"name"`
		} `yaml:"models"`
		VirtualModels []struct {
			Name string `yaml:"name"`
		} `yaml:"virtualModels"`
	} `yaml:"llm"`
}

// gatewayRoutes reads the routing patterns the tracked config declares.
//
// The live gateway answers with every model it can reach — 833 of them on the
// machine this was written on — which is machine state. The tracked config is
// the repo's statement of what the gateway routes, so that is what is listed.
func gatewayRoutes(root string) ([]string, error) {
	path := filepath.Join(root, "hack", "agentgateway", "config.yaml")
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var cfg agentgatewayConfig
	if err := yaml.Unmarshal(b, &cfg); err != nil {
		return nil, fmt.Errorf("parsing %s: %w", path, err)
	}
	seen := map[string]bool{}
	var routes []string
	for _, m := range cfg.LLM.Models {
		if m.Name != "" && !seen[m.Name] {
			seen[m.Name] = true
			routes = append(routes, m.Name)
		}
	}
	for _, v := range cfg.LLM.VirtualModels {
		if v.Name != "" && !seen[v.Name] {
			seen[v.Name] = true
			routes = append(routes, v.Name)
		}
	}
	sort.Strings(routes)
	return routes, nil
}

// agentgatewaySection renders the routing patterns with their context windows
// resolved from the embedded overlay, longest matching prefix wins.
func agentgatewaySection(root string, spec providerSpec) (string, int, error) {
	routes, err := gatewayRoutes(root)
	if err != nil {
		return "", 0, err
	}
	if len(routes) == 0 {
		return "", 0, errors.New("no routes in config.yaml")
	}
	var b strings.Builder
	fmt.Fprintf(&b, "## %s — %d %s\n\n", spec.title, len(routes), spec.count)
	b.WriteString("Local gateway on `localhost:4000`. Entries are routing patterns, not fixed models.\n\n")
	b.WriteString("| Model | Context |\n|---|---|\n")
	for _, r := range routes {
		// ContextWindowSizeFor, not the raw overlay: it applies the same
		// fallback chain the runtime does, including virtual-model names.
		ctx := "—"
		if w := provider.ContextWindowSizeFor("agentgateway", r); w > 0 {
			ctx = humanTokens(w)
		}
		fmt.Fprintf(&b, "| %s | %s |\n", r, ctx)
	}
	b.WriteString("\n")
	return b.String(), len(routes), nil
}

// humanTokens renders a token count as the compact K/M figure the CLI uses.
// The formatting matches internal/cli so the document and `pi model list`
// never disagree about the same number.
func humanTokens(n int64) string {
	switch {
	case n >= 1_000_000:
		return fmt.Sprintf("%.2fM", float64(n)/1_000_000)
	case n >= 1_000:
		return fmt.Sprintf("%dK", n/1_000)
	default:
		return fmt.Sprintf("%d", n)
	}
}

// stripUnit drops the " per 1M" suffix a price carries, since the column
// header already names the unit. A cell repeating it is noise in every row of
// a two-hundred-row table.
func stripUnit(price string) string {
	return strings.TrimSuffix(price, " per 1M")
}

func dash(s string) string {
	if s == "" {
		return "—"
	}
	return s
}

// modelPrice returns a per-million-token price for a model, or "" when the
// snapshot has no entry. This mirrors internal/cli's modelPrice, including the
// Ollama Cloud case: those are the one local provider that also sells per
// token, and their rates live in a separate snapshot.
func modelPrice(providerName, modelID string) string {
	if providerName == "ollama" && provider.IsOllamaCloudModel(modelID) {
		if pm, ok := provider.OllamaCloudCost(ollamaCloudPriceKey(modelID)); ok {
			return formatModelPrice(pm)
		}
	}
	pm, ok := provider.CostFor(providerName, modelID)
	if !ok {
		return ""
	}
	return formatModelPrice(pm)
}

// ollamaCloudPriceKey strips the cloud tag so the name matches the base IDs in
// the ollama-cloud pricing snapshot. A dated cloud ID keeps its date, so the
// prefix lookup still resolves the right base entry.
func ollamaCloudPriceKey(modelID string) string {
	if i := strings.LastIndex(modelID, "-cloud"); i >= 0 {
		return modelID[:i]
	}
	return strings.TrimSuffix(modelID, ":cloud")
}

func formatModelPrice(pm provider.PricingModel) string {
	return fmt.Sprintf("$%s/$%s per 1M", priceAmount(pm.Input), priceAmount(pm.Output))
}

// priceAmount renders a rate at a precision that suits its magnitude: two
// decimals at or above a cent, three for the sub-cent rates some models carry.
func priceAmount(v float64) string {
	if v == 0 {
		return "—"
	}
	if v < 0.01 {
		return fmt.Sprintf("%.3f", v)
	}
	return fmt.Sprintf("%.2f", v)
}
