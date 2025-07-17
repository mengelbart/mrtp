//go:build frontend
// +build frontend

package web

import "embed"

//go:embed frontend/dist/*
var frontendFS embed.FS

//go:embed frontend/dist
var frontendPublicFS embed.FS
