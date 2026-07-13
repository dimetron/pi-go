package main

import (
	"context"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func TestPromptEmbeddedResourceAndCompletion(t *testing.T) {
	promptRes, err := prompt(context.Background(), &mcp.GetPromptRequest{
		Params: &mcp.GetPromptParams{Arguments: map[string]string{"name": "Ada"}},
	})
	if err != nil {
		t.Fatalf("prompt: %v", err)
	}
	if promptRes.Description != "Hi prompt" || len(promptRes.Messages) != 1 {
		t.Fatalf("unexpected prompt result: %#v", promptRes)
	}
	text, ok := promptRes.Messages[0].Content.(*mcp.TextContent)
	if !ok || text.Text != "Say hi to Ada" {
		t.Fatalf("unexpected prompt content: %#v", promptRes.Messages[0].Content)
	}

	resource, err := embeddedResource(context.Background(), &mcp.ReadResourceRequest{
		Params: &mcp.ReadResourceParams{URI: "embedded:info"},
	})
	if err != nil {
		t.Fatalf("embeddedResource: %v", err)
	}
	if len(resource.Contents) != 1 || !strings.Contains(resource.Contents[0].Text, "hello example") {
		t.Fatalf("unexpected resource: %#v", resource)
	}
	if _, err := embeddedResource(context.Background(), &mcp.ReadResourceRequest{Params: &mcp.ReadResourceParams{URI: "http://example.com"}}); err == nil {
		t.Fatal("expected wrong scheme error")
	}
	if _, err := embeddedResource(context.Background(), &mcp.ReadResourceRequest{Params: &mcp.ReadResourceParams{URI: "embedded:missing"}}); err == nil {
		t.Fatal("expected missing resource error")
	}

	completion, err := complete(context.Background(), &mcp.CompleteRequest{
		Params: &mcp.CompleteParams{Argument: mcp.CompleteParamsArgument{Value: "abc"}},
	})
	if err != nil {
		t.Fatalf("complete: %v", err)
	}
	if len(completion.Completion.Values) != 1 || completion.Completion.Values[0] != "abcx" {
		t.Fatalf("unexpected completion: %#v", completion)
	}
}

func TestSimpleToolsAndIcons(t *testing.T) {
	callReq := &mcp.CallToolRequest{}
	content, structured, err := contentTool(context.Background(), callReq, args{Name: "Gopher"})
	if err != nil || structured != nil {
		t.Fatalf("contentTool err=%v structured=%#v", err, structured)
	}
	if len(content.Content) != 1 {
		t.Fatalf("content count = %d", len(content.Content))
	}
	if text, ok := content.Content[0].(*mcp.TextContent); !ok || text.Text != "Hi Gopher" {
		t.Fatalf("unexpected content: %#v", content.Content[0])
	}

	_, sr, err := structuredTool(context.Background(), callReq, &args{Name: "Gopher"})
	if err != nil {
		t.Fatalf("structuredTool: %v", err)
	}
	if sr == nil || sr.Message != "Hi Gopher" {
		t.Fatalf("unexpected structured result: %#v", sr)
	}

	icons := mcpIcons()
	if len(icons) != 1 || icons[0].MIMEType != "image/png" || !strings.HasPrefix(icons[0].Source, "data:image/png;base64,") {
		t.Fatalf("unexpected icons: %#v", icons)
	}
	linkTool := resourceLinkContentTool(icons)
	linkResult, _, err := linkTool(context.Background(), callReq, args{Name: "Gopher Jr"})
	if err != nil {
		t.Fatalf("resourceLinkContentTool: %v", err)
	}
	link, ok := linkResult.Content[0].(*mcp.ResourceLink)
	if !ok || !strings.Contains(link.URI, "Gopher%20Jr") || len(link.Icons) != 1 {
		t.Fatalf("unexpected resource link: %#v", linkResult.Content[0])
	}
}

func TestSessionBackedToolsPanicWithNilSession(t *testing.T) {
	tests := []struct {
		name string
		fn   func()
	}{
		{"ping", func() { _, _, _ = pingingTool(context.Background(), &mcp.CallToolRequest{}, nil) }},
		{"log", func() { _, _, _ = loggingTool(context.Background(), &mcp.CallToolRequest{}, nil) }},
		{"roots", func() { _, _, _ = rootsTool(context.Background(), &mcp.CallToolRequest{}, nil) }},
		{"sampling", func() { _, _, _ = samplingTool(context.Background(), &mcp.CallToolRequest{}, nil) }},
		{"elicit form", func() { _, _, _ = elicitFormTool(context.Background(), &mcp.CallToolRequest{}, nil) }},
		{"elicit url", func() { _, _, _ = elicitURLTool(context.Background(), &mcp.CallToolRequest{}, nil) }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			defer func() {
				if recover() == nil {
					t.Fatal("expected nil session panic")
				}
			}()
			tt.fn()
		})
	}
}

func TestNewServer(t *testing.T) {
	srv := newServer()
	if srv == nil {
		t.Fatal("expected non-nil server")
	}
}
