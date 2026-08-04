package palace

import (
	"context"
	"strings"
	"testing"
)

func TestToolKGExtract_MissingText(t *testing.T) {
	t.Parallel()

	out, err := palaceKGExtractHandler(context.Background(), nil, KGExtractToolInput{})
	if err != nil {
		t.Fatalf("palaceKGExtractHandler: %v", err)
	}
	if !strings.Contains(out.Content, "Error") {
		t.Errorf("expected error for empty text, got: %s", out.Content)
	}
	if len(out.Triples) != 0 {
		t.Errorf("expected no triples on error, got %d", len(out.Triples))
	}
}

func TestToolKGExtract_ImportsWithSourceFile(t *testing.T) {
	t.Parallel()

	text := `package auth

import (
	"github.com/dimetron/pi-go/internal/users"
	"github.com/labstack/echo/v4"
)
`

	out, err := palaceKGExtractHandler(context.Background(), nil, KGExtractToolInput{
		Text:       text,
		SourceFile: "internal/auth/handler.go",
	})
	if err != nil {
		t.Fatalf("palaceKGExtractHandler: %v", err)
	}

	// Find the third-party imports. Stdlib paths (no "." in the first
	// segment, relative paths) are filtered out. The extractor preserves
	// case in subjects and objects, so map keys are compared verbatim.
	want := map[string]bool{
		"handler.go imports github.com/dimetron/pi-go/internal/users": false,
		"handler.go imports github.com/labstack/echo/v4":              false,
	}
	for _, t0 := range out.Triples {
		key := t0.Subject + " " + t0.Predicate + " " + t0.Object
		if _, ok := want[key]; ok {
			want[key] = true
		}
	}
	for k, found := range want {
		if !found {
			t.Errorf("missing expected triple: %q (got %d triples)", k, len(out.Triples))
		}
	}

	// Stdlib must not appear.
	for _, t0 := range out.Triples {
		if strings.Contains(t0.Object, "fmt") {
			t.Errorf("stdlib import leaked into triples: %+v", t0)
		}
	}
}

func TestToolKGExtract_GoFunctionDeclaration(t *testing.T) {
	t.Parallel()

	text := `package auth

func HandleLogin(w http.ResponseWriter, r *http.Request) {
}

func (s *Service) Authenticate(token string) (User, error) {
	return User{}, nil
}
`

	out, err := palaceKGExtractHandler(context.Background(), nil, KGExtractToolInput{
		Text:       text,
		SourceFile: "internal/auth/auth.go",
	})
	if err != nil {
		t.Fatalf("handler: %v", err)
	}

	want := map[string]bool{
		"HandleLogin defined_in internal/auth/auth.go":  false,
		"Authenticate defined_in internal/auth/auth.go": false,
	}
	for _, t0 := range out.Triples {
		key := t0.Subject + " " + t0.Predicate + " " + t0.Object
		if _, ok := want[key]; ok {
			want[key] = true
		}
	}
	for k, found := range want {
		if !found {
			t.Errorf("missing %q", k)
		}
	}
}

func TestToolKGExtract_GoTypeDeclaration(t *testing.T) {
	t.Parallel()

	text := `package users

type User struct {
	ID string
}

type Repository interface {
	Get(id string) (*User, error)
}
`

	out, err := palaceKGExtractHandler(context.Background(), nil, KGExtractToolInput{
		Text:       text,
		SourceFile: "internal/users/types.go",
	})
	if err != nil {
		t.Fatalf("handler: %v", err)
	}

	// Types come back as defined_in.
	want := map[string]bool{
		"User defined_in internal/users/types.go":       false,
		"Repository defined_in internal/users/types.go": false,
	}
	for _, t0 := range out.Triples {
		key := t0.Subject + " " + t0.Predicate + " " + t0.Object
		if _, ok := want[key]; ok {
			want[key] = true
		}
	}
	for k, found := range want {
		if !found {
			t.Errorf("missing %q (got %d triples: %+v)", k, len(out.Triples), out.Triples)
		}
	}
}

