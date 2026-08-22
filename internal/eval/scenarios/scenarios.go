// Package scenarios is the tool-coverage eval suite for the pi coding agent:
// one declarative scenario per tool family, each seeding a workspace and
// asking for something a competent agent does with specific tools, plus
// deterministic assertions over the trajectory and filesystem.
//
// The table is data; the runner (TestEvalTools, build tag e2e) and the
// grading (eval.EvaluateScenario) are generic. TestScenarios_CoverInventory
// fails when a tool registered by the agent has neither a scenario here nor
// an entry in Exclusions, so a new tool cannot ship unmeasured. See README.md.
package scenarios

import (
	"bytes"
	"image"
	"image/color"
	"image/png"
	"time"

	"github.com/dimetron/pi-go/internal/config"
	"github.com/dimetron/pi-go/internal/eval"
)

// GrepTool is the content-search tool's name: "grep" in a plain build, or
// "ripgrep" when rg is on PATH at startup. A scenario target that lists both
// accepts either.
const GrepTool = "grep|ripgrep"

// Exclusions lists registered tools the suite deliberately does not run, with
// the reason. Every inventoried tool must be here or in a scenario.
var Exclusions = []eval.Exclusion{
	{Tool: "a2a", Reason: "needs a remote A2A agent server (external service); not registered without a2a config"},
	{Tool: "palace-*", Reason: "needs a populated memory palace (embedding model + drawers); not registered in a fresh HOME"},
	{Tool: "google_search", Reason: "Gemini provider built-in, not a pi tool; only present when the eval model is a Gemini model"},
}

// Suite returns the scenarios in run order.
func Suite() []eval.Scenario {
	return []eval.Scenario{
		explore(),
		readFile(),
		writeFile(),
		editFile(),
		bashCmd(),
		bashBackground(),
		gitTools(),
		subagentSpawn(),
		sessionStats(),
		memoryTools(),
		readImage(),
		fetchDocs(),
		lspMin(),
		lspFull(),
	}
}

// Lookup returns the scenario with the given name.
func Lookup(name string) (eval.Scenario, bool) {
	for _, s := range Suite() {
		if s.Name == name {
			return s, true
		}
	}
	return eval.Scenario{}, false
}

func explore() eval.Scenario {
	return eval.Scenario{
		Name:        "explore",
		Description: "navigate a small tree with ls, tree, find and grep instead of bash",
		Tools:       []string{"ls", "tree", "find", GrepTool},
		Files: map[string]string{
			"README.md":      "# demo\n\nA tiny fixture project.\n",
			"docs/notes.md":  "# Notes\n\nThe magic token is NEEDLE-7f3a and lives only here.\n",
			"docs/guide.md":  "# Guide\n\nNothing to see.\n",
			"src/a.go":       "package src\n\n// A returns 1.\nfunc A() int { return 1 }\n",
			"src/util/b.go":  "package util\n\n// B returns 2.\nfunc B() int { return 2 }\n",
			"src/util/c.txt": "plain text\n",
		},
		Prompt: "Explore this workspace using the dedicated file tools, NOT bash. " +
			"Do all four steps: (1) call the tree tool on the root directory; " +
			"(2) call the ls tool on the src directory; " +
			"(3) use the find tool with pattern **/*.md to list every markdown file; " +
			"(4) use the grep tool (it may be named grep or ripgrep) to find which file contains the token NEEDLE-7f3a. " +
			"Then reply with the markdown file list and the file that contains the token.",
		Checks: []eval.Check{
			{Kind: eval.CheckToolArgContains, Tool: GrepTool, Text: "NEEDLE-7f3a"},
			{Kind: eval.CheckToolResultContains, Tool: GrepTool, Text: "notes.md"},
			{Kind: eval.CheckToolArgContains, Tool: "find", Text: ".md"},
		},
	}
}

func readFile() eval.Scenario {
	return eval.Scenario{
		Name:        "read",
		Description: "read a config file with the read tool",
		Tools:       []string{"read"},
		Files: map[string]string{
			"config/app.conf": "# app config\nname = demo\nport = 8471\nlog_level = info\n",
		},
		Prompt: "Use the read tool to read config/app.conf and tell me the value of the port setting. Do not use bash.",
		Checks: []eval.Check{
			{Kind: eval.CheckToolResultContains, Tool: "read", Text: "8471"},
		},
	}
}

