package extension

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/dimetron/pi-go/internal/audit"
	"github.com/dimetron/pi-go/internal/notice"
	"github.com/dimetron/pi-go/internal/plugin"
)

// maxCommandFileBytes is the largest prompt template pi-go will load. A
// command is inlined into one user turn, so a file past this is a mistake
// rather than a template.
const maxCommandFileBytes = 256 << 10

// maxCommandNesting is how many parent directories a command file may have
// under its commands folder. 2 allows review.md, git/commit.md and
// git/hooks/pre-commit.md.
const maxCommandNesting = 2

// commandArgPattern matches Claude Code argument placeholders. $10 is one
// placeholder, not $1 followed by 0, and $ARGUMENTS_FILE is left alone.
// The braced form has no trailing word boundary: `}` and the next
// punctuation are both non-word characters, so \b would never match there.
var commandArgPattern = regexp.MustCompile(`\$\{ARGUMENTS\}|\$ARGUMENTS\b|\$[1-9][0-9]*\b`)

// Command is a user-defined slash command loaded from a markdown or prompt file.
// Invoking /Name expands Body into the next user turn.
type Command struct {
	// Name is the slash name without the leading slash, lowercased.
	// Nested files use Claude's colon form: git/commit.md → "git:commit".
	// Plugin files are prefixed with the plugin name: "plugin:git:commit".
	Name string
	// Description is the one-line frontmatter description, possibly empty.
	Description string
	// ArgumentHint is the frontmatter argument-hint, shown in help. Empty
	// when the file does not declare one.
	ArgumentHint string
	// Body is the markdown prompt with frontmatter removed.
	Body string
	// Source is where the file was found: "plugin", "user", or "project".
	Source string
	// Path is the file the command was read from.
	Path string
}

// Invocation is the help label, including the argument hint when one is set.
func (c Command) Invocation() string {
	if c.ArgumentHint == "" {
		return "/" + c.Name
	}
	return "/" + c.Name + " " + c.ArgumentHint
}

// Summary is a single-line description for completion and IDE command lists.
func (c Command) Summary() string {
	desc := strings.Join(strings.Fields(c.Description), " ")
	hint := strings.Join(strings.Fields(c.ArgumentHint), " ")
	switch {
	case desc == "" && hint == "":
		return ""
	case desc == "":
		return hint
	case hint == "":
		return desc
	default:
		return desc + " (" + hint + ")"
	}
}

// CommandDir is one directory of command files plus the namespace those
// files are published under. An empty Namespace means the filename is the
// slash name. Plugin directories set Namespace to the plugin name.
type CommandDir struct {
	Path      string
	Source    string
	Namespace string
}

// LoadCommands discovers command files in dirs. Later directories override
// earlier ones for the same command name. Missing directories are skipped.
// Files with critical hidden-character findings are blocked.
func LoadCommands(dirs ...CommandDir) ([]Command, error) {
	return LoadCommandsWithOptions(LoadOptions{AuditMode: AuditBlock}, dirs...)
}

// LoadCommandsWithOptions is LoadCommands with the skill audit mode.
func LoadCommandsWithOptions(opts LoadOptions, dirs ...CommandDir) ([]Command, error) {
	if opts.AuditMode == "" {
		opts.AuditMode = AuditBlock
	}
	seen := make(map[string]int)
	var commands []Command
	var blocked []string
	for _, dir := range dirs {
		var err error
		commands, err = appendDirCommands(commands, seen, &blocked, opts, dir)
		if err != nil {
			// One unreadable directory must not drop commands already loaded
			// from the others.
			notice.Notifyf("warning: skipping commands in %s: %v", dir.Path, err)
		}
	}
	if len(blocked) > 0 {
		notice.Notifyf("%d command(s) blocked due to security audit. Run 'pi audit' for details.", len(blocked))
	}
	return commands, nil
}

