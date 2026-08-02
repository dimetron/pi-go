package extension

import (
	"bytes"
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"testing"

	"google.golang.org/adk/v2/agent"
	"google.golang.org/adk/v2/model"
	"google.golang.org/genai"

	"github.com/dimetron/pi-go/internal/tools"
)

// writePNG writes a small 2x1 PNG to path.
func writePNG(t *testing.T, path string) {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, 2, 1))
	img.Set(0, 0, color.RGBA{R: 255, A: 255})
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if err := png.Encode(f, img); err != nil {
		t.Fatal(err)
	}
}

// newTestSandbox returns a sandbox rooted at a fresh temp dir.
func newTestSandbox(t *testing.T) *tools.Sandbox {
	t.Helper()
	sb, err := tools.NewSandbox(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { sb.Close() })
	return sb
}

func TestBuildReadImageCallback(t *testing.T) {
	sb := newTestSandbox(t)
	path := filepath.Join(sb.Dir(), "shot.png")
	writePNG(t, path)

	t.Run("injects inline data part", func(t *testing.T) {
		cb := BuildReadImageCallback(sb)
		// Build a request whose user-turn Content contains a read_image
		// FunctionResponse referencing the screenshot path.
		req := &model.LLMRequest{
			Contents: []*genai.Content{
				{
					Role: "user",
					Parts: []*genai.Part{
						{
							FunctionResponse: &genai.FunctionResponse{
								Name: "read_image",
								Response: map[string]any{
									"path": path,
								},
							},
						},
					},
				},
			},
		}
		ctx := &mockReadonlyContext{}
		resp, err := cb(ctx, req)
		if err != nil {
			t.Fatalf("callback: %v", err)
		}
		if resp != nil {
			t.Error("expected nil response (callback must not short-circuit the model)")
		}
		parts := req.Contents[0].Parts
		if len(parts) != 2 {
			t.Fatalf("expected 2 parts (FunctionResponse + InlineData), got %d", len(parts))
		}
		imgPart := parts[1]
		if imgPart.InlineData == nil {
			t.Fatal("expected an InlineData part to be injected")
		}
		if imgPart.InlineData.MIMEType != "image/png" {
			t.Errorf("expected image/png mime, got %q", imgPart.InlineData.MIMEType)
		}
		if len(imgPart.InlineData.Data) == 0 {
			t.Error("expected non-empty image data")
		}
	})

	t.Run("ignores non-read_image tools", func(t *testing.T) {
		cb := BuildReadImageCallback(sb)
		req := &model.LLMRequest{
			Contents: []*genai.Content{
				{
					Role: "user",
					Parts: []*genai.Part{
						{
							FunctionResponse: &genai.FunctionResponse{
								Name:     "bash",
								Response: map[string]any{"output": "hi"},
							},
						},
					},
				},
			},
		}
		ctx := &mockReadonlyContext{}
		if _, err := cb(ctx, req); err != nil {
			t.Fatalf("callback: %v", err)
		}
		if len(req.Contents[0].Parts) != 1 {
			t.Error("expected no parts to be added for a non-read_image tool")
		}
	})

	t.Run("missing path is skipped", func(t *testing.T) {
		cb := BuildReadImageCallback(sb)
		req := &model.LLMRequest{
			Contents: []*genai.Content{
				{
					Role: "user",
					Parts: []*genai.Part{
						{
							FunctionResponse: &genai.FunctionResponse{
								Name:     "read_image",
								Response: map[string]any{},
							},
						},
					},
				},
			},
		}
		ctx := &mockReadonlyContext{}
		if _, err := cb(ctx, req); err != nil {
			t.Fatalf("callback: %v", err)
		}
		if len(req.Contents[0].Parts) != 1 {
			t.Error("expected no part added when path is missing")
		}
	})

	t.Run("nil sandbox is a no-op", func(t *testing.T) {
		cb := BuildReadImageCallback(nil)
		req := &model.LLMRequest{
			Contents: []*genai.Content{
				{Role: "user", Parts: []*genai.Part{{Text: "hi"}}},
			},
		}
		if _, err := cb(&mockReadonlyContext{}, req); err != nil {
			t.Fatalf("callback: %v", err)
		}
		if len(req.Contents[0].Parts) != 1 {
			t.Error("expected no change with nil sandbox")
		}
	})

	t.Run("injects real mascot png", func(t *testing.T) {
		// Resolve repo root from this test file: .../pi-go/internal/extension
		repoRoot, err := filepath.Abs(filepath.Join("..", ".."))
		if err != nil {
			t.Fatalf("resolve repo root: %v", err)
		}
		relPath := filepath.Join("docs", "screen", "pi-go-mascot.png")
		absPath := filepath.Join(repoRoot, relPath)
		info, err := os.Stat(absPath)
		if err != nil {
			t.Skipf("mascot screenshot not present at %s (skipping): %v", absPath, err)
		}
		rawBytes, err := os.ReadFile(absPath)
		if err != nil {
			t.Fatalf("read mascot: %v", err)
		}

		// Sandbox rooted at the repo root so the relative path the agent would
		// pass to read_image resolves cleanly.
		sb, err := tools.NewSandbox(repoRoot)
		if err != nil {
			t.Fatalf("NewSandbox(%s): %v", repoRoot, err)
		}
		t.Cleanup(func() { sb.Close() })

		cb := BuildReadImageCallback(sb)
		req := &model.LLMRequest{
			Contents: []*genai.Content{
				{
					Role: "user",
					Parts: []*genai.Part{
						{
							FunctionResponse: &genai.FunctionResponse{
								Name:     "read_image",
								Response: map[string]any{"path": relPath},
							},
						},
					},
				},
			},
		}
		if _, err := cb(&mockReadonlyContext{}, req); err != nil {
			t.Fatalf("callback: %v", err)
		}

		parts := req.Contents[0].Parts
		if len(parts) != 2 {
			t.Fatalf("expected 2 parts (FunctionResponse + InlineData), got %d", len(parts))
		}
		img := parts[1].InlineData
		if img == nil {
			t.Fatal("expected InlineData part to be injected for real mascot")
		}
		if img.MIMEType != "image/png" {
			t.Errorf("MIMEType = %q, want image/png", img.MIMEType)
		}
		if int64(len(img.Data)) != info.Size() {
			t.Errorf("InlineData size = %d, want %d (file on disk)", len(img.Data), info.Size())
		}
		if !bytes.Equal(img.Data, rawBytes) {
			t.Error("InlineData bytes do not match the on-disk mascot file (callback must re-read, not synthesize)")
		}
	})
}

// Ensure mockReadonlyContext satisfies agent.Context for the callback signature.
var _ agent.Context = (*mockReadonlyContext)(nil)
