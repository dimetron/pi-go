package tools

import (
	"fmt"
	"io/fs"
	"path/filepath"
	"strings"

	"google.golang.org/adk/tool"
)

const (
	defaultTreeDepth  = 3
	maxTreeDepth      = 10
	maxTreeEntries    = 500
	treeConnector     = "├── "
	treeLastConnector = "└── "
	treeIndent        = "│   "
	treeLastIndent    = "    "
)

// skip hidden dirs and common non-code dirs
var skipDirs = map[string]bool{
	"node_modules": true, "vendor": true, "__pycache__": true,
	".git": true, ".hg": true, ".svn": true, ".idea": true, ".vscode": true,
	"dist": true, "build": true, ".next": true, ".cache": true,
}

// TreeInput defines the parameters for the tree tool.
type TreeInput struct {
	// Directory path to show. Defaults to current directory.
	Path string `json:"path,omitempty"`
	// Maximum depth to recurse. Default 3, max 10.
	Depth int `json:"depth,omitempty"`
}

// TreeOutput contains the tree listing.
type TreeOutput struct {
	Tree  string `json:"tree"`
	Dirs  int    `json:"dirs"`
	Files int    `json:"files"`
}

func newTreeTool(sb *Sandbox) (tool.Tool, error) {
	return newTool("tree", "Show directory tree structure. Returns an indented tree view of files and directories, useful for quickly understanding project layout. Skips hidden and common non-code directories.", func(_ tool.Context, input TreeInput) (TreeOutput, error) {
		return treeHandler(sb, input)
	})
}

func treeHandler(sb *Sandbox, input TreeInput) (TreeOutput, error) {
	root := input.Path
	if root == "" {
		root = "."
	}

	depth := input.Depth
	if depth <= 0 {
		depth = defaultTreeDepth
	}
	if depth > maxTreeDepth {
		depth = maxTreeDepth
	}

	rel, err := sb.Resolve(root)
	if err != nil {
		return TreeOutput{}, err
	}

	fsys := sb.FS()
	var b strings.Builder
	b.WriteString(root)
	b.WriteString("\n")

	dirs, files, count := 0, 0, 0
	buildTree(fsys, rel, "", depth, &b, &dirs, &files, &count)

	summary := fmt.Sprintf("\n%d directories, %d files", dirs, files)
	if count >= maxTreeEntries {
		summary += " (truncated)"
	}
	b.WriteString(summary)

	return TreeOutput{
		Tree:  truncateOutput(b.String()),
		Dirs:  dirs,
		Files: files,
	}, nil
}

func buildTree(fsys fs.FS, dir, prefix string, depth int, b *strings.Builder, dirs, files, count *int) {
	if depth <= 0 || *count >= maxTreeEntries {
		return
	}

	entries, err := fs.ReadDir(fsys, dir)
	if err != nil {
		return
	}

	// Filter out hidden dirs and skip dirs.
	var visible []fs.DirEntry
	for _, e := range entries {
		name := e.Name()
		if strings.HasPrefix(name, ".") && e.IsDir() {
			continue
		}
		if e.IsDir() && skipDirs[name] {
			continue
		}
		visible = append(visible, e)
	}

	for i, e := range visible {
		if *count >= maxTreeEntries {
			b.WriteString(prefix + treeLastConnector + "... (truncated)\n")
			return
		}
		*count++

		isLast := i == len(visible)-1
		connector := treeConnector
		childPrefix := prefix + treeIndent
		if isLast {
			connector = treeLastConnector
			childPrefix = prefix + treeLastIndent
		}

		name := e.Name()
		if e.IsDir() {
			*dirs++
			b.WriteString(prefix + connector + name + "/\n")
			buildTree(fsys, filepath.Join(dir, name), childPrefix, depth-1, b, dirs, files, count)
		} else {
			*files++
			b.WriteString(prefix + connector + name + "\n")
		}
	}
}
