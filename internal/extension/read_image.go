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
	return func(ctx agent.Context, req *model.LLMRequest) (*model.LLMResponse, error) {
		if req == nil || sb == nil {
			return nil, nil
		}
		for _, content := range req.Contents {
			if content == nil {
				continue
			}
			// A read_image FunctionResponse is a user-turn part. We append the
			// image part to the same Content after the FunctionResponse so the
			// model sees path+metadata and the image together in one turn.
			for _, part := range content.Parts {
				if part == nil || part.FunctionResponse == nil {
					continue
				}
				fr := part.FunctionResponse
				if fr.Name != readImageToolName {
					continue
				}
				path, ok := fr.Response["path"].(string)
				if !ok || path == "" {
					continue
				}
				data, err := sb.ReadFile(path)
				if err != nil {
					fmt.Fprintf(os.Stderr, "pi-go: warning: read_image callback could not read %q: %v\n", path, err)
					continue
				}
				mime := detectImageMIMEType(data, path)
				content.Parts = append(content.Parts, genai.NewPartFromBytes(data, mime))
			}
		}
		return nil, nil
	}
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