// appendDirCommands loads the command files in one directory, letting them
// override same-named commands loaded from an earlier directory.
func appendDirCommands(commands []Command, seen map[string]int, blocked *[]string, opts LoadOptions, dir CommandDir) ([]Command, error) {
	if dir.Path == "" {
		return commands, nil
	}
	if dir.Namespace != "" && !validCommandSegment(dir.Namespace) {
		notice.Notifyf("warning: skipping commands in %s: namespace %q is not a valid command name", dir.Path, dir.Namespace)
		return commands, nil
	}
	info, err := os.Stat(dir.Path)
	if err != nil {
		if os.IsNotExist(err) {
			return commands, nil
		}
		return nil, fmt.Errorf("reading commands dir %s: %w", dir.Path, err)
	}
	if !info.IsDir() {
		return commands, nil
	}

	files, err := commandFiles(dir.Path, dir.Namespace)
	if err != nil {
		return nil, err
	}
	for _, file := range files {
		if commandAuditRejects(opts, file.path, file.name, blocked) {
			continue
		}
		cmd, err := parseCommandFile(file.path, file.name, dir.Source)
		if err != nil {
			notice.Notifyf("warning: skipping command %s: %v", file.path, err)
			continue
		}
		commands = appendOrOverrideCommand(commands, seen, cmd)
	}
	return commands, nil
}

// commandFile is the winning file for one command name inside a directory.
type commandFile struct {
	path string
	name string
	rank int
}

// commandFiles lists one file per command name under dir. When name.md and
// name.prompt both exist, the markdown file wins.
func commandFiles(dir, namespace string) ([]commandFile, error) {
	best := make(map[string]commandFile)
	err := filepath.WalkDir(dir, func(path string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		rel, relErr := filepath.Rel(dir, path)
		if relErr != nil {
			return relErr
		}
		if rel == "." {
			return nil
		}
		if d.IsDir() {
			return skipCommandDir(d.Name(), rel)
		}
		file, ok := newCommandFile(path, rel, namespace)
		if !ok {
			return nil
		}
		if prev, exists := best[file.name]; exists && file.rank < prev.rank {
			return nil
		}
		best[file.name] = file
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("reading commands dir %s: %w", dir, err)
	}

	names := make([]string, 0, len(best))
	for name := range best {
		names = append(names, name)
	}
	sort.Strings(names)
	files := make([]commandFile, 0, len(names))
	for _, name := range names {
		files = append(files, best[name])
	}
	return files, nil
}

// skipCommandDir tells WalkDir to ignore hidden, vendored, and too-deep
// directories. rel is the path relative to the commands root.
func skipCommandDir(name, rel string) error {
	if strings.HasPrefix(name, ".") || name == "node_modules" || name == "vendor" {
		return filepath.SkipDir
	}
	if strings.Count(filepath.ToSlash(rel), "/") >= maxCommandNesting {
		return filepath.SkipDir
	}
	return nil
}

// newCommandFile accepts path when it is a regular file whose relative path
// is a legal command name. rank prefers .md over .prompt.
func newCommandFile(path, rel, namespace string) (commandFile, bool) {
	info, err := os.Stat(path)
	if err != nil || !info.Mode().IsRegular() || info.Size() > maxCommandFileBytes {
		if err == nil && info.Mode().IsRegular() && info.Size() > maxCommandFileBytes {
			notice.Notifyf("warning: skipping command %s: file exceeds %d bytes", path, maxCommandFileBytes)
		}
		return commandFile{}, false
	}
	name, ok := commandName(rel, namespace)
	if !ok {
		return commandFile{}, false
	}
	return commandFile{path: path, name: name, rank: commandExtRank(rel)}, true
}

// commandName turns a path relative to a commands directory into a slash
// name. README files are documentation, not commands.
func commandName(rel, namespace string) (string, bool) {
	stem, ok := commandStem(rel)
	if !ok {
		return "", false
	}
	parts := strings.Split(stem, "/")
	segs := make([]string, 0, len(parts)+1)
	if namespace != "" {
		segs = append(segs, strings.ToLower(namespace))
	}
	for _, part := range parts {
		if strings.EqualFold(part, "README") || !validCommandSegment(part) {
			return "", false
		}
		segs = append(segs, strings.ToLower(part))
	}
	if len(segs) == 0 {
		return "", false
	}
	return strings.Join(segs, ":"), true
}

// commandStem strips a supported extension and returns the slash-separated
// path that remains.
func commandStem(rel string) (string, bool) {
	slash := filepath.ToSlash(rel)
	lower := strings.ToLower(slash)
	switch {
	case strings.HasSuffix(lower, ".prompt.md"):
		return slash[:len(slash)-len(".prompt.md")], true
	case strings.HasSuffix(lower, ".md"):
		return slash[:len(slash)-len(".md")], true
	case strings.HasSuffix(lower, ".prompt"):
		return slash[:len(slash)-len(".prompt")], true
	default:
		return "", false
	}
}

// commandExtRank ranks file extensions so name.md beats name.prompt when
// both define the same command in one directory.
func commandExtRank(rel string) int {
	lower := strings.ToLower(rel)
	switch {
	case strings.HasSuffix(lower, ".prompt.md"):
		return 3
	case strings.HasSuffix(lower, ".md"):
		return 2
	default:
		return 1
	}
}

// validCommandSegment reports whether s can be one colon-separated piece of
// a slash command name.
func validCommandSegment(s string) bool {
	if s == "" || s == "." || s == ".." {
		return false
	}
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
		case r == '-' || r == '_' || r == '.':
		default:
			return false
		}
	}
	return true
}