func writeFile() eval.Scenario {
	return eval.Scenario{
		Name:        "write",
		Description: "create a new file with the write tool",
		Tools:       []string{"write"},
		Files:       map[string]string{"README.md": "# demo\n"},
		Prompt: "Using the write tool, create a file named hello.txt whose entire content is exactly the line " +
			"`hello from pi` followed by a newline. Do not use bash.",
		Checks: []eval.Check{
			{Kind: eval.CheckFileContains, Path: "hello.txt", Text: "hello from pi"},
		},
	}
}

func editFile() eval.Scenario {
	return eval.Scenario{
		Name:        "edit",
		Description: "change one string in a source file with read + edit",
		Tools:       []string{"read", "edit"},
		Files: map[string]string{
			"greet.go": "package greet\n\n// Greeting is what we say.\nconst Greeting = \"Hello\"\n\n// Name is who we greet.\nconst Name = \"World\"\n",
		},
		Prompt: "In greet.go, change the value of the Greeting constant from \"Hello\" to \"Howdy\". " +
			"Read the file with the read tool first, then apply the change with the edit tool. " +
			"Do not change anything else and do not use bash.",
		Checks: []eval.Check{
			{Kind: eval.CheckFileContains, Path: "greet.go", Text: "const Greeting = \"Howdy\""},
			{Kind: eval.CheckFileNotContains, Path: "greet.go", Text: "\"Hello\""},
			{Kind: eval.CheckFileContains, Path: "greet.go", Text: "const Name = \"World\""},
		},
	}
}

func bashCmd() eval.Scenario {
	return eval.Scenario{
		Name:        "bash",
		Description: "run a shell pipeline with the bash tool and capture its output in a file",
		Tools:       []string{"bash"},
		Files: map[string]string{
			"data/numbers.txt": "10\n20\n30\n40\n50\n",
		},
		Prompt: "Use the bash tool to count the lines in data/numbers.txt with `wc -l` and write just that number " +
			"into a file named count.txt using shell redirection (one bash command is fine). Then tell me the number.",
		Checks: []eval.Check{
			{Kind: eval.CheckFileContains, Path: "count.txt", Text: "5"},
			{Kind: eval.CheckToolArgContains, Tool: "bash", Arg: "command", Text: "wc"},
		},
	}
}

func bashBackground() eval.Scenario {
	return eval.Scenario{
		Name:        "bash-background",
		Description: "let a command outlive its timeout, then bash_wait on it and bash_kill another",
		Tools:       []string{"bash", "bash_wait", "bash_kill"},
		Files:       map[string]string{"README.md": "# demo\n"},
		Timeout:     8 * time.Minute,
		Prompt: "Two tasks, strictly in this order, using the bash tool and its companions bash_wait and bash_kill. " +
			"(1) Run the command `sleep 70; echo FINISHED-OK` with timeout 60000 (the minimum). It will exceed the " +
			"timeout and be moved to the background; the result carries running=true and a handle. Then call bash_wait " +
			"with that handle and wait_ms 30000, repeating if needed, until it reports running=false and the output " +
			"contains FINISHED-OK. " +
			"(2) Run the command `sleep 600` with timeout 60000. Once it has been moved to the background, stop it with " +
			"bash_kill using its handle. " +
			"Finally report the outcome of both tasks.",
		Checks: []eval.Check{
			{Kind: eval.CheckToolResultContains, Tool: "bash_wait", Text: "FINISHED-OK"},
			{Kind: eval.CheckToolArgContains, Tool: "bash_kill", Arg: "handle", Text: ""},
		},
	}
}

func gitTools() eval.Scenario {
	return eval.Scenario{
		Name:        "git",
		Description: "inspect a repo with uncommitted changes through git-overview, git-file-diff and git-hunk",
		Tools:       []string{"git-overview", "git-file-diff", "git-hunk"},
		Git:         true,
		Files: map[string]string{
			"README.md": "# demo\n",
			"main.go":   "package main\n\nimport \"fmt\"\n\nfunc main() {\n\tfmt.Println(\"version 1\")\n}\n",
			"util.go":   "package main\n\nfunc helper() int { return 1 }\n",
		},
		Modified: map[string]string{
			"main.go": "package main\n\nimport \"fmt\"\n\nfunc main() {\n\tfmt.Println(\"version 2\")\n\tfmt.Println(\"added line\")\n}\n",
		},
		Prompt: "This directory is a git repository with uncommitted changes. Without using bash: " +
			"(1) call git-overview to report the branch and which files changed; " +
			"(2) call git-file-diff for main.go; " +
			"(3) call git-hunk for main.go. " +
			"Then summarize in two sentences what changed in main.go.",
		Checks: []eval.Check{
			{Kind: eval.CheckToolResultContains, Tool: "git-overview", Text: "main.go"},
			{Kind: eval.CheckToolArgContains, Tool: "git-file-diff", Arg: "file", Text: "main.go"},
			{Kind: eval.CheckToolResultContains, Tool: "git-file-diff", Text: "version 2"},
			{Kind: eval.CheckToolArgContains, Tool: "git-hunk", Arg: "file", Text: "main.go"},
		},
	}
}

