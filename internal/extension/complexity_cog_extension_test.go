package extension

import (
	"bytes"
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"google.golang.org/adk/v2/model"
	"google.golang.org/genai"

	"github.com/dimetron/pi-go/internal/tools"
)

// These tests pin the branch structure of BuildReadImageCallback,
// parseSkillContent and LoadBundledSkills before those functions were
// flattened for cognitive complexity. Every skip, fallback and boundary the
// original nesting encoded gets a case here, so the refactor is provably a
// no-op rather than a rewrite that merely agrees with itself.

// cogSandbox returns a sandbox rooted at a fresh temp dir.
func cogSandbox(t *testing.T) *tools.Sandbox {
	t.Helper()
	sb, err := tools.NewSandbox(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { sb.Close() })
	return sb
}

// cogPNGBytes returns the bytes of a small 2x1 PNG.
func cogPNGBytes(t *testing.T) []byte {
	t.Helper()
	var buf bytes.Buffer
	img := image.NewRGBA(image.Rect(0, 0, 2, 1))
	img.Set(0, 0, color.RGBA{G: 255, A: 255})
	if err := png.Encode(&buf, img); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

// cogWriteImage writes a PNG into the sandbox and returns its absolute path.
func cogWriteImage(t *testing.T, sb *tools.Sandbox, name string) string {
	t.Helper()
	path := filepath.Join(sb.Dir(), name)
	if err := os.WriteFile(path, cogPNGBytes(t), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

// cogRespPart builds a FunctionResponse part with the given tool name and
// response map.
func cogRespPart(name string, resp map[string]any) *genai.Part {
	return &genai.Part{FunctionResponse: &genai.FunctionResponse{Name: name, Response: resp}}
}

// cogInlineCount counts the InlineData parts on a Content.
func cogInlineCount(c *genai.Content) int {
	n := 0
	for _, p := range c.Parts {
		if p != nil && p.InlineData != nil {
			n++
		}
	}
	return n
}

// TestReadImageCallback_SkipsEverythingButReadImageResults walks the skip
// ladder the callback encodes: nil request, nil sandbox, nil Content, nil
// part, a part with no FunctionResponse, a FunctionResponse from another
// tool, and a read_image response whose "path" is missing, non-string or
// empty. None of these may produce an InlineData part, and none may panic.
func TestReadImageCallback_SkipsEverythingButReadImageResults(t *testing.T) {
	sb := cogSandbox(t)

	t.Run("nil request", func(t *testing.T) {
		resp, err := BuildReadImageCallback(sb)(&mockReadonlyContext{}, nil)
		if resp != nil || err != nil {
			t.Fatalf("callback(nil req) = %v, %v; want nil, nil", resp, err)
		}
	})

	t.Run("nil sandbox leaves parts untouched", func(t *testing.T) {
		content := &genai.Content{
			Role:  "user",
			Parts: []*genai.Part{cogRespPart(readImageToolName, map[string]any{"path": "shot.png"})},
		}
		req := &model.LLMRequest{Contents: []*genai.Content{content}}
		if _, err := BuildReadImageCallback(nil)(&mockReadonlyContext{}, req); err != nil {
			t.Fatalf("callback: %v", err)
		}
		if len(content.Parts) != 1 {
			t.Fatalf("parts = %d, want 1 (nil sandbox must not inject)", len(content.Parts))
		}
	})

	skipCases := []struct {
		name string
		part *genai.Part
	}{
		{"nil part", nil},
		{"text part", &genai.Part{Text: "hello"}},
		{"empty part", &genai.Part{}},
		{"other tool", cogRespPart("read", map[string]any{"path": "shot.png"})},
		{"no path key", cogRespPart(readImageToolName, map[string]any{"size": 12})},
		{"non-string path", cogRespPart(readImageToolName, map[string]any{"path": 42})},
		{"empty path", cogRespPart(readImageToolName, map[string]any{"path": ""})},
		{"nil response map", &genai.Part{FunctionResponse: &genai.FunctionResponse{Name: readImageToolName}}},
	}
	for _, tc := range skipCases {
		t.Run(tc.name, func(t *testing.T) {
			content := &genai.Content{Role: "user", Parts: []*genai.Part{tc.part}}
			req := &model.LLMRequest{Contents: []*genai.Content{nil, content}}
			if _, err := BuildReadImageCallback(sb)(&mockReadonlyContext{}, req); err != nil {
				t.Fatalf("callback: %v", err)
			}
			if got := len(content.Parts); got != 1 {
				t.Fatalf("parts = %d, want 1 (nothing should have been injected)", got)
			}
			if n := cogInlineCount(content); n != 0 {
				t.Fatalf("inline parts = %d, want 0", n)
			}
		})
	}
}

// TestReadImageCallback_UnreadablePathIsWarnedNotFatal pins that a read
// failure only skips that one part: the callback still returns nil, nil and
// still injects the image for a sibling part that does resolve.
func TestReadImageCallback_UnreadablePathIsWarnedNotFatal(t *testing.T) {
	sb := cogSandbox(t)
	good := cogWriteImage(t, sb, "good.png")

	content := &genai.Content{
		Role: "user",
		Parts: []*genai.Part{
			cogRespPart(readImageToolName, map[string]any{"path": filepath.Join(sb.Dir(), "missing.png")}),
			cogRespPart(readImageToolName, map[string]any{"path": good}),
		},
	}
	req := &model.LLMRequest{Contents: []*genai.Content{content}}

	resp, err := BuildReadImageCallback(sb)(&mockReadonlyContext{}, req)
	if resp != nil || err != nil {
		t.Fatalf("callback = %v, %v; want nil, nil", resp, err)
	}
	if got := len(content.Parts); got != 3 {
		t.Fatalf("parts = %d, want 3 (2 responses + 1 injected image)", got)
	}
	if n := cogInlineCount(content); n != 1 {
		t.Fatalf("inline parts = %d, want 1 (only the readable path)", n)
	}
	if got := content.Parts[2].InlineData.MIMEType; got != "image/png" {
		t.Errorf("MIMEType = %q, want image/png", got)
	}
}

// TestReadImageCallback_AppendsPerContentInOrder pins that images are appended
// to the Content that carried the response — not pooled onto the first one —
// and that two responses on one Content append in the order they appear.
func TestReadImageCallback_AppendsPerContentInOrder(t *testing.T) {
	sb := cogSandbox(t)
	first := cogWriteImage(t, sb, "first.png")
	second := cogWriteImage(t, sb, "second.png")
	other := cogWriteImage(t, sb, "other.png")

	// Make the payloads distinguishable by size.
	if err := os.WriteFile(second, append(cogPNGBytes(t), make([]byte, 64)...), 0o600); err != nil {
		t.Fatal(err)
	}

	contentA := &genai.Content{
		Role: "user",
		Parts: []*genai.Part{
			cogRespPart(readImageToolName, map[string]any{"path": first}),
			{Text: "between"},
			cogRespPart(readImageToolName, map[string]any{"path": second}),
		},
	}
	contentB := &genai.Content{
		Role:  "user",
		Parts: []*genai.Part{cogRespPart(readImageToolName, map[string]any{"path": other})},
	}
	req := &model.LLMRequest{Contents: []*genai.Content{contentA, contentB}}

	if _, err := BuildReadImageCallback(sb)(&mockReadonlyContext{}, req); err != nil {
		t.Fatalf("callback: %v", err)
	}

	if got := len(contentA.Parts); got != 5 {
		t.Fatalf("contentA parts = %d, want 5", got)
	}
	firstBytes, err := os.ReadFile(first)
	if err != nil {
		t.Fatal(err)
	}
	secondBytes, err := os.ReadFile(second)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(contentA.Parts[3].InlineData.Data, firstBytes) {
		t.Error("contentA.Parts[3] is not the first image: injection order changed")
	}
	if !bytes.Equal(contentA.Parts[4].InlineData.Data, secondBytes) {
		t.Error("contentA.Parts[4] is not the second image: injection order changed")
	}
	if got := len(contentB.Parts); got != 2 {
		t.Fatalf("contentB parts = %d, want 2 (its own image, not contentA's)", got)
	}
	if cogInlineCount(contentB) != 1 {
		t.Error("contentB did not receive its own inline image")
	}
}

// TestParseSkillContent_Boundaries pins the frontmatter state machine: when
// the delimiter opens and closes it, what counts as a frontmatter line, how
// "tools" is split, and which lines land in the body.
func TestParseSkillContent_Boundaries(t *testing.T) {
	tests := []struct {
		name     string
		content  string
		defName  string
		wantName string
		wantDesc string
		wantTool []string
		wantBody string
	}{
		{
			name:     "no frontmatter at all",
			content:  "just a body\nsecond line\n",
			defName:  "fallback",
			wantName: "fallback",
			wantBody: "just a body\nsecond line",
		},
		{
			name:     "empty content",
			content:  "",
			defName:  "fallback",
			wantName: "fallback",
			wantBody: "",
		},
		{
			name:     "full frontmatter overrides default name",
			content:  "---\nname: real-name\ndescription: does a thing\ntools: read, write\n---\nBody here.\n",
			defName:  "fallback",
			wantName: "real-name",
			wantDesc: "does a thing",
			wantTool: []string{"read", "write"},
			wantBody: "Body here.",
		},
		{
			name:     "indented delimiter still opens frontmatter",
			content:  "   ---   \nname: trimmed\n---\nbody\n",
			defName:  "fallback",
			wantName: "trimmed",
			wantBody: "body",
		},
		{
			name:     "unknown keys and colonless lines are ignored",
			content:  "---\nname: keeper\nno-colon-here\nunknown: value\n---\nbody\n",
			defName:  "fallback",
			wantName: "keeper",
			wantBody: "body",
		},
		{
			name:     "tools drops empty entries and trims each",
			content:  "---\ntools:  read , , write ,\n---\n",
			defName:  "fallback",
			wantName: "fallback",
			wantTool: []string{"read", "write"},
			wantBody: "",
		},
		{
			name:     "empty tools value yields no tools",
			content:  "---\ntools:\n---\nbody\n",
			defName:  "fallback",
			wantName: "fallback",
			wantBody: "body",
		},
		{
			name:     "value keeps colons after the first",
			content:  "---\ndescription: see http://example.com/x\n---\n",
			defName:  "fallback",
			wantName: "fallback",
			wantDesc: "see http://example.com/x",
			wantBody: "",
		},
		{
			name:     "a later --- is body, not a new frontmatter block",
			content:  "---\nname: once\n---\nbefore\n---\nafter: not-frontmatter\n",
			defName:  "fallback",
			wantName: "once",
			wantBody: "before\n---\nafter: not-frontmatter",
		},
		{
			name:     "unterminated frontmatter consumes the whole file",
			content:  "---\nname: swallowed\ndescription: everything\nbody line\n",
			defName:  "fallback",
			wantName: "swallowed",
			wantDesc: "everything",
			wantBody: "",
		},
		{
			name:     "body-only lines before an opening delimiter are body",
			content:  "preamble\n---\nname: late\n---\ntail\n",
			defName:  "fallback",
			wantName: "late",
			wantBody: "preamble\ntail",
		},
		{
			name:     "empty frontmatter block leaves defaults",
			content:  "---\n---\nbody\n",
			defName:  "fallback",
			wantName: "fallback",
			wantBody: "body",
		},
		{
			name:     "body whitespace is trimmed at both ends",
			content:  "---\nname: n\n---\n\n\n  padded  \n\n",
			defName:  "fallback",
			wantName: "n",
			wantBody: "padded",
		},
		{
			name:     "empty name value clears the default",
			content:  "---\nname:\n---\nbody\n",
			defName:  "fallback",
			wantName: "",
			wantBody: "body",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			skill, body, err := parseSkillContent(tc.content, tc.defName)
			if err != nil {
				t.Fatalf("parseSkillContent: %v", err)
			}
			if skill.Name != tc.wantName {
				t.Errorf("Name = %q, want %q", skill.Name, tc.wantName)
			}
			if skill.Description != tc.wantDesc {
				t.Errorf("Description = %q, want %q", skill.Description, tc.wantDesc)
			}
			if body != tc.wantBody {
				t.Errorf("body = %q, want %q", body, tc.wantBody)
			}
			if len(skill.Tools) != len(tc.wantTool) {
				t.Fatalf("Tools = %v, want %v", skill.Tools, tc.wantTool)
			}
			for i, want := range tc.wantTool {
				if skill.Tools[i] != want {
					t.Errorf("Tools[%d] = %q, want %q", i, skill.Tools[i], want)
				}
			}
		})
	}
}

// TestParseSkillContent_ScannerErrorSurfaces pins that a line longer than
// bufio.Scanner's buffer is reported as an error rather than silently
// truncating the skill.
func TestParseSkillContent_ScannerErrorSurfaces(t *testing.T) {
	huge := strings.Repeat("x", 128*1024)
	_, _, err := parseSkillContent(huge, "big")
	if err == nil {
		t.Fatal("expected a scanner error for an over-long line, got nil")
	}
}

// TestLoadBundledSkills_ShapeOfEveryEntry pins the invariants the walk
// enforces: only files under a skill subdirectory are collected, each is
// filed under that subdirectory's name, and RelPath is the full embedded
// path with content attached.
func TestLoadBundledSkills_ShapeOfEveryEntry(t *testing.T) {
	got, err := LoadBundledSkills()
	if err != nil {
		t.Fatalf("LoadBundledSkills: %v", err)
	}
	if len(got) == 0 {
		t.Fatal("expected at least one bundled skill")
	}
	for skillName, files := range got {
		if skillName == "" {
			t.Error("bundled skill has an empty name")
		}
		if strings.Contains(skillName, "/") {
			t.Errorf("skill name %q contains a slash: only the first path segment is the name", skillName)
		}
		if len(files) == 0 {
			t.Errorf("skill %q has no files", skillName)
		}
		cogCheckBundledFiles(t, skillName, files)
	}
}

// cogCheckBundledFiles asserts every file of one bundled skill is filed under
// that skill, carries its full embedded path, and has content.
func cogCheckBundledFiles(t *testing.T, skillName string, files []BundledSkillFile) {
	t.Helper()
	wantPrefix := "bundled_skills/" + skillName + "/"
	for _, f := range files {
		if f.SkillName != skillName {
			t.Errorf("file %q filed under %q but SkillName = %q", f.RelPath, skillName, f.SkillName)
		}
		if !strings.HasPrefix(f.RelPath, wantPrefix) {
			t.Errorf("RelPath = %q, want prefix %q", f.RelPath, wantPrefix)
		}
		if len(f.Content) == 0 {
			t.Errorf("file %q has empty content", f.RelPath)
		}
	}
}

// TestLoadBundledSkills_Deterministic pins that repeated calls produce the
// same skill set and the same per-skill file counts — the walk keeps no
// state between calls.
func TestLoadBundledSkills_Deterministic(t *testing.T) {
	first, err := LoadBundledSkills()
	if err != nil {
		t.Fatalf("LoadBundledSkills: %v", err)
	}
	second, err := LoadBundledSkills()
	if err != nil {
		t.Fatalf("LoadBundledSkills: %v", err)
	}
	if len(first) != len(second) {
		t.Fatalf("skill count = %d then %d", len(first), len(second))
	}
	for name, files := range first {
		other, ok := second[name]
		if !ok {
			t.Errorf("skill %q missing from second call", name)
			continue
		}
		if len(files) != len(other) {
			t.Errorf("skill %q file count = %d then %d", name, len(files), len(other))
		}
	}
}

// TestLoadSkillBody_ErrorLadder pins each early return LoadSkillBody makes
// before it reaches the disk.
func TestLoadSkillBody_ErrorLadder(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "SKILL.md")
	if err := os.WriteFile(path, []byte("---\nname: on-disk\n---\nthe body\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	skills := []Skill{
		{Name: "missing-body-path", Source: "user"},
		{Name: "preloaded", Source: "user", Instruction: "already here"},
		{Name: "cog-bundled-cached", Source: "bundled"},
		{Name: "cog-bundled-uncached", Source: "bundled"},
		{Name: "on-disk", Source: "user", BodyPath: path},
		{Name: "unreadable", Source: "user", BodyPath: filepath.Join(dir, "nope", "SKILL.md")},
	}
	bundledBodyCache.Store("cog-bundled-cached", "cached bundled body")
	t.Cleanup(func() { bundledBodyCache.Delete("cog-bundled-cached") })

	t.Run("unknown skill", func(t *testing.T) {
		if _, err := LoadSkillBody(skills, "nope"); err == nil {
			t.Fatal("expected an error for an unknown skill")
		}
	})
	t.Run("preloaded instruction wins", func(t *testing.T) {
		got, err := LoadSkillBody(skills, "preloaded")
		if err != nil || got != "already here" {
			t.Fatalf("LoadSkillBody = %q, %v; want %q, nil", got, err, "already here")
		}
	})
	t.Run("bundled cached body", func(t *testing.T) {
		got, err := LoadSkillBody(skills, "cog-bundled-cached")
		if err != nil || got != "cached bundled body" {
			t.Fatalf("LoadSkillBody = %q, %v", got, err)
		}
	})
	t.Run("bundled without cache errors", func(t *testing.T) {
		if _, err := LoadSkillBody(skills, "cog-bundled-uncached"); err == nil {
			t.Fatal("expected an error for a bundled skill with no cached body")
		}
	})
	t.Run("no body path errors", func(t *testing.T) {
		if _, err := LoadSkillBody(skills, "missing-body-path"); err == nil {
			t.Fatal("expected an error for a skill with no BodyPath")
		}
	})
	t.Run("unreadable body path errors", func(t *testing.T) {
		if _, err := LoadSkillBody(skills, "unreadable"); err == nil {
			t.Fatal("expected an error for an unreadable BodyPath")
		}
	})
	t.Run("reads and then caches by path", func(t *testing.T) {
		got, err := LoadSkillBody(skills, "on-disk")
		if err != nil || got != "the body" {
			t.Fatalf("LoadSkillBody = %q, %v; want %q, nil", got, err, "the body")
		}
		// Delete the file: a second call must be served from the cache.
		if err := os.Remove(path); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { fileBodyCache.Delete(path) })
		again, err := LoadSkillBody(skills, "on-disk")
		if err != nil || again != "the body" {
			t.Fatalf("cached LoadSkillBody = %q, %v; want %q, nil", again, err, "the body")
		}
		if size, ok := SkillBodySize(skills, "on-disk"); !ok || size != len("the body") {
			t.Fatalf("SkillBodySize = %d, %v; want %d, true", size, ok, len("the body"))
		}
	})
}

// TestSkillBodySize_UnloadedReportsFalse pins that SkillBodySize never
// triggers I/O: an unloaded skill reports (0, false) rather than reading.
func TestSkillBodySize_UnloadedReportsFalse(t *testing.T) {
	skills := []Skill{
		{Name: "preloaded", Instruction: "abcd"},
		{Name: "cog-size-uncached", Source: "bundled"},
		{Name: "no-path", Source: "user"},
		{Name: "unread", Source: "user", BodyPath: filepath.Join(t.TempDir(), "SKILL.md")},
	}
	cases := []struct {
		name     string
		wantSize int
		wantOK   bool
	}{
		{"nope", 0, false},
		{"preloaded", 4, true},
		{"cog-size-uncached", 0, false},
		{"no-path", 0, false},
		{"unread", 0, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			size, ok := SkillBodySize(skills, tc.name)
			if size != tc.wantSize || ok != tc.wantOK {
				t.Errorf("SkillBodySize(%q) = %d, %v; want %d, %v", tc.name, size, ok, tc.wantSize, tc.wantOK)
			}
		})
	}
}