func appendOrOverrideCommand(commands []Command, seen map[string]int, cmd Command) []Command {
	if idx, ok := seen[cmd.Name]; ok {
		commands[idx] = cmd
		return commands
	}
	seen[cmd.Name] = len(commands)
	return append(commands, cmd)
}

// commandAuditRejects reports whether path must not be loaded. Only a
// critical finding under AuditBlock rejects.
func commandAuditRejects(opts LoadOptions, path, name string, blocked *[]string) bool {
	if opts.AuditMode == AuditSkip {
		return false
	}
	scanResult, scanErr := audit.ScanFile(path)
	if scanErr != nil {
		notice.Notifyf("warning: audit scan failed for %s: %v", path, scanErr)
		return false
	}
	if !scanResult.HasCritical() {
		return false
	}
	if opts.AuditMode == AuditBlock {
		*blocked = append(*blocked, path)
		notice.Notifyf("BLOCKED command %s — critical hidden characters detected", name)
		return true
	}
	notice.Notifyf("WARNING: command %s has critical hidden characters", name)
	return false
}

// parseCommandFile reads a command file. Frontmatter is optional:
//
//	---
//	description: Review a change
//	argument-hint: [path]
//	---
//	Review $ARGUMENTS
//
// The filename decides the command name. A name: key in the frontmatter is
// ignored so the directory layout stays the source of truth. allowed-tools
// and model are ignored: pi-go does not switch tools or models per command.
func parseCommandFile(path, name, source string) (Command, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return Command{}, err
	}
	if len(data) > maxCommandFileBytes {
		return Command{}, fmt.Errorf("file exceeds %d bytes", maxCommandFileBytes)
	}
	cmd := Command{Name: name, Source: source, Path: path}
	cmd.Description, cmd.ArgumentHint, cmd.Body = splitCommandFrontmatter(string(data))
	return cmd, nil
}

// splitCommandFrontmatter separates optional YAML-style frontmatter from the
// prompt body. Frontmatter is recognized only when the file starts with a
// "---" line, so a markdown horizontal rule later in the prompt stays in the
// body. Only single-line key: value pairs are read.
func splitCommandFrontmatter(content string) (description, argumentHint, body string) {
	normalized := strings.ReplaceAll(content, "\r\n", "\n")
	normalized = strings.ReplaceAll(normalized, "\r", "\n")
	lines := strings.Split(normalized, "\n")

	start := 0
	for start < len(lines) && strings.TrimSpace(lines[start]) == "" {
		start++
	}
	if start >= len(lines) || strings.TrimSpace(lines[start]) != "---" {
		return "", "", strings.TrimSpace(normalized)
	}

	var b strings.Builder
	closed := false
	for _, line := range lines[start+1:] {
		if !closed && strings.TrimSpace(line) == "---" {
			closed = true
			continue
		}
		if !closed {
			if key, value, ok := parseFrontmatterLine(line); ok {
				switch key {
				case "description":
					description = unquoteFrontmatter(value)
				case "argument-hint", "argument_hint":
					argumentHint = unquoteFrontmatter(value)
				}
			}
			continue
		}
		b.WriteString(line)
		b.WriteString("\n")
	}
	return description, argumentHint, strings.TrimSpace(b.String())
}

func unquoteFrontmatter(value string) string {
	if len(value) >= 2 {
		if (value[0] == '"' && value[len(value)-1] == '"') || (value[0] == '\'' && value[len(value)-1] == '\'') {
			return value[1 : len(value)-1]
		}
	}
	return value
}

