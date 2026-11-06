package config

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// MCPServersKey is the JSON key holding MCP server definitions in a standalone
// mcp.json file, in both the Claude Desktop object form and the legacy array.
const MCPServersKey = "mcpServers"

// ErrMCPServerExists reports that AddMCPServer was asked to write a name that
// already has an entry. Replacing is opt-in, so a mistyped URL can never
// silently overwrite a server that works.
var ErrMCPServerExists = errors.New("MCP server already configured")

// NearestProjectMCPFile returns the .pi-go/mcp.json that a pi run started in cwd
// would load, walking up the tree the way LoadMCPServersFrom does. It returns ""
// when this tree has none, which is the case where the global file is the only
// source.
//
// It exists because a project mcp.json does not merge with the global one — it
// replaces it. A caller that just wrote the global file can use this to notice
// that the entry will be invisible from here, which is otherwise a silent
// surprise: the server is configured, and the project still does not see it.
func NearestProjectMCPFile(cwd string) string {
	return findNearestProjectFile(cwd, filepath.Join(".pi-go", "mcp.json"))
}

// MCPStoreFile returns the mcp.json path that `pi mcp add` writes: the global
// ~/.pi-go/mcp.json, or the project's .pi-go/mcp.json when project is set.
//
// Global is the default because a server named on the command line is almost
// always meant to be available in every project. The project file is for a
// server that only makes sense beside one codebase — and note that
// LoadMCPServersFrom lets a project file replace the global server list
// outright, so writing one hides the global servers in that tree.
func MCPStoreFile(project bool) (string, error) {
	if project {
		cwd, err := os.Getwd()
		if err != nil {
			return "", fmt.Errorf("locating working directory: %w", err)
		}
		// Reuse an mcp.json that pi would actually load from here, walking up
		// like LoadMCPServersFrom does. Otherwise a pi run from the repository
		// root would never see a file written into a subdirectory.
		if existing := NearestProjectMCPFile(cwd); existing != "" {
			return existing, nil
		}
		return filepath.Join(cwd, ".pi-go", "mcp.json"), nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("locating home directory: %w", err)
	}
	return filepath.Join(home, ".pi-go", "mcp.json"), nil
}

// AddMCPServer writes srv into the standalone mcp.json at path, creating the
// file when it does not exist.
//
// The file is read and rewritten as a generic JSON document rather than through
// the MCPServer struct: mcp.json is shared with other MCP clients, and a round
// trip through the struct would drop the fields pi-go does not model (env,
// cwd, disabled, …) from every other server in the same file.
//
// Entries keep their unknown keys — only the named one is touched. A name
// already present is replaced only when overwrite is set; otherwise
// ErrMCPServerExists is returned so the caller can name the entry it refused to
// touch.
func AddMCPServer(path string, srv MCPServer, overwrite bool) error {
	doc, err := readMCPDocument(path)
	if err != nil {
		return err
	}
	servers, err := mcpServersObject(doc)
	if err != nil {
		return fmt.Errorf("%s: %w", path, err)
	}
	if _, exists := servers[srv.Name]; exists && !overwrite {
		return fmt.Errorf("%w: %q in %s (pass --force to replace it)", ErrMCPServerExists, srv.Name, path)
	}
	entry, err := mcpServerEntry(srv)
	if err != nil {
		return err
	}
	servers[srv.Name] = entry
	// Entries keep their contents but not their order: encoding/json emits map
	// keys sorted, so the file comes back alphabetically. That is a real
	// rewrite of a hand-maintained file's layout, accepted because a stable
	// order is what keeps later saves from churning the diff.
	doc[MCPServersKey] = servers
	return writeJSONDocument(path, doc)
}

// RemoveMCPServer deletes the named entry from the standalone mcp.json at path.
// A name that is not present is an error rather than a no-op, so a removal never
// reports success when it changed nothing.
func RemoveMCPServer(path, name string) error {
	doc, err := readMCPDocument(path)
	if err != nil {
		return err
	}
	servers, err := mcpServersObject(doc)
	if err != nil {
		return fmt.Errorf("%s: %w", path, err)
	}
	if _, ok := servers[name]; !ok {
		return fmt.Errorf("no MCP server named %q in %s", name, path)
	}
	delete(servers, name)
	doc[MCPServersKey] = servers
	return writeJSONDocument(path, doc)
}

