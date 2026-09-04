//go:build frontend
// +build frontend

package web

import "embed"

// frontendFS includes the dot-prefixed .vite directory holding the manifest.
//
//go:embed frontend/dist/*
var frontendFS embed.FS

// frontendPublicFS holds only what is served under /static, so the manifest stays out.
//
//go:embed frontend/dist
var frontendPublicFS embed.FS
