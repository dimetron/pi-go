package auth

import (
	"net/http/httptest"
	"strings"
	"testing"
)

func TestRenderCallbackPage_Success(t *testing.T) {
	w := httptest.NewRecorder()
	RenderCallbackPage(w, 200, true, "Authentication successful", "You can close this tab and return to pi.")

	if w.Code != 200 {
		t.Errorf("expected 200, got %d", w.Code)
	}
	body := w.Body.String()
	for _, want := range []string{
		"Authentication successful",
		"You can close this tab and return to pi.",
		"PI-GO<span>.SH</span>",
		"window.close()",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("expected body to contain %q", want)
		}
	}

	ct := w.Header().Get("Content-Type")
	if !strings.Contains(ct, "text/html") || !strings.Contains(ct, "utf-8") {
		t.Errorf("expected html+utf8 content type, got %q", ct)
	}
}

func TestRenderCallbackPage_Failure(t *testing.T) {
	w := httptest.NewRecorder()
	RenderCallbackPage(w, 400, false, "Authentication failed", "access_denied")

	if w.Code != 400 {
		t.Errorf("expected 400, got %d", w.Code)
	}
	body := w.Body.String()
	for _, want := range []string{"card err", "✗", "Authentication failed", "access_denied"} {
		if !strings.Contains(body, want) {
			t.Errorf("expected body to contain %q", want)
		}
	}
}

func TestRenderCallbackPage_EscapesDetail(t *testing.T) {
	w := httptest.NewRecorder()
	RenderCallbackPage(w, 200, true, "T<i>tle", "<script>alert(1)</script>")

	body := w.Body.String()
	if strings.Contains(body, "T<i>tle") || strings.Contains(body, "<script>alert") {
		t.Error("expected title and detail to be HTML-escaped")
	}
	if !strings.Contains(body, "&lt;script&gt;") {
		t.Error("expected escaped script tag in detail")
	}
}
