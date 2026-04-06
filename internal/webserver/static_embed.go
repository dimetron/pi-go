package webserver

import (
	"bytes"
	"embed"
	"io/fs"
	"net/http"
	"path/filepath"
	"time"
)

//go:embed static/*
var embeddedStaticFiles embed.FS

func staticAssetsFileServer(staticDir string) http.Handler {
	if staticDir != "" {
		diskStaticDir := filepath.Join(staticDir, "static")
		return http.FileServer(http.Dir(diskStaticDir))
	}

	embeddedStaticDir, err := fs.Sub(embeddedStaticFiles, "static")
	if err != nil {
		return http.NotFoundHandler()
	}

	return http.FileServer(http.FS(embeddedStaticDir))
}

func serveStaticPage(w http.ResponseWriter, r *http.Request, staticDir, name string) {
	if staticDir != "" {
		http.ServeFile(w, r, filepath.Join(staticDir, "static", name))
		return
	}

	data, err := embeddedStaticFiles.ReadFile(filepath.ToSlash(filepath.Join("static", name)))
	if err != nil {
		http.NotFound(w, r)
		return
	}

	http.ServeContent(w, r, name, time.Time{}, bytes.NewReader(data))
}