// ExpandCommand substitutes Claude-style argument placeholders in body.
//
// $ARGUMENTS and ${ARGUMENTS} become args joined by spaces. $1, $2, … become
// the individual arguments, split the same way the slash-command parser
// already splits input (whitespace, not shell quotes). A missing positional
// argument expands to empty.
//
// When the body contains no placeholder and args is non-empty, the arguments
// are appended. When a placeholder is present, unused arguments are not
// appended — the template asked for the ones it names.
func ExpandCommand(body string, args []string) string {
	used := false
	out := commandArgPattern.ReplaceAllStringFunc(body, func(match string) string {
		used = true
		if match == "$ARGUMENTS" || match == "${ARGUMENTS}" {
			return strings.Join(args, " ")
		}
		n, err := strconv.Atoi(strings.TrimPrefix(match, "$"))
		if err != nil || n < 1 || n > len(args) {
			return ""
		}
		return args[n-1]
	})
	if used {
		return out
	}
	all := strings.Join(args, " ")
	if all == "" {
		return out
	}
	return strings.TrimRight(out, "\n") + "\n\n" + all
}

// FindCommand looks up a command by slash name. The match is case-insensitive.
func FindCommand(commands []Command, name string) (Command, bool) {
	name = strings.ToLower(name)
	for _, c := range commands {
		if c.Name == name {
			return c, true
		}
	}
	return Command{}, false
}

// DefaultCommandDirs returns the command directories for the process working
// directory. See DefaultCommandDirsIn for the order.
func DefaultCommandDirs() []CommandDir {
	cwd, err := os.Getwd()
	if err != nil {
		cwd = "."
	}
	return DefaultCommandDirsIn(cwd)
}

// DefaultCommandDirsIn returns command directories for a project rooted at
// root, lowest precedence first. A later directory overrides an earlier one:
//
//  1. Installed plugin command directories (namespaced as /plugin:name)
//  2. ~/.claude/commands
//  3. ~/.pi-go/commands
//  4. The nearest ancestor .claude/commands
//  5. The nearest ancestor .pi-go/commands
//
// Plugin directories come from ~/.pi-go (or PI_GO_HOME), which is where
// `pi plugin` installs them. Within one plugin the order is commands/, then
// .claude/commands, then .pi-go/commands.
func DefaultCommandDirsIn(root string) []CommandDir {
	userHome, err := os.UserHomeDir()
	if err != nil {
		userHome = ""
	}
	return commandDirs(root, userHome, resolvePiHome(userHome))
}

// resolvePiHome is the directory `pi plugin` uses: PI_GO_HOME when set,
// otherwise ~/.pi-go.
func resolvePiHome(userHome string) string {
	if override := strings.TrimSpace(os.Getenv("PI_GO_HOME")); override != "" {
		return override
	}
	if userHome == "" {
		return ""
	}
	return filepath.Join(userHome, ".pi-go")
}

// commandDirs is DefaultCommandDirsIn with the homes injected so tests do
// not have to relocate the process home directory.
func commandDirs(root, userHome, piHome string) []CommandDir {
	var dirs []CommandDir
	seen := make(map[string]struct{})
	add := func(path, source, namespace string) {
		if path == "" {
			return
		}
		key := source + "\x00" + namespace + "\x00" + path
		if _, ok := seen[key]; ok {
			return
		}
		seen[key] = struct{}{}
		dirs = append(dirs, CommandDir{Path: path, Source: source, Namespace: namespace})
	}

	if piHome != "" {
		for _, loc := range installedPluginCommandDirs(piHome) {
			add(loc.Dir, "plugin", loc.Name)
		}
	}
	if userHome != "" {
		add(filepath.Join(userHome, ".claude", "commands"), "user", "")
		add(filepath.Join(userHome, ".pi-go", "commands"), "user", "")
	}
	add(findNearestDir(root, filepath.Join(".claude", "commands")), "project", "")
	add(findNearestDir(root, filepath.Join(".pi-go", "commands")), "project", "")
	return dirs
}

// installedPluginCommandDirs lists command directories of plugins installed
// under piHome. A missing registry yields no directories.
func installedPluginCommandDirs(piHome string) []plugin.CommandRoot {
	registry, err := plugin.LoadRegistry(piHome)
	if err != nil {
		notice.Notifyf("warning: could not read the plugin registry: %v", err)
		return nil
	}
	return registry.CommandRoots(piHome)
}
