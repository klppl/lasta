package handler

import (
	"io/fs"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
)

// Routes builds the chi router. staticFS is the embedded static/ tree.
func Routes(h *Handler, staticFS fs.FS) http.Handler {
	r := chi.NewRouter()
	r.Use(middleware.RealIP)
	r.Use(middleware.Recoverer)

	r.Get("/", h.Index)
	r.Post("/extract", h.Extract)
	r.Get("/download", h.Download)
	r.Post("/cookies", h.Cookies)
	r.Get("/health", h.Health)
	r.Handle("/static/*", http.StripPrefix("/static/", http.FileServer(http.FS(staticFS))))

	// Deep links: any other path is treated as a video URL pasted after the
	// base URL, e.g. /threads.com/@user/post/X.
	r.NotFound(h.DeepLink)

	return r
}
