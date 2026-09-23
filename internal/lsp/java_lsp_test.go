package lsp

import (
	"context"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// javaProject writes a minimal Maven project and returns its root plus a
// source file inside it. jdtls keys its workspace off the directory holding a
// pom.xml/build.gradle marker, so the fixture has to look like a real project
// rather than a loose .java file.
func javaProject(t *testing.T) (root, src string) {
	t.Helper()

	root = t.TempDir()
	pom := `<?xml version="1.0" encoding="UTF-8"?>
<project xmlns="http://maven.apache.org/POM/4.0.0">
  <modelVersion>4.0.0</modelVersion>
  <groupId>com.example</groupId>
  <artifactId>lsp-fixture</artifactId>
  <version>1.0.0</version>
</project>
`
	if err := os.WriteFile(filepath.Join(root, "pom.xml"), []byte(pom), 0o644); err != nil {
		t.Fatalf("write pom.xml: %v", err)
	}

	srcDir := filepath.Join(root, "src", "main", "java", "com", "example")
	if err := os.MkdirAll(srcDir, 0o755); err != nil {
		t.Fatalf("mkdir src: %v", err)
	}

	src = filepath.Join(srcDir, "Greeter.java")
	code := `package com.example;

public class Greeter {
    private final String name;

    public Greeter(String name) {
        this.name = name;
    }

    public String greet() {
        return "hello " + name;
    }
}
`
	if err := os.WriteFile(src, []byte(code), 0o644); err != nil {
		t.Fatalf("write Greeter.java: %v", err)
	}

	return root, src
}

// TestJavaLSP_DocumentSymbols drives a real jdtls process through the Manager
// and asserts documentSymbol comes back with the class and its methods. It is
// the end-to-end check for the Java config: DetectLanguage → FindRoot →
// startServer → initialize handshake → textDocument/documentSymbol.
//
// documentSymbol is the right first request because it is answered from the
// file's own AST and does not wait on project indexing, unlike diagnostics.
func TestJavaLSP_DocumentSymbols(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping Java LSP E2E test in short mode")
	}
	if _, err := exec.LookPath("jdtls"); err != nil {
		t.Skip("jdtls not installed; skipping Java LSP integration test")
	}

	root, src := javaProject(t)

	mgr := NewManager(nil)
	if !mgr.Available("java") {
		mgr.Shutdown()
		t.Fatal("java configured but marked unavailable despite jdtls being on PATH")
	}

	srv, err := mgr.ServerFor(src)
	if err != nil {
		mgr.Shutdown()
		t.Fatalf("ServerFor(%s): %v", src, err)
	}
	if srv == nil {
		mgr.Shutdown()
		t.Fatal("ServerFor returned nil for a .java file — java is not wired into DetectLanguage")
	}
	// Close the server before the manager so jdtls releases its workspace
	// handle before t.TempDir cleanup runs.
	defer func() {
		_ = srv.Close()
		mgr.Shutdown()
	}()

	if srv.language != "java" {
		t.Errorf("server language = %q, want java", srv.language)
	}
	if !samePath(t, srv.rootURI, root) {
		t.Errorf("rootURI = %q, want the directory containing pom.xml (%q)", srv.rootURI, root)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()

	symbols, err := srv.Symbols(ctx, src)
	if err != nil {
		t.Fatalf("documentSymbol: %v", err)
	}
	if len(symbols) == 0 {
		t.Fatal("documentSymbol returned no symbols for Greeter.java")
	}

	var names []string
	var collect func([]DocumentSymbol)
	collect = func(syms []DocumentSymbol) {
		for _, s := range syms {
			names = append(names, s.Name)
			collect(s.Children)
		}
	}
	collect(symbols)

	joined := strings.Join(names, ",")
	t.Logf("documentSymbol names: %s", joined)

	for _, want := range []string{"Greeter", "greet"} {
		if !strings.Contains(joined, want) {
			t.Errorf("documentSymbol output %q missing %q", joined, want)
		}
	}
}

// TestJavaLSP_Diagnostics asserts jdtls reports a compile error introduced into
// the fixture. Diagnostics are pushed asynchronously after didOpen, so this
// polls the Manager's cache rather than reading a single response.
func TestJavaLSP_Diagnostics(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping Java LSP E2E test in short mode")
	}
	if _, err := exec.LookPath("jdtls"); err != nil {
		t.Skip("jdtls not installed; skipping Java LSP integration test")
	}

	_, src := javaProject(t)

	broken := `package com.example;

public class Broken {
    public void run() {
        int x = "not an int";
    }
}
`
	brokenPath := filepath.Join(filepath.Dir(src), "Broken.java")
	if err := os.WriteFile(brokenPath, []byte(broken), 0o644); err != nil {
		t.Fatalf("write Broken.java: %v", err)
	}

	mgr := NewManager(nil)
	srv, err := mgr.ServerFor(brokenPath)
	if err != nil {
		mgr.Shutdown()
		t.Fatalf("ServerFor: %v", err)
	}
	if srv == nil {
		mgr.Shutdown()
		t.Fatal("no server for .java file")
	}
	defer func() {
		_ = srv.Close()
		mgr.Shutdown()
	}()

	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()

	if _, err := srv.Diagnostics(ctx, brokenPath); err != nil {
		t.Fatalf("Diagnostics: %v", err)
	}

	uri := fileURI(brokenPath)
	deadline := time.Now().Add(90 * time.Second)
	for {
		diags := mgr.CachedDiagnostics(uri)
		if len(diags) > 0 {
			for _, d := range diags {
				t.Logf("line %d: [%s] %s", d.Range.Start.Line+1, d.SeverityString(), d.Message)
			}
			// The fixture's error is a String→int assignment, which javac
			// reports as an incompatible-type error.
			var found bool
			for _, d := range diags {
				if strings.Contains(strings.ToLower(d.Message), "incompatible") ||
					strings.Contains(strings.ToLower(d.Message), "cannot convert") ||
					strings.Contains(strings.ToLower(d.Message), "int") {
					found = true
					break
				}
			}
			if !found {
				t.Errorf("diagnostics did not mention the type error; got %d: %v", len(diags), diags)
			}
			return
		}
		if time.Now().After(deadline) {
			t.Fatal("jdtls published no diagnostics for a file with a type error within 90s")
		}
		time.Sleep(500 * time.Millisecond)
	}
}

