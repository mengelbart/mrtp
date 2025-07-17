//go:build !frontend
// +build !frontend

package web

import "embed"

var frontendFS embed.FS

var frontendPublicFS embed.FS
