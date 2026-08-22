package palace

import (
	"context"
	"errors"
	"testing"

	"github.com/knights-analytics/hugot"
)

// DownloadModel is a thin shim over hugot; what it owns is the model name, the
// destination, and whether an explicit ONNX file overrides hugot's default.
// The fetcher is swapped for a recorder so no network is touched and no real
// weights land on disk.
func TestDownloadModel_PassesDestAndOnnxOverrideToTheFetcher(t *testing.T) {
	type call struct {
		model, dest string
		opts        hugot.DownloadOptions
	}
	var got []call
	orig := hugotDownloadModel
	hugotDownloadModel = func(_ context.Context, model, dest string, opts hugot.DownloadOptions) (string, error) {
		got = append(got, call{model, dest, opts})
		return dest + "/resolved", nil
	}
	t.Cleanup(func() { hugotDownloadModel = orig })

	path, err := DownloadModel("/models", "")
	if err != nil {
		t.Fatalf("DownloadModel: %v", err)
	}
	if path != "/models/resolved" {
		t.Errorf("path = %q, want the fetcher's answer", path)
	}
	if _, err := DownloadModel("/models", "onnx/custom.onnx"); err != nil {
		t.Fatalf("DownloadModel with override: %v", err)
	}

	if len(got) != 2 {
		t.Fatalf("fetcher called %d times, want 2", len(got))
	}
	for i, c := range got {
		if c.model != "sentence-transformers/all-MiniLM-L6-v2" || c.dest != "/models" {
			t.Errorf("call %d: model/dest = %q/%q", i, c.model, c.dest)
		}
	}
	if got[0].opts.OnnxFilePath != hugot.NewDownloadOptions().OnnxFilePath {
		t.Errorf("empty override changed OnnxFilePath to %q, want hugot's default", got[0].opts.OnnxFilePath)
	}
	if got[1].opts.OnnxFilePath != "onnx/custom.onnx" {
		t.Errorf("OnnxFilePath = %q, want the explicit override", got[1].opts.OnnxFilePath)
	}
}

func TestDownloadModel_ReportsFetcherErrors(t *testing.T) {
	orig := hugotDownloadModel
	hugotDownloadModel = func(context.Context, string, string, hugot.DownloadOptions) (string, error) {
		return "", errors.New("offline")
	}
	t.Cleanup(func() { hugotDownloadModel = orig })

	if _, err := DownloadModel("/models", ""); err == nil || err.Error() != "offline" {
		t.Errorf("err = %v, want the fetcher's error passed through", err)
	}
}
