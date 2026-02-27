package gpuscheduler

import (
	"embed"
	"io/fs"
	"net/http"
	"strings"
)

// Embed the entire web directory containing index.html, css/, js/, etc.
//
//go:embed web/dist
var webContent embed.FS

func DashboardHandler() http.Handler {
	distFS, err := fs.Sub(webContent, "web/dist")
	if err != nil {
		panic(err)
	}

	fileServer := http.FileServer(http.FS(distFS))

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Let API routes pass through (they're registered separately on the mux)
		// Serve static files if they exist, otherwise fallback to index.html
		path := r.URL.Path
		if path != "/" && !strings.HasPrefix(path, "/api") {
			// Check if the file exists in the embedded FS
			if f, err := distFS.Open(strings.TrimPrefix(path, "/")); err == nil {
				f.Close()
				fileServer.ServeHTTP(w, r)
				return
			}
		}

		if path == "/" || !strings.HasPrefix(path, "/api") {
			// SPA fallback: serve index.html for unknown routes
			r.URL.Path = "/"
			fileServer.ServeHTTP(w, r)
			return
		}

		fileServer.ServeHTTP(w, r)
	})
}
