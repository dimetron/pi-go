package agent

import (
	"fmt"
	"strings"
	"sync"

	adkagent "google.golang.org/adk/v2/agent"
	"google.golang.org/adk/v2/model"
	"google.golang.org/adk/v2/tool"
	"google.golang.org/adk/v2/tool/toolutils"
	"google.golang.org/genai"

	"github.com/dimetron/pi-go/internal/notice"
)

// maxToolNameLen is the longest function name the model APIs accept. Gemini,
// OpenAI and Anthropic all cap it at 64 characters, so a prefixed name is
// trimmed to fit rather than sent and rejected.
const maxToolNameLen = 64

// dedupeToolsets makes tool names unique across every source that reaches the
// model in one request.
//
// The ADK packs the agent's own tools first and each toolset's tools after
// (see llminternal.toolProcessor), and the packer rejects a second tool
// claiming a name that is already taken — "duplicate tool: %q" — which fails
// the whole turn, not just the offending tool. One real collision: an MCP
// server exposing find (the Chrome extension does) against pi-go's built-in
// find tool, which made every request error out until the server was removed
// from mcp.json.
//
// Built-in tools keep their names, because they are the ones the system prompt
// and the tool-display code name explicitly. A colliding toolset tool is
// renamed to "<toolset>_<tool>" and stays callable; only its model-facing name
// changes, so it still reaches its server under its original name.
func dedupeToolsets(tools []tool.Tool, toolsets []tool.Toolset) []tool.Toolset {
	if len(toolsets) == 0 {
		return toolsets
	}
	claims := &nameClaims{taken: make(map[string]bool, len(tools))}
	for _, t := range tools {
		if t != nil {
			claims.taken[t.Name()] = true
		}
	}
	out := make([]tool.Toolset, 0, len(toolsets))
	for _, ts := range toolsets {
		if ts == nil {
			continue
		}
		out = append(out, &dedupedToolset{inner: ts, claims: claims, assigned: map[string]string{}})
	}
	return out
}

// nameClaims is the set of tool names already handed out for one agent. It is
// shared by every wrapped toolset, so two MCP servers exposing the same tool
// name cannot collide with each other either.
type nameClaims struct {
	mu    sync.Mutex
	taken map[string]bool
}

// claim reserves want, or the first free "<want>_2", "<want>_3", ... variant,
// and reports the name that was reserved.
func (c *nameClaims) claim(want string) string {
	c.mu.Lock()
	defer c.mu.Unlock()
	if !c.taken[want] {
		c.taken[want] = true
		return want
	}
	for n := 2; ; n++ {
		alt := truncateToolName(fmt.Sprintf("%s_%d", want, n))
		if !c.taken[alt] {
			c.taken[alt] = true
			return alt
		}
	}
}

// isTaken reports whether name is already spoken for.
func (c *nameClaims) isTaken(name string) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.taken[name]
}

// dedupedToolset renames the tools of one toolset whose names are already
// taken. Assignments are remembered so a tool keeps the same name for the whole
// session: Tools is called once per request, and a name that drifted from
// "chrome_find" to "chrome_find_2" between turns would break every pending
// function call referring to the old one.
type dedupedToolset struct {
	inner  tool.Toolset
	claims *nameClaims

	mu       sync.Mutex
	assigned map[string]string // original tool name → model-facing name
}

func (d *dedupedToolset) Name() string { return d.inner.Name() }

// Unwrap returns the toolset underneath, so callers that inspect concrete
// toolset types (the MCP status listing, for one) still see through the
// rename layer.
func (d *dedupedToolset) Unwrap() tool.Toolset { return d.inner }

func (d *dedupedToolset) Tools(ctx adkagent.ReadonlyContext) ([]tool.Tool, error) {
	inner, err := d.inner.Tools(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]tool.Tool, 0, len(inner))
	for _, t := range inner {
		if t == nil {
			continue
		}
		orig := t.Name()
		final := d.nameFor(orig)
		if final == orig {
			out = append(out, t)
			continue
		}
		out = append(out, &renamedTool{Tool: t, name: final})
	}
	return out, nil
}

// nameFor resolves the model-facing name for one of this toolset's tools,
// reusing an earlier assignment when there is one.
func (d *dedupedToolset) nameFor(orig string) string {
	d.mu.Lock()
	defer d.mu.Unlock()
	if final, ok := d.assigned[orig]; ok {
		return final
	}
	if !d.claims.isTaken(orig) {
		final := d.claims.claim(orig)
		d.assigned[orig] = final
		return final
	}
	final := d.claims.claim(prefixedToolName(d.inner.Name(), orig))
	notice.Notifyf("tool %q from %q renamed to %q: that name is already taken by another tool",
		orig, d.inner.Name(), final)
	d.assigned[orig] = final
	return final
}

// prefixedToolName builds the fallback name for a colliding tool. The toolset
// name is sanitized because MCP server names are free-form config strings
// while function names are not.
func prefixedToolName(toolset, name string) string {
	prefix := sanitizeToolName(toolset)
	if prefix == "" {
		prefix = "mcp"
	}
	return truncateToolName(prefix + "_" + name)
}

// sanitizeToolName maps anything the model APIs do not accept in a function
// name to an underscore, and collapses the runs that produces.
func sanitizeToolName(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	prevUnderscore := false
	for _, r := range s {
		ok := r == '_' || r == '-' || r == '.' ||
			(r >= '0' && r <= '9') || (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z')
		if !ok {
			r = '_'
		}
		if r == '_' {
			if prevUnderscore {
				continue
			}
			prevUnderscore = true
		} else {
			prevUnderscore = false
		}
		b.WriteRune(r)
	}
	return strings.Trim(b.String(), "_")
}

// truncateToolName trims a name to the API limit. It cuts from the front, so
// the tool's own name survives and the toolset prefix is what gets shortened —
// a truncated prefix still disambiguates, a truncated tool name says nothing.
func truncateToolName(s string) string {
	if len(s) <= maxToolNameLen {
		return s
	}
	return strings.TrimLeft(s[len(s)-maxToolNameLen:], "_-.")
}

// renamedTool presents a tool to the model under a different name. Only the
// declaration and the request registration change: Run delegates to the inner
// tool, which still addresses its MCP server by the tool's original name.
type renamedTool struct {
	tool.Tool
	name string
}

func (r *renamedTool) Name() string { return r.name }

func (r *renamedTool) Declaration() *genai.FunctionDeclaration {
	type declarer interface {
		Declaration() *genai.FunctionDeclaration
	}
	d, ok := r.Tool.(declarer)
	if !ok {
		return nil
	}
	decl := d.Declaration()
	if decl == nil {
		return nil
	}
	renamed := *decl
	renamed.Name = r.name
	return &renamed
}

func (r *renamedTool) ProcessRequest(_ adkagent.Context, req *model.LLMRequest) error {
	return toolutils.PackTool(req, r)
}

func (r *renamedTool) Run(ctx adkagent.Context, args any) (map[string]any, error) {
	type runner interface {
		Run(adkagent.Context, any) (map[string]any, error)
	}
	inner, ok := r.Tool.(runner)
	if !ok {
		return nil, fmt.Errorf("tool %q (renamed from %q) is not callable", r.name, r.Tool.Name())
	}
	return inner.Run(ctx, args)
}