func subagentSpawn() eval.Scenario {
	return eval.Scenario{
		Name:        "subagent",
		Description: "delegate a lookup to the explore subagent and relay its answer",
		Tools:       []string{"subagent"},
		Timeout:     8 * time.Minute,
		Files: map[string]string{
			"pkg/alpha.go": "package pkg\n\n// Alpha does alpha things.\nfunc Alpha() int { return 1 }\n",
			"pkg/beta.go":  "package pkg\n\n// Beta does beta things.\nfunc Beta() int { return 2 }\n",
			"lib/gamma.go": "package lib\n\n// Frobnicate frobnicates.\nfunc Frobnicate() string { return \"frob\" }\n",
		},
		Prompt: "Use the subagent tool to spawn the `explore` agent with exactly this task: " +
			"\"Find which file in this directory defines the function Frobnicate and report its relative path.\" " +
			"Do not search for it yourself and do not use bash; relay the subagent's answer verbatim.",
		Checks: []eval.Check{
			{Kind: eval.CheckSubagentSpawned},
			{Kind: eval.CheckToolResultContains, Tool: "subagent", Text: "gamma"},
		},
	}
}

func sessionStats() eval.Scenario {
	return eval.Scenario{
		Name:        "session-stats",
		Description: "scan the session store for anomalies with session-stats",
		Tools:       []string{"session-stats"},
		Files:       map[string]string{"README.md": "# demo\n"},
		Prompt: "Call the session-stats tool with all=true to scan every recorded session, " +
			"then summarize in two sentences how many sessions it found and whether any were flagged.",
	}
}

func memoryTools() eval.Scenario {
	return eval.Scenario{
		Name:        "memory",
		Description: "search, fetch and walk the observation memory with mem-search, mem-get and mem-timeline",
		Tools:       []string{"mem-search", "mem-get", "mem-timeline"},
		Files:       map[string]string{"README.md": "# demo\n"},
		Memory: []eval.MemorySeed{
			{
				Title: "Widget rotation bug in the renderer",
				Type:  "bugfix",
				Text: "Widget rotation bug: icons rendered rotated by 90 degrees because the matrix rows were " +
					"swapped in rotate.go. Fixed by transposing the matrix before the multiply.",
			},
			{
				Title: "Decision: SQLite for the local cache",
				Type:  "decision",
				Text:  "Chose SQLite over a flat file for the local cache because concurrent writers need locking.",
			},
		},
		Prompt: "Search the persistent observation memory. (1) Call mem-search with query `widget rotation`. " +
			"(2) Call mem-get with the id of the top result. (3) Call mem-timeline with that id as the anchor. " +
			"Then report in one sentence what the memory says caused the rotation bug.",
		Checks: []eval.Check{
			{Kind: eval.CheckToolResultContains, Tool: "mem-search", Text: "rotation"},
		},
	}
}

func readImage() eval.Scenario {
	return eval.Scenario{
		Name:        "read-image",
		Description: "look at a PNG with read_image (vision)",
		Tools:       []string{"read_image"},
		Requires:    []string{"vision"},
		Files:       map[string]string{"logo.png": solidPNG(color.RGBA{R: 220, G: 20, B: 20, A: 255})},
		Prompt: "Use the read_image tool to look at logo.png in this directory and tell me its dominant color " +
			"in one word. Do not use bash.",
	}
}

