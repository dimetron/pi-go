package extension

import (
	"fmt"
	"os"

	"google.golang.org/adk/v2/agent"
	"google.golang.org/adk/v2/agent/llmagent"
	"google.golang.org/adk/v2/model"
	"google.golang.org/genai"

	"github.com/dimetron/pi-go/internal/tools"
)

// readImageToolName is the name of the core read_image tool whose results this
// callback turns into visible image parts.
const readImageToolName = "read_image"

// BuildReadImageCallback returns a BeforeModelCallback that finds FunctionResponse
// parts from the read_image tool in the current turn and injects the underlying
// image bytes as an InlineData part on the same user Content. This is what makes
// a screenshot actually visible to a vision-capable model (Gemini, etc.).
//
// The read_image tool returns only the path + metadata (never the bytes), so the
// bytes never transit the text channel. The callback re-reads the file (sandboxed)
// and appends a genai.Part{InlineData} alongside the text result. Providers that
// forward InlineData (Gemini via ADK-native model) see the image; others simply
// ignore it and fall back to the textual path.
func BuildReadImageCallback(sb *tools.Sandbox) llmagent.BeforeModelCallback {
	return func(_ agent.Context, req *model.LLMRequest) (*model.LLMResponse, error) {
		injectReadImageParts(sb, req)
		return nil, nil
	}
}

// injectReadImageParts appends the image bytes behind each read_image result
// to the Content that carried the result. A nil request or sandbox is a no-op.
func injectReadImageParts(sb *tools.Sandbox, req *model.LLMRequest) {
	if req == nil || sb == nil {
		return
	}
	for _, content := range req.Contents {
		if content == nil {
			continue
		}
		// A read_image FunctionResponse is a user-turn part. We append the
		// image parts to the same Content after the FunctionResponse so the
		// model sees path+metadata and the image together in one turn.
		content.Parts = append(content.Parts, readImageParts(sb, content.Parts)...)
	}
}

// readImageParts reads the file behind every read_image FunctionResponse in
// parts and returns the inline image parts to append, in the order the
// responses appear. A file that cannot be read is warned about and skipped —
// the textual path result still stands on its own.
func readImageParts(sb *tools.Sandbox, parts []*genai.Part) []*genai.Part {
	var out []*genai.Part
	for _, part := range parts {
		path, ok := readImagePath(part)
		if !ok {
			continue
		}
		data, err := sb.ReadFile(path)
		if err != nil {
			fmt.Fprintf(os.Stderr, "pi-go: warning: read_image callback could not read %q: %v\n", path, err)
			continue
		}
		out = append(out, genai.NewPartFromBytes(data, detectImageMIMEType(data, path)))
	}
	return out
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
