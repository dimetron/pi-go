package extension

import (
	"errors"
	"io/fs"
	"sync"

	"google.golang.org/adk/v2/agent"
	"google.golang.org/adk/v2/agent/llmagent"
	"google.golang.org/adk/v2/model"
	"google.golang.org/genai"

	"github.com/dimetron/pi-go/internal/notice"
	"github.com/dimetron/pi-go/internal/tools"
)

// readImageToolName is the name of the core read_image tool whose results this
// callback turns into visible image parts.
const readImageToolName = "read_image"

// BuildReadImageCallback returns a BeforeModelCallback that finds the newest
// read_image FunctionResponse in the request and injects the underlying image
// bytes as an InlineData part on the Content that carried it. This is what
// makes a screenshot actually visible to a vision-capable model.
//
// The read_image tool returns only the path + metadata (never the bytes), so
// the bytes never transit the text channel. The callback re-reads the file
// (sandboxed) and appends a genai.Part{InlineData} alongside the text result.
//
// Two things bound the work, because a BeforeModelCallback runs once per model
// request — that is once per agentic step, not once per user turn, and ADK
// rebuilds req.Contents from the session events each step, so nothing injected
// here survives into the next one:
//
//   - Providers whose adapter drops InlineData get a no-op callback. Reading
//     the file would be pure waste there; see providerForwardsInlineImages.
//   - Only the most recent Content carrying read_image results is injected
//     into, not every such Content in the history. Walking the whole history
//     re-read and re-uploaded every screenshot the session had ever taken on
//     every step. The trade is that only the latest image stays visible; an
//     older one is still in context as its textual path + metadata.
func BuildReadImageCallback(sb *tools.Sandbox, providerName string) llmagent.BeforeModelCallback {
	if !providerForwardsInlineImages(providerName) {
		return func(agent.Context, *model.LLMRequest) (*model.LLMResponse, error) {
			return nil, nil
		}
	}
	inj := &readImageInjector{sb: sb, warned: make(map[string]bool)}
	return func(_ agent.Context, req *model.LLMRequest) (*model.LLMResponse, error) {
		inj.inject(req)
		return nil, nil
	}
}

// readImageInjector holds the per-callback state: the sandbox to read through,
// and the set of paths already warned about, so a file that stays unreadable
// produces one notice rather than one per agentic step.
type readImageInjector struct {
	sb *tools.Sandbox

	mu     sync.Mutex
	warned map[string]bool
}

// inject appends the image bytes behind the newest read_image results to the
// Content that carried them. A nil request or sandbox is a no-op.
func (in *readImageInjector) inject(req *model.LLMRequest) {
	if req == nil || in.sb == nil {
		return
	}
	content := latestReadImageContent(req.Contents)
	if content == nil {
		return
	}
	// The image parts go after the FunctionResponse on the same Content so the
	// model sees path+metadata and the image together in one turn.
	content.Parts = append(content.Parts, in.readImageParts(content.Parts)...)
}

// latestReadImageContent returns the last Content in contents that carries at
// least one read_image FunctionResponse, or nil when none does.
func latestReadImageContent(contents []*genai.Content) *genai.Content {
	for i := len(contents) - 1; i >= 0; i-- {
		content := contents[i]
		if content == nil {
			continue
		}
		for _, part := range content.Parts {
			if _, ok := readImagePath(part); ok {
				return content
			}
		}
	}
	return nil
}

// readImageParts reads the file behind every read_image FunctionResponse in
// parts and returns the inline image parts to append, in the order the
// responses appear. A file that cannot be read is skipped — the textual path
// result still stands on its own.
func (in *readImageInjector) readImageParts(parts []*genai.Part) []*genai.Part {
	var out []*genai.Part
	for _, part := range parts {
		path, ok := readImagePath(part)
		if !ok {
			continue
		}
		data, err := in.sb.ReadFile(path)
		if err != nil {
			in.warn(path, err)
			continue
		}
		out = append(out, genai.NewPartFromBytes(data, detectImageMIMEType(data, path)))
	}
	return out
}

// warn reports a path the callback could not read, at most once per path.
//
// A file that is simply gone is not reported at all: screenshots land in
// scratch directories the agent cleans up itself, so a deleted image is the
// normal end of its life, not a problem the user can act on.
func (in *readImageInjector) warn(path string, err error) {
	if errors.Is(err, fs.ErrNotExist) {
		return
	}
	in.mu.Lock()
	defer in.mu.Unlock()
	if in.warned[path] {
		return
	}
	in.warned[path] = true
	notice.Notifyf("warning: read_image callback could not read %q: %v", path, err)
}

// readImagePath returns the path a read_image FunctionResponse reported, and
// whether part is such a response at all.
func readImagePath(part *genai.Part) (string, bool) {
	if part == nil || part.FunctionResponse == nil {
		return "", false
	}
	fr := part.FunctionResponse
	if fr.Name != readImageToolName {
		return "", false
	}
	path, ok := fr.Response["path"].(string)
	if !ok || path == "" {
		return "", false
	}
	return path, true
}

// detectImageMIMEType sniffs an image's MIME type from its bytes, falling back
// to "image/png" for common screenshot extensions when the sniff is ambiguous.
func detectImageMIMEType(data []byte, path string) string {
	detected := tools.DetectImageMIMEType(data)
	if detected == "application/octet-stream" {
		switch {
		case hasExt(path, ".png"):
			return "image/png"
		case hasExt(path, ".jpg") || hasExt(path, ".jpeg"):
			return "image/jpeg"
		case hasExt(path, ".gif"):
			return "image/gif"
		case hasExt(path, ".webp"):
			return "image/webp"
		}
	}
	return detected
}

func hasExt(path, ext string) bool {
	return len(path) >= len(ext) && path[len(path)-len(ext):] == ext
}