// ReadMCPServerEntry returns the raw object stored for name in the standalone
// mcp.json at path. It reports false when the file or the entry is absent, so a
// caller can say "not in mcp.json" instead of guessing from the merged config,
// where a name may come from config.json or a different scope.
func ReadMCPServerEntry(path, name string) (map[string]any, bool) {
	doc, err := readMCPDocument(path)
	if err != nil {
		return nil, false
	}
	servers, err := mcpServersObject(doc)
	if err != nil {
		return nil, false
	}
	entry, ok := servers[name].(map[string]any)
	if !ok || entry == nil {
		return nil, false
	}
	return entry, true
}

// readMCPDocument reads path as a generic JSON object. A missing or empty file
// yields an empty document, which is what lets AddMCPServer create the file on
// first use.
func readMCPDocument(path string) (map[string]any, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return map[string]any{}, nil
		}
		return nil, fmt.Errorf("reading %s: %w", path, err)
	}
	if len(bytes.TrimSpace(data)) == 0 {
		return map[string]any{}, nil
	}
	doc := map[string]any{}
	if err := json.Unmarshal(data, &doc); err != nil {
		return nil, fmt.Errorf("parsing %s: %w", path, err)
	}
	return doc, nil
}

// mcpServersObject returns the "mcpServers" object from doc, converting the
// legacy array form when that is what the file holds. Any other JSON type is an
// error rather than something to overwrite: discarding a document someone wrote
// by hand is worse than refusing to edit it.
func mcpServersObject(doc map[string]any) (map[string]any, error) {
	raw, ok := doc[MCPServersKey]
	if !ok || raw == nil {
		return map[string]any{}, nil
	}
	switch v := raw.(type) {
	case map[string]any:
		return v, nil
	case []any:
		// The legacy array carries the name inside each element. Keying it here
		// makes both formats writable through one path.
		out := make(map[string]any, len(v))
		for _, item := range v {
			m, ok := item.(map[string]any)
			if !ok {
				continue
			}
			name, _ := m["name"].(string)
			if name == "" {
				continue
			}
			delete(m, "name")
			out[name] = m
		}
		return out, nil
	default:
		return nil, fmt.Errorf("%q must be an object or an array, got %T", MCPServersKey, raw)
	}
}

// mcpServerEntry renders srv as the JSON object stored under its name. Only the
// fields the user supplied are emitted, so the written entry stays readable and
// does not advertise defaults the server never asked for.
func mcpServerEntry(srv MCPServer) (map[string]any, error) {
	if strings.TrimSpace(srv.Name) == "" {
		return nil, errors.New("MCP server name is required")
	}
	entry := map[string]any{}
	switch {
	case srv.URL != "" && srv.Command != "":
		return nil, fmt.Errorf("MCP server %q sets both url and command; pick one transport", srv.Name)
	case srv.URL != "":
		entry["url"] = srv.URL
	case srv.Command != "":
		entry["command"] = srv.Command
		if len(srv.Args) > 0 {
			entry["args"] = srv.Args
		}
	default:
		return nil, fmt.Errorf("MCP server %q needs a url or a command", srv.Name)
	}
	if len(srv.Headers) > 0 {
		entry["headers"] = srv.Headers
	}
	if srv.OAuth {
		entry["oauth"] = true
	}
	return entry, nil
}

// writeJSONDocument writes doc to path atomically: a temporary file in the same
// directory is renamed over the target, so a crash mid-write leaves the previous
// mcp.json intact instead of a truncated one.
func writeJSONDocument(path string, doc map[string]any) error {
	data, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		return fmt.Errorf("encoding %s: %w", path, err)
	}
	data = append(data, '\n')

	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("creating %s: %w", dir, err)
	}
	tmp, err := os.CreateTemp(dir, "."+filepath.Base(path)+".tmp-")
	if err != nil {
		return fmt.Errorf("creating temp file for %s: %w", path, err)
	}
	tmpName := tmp.Name()
	cleanup := func() { _ = tmp.Close(); _ = os.Remove(tmpName) }
	if _, err := tmp.Write(data); err != nil {
		cleanup()
		return fmt.Errorf("writing %s: %w", path, err)
	}
	if err := tmp.Sync(); err != nil {
		cleanup()
		return fmt.Errorf("syncing %s: %w", path, err)
	}
	if err := tmp.Close(); err != nil {
		_ = os.Remove(tmpName)
		return fmt.Errorf("closing %s: %w", path, err)
	}
	if err := os.Rename(tmpName, path); err != nil {
		_ = os.Remove(tmpName)
		return fmt.Errorf("installing %s: %w", path, err)
	}
	return nil
}
