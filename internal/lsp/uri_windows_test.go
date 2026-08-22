//go:build windows

package lsp

import "testing"

func TestURIPath_PrefixesDriveLetterWithSlash(t *testing.T) {
	cases := map[string]string{
		`C:\Users\me\main.go`: "/C:/Users/me/main.go",
		`C:/already/slashed`:  "/C:/already/slashed",
		`\\server\share\x.go`: "//server/share/x.go", // UNC: already rooted
	}
	for in, want := range cases {
		if got := URIPath(in); got != want {
			t.Errorf("URIPath(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestPathFromURIPath_StripsDriveLetterSlash(t *testing.T) {
	cases := map[string]string{
		"/C:/Users/me/main.go": `C:\Users\me\main.go`,
		"/tmp/foo.go":          `\tmp\foo.go`, // no drive letter: rooted path kept
		"//server/share/x.go":  `\\server\share\x.go`,
	}
	for in, want := range cases {
		if got := PathFromURIPath(in); got != want {
			t.Errorf("PathFromURIPath(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestURIPathRoundTrip(t *testing.T) {
	for _, p := range []string{`C:\x\y.go`, `D:\a b\c.rs`} {
		if back := PathFromURIPath(URIPath(p)); back != p {
			t.Errorf("PathFromURIPath(URIPath(%q)) = %q", p, back)
		}
	}
}
