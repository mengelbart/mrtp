package web

import (
	"embed"
	"fmt"
	"html/template"
	"io/fs"
	"log/slog"
	"net/http"
	"path/filepath"
	"strings"

	"github.com/julienschmidt/httprouter"
)

//go:embed templates/*
var templateFS embed.FS

type Option func(*Handler) error

func Mux(mux *httprouter.Router) Option {
	return func(h *Handler) error {
		h.mux = mux
		return nil
	}
}

type Handler struct {
	mux *httprouter.Router
}

func NewHandler(opts ...Option) (*Handler, error) {
	s := &Handler{
		mux: httprouter.New(),
	}
	for _, opt := range opts {
		if err := opt(s); err != nil {
			return nil, err
		}
	}

	manifestData, err := frontendFS.ReadFile("frontend/dist/.vite/manifest.json")
	if err != nil {
		return nil, err
	}
	var m manifest
	if err = m.parse(manifestData); err != nil {
		return nil, err
	}
	templateFiles, err := templateFS.ReadDir("templates")
	if err != nil {
		return nil, err
	}

	pages := map[string]*template.Template{}
	for _, f := range templateFiles {
		ext := filepath.Ext(f.Name())
		page := strings.TrimSuffix(f.Name(), ext)
		var tmplFile []byte
		tmplFile, err = templateFS.ReadFile(fmt.Sprintf("templates/%v", f.Name()))
		if err != nil {
			return nil, err
		}
		var t *template.Template
		t, err = template.New(page).Parse(string(tmplFile))
		if err != nil {
			return nil, err
		}
		pages[page] = t
	}
	s.mux.HandlerFunc("GET", "/", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/index", http.StatusFound)
	})

	for page, tmpl := range pages {
		slog.Info("setting up handler to render page", "page", page)
		s.mux.HandlerFunc("GET", fmt.Sprintf("/%v", page), s.renderHTML(page, m, tmpl))
	}

	publicFS, err := fs.Sub(frontendPublicFS, "frontend/dist")
	if err != nil {
		return nil, err
	}
	// Uncomment for net/http.ServeMux version
	// s.mux.Handler("GET", "/static/", http.StripPrefix("/static/", http.FileServer(http.FS(publicFS))))
	s.mux.ServeFiles("/static/*filepath", http.FS(publicFS))
	return s, nil
}

func (h *Handler) renderHTML(name string, m manifest, t *template.Template) http.HandlerFunc {
	type templateData struct {
		Chunk          chunk
		ImportedChunks []chunk
	}
	name = fmt.Sprintf("src/%s.ts", name)
	data := templateData{
		Chunk:          m[name],
		ImportedChunks: m.importedChunks(name),
	}
	return func(w http.ResponseWriter, r *http.Request) {
		if err := t.Execute(w, data); err != nil {
			http.Error(w, "render error", http.StatusInternalServerError)
			slog.Error("failed to execute template", "page", name, "error", err)
			return
		}
	}
}
