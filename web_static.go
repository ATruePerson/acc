package main

import (
	"embed"
	"io/fs"
	"net/http"
	"strings"
)

//go:embed web/app/*
var appWeb embed.FS

//go:embed web/dashboard/*
var dashboardWeb embed.FS

func embeddedFileServer(content embed.FS, root string) http.Handler {
	sub, err := fs.Sub(content, root)
	if err != nil {
		panic("embed " + root + ": " + err.Error())
	}
	return http.FileServer(http.FS(sub))
}

func (s *server) handleApp(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path == "/app" {
		http.Redirect(w, r, "/app/", http.StatusPermanentRedirect)
		return
	}
	if !strings.HasPrefix(r.URL.Path, "/app/") {
		http.NotFound(w, r)
		return
	}
	http.StripPrefix("/app/", embeddedFileServer(appWeb, "web/app")).ServeHTTP(w, r)
}

func (s *server) handleDashboardUI(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path == "/dashboard" {
		http.Redirect(w, r, "/dashboard/", http.StatusPermanentRedirect)
		return
	}
	if !strings.HasPrefix(r.URL.Path, "/dashboard/") || strings.HasPrefix(r.URL.Path, "/dashboard/api/") {
		http.NotFound(w, r)
		return
	}
	http.StripPrefix("/dashboard/", embeddedFileServer(dashboardWeb, "web/dashboard")).ServeHTTP(w, r)
}