// TestBundledSkillName_PathBoundaries pins the path shapes that carry no skill
// name. The real embed fs cannot produce them — the walk is rooted at
// "bundled_skills" and every entry is nested — but the guards are what keeps a
// stray path from being filed under an empty skill name.
func TestBundledSkillName_PathBoundaries(t *testing.T) {
	tests := []struct {
		path string
		want string
		ok   bool
	}{
		{"bundled_skills/agents-md/SKILL.md", "agents-md", true},
		{"bundled_skills/agents-md/refs/extra.md", "agents-md", true},
		{"bundled_skills/agents-md/", "agents-md", true},
		{"bundled_skills/loose.md", "", false},
		{"bundled_skills/", "", false},
		{"bundled_skills", "", false},
		{"", "", false},
	}
	for _, tc := range tests {
		t.Run(tc.path, func(t *testing.T) {
			got, ok := bundledSkillName(tc.path)
			if got != tc.want || ok != tc.ok {
				t.Errorf("bundledSkillName(%q) = %q, %v; want %q, %v", tc.path, got, ok, tc.want, tc.ok)
			}
		})
	}
}

// TestReadImagePath_Ladder pins, on the helper directly, the same skip ladder
// TestReadImageCallback_SkipsEverythingButReadImageResults checks end to end.
func TestReadImagePath_Ladder(t *testing.T) {
	tests := []struct {
		name string
		part *genai.Part
		want string
		ok   bool
	}{
		{"nil part", nil, "", false},
		{"text part", &genai.Part{Text: "hi"}, "", false},
		{"other tool", cogRespPart("read", map[string]any{"path": "/a.png"}), "", false},
		{"nil response map", &genai.Part{
			FunctionResponse: &genai.FunctionResponse{Name: readImageToolName},
		}, "", false},
		{"missing path", cogRespPart(readImageToolName, map[string]any{"size": 1}), "", false},
		{"non-string path", cogRespPart(readImageToolName, map[string]any{"path": 1}), "", false},
		{"empty path", cogRespPart(readImageToolName, map[string]any{"path": ""}), "", false},
		{"good path", cogRespPart(readImageToolName, map[string]any{"path": "/a.png"}), "/a.png", true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := readImagePath(tc.part)
			if got != tc.want || ok != tc.ok {
				t.Errorf("readImagePath = %q, %v; want %q, %v", got, ok, tc.want, tc.ok)
			}
		})
	}
}