// TestJavaLSP_Hello exercises a request/response round trip on a canonical
// Hello.java: hover over a variable use. Unlike documentSymbol and
// diagnostics, hover goes through textDocument/hover and is answered from the
// file's own AST, so it does not depend on project import completing.
//
// This is also the path that would have caught the symlink mismatch between
// the URI pi-go opens a file under and the URI the server reports back, since
// hover resolves the position against the document it was sent.
func TestJavaLSP_Hello(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping Java LSP E2E test in short mode")
	}
	if _, err := exec.LookPath("jdtls"); err != nil {
		t.Skip("jdtls not installed; skipping Java LSP integration test")
	}

	root, src := javaProject(t)

	hello := `package com.example;

public class Hello {
    public static void main(String[] args) {
        String greeting = "Hello, world!";
        System.out.println(greeting);
    }
}
`
	helloPath := filepath.Join(filepath.Dir(src), "Hello.java")
	if err := os.WriteFile(helloPath, []byte(hello), 0o644); err != nil {
		t.Fatalf("write Hello.java: %v", err)
	}

	mgr := NewManager(nil)
	srv, err := mgr.ServerFor(helloPath)
	if err != nil {
		mgr.Shutdown()
		t.Fatalf("ServerFor: %v", err)
	}
	if srv == nil {
		mgr.Shutdown()
		t.Fatal("no server for .java file")
	}
	defer func() {
		_ = srv.Close()
		mgr.Shutdown()
	}()

	// FindRoot reports the directory holding pom.xml. The server is handed
	// that path as-is (it is not symlink-resolved, unlike the per-file URIs),
	// and resolves it itself, so compare the two canonically.
	if !samePath(t, srv.rootURI, root) {
		t.Errorf("rootURI = %q, want the directory containing pom.xml (%q)", srv.rootURI, root)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()

	// Symbols first: confirms the file was parsed before we ask about it.
	symbols, err := srv.Symbols(ctx, helloPath)
	if err != nil {
		t.Fatalf("documentSymbol: %v", err)
	}
	var names []string
	var collect func([]DocumentSymbol)
	collect = func(syms []DocumentSymbol) {
		for _, s := range syms {
			names = append(names, s.Name)
			collect(s.Children)
		}
	}
	collect(symbols)
	joined := strings.Join(names, ",")
	t.Logf("Hello.java documentSymbol names: %s", joined)
	if !strings.Contains(joined, "Hello") {
		t.Errorf("documentSymbol output %q missing the Hello class", joined)
	}
	if !strings.Contains(joined, "main") {
		t.Errorf("documentSymbol output %q missing main", joined)
	}

	// Hover over `greeting` in the println call (line 5, 0-indexed; the
	// identifier starts at character 27).
	hover, err := srv.Hover(ctx, helloPath, 5, 27)
	if err != nil {
		t.Fatalf("hover: %v", err)
	}
	if hover == nil {
		t.Fatal("hover returned no result for `greeting`")
	}
	text := hoverText(hover)
	t.Logf("hover text: %q", text)
	if !strings.Contains(text, "String") {
		t.Errorf("hover text %q does not mention String", text)
	}
}

// mustEvalSymlinks resolves path for assertions that compare against a
// server-reported URI.
func mustEvalSymlinks(t *testing.T, path string) string {
	t.Helper()
	resolved, err := filepath.EvalSymlinks(path)
	if err != nil {
		t.Fatalf("EvalSymlinks(%s): %v", path, err)
	}
	return resolved
}

// samePath reports whether a file:// URI and an OS path name the same
// location, comparing them after symlink resolution so an unresolved /var
// path and its /private/var canonical form are treated as equal.
func samePath(t *testing.T, uri, path string) bool {
	t.Helper()
	u, err := url.Parse(uri)
	if err != nil {
		t.Fatalf("parse rootURI %q: %v", uri, err)
	}
	return mustEvalSymlinks(t, PathFromURIPath(u.Path)) == mustEvalSymlinks(t, path)
}

// hoverText returns the human-readable value of a hover result. Contents is a
// MarkupContent in this client, so the text is the Value field.
func hoverText(h *HoverResult) string {
	if h == nil {
		return ""
	}
	return h.Contents.Value
}