func fetchDocs() eval.Scenario {
	return eval.Scenario{
		Name:        "fetch-docs",
		Description: "pull a public llms.txt with fetch_docs (needs outbound HTTPS)",
		Tools:       []string{"fetch_docs"},
		Requires:    []string{"network"},
		Files:       map[string]string{"README.md": "# demo\n"},
		LLMS:        []config.LLMSSource{{Name: "llmstxt", URL: "https://llmstxt.org/llms.txt"}},
		Prompt: "Use the fetch_docs tool to fetch https://llmstxt.org/llms.txt and then summarize in two sentences " +
			"what the llms.txt proposal is.",
		Checks: []eval.Check{
			{Kind: eval.CheckToolArgContains, Tool: "fetch_docs", Arg: "url", Text: "llmstxt.org"},
			{Kind: eval.CheckToolResultContains, Tool: "fetch_docs", Text: "llms.txt"},
		},
	}
}

// lspFixture is a tiny Go module with a deliberate compile error (an unused
// variable) so diagnostics have something to report, and two files so
// references and definitions cross a file boundary.
//
// main.go, 0-based positions: `Alpha` declaration at line 4 col 5; the call
// `Alpha()` inside main at line 9 col 12.
var lspFixture = map[string]string{
	"go.mod": "module example.com/lspfix\n\ngo 1.22\n",
	"main.go": "package main\n" + // 0
		"\n" + // 1
		"import \"fmt\"\n" + // 2
		"\n" + // 3
		"func Alpha() int { return 1 }\n" + // 4
		"\n" + // 5
		"func main() {\n" + // 6
		"\tvar unusedValue int\n" + // 7
		"\tfmt.Println(Beta())\n" + // 8
		"\tfmt.Println(Alpha())\n" + // 9
		"}\n", // 10
	"beta.go": "package main\n\n// Beta returns two.\nfunc Beta() int { return 2 }\n",
}

func lspMin() eval.Scenario {
	return eval.Scenario{
		Name:        "lsp",
		Description: "list symbols and diagnostics through the default (min) LSP tools",
		Tools:       []string{"lsp-symbols", "lsp-diagnostics"},
		Requires:    []string{"lsp"},
		Files:       lspFixture,
		Timeout:     6 * time.Minute,
		Prompt: "This is a Go module. Using only the language-server tools, not bash or go build: " +
			"(1) call lsp-symbols on main.go to list its symbols; " +
			"(2) call lsp-diagnostics on main.go and report every problem it finds — the language server " +
			"publishes diagnostics asynchronously, so if the first call reports no problems, call " +
			"lsp-diagnostics on main.go once more before concluding the file is clean. " +
			"Then summarize the symbols and the diagnostics.",
		Checks: []eval.Check{
			{Kind: eval.CheckToolResultContains, Tool: "lsp-symbols", Text: "Alpha"},
			{Kind: eval.CheckToolResultContains, Tool: "lsp-diagnostics", Text: "unusedValue"},
		},
	}
}

func lspFull() eval.Scenario {
	return eval.Scenario{
		Name:        "lsp-full",
		Description: "navigate code with the full LSP surface (hover, definition, references, workspace symbol, code action)",
		Tools:       []string{"lsp-hover", "lsp-definition", "lsp-references", "lsp-workspace-symbol", "lsp-code-action"},
		Requires:    []string{"lsp"},
		Files:       lspFixture,
		Args:        []string{"--lsp", "full"},
		Timeout:     8 * time.Minute,
		Prompt: "This is a Go module. Using only the language-server tools (not bash or go build), do all five " +
			"steps; lines and columns are 0-based: " +
			"(1) call lsp-workspace-symbol with query Alpha; " +
			"(2) call lsp-definition for the call to Alpha in main.go at line 9, column 14; " +
			"(3) call lsp-references for Alpha at its declaration in main.go, line 4, column 5; " +
			"(4) call lsp-hover on main.go at line 4, column 5; " +
			"(5) call lsp-code-action on main.go for the range startLine 7, startCol 1, endLine 7, endCol 20. " +
			"Then report what each call returned.",
		Checks: []eval.Check{
			{Kind: eval.CheckToolArgContains, Tool: "lsp-workspace-symbol", Arg: "query", Text: "Alpha"},
			{Kind: eval.CheckToolResultContains, Tool: "lsp-references", Text: "main.go"},
		},
	}
}

// solidPNG renders a 16x16 PNG of one color as a string of bytes, so the
// fixture needs no binary file in the repo.
func solidPNG(c color.RGBA) string {
	img := image.NewRGBA(image.Rect(0, 0, 16, 16))
	for y := 0; y < 16; y++ {
		for x := 0; x < 16; x++ {
			img.SetRGBA(x, y, c)
		}
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		panic(err) // a fixed-size in-memory encode cannot fail
	}
	return buf.String()
}