func TestToolKGExtract_PythonDecls(t *testing.T) {
	t.Parallel()

	text := `import os

def authenticate(token):
    return User.query.filter_by(token=token).first()

class AuthService:
    pass
`

	out, err := palaceKGExtractHandler(context.Background(), nil, KGExtractToolInput{
		Text:       text,
		SourceFile: "auth/service.py",
	})
	if err != nil {
		t.Fatalf("handler: %v", err)
	}

	want := map[string]bool{
		"authenticate defined_in auth/service.py": false,
		"AuthService defined_in auth/service.py":  false,
	}
	for _, t0 := range out.Triples {
		key := t0.Subject + " " + t0.Predicate + " " + t0.Object
		if _, ok := want[key]; ok {
			want[key] = true
		}
	}
	for k, found := range want {
		if !found {
			t.Errorf("missing %q (got %d triples: %+v)", k, len(out.Triples), out.Triples)
		}
	}
}

func TestToolKGExtract_FreeFormProseYieldsNothing(t *testing.T) {
	t.Parallel()

	text := `The auth system was rewritten last quarter. It now uses JWTs and
sessions are no longer shared across pods. The login flow has three steps
and the password reset email is templated.`

	out, err := palaceKGExtractHandler(context.Background(), nil, KGExtractToolInput{Text: text})
	if err != nil {
		t.Fatalf("handler: %v", err)
	}
	if len(out.Triples) != 0 {
		t.Errorf("expected no triples from free-form prose, got %d: %+v", len(out.Triples), out.Triples)
	}
	if !strings.Contains(out.Content, "No candidate triples extracted") {
		t.Errorf("expected helpful no-results message, got: %s", out.Content)
	}
}

func TestToolKGExtract_DedupAcrossTypes(t *testing.T) {
	t.Parallel()

	// Both goTypeDeclRe and genericDeclRe could match a top-level name;
	// emit only one "defined_in" triple.
	text := `type Service struct{}`

	out, err := palaceKGExtractHandler(context.Background(), nil, KGExtractToolInput{
		Text:       text,
		SourceFile: "x/y.go",
	})
	if err != nil {
		t.Fatalf("handler: %v", err)
	}
	count := 0
	for _, t0 := range out.Triples {
		if t0.Subject == "Service" && t0.Predicate == "defined_in" {
			count++
		}
	}
	if count != 1 {
		t.Errorf("expected exactly 1 'Service defined_in' triple, got %d", count)
	}
}

func TestToolKGExtract_MaxTriplesCap(t *testing.T) {
	t.Parallel()

	// Construct input that would yield many imports.
	var b strings.Builder
	b.WriteString("package x\n\nimport (\n")
	for i := 0; i < 30; i++ {
		b.WriteString("\t\"github.com/example/mod")
		// Stringify i to keep imports unique.
		b.WriteString("/v")
		b.WriteString(itoa(i))
		b.WriteString("\"\n")
	}
	b.WriteString(")\n")

	out, err := palaceKGExtractHandler(context.Background(), nil, KGExtractToolInput{
		Text:       b.String(),
		SourceFile: "x/x.go",
		MaxTriples: 5,
	})
	if err != nil {
		t.Fatalf("handler: %v", err)
	}
	if len(out.Triples) != 5 {
		t.Errorf("expected 5 triples with MaxTriples=5, got %d", len(out.Triples))
	}
}

func TestToolKGExtract_ConfidenceOrdering(t *testing.T) {
	t.Parallel()

	// A code chunk with a func declaration should rank the "defined_in"
	// triple above any "imports" triple.
	text := `package auth

import "github.com/example/logger"

func HandleLogin(w http.ResponseWriter, r *http.Request) {}
`

	out, err := palaceKGExtractHandler(context.Background(), nil, KGExtractToolInput{
		Text:       text,
		SourceFile: "auth/handler.go",
	})
	if err != nil {
		t.Fatalf("handler: %v", err)
	}
	if len(out.Triples) < 2 {
		t.Fatalf("expected at least 2 triples, got %d", len(out.Triples))
	}
	// The first triple (after sort) should be the higher-confidence one.
	// Both import + defined_in are 0.95 here, so we just check that the
	// ordering is stable and non-empty.
	for i := 1; i < len(out.Triples); i++ {
		if out.Triples[i].Confidence > out.Triples[i-1].Confidence {
			t.Errorf("triple %d (%v) outranks %d (%v) — sort is wrong",
				i, out.Triples[i], i-1, out.Triples[i-1])
		}
	}
}

// itoa is a tiny integer-to-string used only by the test cases, to avoid
// importing strconv just for a sequence number.
func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var out []byte
	for n > 0 {
		out = append([]byte{byte('0' + n%10)}, out...)
		n /= 10
	}
	return string(out)
}
