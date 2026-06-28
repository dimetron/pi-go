package server

import (
	"context"
	"strings"
	"testing"

	acp "github.com/coder/acp-go-sdk"
)

func TestAgentLogout(t *testing.T) {
	a := &Agent{}
	if _, err := a.Logout(context.Background(), acp.LogoutRequest{}); err != nil {
		t.Fatalf("Logout() error = %v, want nil", err)
	}
}

func TestExtractPromptText(t *testing.T) {
	textURI := "file:///tmp/main.go"
	blocks := []acp.ContentBlock{
		acp.TextBlock("first line"),
		{Resource: &acp.ContentBlockResource{
			Resource: acp.EmbeddedResourceResource{
				TextResourceContents: &acp.TextResourceContents{Uri: textURI, Text: "package main"},
			},
		}},
		acp.ResourceLinkBlock("link", "file:///tmp/other.go"),
		acp.TextBlock(""), // skipped: empty text
		{},                // skipped: no recognized content
	}

	got := extractPromptText(blocks)

	wantParts := []string{
		"first line",
		"[File: " + textURI + "]",
		"package main",
		"[Reference: file:///tmp/other.go]",
	}
	for _, want := range wantParts {
		if !strings.Contains(got, want) {
			t.Errorf("extractPromptText() = %q, missing %q", got, want)
		}
	}
}

func TestFormatEmbeddedResource(t *testing.T) {
	tests := []struct {
		name     string
		resource *acp.ContentBlockResource
		want     string
	}{
		{
			name: "text resource is fenced",
			resource: &acp.ContentBlockResource{
				Resource: acp.EmbeddedResourceResource{
					TextResourceContents: &acp.TextResourceContents{Uri: "file:///a.txt", Text: "hello"},
				},
			},
			want: "[File: file:///a.txt]\n```\nhello\n```",
		},
		{
			name: "blob resource surfaces uri only",
			resource: &acp.ContentBlockResource{
				Resource: acp.EmbeddedResourceResource{
					BlobResourceContents: &acp.BlobResourceContents{Uri: "file:///b.bin", Blob: "AAAA"},
				},
			},
			want: "[Binary file: file:///b.bin]",
		},
		{
			name:     "empty resource returns empty string",
			resource: &acp.ContentBlockResource{},
			want:     "",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := formatEmbeddedResource(tt.resource); got != tt.want {
				t.Errorf("formatEmbeddedResource() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestRandomSessionID(t *testing.T) {
	a := randomSessionID()
	b := randomSessionID()
	if !strings.HasPrefix(a, "sess_") {
		t.Errorf("randomSessionID() = %q, want sess_ prefix", a)
	}
	if a == b {
		t.Errorf("randomSessionID() returned duplicate IDs: %q", a)
	}
	// "sess_" + 24 hex chars (12 random bytes).
	if len(a) != len("sess_")+24 {
		t.Errorf("randomSessionID() length = %d, want %d", len(a), len("sess_")+24)
	}
}