// TestSplitSkillTools_TrimAndDrop pins the "tools:" splitting rules on the
// helper directly, including the all-empty case that must yield no slice at
// all rather than a slice of empty strings.
func TestSplitSkillTools_TrimAndDrop(t *testing.T) {
	tests := []struct {
		value string
		want  []string
	}{
		{"", nil},
		{",", nil},
		{"   ,  ,  ", nil},
		{"read", []string{"read"}},
		{" read , write ", []string{"read", "write"}},
		{"read,,write,", []string{"read", "write"}},
	}
	for _, tc := range tests {
		t.Run(tc.value, func(t *testing.T) {
			got := splitSkillTools(tc.value)
			if len(got) != len(tc.want) {
				t.Fatalf("splitSkillTools(%q) = %v, want %v", tc.value, got, tc.want)
			}
			for i, want := range tc.want {
				if got[i] != want {
					t.Errorf("[%d] = %q, want %q", i, got[i], want)
				}
			}
		})
	}
}

// TestAppendOrOverrideSkill_IndexBookkeeping pins that an override replaces in
// place — keeping load order stable — while a new name is appended and its
// index recorded for a later override to find.
func TestAppendOrOverrideSkill_IndexBookkeeping(t *testing.T) {
	seen := map[string]int{}
	skills := appendOrOverrideSkill(nil, seen, Skill{Name: "a", Source: "bundled"})
	skills = appendOrOverrideSkill(skills, seen, Skill{Name: "b", Source: "bundled"})
	skills = appendOrOverrideSkill(skills, seen, Skill{Name: "a", Source: "project"})

	if len(skills) != 2 {
		t.Fatalf("len(skills) = %d, want 2 — an override must not append", len(skills))
	}
	if skills[0].Name != "a" || skills[0].Source != "project" {
		t.Errorf("skills[0] = %+v, want a/project replaced in place", skills[0])
	}
	if skills[1].Name != "b" {
		t.Errorf("skills[1] = %+v, want b to keep its slot", skills[1])
	}
	if seen["a"] != 0 || seen["b"] != 1 {
		t.Errorf("seen = %v, want a:0 b:1", seen)
	}
}
